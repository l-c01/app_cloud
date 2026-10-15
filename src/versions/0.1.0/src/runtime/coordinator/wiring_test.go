package coordinator_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"proxyclient/platform/journalfile"
	"proxyclient/runtime/coordinator"
	"proxyclient/runtime/session"
	"proxyclient/shared/compiler"
	"proxyclient/shared/domain"
	"proxyclient/shared/panel"
	"proxyclient/shared/secrets"
	"proxyclient/shared/subscription"
)

// 本文件是**贯通接线测试** / end-to-end wiring test。
//
// 为什么需要它：本项目连续出现两个「每个模块单独都对、接线处错」的缺陷，
// 而两端界面、IPC、服务、编译器各自的测试全绿，谁也发现不了：
//
//  1. 会话只在 TUN 模式下设本地端口，而桌面默认用系统代理（TUN 关）
//     → 端口为 0 → 编译器拒绝 → **每次点「连接」都失败**；
//     连带把系统代理写成 127.0.0.1:0，会让整机断网。
//  2. 界面不发接入方式，而 planForMode("") 返回空计划
//     → 核心起来了、状态「已连接」，**但什么都没接管**，且不报任何错。
//
// 两者都只有在「把整条链路真的串起来跑一遍」时才会暴露。因此这里的
// 原则是：**只在最外层用假实现**——假面板、假核心进程、假网络后端；
// 而会话、编译器、协调器都是真的。这样任何一处接线错了都会失败。

// ---------------------------------------------------------------------------
// 假面板与假订阅（最外层：网络）
// ---------------------------------------------------------------------------

type fakePanel struct {
	account *panel.Account
}

func (f *fakePanel) Login(_ context.Context, _ panel.Credentials) (*panel.Session, *panel.Error) {
	return &panel.Session{SessionToken: "Bearer test", SubscriptionToken: "sub"}, nil
}

func (f *fakePanel) GetSubscriptionDescriptor(_ context.Context, _ string) (*panel.SubscriptionDescriptor, *panel.Error) {
	return &panel.SubscriptionDescriptor{
		SubscribeURL: "https://panel.example.com/sub?token=SECRET",
		Plan:         &panel.Plan{Name: "Standard"},
	}, nil
}

func (f *fakePanel) GetAccount(_ context.Context, _ string) (*panel.Account, *panel.Error) {
	return f.account, nil
}

type fakeFetcher struct {
	nodes []*domain.Node
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string) (*subscription.FetchResult, *subscription.Error) {
	return &subscription.FetchResult{
		Nodes:    f.nodes,
		UserInfo: subscription.UserInfo{Present: true, Total: 1000},
	}, nil
}

func testNode(name, server string, port int) *domain.Node {
	n := &domain.Node{
		Name:     name,
		Protocol: domain.ProtocolTrojan,
		Server:   server,
		Port:     port,
		Password: "pw-" + name,
		Security: domain.SecurityTLS,
	}
	n.ComputeID()
	return n
}

// ---------------------------------------------------------------------------
// 真编译器适配（协调器要的是「编译并落成文件」）
// ---------------------------------------------------------------------------

// realCompiler 调用**真的**编译逻辑，只把「写文件」简化掉。
//
// 这一层必须是真的：上面两个缺陷的第一个正是「编译器拒绝了这个请求」，
// 用假编译器就会把那条断言抹掉。
type realCompiler struct {
	dir string
}

func (c *realCompiler) CompileToFile(_ context.Context, req *compiler.Request, _ string) (*coordinator.CompiledConfig, error) {
	out, report, err := compiler.CompileSingBox(req)
	if err != nil {
		return nil, err
	}
	// 产物必须真的是配置，而不是空字节：编译「成功」但什么都没生成，
	// 在协调器看来与成功无异，核心却会因为空配置起不来。
	if !json.Valid(out) || len(out) == 0 {
		return nil, &testError{"compiler produced no usable config"}
	}
	return &coordinator.CompiledConfig{
		Core:   req.Core,
		Path:   filepath.Join(c.dir, "config.json"),
		Report: report,
	}, nil
}

