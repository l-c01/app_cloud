package coordinator_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	return &fakeRunning{}, nil
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// fakeNetwork 记录实际被要求接管的设置。
//
// 这是本文件最重要的观察点：**接线错了就什么都不会被写进来**，
// 而缺陷 2 正是「计划为空」这种情形。
type fakeNetwork struct {
	appliedPlan coordinator.NetworkPlan
	applyCalls  int
}

func (f *fakeNetwork) Snapshot(context.Context) (coordinator.NetworkSettings, error) {
	return coordinator.NetworkSettings{}, nil
}

func (f *fakeNetwork) Apply(_ context.Context, plan coordinator.NetworkPlan) (coordinator.NetworkSettings, error) {
	f.applyCalls++
	f.appliedPlan = plan
	return plan.Values, nil
}

func (f *fakeNetwork) Restore(context.Context, coordinator.NetworkSettings, coordinator.NetworkSettings) (coordinator.RestoreResult, error) {
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

// 主链路贯通：登录 → 取订阅 → 选节点 → 构造请求 → 编译器接受 → 协调器接管网络。
//
// 这条用例存在的意义是让「接线」本身成为被断言的对象。
// 它同时覆盖了此前两个真实缺陷：
//   - 请求必须能被真编译器接受（端口缺陷）；
//   - 系统代理必须真的被写入且指向正确的端口（接入方式缺陷）。
func TestWiringLoginToNetworkTakeover(t *testing.T) {
	ctx := context.Background()
	mgr, sess := buildWiredSession(t)

	if _, err := sess.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := sess.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}
	if _, err := sess.SelectNode("日本 01"); err != nil {
		t.Fatalf("SelectNode: %v", err)
	}

	req, serr := sess.BuildRequest()
	if serr != nil {
		t.Fatalf("BuildRequest: %v", serr)
	}

	dir := t.TempDir()
	comp := &realCompiler{dir: dir}
	core := &fakeCoreRunner{compiler: comp}
	net := &fakeNetwork{}
	coord, err := coordinator.New(coordinator.Options{
		Compiler:     comp,
		Core:         core,
		Network:      net,
		Journal:      &fakeJournal{},
		ConfigDir:    dir,
		Owner:        "test",
		ReadyTimeout: 2 * time.Second,
		StopTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("coordinator.New: %v", err)
	}

	// 接入方式必须由 Manager 给出来——**服务进程就是这么取值的**，
	// 不能在这里直接读选项模型：那样就绕过了真正会在生产里跑的那条路径，
	// 而这正是本文件要防的事（缺陷 2 就是「取值路径返回了空串」）。
	mode := mgr.TrafficEntry()
	if mode == "" {
		t.Fatalf("接入方式为空：协调器会返回空计划，于是什么都不接管")
	}
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
	ctx := context.Background()
	_, sess := buildWiredSession(t)

	if _, err := sess.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := sess.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}
	if _, err := sess.SelectNode("美国 01"); err != nil {
		t.Fatalf("SelectNode: %v", err)
	}
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