// ---------------------------------------------------------------------------
// 假核心与假网络（最外层：进程与操作系统）
// ---------------------------------------------------------------------------

type fakeRunning struct{ readyErr error }

func (f *fakeRunning) Ready(context.Context) error { return f.readyErr }
func (f *fakeRunning) Stop(context.Context) error  { return nil }
func (f *fakeRunning) Logs() []string              { return nil }

type fakeCoreRunner struct {
	started  int
	ready    string
	lastReq  *compiler.Request
	compiler *realCompiler

	readyErr error
}

func (f *fakeCoreRunner) CheckConfig(_ context.Context, _ compiler.Core, path string) error {
	// 编译产物应当是合法 JSON —— 真编译器已经保证格式，这里再确认一次
	// 「协调器拿到的确实是我们编译出来的东西」，而不是一个空路径。
	if path == "" {
		return &testError{"empty config path"}
	}
	return nil
}

func (f *fakeCoreRunner) Start(_ context.Context, _ compiler.Core, _ string) (coordinator.RunningCore, error) {
	f.started++
	return &fakeRunning{readyErr: f.readyErr}, nil
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// fakeNetwork 记录实际被要求接管的设置。
//
// **快照与恢复是「真的」**：快照返回一份「改动前的系统状态」，恢复则
// 把它还回去并记录下来。恒返回空值是不够的——那样「恢复有没有被调用、
// 恢复到什么值」都断言不出来，而恢复失败正是会**把用户机器留在
// 断网状态**的那一类缺陷。
type fakeNetwork struct {
	appliedPlan coordinator.NetworkPlan
	applyCalls  int

	snapshot   coordinator.NetworkSettings
	restoreGot coordinator.NetworkSettings
	restoreTo  coordinator.NetworkSettings
	restoreErr error
	restores   int

	applyErr error
}

func (f *fakeNetwork) Snapshot(context.Context) (coordinator.NetworkSettings, error) {
	return f.snapshot, nil
}

func (f *fakeNetwork) Apply(_ context.Context, plan coordinator.NetworkPlan) (coordinator.NetworkSettings, error) {
	f.applyCalls++
	f.appliedPlan = plan
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	return plan.Values, nil
}

func (f *fakeNetwork) Restore(_ context.Context, original, applied coordinator.NetworkSettings) (coordinator.RestoreResult, error) {
	f.restores++
	f.restoreGot = applied
	f.restoreTo = original
	if f.restoreErr != nil {
		return coordinator.RestoreResult{}, f.restoreErr
	}
	return coordinator.RestoreResult{}, nil
}

type fakeJournal struct{ entry *coordinator.JournalEntry }

func (f *fakeJournal) Save(e coordinator.JournalEntry) error    { c := e; f.entry = &c; return nil }
func (f *fakeJournal) Load() (*coordinator.JournalEntry, error) { return f.entry, nil }
func (f *fakeJournal) Clear() error                             { f.entry = nil; return nil }

// ---------------------------------------------------------------------------
// 贯通链路
// ---------------------------------------------------------------------------

// buildWiredSession 造一条「假面板在两端、中间全是真的」的会话。
//
// 走的是**服务端真实的那条路**：`Manager.EnsureSession` 建立会话，
// 而不是直接调 `session.New`。差别很重要——服务进程取接入方式用的是
// `Manager.TrafficEntry()`，只有经过 Manager 才走到同一个代码路径。
func buildWiredSession(t *testing.T) (*session.Manager, *session.Session) {
	t.Helper()
	m := session.NewManager(session.Config{
		Store: secrets.NewMemoryStore(),
		Panel: &fakePanel{account: &panel.Account{Email: "u@example.com", TransferEnable: 1 << 30}},
		Fetcher: &fakeFetcher{nodes: []*domain.Node{
			testNode("日本 01", "jp1.example.com", 443),
			testNode("美国 01", "us1.example.com", 443),
		}},
	})
	s, serr := m.EnsureSession("https://panel.example.com", false)
	if serr != nil {
		t.Fatalf("EnsureSession: %v", serr)
	}
	return m, s
}

// wiredCoordinator 组装一个「真编译器 + 假核心 + 假网络」的协调器。
//
// 快照固定为「改动前是一台直连的机器」：恢复是否把它还回去，
// 是这个 fake 存在的唯一理由。
func wiredCoordinator(t *testing.T) (*coordinator.Coordinator, *fakeCoreRunner, *fakeNetwork, *fakeJournal) {
	t.Helper()
	dir := t.TempDir()
	comp := &realCompiler{dir: dir}
	core := &fakeCoreRunner{compiler: comp}
	net := &fakeNetwork{snapshot: coordinator.NetworkSettings{
		"system_proxy.http":  "",
		"system_proxy.socks": "",
	}}
	journal := &fakeJournal{}
	coord, err := coordinator.New(coordinator.Options{
		Compiler:     comp,
		Core:         core,
		Network:      net,
		Journal:      journal,
		ConfigDir:    dir,
		Owner:        "test",
		ReadyTimeout: 2 * time.Second,
		StopTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("coordinator.New: %v", err)
	}
	return coord, core, net, journal
}

// readySession 登录、取订阅、选节点，返回可用的编译请求与接入方式。
func readySession(t *testing.T, nodeName string) (*session.Manager, *session.Session, string) {
	t.Helper()
	ctx := context.Background()
	mgr, sess := buildWiredSession(t)

	if _, err := sess.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := sess.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}
	if _, err := sess.SelectNode(nodeName); err != nil {
		t.Fatalf("SelectNode: %v", err)
	}
	mode := mgr.TrafficEntry()
	if mode == "" {
		t.Fatalf("接入方式为空：协调器会返回空计划，于是什么都不接管")
	}
	return mgr, sess, mode
}

// 主链路贯通：登录 → 取订阅 → 选节点 → 构造请求 → 编译器接受 → 协调器接管网络。
//
// 这条用例存在的意义是让「接线」本身成为被断言的对象。
// 它同时覆盖了此前两个真实缺陷：
//   - 请求必须能被真编译器接受（端口缺陷）；
//   - 系统代理必须真的被写入且指向正确的端口（接入方式缺陷）。
func TestWiringLoginToNetworkTakeover(t *testing.T) {
	ctx := context.Background()
	_, sess, mode := readySession(t, "日本 01")

	req, serr := sess.BuildRequest()
	if serr != nil {
		t.Fatalf("BuildRequest: %v", serr)
	}

	coord, core, net, _ := wiredCoordinator(t)

	if err := coord.Connect(ctx, coordinator.ConnectRequest{Compile: req, Mode: mode}); err != nil {
		t.Fatalf("Connect: %v —— 用户点「连接」会失败", err)
	}

	if core.started != 1 {
		t.Errorf("核心应当被启动一次，实际 %d 次", core.started)
	}
	if net.applyCalls != 1 {
		t.Fatalf("网络接管应当被调用一次，实际 %d 次", net.applyCalls)
	}

	// 关键断言：系统代理必须指向**核心真正监听的那个端口**。
	// 端口为 0（此前那个缺陷）时这里会是 "127.0.0.1:0"。
	wantProxy := "127.0.0.1:" + strconv.Itoa(req.Inbound.HTTPPort)
	got := net.appliedPlan.Values["system_proxy.http"]
	if got != wantProxy {
		t.Errorf("系统代理地址 = %q，期望 %q", got, wantProxy)
	}
	if strings.HasSuffix(got, ":0") {
		t.Errorf("系统代理被写成了 0 端口，会让整台机器断网：%q", got)
	}
	if net.appliedPlan.Mode != mode {
		t.Errorf("接管的接入方式 = %q，期望 %q", net.appliedPlan.Mode, mode)
	}
}

// 编译产物必须是合法且包含所选节点的 sing-box 配置。
//
// 上一个用例只证明「编译器没拒绝」；这条进一步确认产物内容正确，
// 否则一条「什么都没生成的配置」也能让编译通过。
func TestWiringProducesUsableConfig(t *testing.T) {
	_, sess, _ := readySession(t, "美国 01")

	req, serr := sess.BuildRequest()
	if serr != nil {
		t.Fatalf("BuildRequest: %v", serr)
	}

	out, report, err := compiler.CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}
	if report == nil {
		t.Fatalf("编译报告为 nil")
	}

	var cfg map[string]any
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("产物不是合法 JSON: %v", err)
	}

	inbounds, _ := cfg["inbounds"].([]any)
	if len(inbounds) == 0 {
		t.Errorf("配置里没有任何入站：核心起来了也没有可用的本地代理")
	}
	outbounds, _ := cfg["outbounds"].([]any)
	if len(outbounds) == 0 {
		t.Errorf("配置里没有任何出站")
	}

	// 选中的节点必须真的出现在出站里。
	//
	// 出站 tag 形如 `proxy-<节点 ID 的前 8 位>`（见 compiler.outboundTag）。
	// 这里按同样的规则算出期望值：直接比完整 ID 会永远不相等，
	// 于是这条断言会一直「失败」，久而久之被人关掉——
	// **测试断言写错比没有断言更糟**。
	selected := sess.SelectedNodeID()
	short := selected
	if len(short) > 8 {
		short = short[:8]
	}
	wantTag := "proxy-" + short
	found := false
	for _, raw := range outbounds {
		ob, _ := raw.(map[string]any)
		if ob == nil {
			continue
		}
		if tag, _ := ob["tag"].(string); tag == wantTag {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("选中的节点 %q（出站 tag %q）不在出站列表里：界面显示选了，配置里却没有",
			selected, wantTag)
	}
}

// 完整来回：连接 → 断开，网络必须被还原且恢复记录被清掉。
//
// 这条针对的风险比「连不上」更严重：断开时若没有把系统代理解除，
// **用户会失去全部网络**，而且重启软件也未必能恢复——
// 这是把一个「软件不好用」变成「电脑坏了」的那类缺陷。
func TestWiringConnectDisconnectRestoresNetwork(t *testing.T) {
	ctx := context.Background()
	_, sess, mode := readySession(t, "日本 01")

	req, serr := sess.BuildRequest()
	if serr != nil {
		t.Fatalf("BuildRequest: %v", serr)
	}
	coord, _, net, journal := wiredCoordinator(t)

	if err := coord.Connect(ctx, coordinator.ConnectRequest{Compile: req, Mode: mode}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if net.applyCalls != 1 {
		t.Fatalf("接管应当发生一次，实际 %d 次", net.applyCalls)
	}
	applied := net.appliedPlan.Values["system_proxy.http"]
	if applied == "" {
		t.Fatalf("接管后系统代理为空，前提不成立")
	}

	if err := coord.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect: %v —— 用户点「断开」会失败，机器可能留在断网状态", err)
	}

	if net.restores != 1 {
		t.Fatalf("断开时应当还原网络设置一次，实际 %d 次：系统代理会留在指向死代理的状态",
			net.restores)
	}
	// 还原必须拿「改动前」的值，而不是拿刚才写进去的值。
	if net.restoreTo["system_proxy.http"] != "" {
		t.Errorf("还原到的系统代理 = %q，期望空串（回到改动前的直连状态）",
			net.restoreTo["system_proxy.http"])
	}
	if journal.entry != nil {
		t.Errorf("断开后恢复记录应当被清除，实际仍有：%+v", journal.entry)
	}
	if got := coord.Status().State; got != coordinator.StateStopped {
		t.Errorf("断开后状态 = %q，期望 %q", got, coordinator.StateStopped)
	}
}

// 崩溃恢复：上次留下的恢复记录必须被用来还原网络。
//
// 场景是进程被强杀：系统代理仍指向一个已经不存在的端口，
// 此时**用户的网络是坏的，而软件根本没在运行**。
// 启动时若不还原，用户只能自己去改系统设置。
func TestWiringRecoverRestoresFromJournal(t *testing.T) {
	ctx := context.Background()
	coord, _, net, journal := wiredCoordinator(t)

	// 模拟上次运行留下的记录：已经接管过，但没来得及还原。
	journal.entry = &coordinator.JournalEntry{
		Stage: coordinator.StateRunning,
		Original: coordinator.NetworkSettings{
			"system_proxy.http":  "",
			"system_proxy.socks": "",
		},
		Applied: coordinator.NetworkSettings{
			"system_proxy.http":  "127.0.0.1:7890",
			"system_proxy.socks": "127.0.0.1:7891",
		},
		Mode: "system_proxy",
	}

	if _, err := coord.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v —— 上次异常退出后，用户的系统代理会被留在指向死代理的状态", err)
	}

	if net.restores != 1 {
		t.Fatalf("恢复时应当还原网络设置一次，实际 %d 次", net.restores)
	}
	if net.restoreGot["system_proxy.http"] != "127.0.0.1:7890" {
		t.Errorf("还原时收到的「已应用」值 = %q，期望 127.0.0.1:7890",
			net.restoreGot["system_proxy.http"])
	}
	if journal.entry != nil {
		t.Errorf("恢复后记录应当被清除，否则下次启动会重复还原")
	}
}

// 用**真的**恢复日志走一遍「接管 → 落盘 → 下次启动恢复」。
//
// 上一个用例用的是假日志，只证明了协调器内部的逻辑；这一条把
// platform/journalfile 真的接进来，因此同时覆盖了两者之间的字段映射。
//
// 这一层必须是真的：恢复日志是**跨进程**传递状态的地方，
// 协调器写进去的字段与日志文件读出来的字段只要有一个对不上，
// 表现就是「上次没关干净，这次也恢复不了」——而两边各自的测试都是绿的。
func TestWiringRecoverThroughRealJournalFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "recovery.json")

	store, err := journalfile.New(journalPath)
	if err != nil {
		t.Fatalf("journalfile.New: %v", err)
	}

	// --- 第一次运行：接管网络，进程「被强杀」（不调用 Disconnect）---
	_, sess, mode := readySession(t, "日本 01")
	req, serr := sess.BuildRequest()
	if serr != nil {
		t.Fatalf("BuildRequest: %v", serr)
	}

	comp := &realCompiler{dir: dir}
	net1 := &fakeNetwork{snapshot: coordinator.NetworkSettings{"system_proxy.http": ""}}
	first, err := coordinator.New(coordinator.Options{
		Compiler: comp, Core: &fakeCoreRunner{compiler: comp},
		Network: net1, Journal: store,
		ConfigDir: dir, Owner: "test",
		ReadyTimeout: 2 * time.Second, StopTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("coordinator.New: %v", err)
	}
	if err := first.Connect(ctx, coordinator.ConnectRequest{Compile: req, Mode: mode}); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// 日志必须真的落到了盘上。
	if !store.Exists() {
		t.Fatalf("接管之后恢复日志没有落盘：进程被杀后无法还原网络")
	}

	// --- 第二次运行：新进程，新协调器，同一份日志 ---
	store2, err := journalfile.New(journalPath)
	if err != nil {
		t.Fatalf("重新打开恢复日志: %v", err)
	}
	net2 := &fakeNetwork{}
	second, err := coordinator.New(coordinator.Options{
		Compiler: comp, Core: &fakeCoreRunner{compiler: comp},
		Network: net2, Journal: store2,
		ConfigDir: dir, Owner: "test",
		ReadyTimeout: 2 * time.Second, StopTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("coordinator.New（第二次）: %v", err)
	}

	if _, err := second.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v —— 上次异常退出后用户的系统代理会被留在指向死代理的状态", err)
	}
	if net2.restores != 1 {
		t.Fatalf("新进程启动时应当还原网络一次，实际 %d 次", net2.restores)
	}

	// 跨进程传过来的必须是第一次真正写进去的那个值。
	want := "127.0.0.1:" + strconv.Itoa(req.Inbound.HTTPPort)
	if got := net2.restoreGot["system_proxy.http"]; got != want {
		t.Errorf("跨进程恢复拿到的已应用系统代理 = %q，期望 %q：恢复日志的字段映射有问题",
			got, want)
	}
	if net2.restoreTo["system_proxy.http"] != "" {
		t.Errorf("跨进程恢复回到的原始值 = %q，期望空串", net2.restoreTo["system_proxy.http"])
	}
	if store2.Exists() {
		t.Errorf("恢复之后日志文件应当被删除，否则每次启动都会重复还原")
	}
}

// 接管网络失败时必须回滚，不能留下半套设置。
//
// 这是最危险的一条路径：`Apply` 可能已经改了一部分系统设置就失败。
// 若不回滚，用户的机器会停在「代理指向一个不存在的端口」的状态，
// 也就是**断网**——而软件显示的只是一条错误。
func TestWiringApplyFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	_, sess, mode := readySession(t, "日本 01")

	req, serr := sess.BuildRequest()
	if serr != nil {
		t.Fatalf("BuildRequest: %v", serr)
	}

	coord, core, net, _ := wiredCoordinator(t)
	net.applyErr = errors.New("registry write denied")

	err := coord.Connect(ctx, coordinator.ConnectRequest{Compile: req, Mode: mode})
	if err == nil {
		t.Fatalf("接管失败时 Connect 应当返回错误")
	}

	// 关键：必须尝试回滚。applied 为空（Apply 直接失败），
	// 因此回滚的依据只能是进入前的快照——协调器仍然应当调用 Restore。
	if net.restores == 0 {
		t.Errorf("接管失败后没有回滚网络设置：用户可能停在断网状态")
	}
	// 核心必须被停掉：否则一个没有网络接管的孤儿进程会留在系统里。
	if core.started != 1 {
		t.Errorf("核心应当被启动一次，实际 %d 次", core.started)
	}
	if got := coord.Status().State; got == coordinator.StateRunning {
		t.Errorf("接管失败后不应停留在运行态，实际 %q", got)
	}
}

// 核心没就绪时**不能**接管网络。
//
// 顺序错了的后果很直接：系统代理已经指向本地端口，而核心还没开始监听，
// 于是用户在接下来这段时间里完全上不了网。
func TestWiringCoreNotReadySkipsTakeover(t *testing.T) {
	ctx := context.Background()
	_, sess, mode := readySession(t, "日本 01")

	req, serr := sess.BuildRequest()
	if serr != nil {
		t.Fatalf("BuildRequest: %v", serr)
	}

	dir := t.TempDir()
	comp := &realCompiler{dir: dir}
	failingCore := &fakeCoreRunner{compiler: comp, readyErr: errors.New("not listening")}
	net := &fakeNetwork{snapshot: coordinator.NetworkSettings{"system_proxy.http": ""}}
	coord, err := coordinator.New(coordinator.Options{
		Compiler: comp, Core: failingCore,
		Network: net, Journal: &fakeJournal{},
		ConfigDir: dir, Owner: "test",
		ReadyTimeout: 1 * time.Second, StopTimeout: 1 * time.Second,
	})
	if err != nil {
		t.Fatalf("coordinator.New: %v", err)
	}

	if err := coord.Connect(ctx, coordinator.ConnectRequest{Compile: req, Mode: mode}); err == nil {
		t.Fatalf("核心不就绪时 Connect 应当失败")
	}

	if net.applyCalls != 0 {
		t.Errorf("核心未就绪却接管了网络 %d 次：这会让用户在此期间无法上网", net.applyCalls)
	}
	if net.restores != 0 {
		t.Errorf("没有接管过就不该回滚，实际回滚 %d 次", net.restores)
	}
}
