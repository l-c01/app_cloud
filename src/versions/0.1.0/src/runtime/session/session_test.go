package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"proxyclient/shared/compiler"
	"proxyclient/shared/domain"
	"proxyclient/shared/panel"
	"proxyclient/shared/secrets"
	"proxyclient/shared/subscription"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// fakePanel 是一个可编程的面板。
//
// 每个字段是一个「第几次调用返回什么」的脚本，这样一次测试就能表达
// 「先成功取订阅、再失败」这类序列，而不必起 HTTP 服务。
type fakePanel struct {
	loginSessions []*panel.Session
	loginErrs     []*panel.Error
	loginCalls    int

	desc      *panel.SubscriptionDescriptor
	descErr   *panel.Error
	descCalls int

	account      *panel.Account
	accountErrs  []*panel.Error
	accountCalls int
}

func (f *fakePanel) Login(_ context.Context, _ panel.Credentials) (*panel.Session, *panel.Error) {
	i := f.loginCalls
	f.loginCalls++
	if i < len(f.loginErrs) && f.loginErrs[i] != nil {
		return nil, f.loginErrs[i]
	}
	if i < len(f.loginSessions) {
		return f.loginSessions[i], nil
	}
	if len(f.loginSessions) > 0 {
		return f.loginSessions[len(f.loginSessions)-1], nil
	}
	return &panel.Session{SessionToken: "Bearer tok"}, nil
}

func (f *fakePanel) GetSubscriptionDescriptor(_ context.Context, _ string) (*panel.SubscriptionDescriptor, *panel.Error) {
	f.descCalls++
	if f.descErr != nil {
		return nil, f.descErr
	}
	if f.desc != nil {
		return f.desc, nil
	}
	return &panel.SubscriptionDescriptor{SubscribeURL: "https://panel.example.com/sub?token=SECRET"}, nil
}

func (f *fakePanel) GetAccount(_ context.Context, _ string) (*panel.Account, *panel.Error) {
	i := f.accountCalls
	f.accountCalls++
	if i < len(f.accountErrs) && f.accountErrs[i] != nil {
		return nil, f.accountErrs[i]
	}
	if f.account != nil {
		return f.account, nil
	}
	return &panel.Account{Email: "u@example.com", TransferEnable: 100 << 30}, nil
}

// fakeFetcher 返回预设的节点集合。
type fakeFetcher struct {
	results []*subscription.FetchResult
	errs    []*subscription.Error
	calls   int
	urls    []string
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) (*subscription.FetchResult, *subscription.Error) {
	i := f.calls
	f.calls++
	f.urls = append(f.urls, url)
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.results) {
		return f.results[i], nil
	}
	if len(f.results) > 0 {
		return f.results[len(f.results)-1], nil
	}
	return &subscription.FetchResult{}, nil
}

// node 造一个节点。ID 由连接参数派生（见 domain.Node.ComputeID）。
func node(name, server string, port int) *domain.Node {
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

// newTestSession 造一个注入替身的会话。
//
// 单独写这个而不是直接调 New，是因为 New 内部会构造真实的 panel.Client
// 与 Fetcher；那样测试就得起 HTTP 服务，而本文件要验证的是**编排逻辑**，
// 不是 HTTP。真实网络路径由 shared/panel 与 shared/subscription 自己的
// 测试覆盖。
func newTestSession(t *testing.T, fp *fakePanel, ff *fakeFetcher, store secrets.Store) *Session {
	t.Helper()
	if store == nil {
		store = secrets.NewMemoryStore()
	}
	s, err := New(Config{
		PanelURL: testPanelURL,
		Store:    store,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.panel = fp
	s.fetch = ff
	return s
}

// scopedKey 复现凭据的命名空间前缀，用于**从测试里**核查底层存储的内容。
//
// 它必须与 New 里的 normalizeNamespace 保持一致。刻意在测试里重写一遍
// 而不是导出会话的内部键：这样若命名空间规则改了，这些断言会失败，
// 而「凭据确实按服务器隔离」这件事本身是需要被验证的。
func scopedKey(panelURL string, key secrets.Key) secrets.Key {
	return secrets.Key(normalizeNamespace(panelURL) + "|" + string(key))
}

// testPanelURL 与 newTestSession 使用的地址一致。
const testPanelURL = "https://panel.example.com"

// ---------------------------------------------------------------------------
// 主链路
// ---------------------------------------------------------------------------

// 完整链路：登录 → 取订阅 → 选节点 → 构造编译请求。
//
// 这是本包存在的理由，因此这个用例是核心用例：它证明
// 「面板凭据」确实能变成「一份可交给协调器编译的请求」。
func TestLoginSubscribeSelectBuild(t *testing.T) {
	fp := &fakePanel{
		account: &panel.Account{Email: "u@example.com", TransferEnable: 100 << 30},
	}
	ff := &fakeFetcher{
		results: []*subscription.FetchResult{{
			Nodes: []*domain.Node{
				node("日本 01", "jp1.example.com", 443),
				node("美国 01", "us1.example.com", 443),
			},
			UserInfo: subscription.UserInfo{
				Present: true, Upload: 100, Download: 200, Total: 1000,
			},
		}},
	}
	s := newTestSession(t, fp, ff, nil)
	ctx := context.Background()

	acct, err := s.Login(ctx, "u@example.com", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if acct.Email != "u@example.com" {
		t.Errorf("账户邮箱 = %q，期望 u@example.com", acct.Email)
	}
	if !s.LoggedIn() {
		t.Errorf("登录后 LoggedIn 应为 true")
	}

	sub, err := s.RefreshSubscription(ctx)
	if err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}
	if sub.NodeCount != 2 {
		t.Fatalf("节点数 = %d，期望 2", sub.NodeCount)
	}
	if !sub.Usage.Present || sub.Usage.Remaining != 700 {
		t.Errorf("用量 = %+v，期望 Present 且剩余 700", sub.Usage)
	}

	nodes := s.ListNodes()
	if len(nodes) != 2 {
		t.Fatalf("ListNodes 返回 %d 个，期望 2", len(nodes))
	}
	// 未选择时不应有任何节点被标为选中。
	for _, n := range nodes {
		if n.Selected {
			t.Errorf("尚未选择节点，但 %q 被标为选中", n.ID)
		}
	}

	// 按名字选择（Clash 风格界面用名字），应解析成 ID。
	id, err := s.SelectNode("美国 01")
	if err != nil {
		t.Fatalf("SelectNode: %v", err)
	}
	if id == "" {
		t.Fatalf("SelectNode 返回空 ID")
	}

	req, err := s.BuildRequest()
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if req.SelectedNodeID != id {
		t.Errorf("请求里选中的节点 = %q，期望 %q", req.SelectedNodeID, id)
	}
	if len(req.Nodes) != 2 {
		t.Errorf("请求里节点数 = %d，期望 2", len(req.Nodes))
	}
	if req.Policy != compiler.PolicyRule {
		t.Errorf("默认路由模式应当映射为 PolicyRule，实际 %q", req.Policy)
	}
	if req.Options == nil {
		t.Errorf("请求必须带上选项模型，否则界面改的选项不会生效")
	}
	if req.Core != compiler.CoreSingBox {
		t.Errorf("核心 = %q，期望 sing-box", req.Core)
	}
}

// 未登录时拉订阅必须明确报「未登录」，而不是发出一个没有凭据的请求。
func TestRefreshWithoutLoginIsRejected(t *testing.T) {
	s := newTestSession(t, &fakePanel{}, &fakeFetcher{}, nil)

	_, err := s.RefreshSubscription(context.Background())
	if err == nil {
		t.Fatalf("未登录时拉订阅应当失败")
	}
	if err.Kind != KindNotAuthenticated {
		t.Errorf("分类 = %q，期望 %q", err.Kind, KindNotAuthenticated)
	}
}

// 没有节点时构造请求必须报「没有节点」，而不是给编译器一个空列表。
//
// 空列表会让编译器报「节点数据不完整」，与真正的原因（还没拉订阅）
// 相距很远，用户会去检查节点而不是去拉订阅。
func TestBuildRequestWithoutNodesIsRejected(t *testing.T) {
	ff := &fakeFetcher{results: []*subscription.FetchResult{{Nodes: nil}}}
	s := newTestSession(t, &fakePanel{}, ff, nil)
	ctx := context.Background()

	if _, err := s.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.BuildRequest(); err == nil {
		t.Fatalf("没有节点时构造请求应当失败")
	} else if err.Kind != KindNoNodes {
		t.Errorf("分类 = %q，期望 %q", err.Kind, KindNoNodes)
	}
}

// 构造出来的请求必须**真的能被编译器接受**。
//
// 这是一条回归测试，针对一个编译期看不出来的缺陷：
// 端口原先只在 TUN 模式下设置，而桌面端默认用系统代理（TUN 关），
// 于是 http/socks 端口都留在 0，编译器的 Request.validate() 直接拒绝
// （"invalid http port: 0"）——**每次点「连接」都会失败**。
//
// 断言「端口非零」是不够的：那只覆盖了这个具体表现。
// 这里直接把它交给 compiler.CompileSingBox，让校验器来说话——
// 那样任何一个会让请求被拒的字段都会在这里暴露，而不只是端口。
func TestBuiltRequestIsAcceptedByTheCompiler(t *testing.T) {
	ff := &fakeFetcher{results: []*subscription.FetchResult{{
		Nodes: []*domain.Node{node("A", "a.example.com", 443)},
	}}}
	s := newTestSession(t, &fakePanel{}, ff, nil)
	ctx := context.Background()

	if _, err := s.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}

	req, serr := s.BuildRequest()
	if serr != nil {
		t.Fatalf("BuildRequest: %v", serr)
	}

	// 默认接入方式是系统代理，TUN 是关的——这正是原先出错的那种组合。
	if s.Options().Inbound.TUN.Enabled {
		t.Fatalf("本用例要覆盖的正是「TUN 关闭」的情形，默认值不该是开启")
	}

	if _, _, err := compiler.CompileSingBox(req); err != nil {
		t.Fatalf("编译器拒绝了会话构造的请求，用户点「连接」会直接失败：%v", err)
	}
}

// 端口不可用时必须回退，而不是交出一个必然被拒绝的请求。
func TestBuiltRequestFallsBackToUsablePorts(t *testing.T) {
	ff := &fakeFetcher{results: []*subscription.FetchResult{{
		Nodes: []*domain.Node{node("A", "a.example.com", 443)},
	}}}
	s := newTestSession(t, &fakePanel{}, ff, nil)
	ctx := context.Background()

	if _, err := s.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}

	for _, port := range []int{0, -1, 65535, 70000} {
		s.UpdateOptions(func(o *domain.Options) { o.Inbound.MixedPort = port })
		req, serr := s.BuildRequest()
		if serr != nil {
			t.Fatalf("端口 %d 时 BuildRequest 不应失败：%v", port, serr)
		}
		if _, _, err := compiler.CompileSingBox(req); err != nil {
			t.Errorf("端口 %d 时编译器拒绝了请求：%v", port, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 凭据处理
// ---------------------------------------------------------------------------

// 凭据必须进入安全存储，且**不得**出现在会话的可打印形式里。
//
// 这条是安全属性，不是风格问题：一次 `%+v` 调试就能把会话凭据
// 写进终端历史与日志。
func TestCredentialsGoToStoreAndNeverPrint(t *testing.T) {
	store := secrets.NewMemoryStore()
	s := newTestSession(t, &fakePanel{
		loginSessions: []*panel.Session{{SessionToken: "Bearer SUPER-SECRET", SubscriptionToken: "subtok"}},
	}, &fakeFetcher{}, store)

	if _, err := s.Login(context.Background(), "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	got, gerr := store.Get(scopedKey(testPanelURL, secrets.KeyPanelSession))
	if gerr != nil {
		t.Fatalf("会话凭据应当已写入存储：%v", gerr)
	}
	if got != "Bearer SUPER-SECRET" {
		t.Errorf("存储里的会话凭据 = %q，期望原值", got)
	}
	if tok, _ := store.Get(scopedKey(testPanelURL, secrets.KeySubscriptionToken)); tok != "subtok" {
		t.Errorf("订阅 token 应当已写入存储，实际 %q", tok)
	}

	// 凭据必须**带上命名空间**：否则切换面板时旧服务器的凭据会被新主机读到。
	if _, gerr := store.Get(secrets.KeyPanelSession); !errors.Is(gerr, secrets.ErrNotFound) {
		t.Errorf("凭据不应以无命名空间的裸键存储，实际读取错误 %v", gerr)
	}
	printed := s.String()
	if strings.Contains(printed, "SUPER-SECRET") {
		t.Errorf("会话的可打印形式泄漏了凭据：%s", printed)
	}
	if strings.Contains(printed, "subtok") {
		t.Errorf("会话的可打印形式泄漏了订阅 token：%s", printed)
	}
}

// 登出必须清空内存状态**和**持久化凭据。
//
// 只清内存的话，下次启动 Restore 会把已经「登出」的会话又恢复回来。
func TestLogoutClearsMemoryAndStore(t *testing.T) {
	store := secrets.NewMemoryStore()
	ff := &fakeFetcher{results: []*subscription.FetchResult{{
		Nodes: []*domain.Node{node("A", "a.example.com", 443)},
	}}}
	s := newTestSession(t, &fakePanel{}, ff, store)
	ctx := context.Background()

	if _, err := s.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}
	if len(s.ListNodes()) == 0 {
		t.Fatalf("应当已有节点")
	}

	if err := s.Logout(); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	if s.LoggedIn() {
		t.Errorf("登出后 LoggedIn 应为 false")
	}
	if len(s.ListNodes()) != 0 {
		t.Errorf("登出后不应还有节点（订阅链接本身就是凭据，派生数据要一并清掉）")
	}
	if _, gerr := store.Get(scopedKey(testPanelURL, secrets.KeyPanelSession)); !errors.Is(gerr, secrets.ErrNotFound) {
		t.Errorf("登出后存储里的会话凭据应当已删除，实际错误 %v", gerr)
	}
	if _, gerr := store.Get(scopedKey(testPanelURL, secrets.KeySubscriptionURL)); !errors.Is(gerr, secrets.ErrNotFound) {
		t.Errorf("登出后存储里的订阅链接应当已删除，实际错误 %v", gerr)
	}

	// 登出后恢复应当什么也恢复不出来。
	info, rerr := s.Restore(ctx)
	if rerr != nil {
		t.Fatalf("Restore: %v", rerr)
	}
	if info != nil {
		t.Errorf("登出后 Restore 应当返回 nil，实际 %+v", info)
	}
}

// 没有保存过凭据时，Restore 返回 (nil, nil) —— 这不是错误。
//
// 首次启动就是这个情形。把它当错误会让第一屏就是一个红色提示。
func TestRestoreWithNoCredentialsIsNotAnError(t *testing.T) {
	s := newTestSession(t, &fakePanel{}, &fakeFetcher{}, secrets.NewMemoryStore())

	info, err := s.Restore(context.Background())
	if err != nil {
		t.Fatalf("没有凭据时 Restore 不应报错：%v", err)
	}
	if info != nil {
		t.Errorf("没有凭据时应当返回 nil，实际 %+v", info)
	}
}

// 会话失效时 Restore 必须清掉坏凭据并返回未登录。
//
// 不清的话界面会一直显示「已登录」，而每个操作都失败。
func TestRestoreClearsInvalidSession(t *testing.T) {
	store := secrets.NewMemoryStore()
	if err := store.Set(scopedKey(testPanelURL, secrets.KeyPanelSession), "Bearer STALE"); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	fp := &fakePanel{
		accountErrs: []*panel.Error{{Kind: panel.KindUnauthorized, MessageKey: "panel.unauthorized"}},
	}
	s := newTestSession(t, fp, &fakeFetcher{}, store)

	info, err := s.Restore(context.Background())
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if info != nil {
		t.Errorf("会话失效时应当返回 nil，实际 %+v", info)
	}
	if s.LoggedIn() {
		t.Errorf("会话失效后 LoggedIn 应为 false")
	}
	if _, gerr := store.Get(scopedKey(testPanelURL, secrets.KeyPanelSession)); !errors.Is(gerr, secrets.ErrNotFound) {
		t.Errorf("失效凭据应当已被清除，实际错误 %v", gerr)
	}
}

// 登录成功但账户被封时，仍应成功登录但如实报告 banned。
//
// 把封禁当成登录失败会让用户以为是密码错了，从而反复重试。
func TestLoginReportsBannedAccount(t *testing.T) {
	fp := &fakePanel{account: &panel.Account{Email: "u@example.com", Banned: 1}}
	s := newTestSession(t, fp, &fakeFetcher{}, nil)

	info, err := s.Login(context.Background(), "u@example.com", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !info.Banned {
		t.Errorf("被封禁的账户应当报告 Banned=true，实际 %+v", info)
	}
}

// 密码错误必须归类为「未授权」，并带上面板给的具体文案键。
func TestLoginInvalidCredentials(t *testing.T) {
	fp := &fakePanel{
		loginErrs: []*panel.Error{{Kind: panel.KindInvalidCredentials, MessageKey: "panel.invalid_credentials"}},
	}
	s := newTestSession(t, fp, &fakeFetcher{}, nil)

	_, err := s.Login(context.Background(), "u@example.com", "wrong")
	if err == nil {
		t.Fatalf("密码错误时登录应当失败")
	}
	if err.Kind != KindUnauthorized {
		t.Errorf("分类 = %q，期望 %q", err.Kind, KindUnauthorized)
	}
	if err.MessageKey != "panel.invalid_credentials" {
		t.Errorf("应当采用面板给的文案键，实际 %q", err.MessageKey)
	}
}

// ---------------------------------------------------------------------------
// 订阅刷新后已选节点消失
// ---------------------------------------------------------------------------

// 重新拉订阅后若已选节点不见了，必须清掉选择并提示。
//
// 留着不存在的 ID 会让编译器回退到第一个节点，于是界面显示 A、
// 实际连 B —— 这是最难查的一类问题。
func TestSelectedNodeGoneAfterRefreshIsCleared(t *testing.T) {
	ff := &fakeFetcher{results: []*subscription.FetchResult{
		{Nodes: []*domain.Node{
			node("日本 01", "jp1.example.com", 443),
			node("美国 01", "us1.example.com", 443),
		}},
		// 第二次订阅里「日本 01」消失了。
		{Nodes: []*domain.Node{node("美国 01", "us1.example.com", 443)}},
	}}
	s := newTestSession(t, &fakePanel{}, ff, nil)
	ctx := context.Background()

	if _, err := s.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.RefreshSubscription(ctx); err != nil {
		t.Fatalf("第一次 RefreshSubscription: %v", err)
	}
	jpID, err := s.SelectNode("日本 01")
	if err != nil {
		t.Fatalf("SelectNode: %v", err)
	}

	sub, err := s.RefreshSubscription(ctx)
	if err != nil {
		t.Fatalf("第二次 RefreshSubscription: %v", err)
	}

	if got := s.SelectedNodeID(); got != "" {
		t.Errorf("已选节点消失后应当清空选择，实际仍为 %q", got)
	}
	if sub.SelectedNodeID != "" {
		t.Errorf("订阅结果里的 SelectedNodeID 应当为空，实际 %q", sub.SelectedNodeID)
	}
	// 必须留下可见记录，否则用户只会看到「选中没了」而不知道原因。
	found := false
	for _, w := range sub.Warnings {
		if w == "session.warning.selected_node_gone" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("清掉已选节点时必须提示，实际 warnings = %v", sub.Warnings)
	}
	// 而且此时构造请求**不应**偷偷用别的节点。
	req, berr := s.BuildRequest()
	if berr != nil {
		t.Fatalf("BuildRequest: %v", berr)
	}
	if req.SelectedNodeID == jpID {
		t.Errorf("不应继续引用已消失的节点")
	}
}

// 已选节点仍在列表里时不应被清掉。
//
// 与上一个用例成对：否则「每次都清空选择」也能让那个用例通过。
func TestSelectedNodeSurvivesRefreshWhenStillPresent(t *testing.T) {
	nodes := []*domain.Node{
		node("日本 01", "jp1.example.com", 443),
		node("美国 01", "us1.example.com", 443),
	}
	ff := &fakeFetcher{results: []*subscription.FetchResult{{Nodes: nodes}, {Nodes: nodes}}}
	s := newTestSession(t, &fakePanel{}, ff, nil)
	ctx := context.Background()

	if _, err := s.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}
	id, err := s.SelectNode("日本 01")
	if err != nil {
		t.Fatalf("SelectNode: %v", err)
	}
	if _, err := s.RefreshSubscription(ctx); err != nil {
		t.Fatalf("第二次 RefreshSubscription: %v", err)
	}
	if got := s.SelectedNodeID(); got != id {
		t.Errorf("节点仍在列表里时选择应保留：期望 %q，实际 %q", id, got)
	}
}

// 重名节点必须报错而不是猜一个。
//
// 订阅里同名节点很常见（同一入口的不同倍率），猜错会让用户
// 连到自己不想连的节点。
func TestAmbiguousNodeNameIsRejected(t *testing.T) {
	ff := &fakeFetcher{results: []*subscription.FetchResult{{Nodes: []*domain.Node{
		node("香港", "hk1.example.com", 443),
		node("香港", "hk2.example.com", 443),
	}}}}
	s := newTestSession(t, &fakePanel{}, ff, nil)
	ctx := context.Background()

	if _, err := s.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}

	_, err := s.SelectNode("香港")
	if err == nil {
		t.Fatalf("重名节点应当报错而不是随便选一个")
	}
	if err.MessageKey != "session.error.node_name_ambiguous" {
		t.Errorf("文案键 = %q，期望 session.error.node_name_ambiguous", err.MessageKey)
	}
	// 但按 ID 选择仍然可行——这是界面解决重名的办法。
	nodes := s.ListNodes()
	if err := s.SelectNodeByID(nodes[0].ID); err != nil {
		t.Errorf("按 ID 选择重名节点应当成功：%v", err)
	}
}

// ---------------------------------------------------------------------------
// 选项
// ---------------------------------------------------------------------------

// BuildRequest 必须反映界面改过的选项，且不能把内部状态暴露出去。
func TestBuildRequestReflectsOptionsAndIsIsolated(t *testing.T) {
	ff := &fakeFetcher{results: []*subscription.FetchResult{{
		Nodes: []*domain.Node{node("A", "a.example.com", 443)},
	}}}
	s := newTestSession(t, &fakePanel{}, ff, nil)
	ctx := context.Background()

	if _, err := s.Login(ctx, "u@example.com", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := s.RefreshSubscription(ctx); err != nil {
		t.Fatalf("RefreshSubscription: %v", err)
	}

	// 界面切到全局代理。
	s.UpdateOptions(func(o *domain.Options) { o.RouteMode = domain.RouteGlobal })

	req, err := s.BuildRequest()
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if req.Policy != compiler.PolicyGlobal {
		t.Errorf("路由模式改成全局后，策略应当为 PolicyGlobal，实际 %q", req.Policy)
	}

	// 改请求里的选项不应影响会话内部状态（返回的是副本）。
	req.Options.Rules = append(req.Options.Rules, domain.Rule{Type: domain.RuleDomain, Value: "x.com"})
	if got := len(s.Options().Rules); got != 0 {
		t.Errorf("改动请求里的选项污染了会话状态：规则数 = %d，期望 0", got)
	}

	// Options() 返回的也必须是副本。
	o := s.Options()
	o.DNS.Nameservers = append(o.DNS.Nameservers, "1.1.1.1")
	if n := len(s.Options().DNS.Nameservers); n != 3 {
		t.Errorf("改动 Options() 的返回值污染了会话状态：名称服务器数 = %d，期望 3", n)
	}
}

// 客户端必须设置 flag=general 向面板要 URI 列表格式。
//
// 不设置的话服务端可能返回 Clash YAML，而那个格式本版本尚未实现，
// 用户会看到「格式不支持」而不是能用的节点列表。
func TestSessionRequestsURIFlag(t *testing.T) {
	// New 会构造真实的 Fetcher，这里只验证它的 Flag 被设上。
	s, err := New(Config{PanelURL: "https://panel.example.com"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f, ok := s.fetch.(*subscription.Fetcher)
	if !ok {
		t.Fatalf("内部 fetcher 类型 = %T，期望 *subscription.Fetcher", s.fetch)
	}
	if f.Flag != "general" {
		t.Errorf("订阅请求的 flag = %q，期望 general", f.Flag)
	}
}

// TrafficEntry **绝不能返回空串**。
//
// 这是一条回归测试，针对一个会静默失效的缺陷：
// 协调器的 planForMode("") 落在 default 分支，返回**空计划**，
// 也就是「不接管任何网络设置」。结果是核心起来了、状态显示「已连接」，
// 而系统代理根本没被改动——用户的流量一点都没走代理，
// 界面上却看不出任何异常。
//
// 界面只发「连接」、不发接入方式，所以这个值必须由服务端给出来。
func TestTrafficEntryIsNeverEmpty(t *testing.T) {
	// 尚未建立会话时也要给得出默认值，而不是空串。
	fresh := NewManager(Config{Store: secrets.NewMemoryStore()})
	if got := fresh.TrafficEntry(); got == "" {
		t.Errorf("没有会话时 TrafficEntry 不应返回空串（空串等于「不接管网络」）")
	}

	// 会话存在但选项里的值为空时同样要回退。
	ff := &fakeFetcher{results: []*subscription.FetchResult{{
		Nodes: []*domain.Node{node("A", "a.example.com", 443)},
	}}}
	s := newTestSession(t, &fakePanel{}, ff, nil)
	s.UpdateOptions(func(o *domain.Options) { o.Inbound.TrafficEntry = "" })

	m := NewManager(Config{Store: secrets.NewMemoryStore()})
	m.cur = s
	if got := m.TrafficEntry(); got == "" {
		t.Errorf("选项里的接入方式为空时应当回退到出厂默认，而不是返回空串")
	}

	// 用户改过就应当用用户的值。
	s.UpdateOptions(func(o *domain.Options) { o.Inbound.TrafficEntry = "tun" })
	if got := m.TrafficEntry(); got != "tun" {
		t.Errorf("TrafficEntry = %q，期望 tun", got)
	}
}

// 订阅链接模式：**不登录**也要能用。
//
// 这是一条回归测试，针对一个让整类用户完全无法使用客户端的缺陷：
// 此前 `subscriptionURL` 只能由 Login → GetSubscriptionDescriptor 产生，
// 于是「只有一个订阅链接、没有面板账号」的用户在订阅那一栏上走不通，
// 而错误只说「尚未登录」——他会去找一个自己根本没有的账号。
func TestSubscriptionURLAccessWithoutLogin(t *testing.T) {
	ctx := context.Background()
	ff := &fakeFetcher{results: []*subscription.FetchResult{{
		Nodes: []*domain.Node{
			node("A", "a.example.com", 443),
			node("B", "b.example.com", 443),
		},
	}}}
	s := newTestSession(t, &fakePanel{}, ff, nil)

	// 没有任何登录动作。
	if s.LoggedIn() {
		t.Fatalf("前提不成立：本用例要覆盖的正是「未登录」")
	}

	if serr := s.SetSubscriptionURL("https://panel.example.com/api/sub?token=SECRET"); serr != nil {
		t.Fatalf("SetSubscriptionURL: %v", serr)
	}

	info, serr := s.RefreshSubscription(ctx)
	if serr != nil {
		t.Fatalf("只有订阅链接时必须能拉取，实际失败：%v", serr)
	}
	if info.NodeCount != 2 {
		t.Errorf("节点数 = %d，期望 2", info.NodeCount)
	}
	if len(s.ListNodes()) != 2 {
		t.Errorf("ListNodes = %d", len(s.ListNodes()))
	}
}

// 两者都没有时，错误必须说清**缺的是什么**。
//
// 「未登录」与「没填订阅链接」对用户的下一步动作完全不同：
// 前者让他去找账号，后者让他去要一条链接。
func TestRefreshWithoutAnySourceExplainsWhatIsMissing(t *testing.T) {
	s := newTestSession(t, &fakePanel{}, &fakeFetcher{}, nil)
	_, serr := s.RefreshSubscription(context.Background())
	if serr == nil {
		t.Fatalf("既没登录也没订阅链接时应当失败")
	}
	if serr.MessageKey != "session.error.subscription_not_configured" {
		t.Errorf("MessageKey = %q，期望 subscription_not_configured（当前的提示会把用户引向一个他没有的账号）",
			serr.MessageKey)
	}
}

// 订阅链接必须做基本校验：空、非 http(s)、明文 http 都要拦。
func TestSetSubscriptionURLValidates(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"empty", "   ", "session.error.subscription_url_required"},
		{"no scheme", "panel.example.com/api", "session.error.subscription_url_invalid"},
		{"file scheme", "file:///etc/passwd", "session.error.subscription_url_invalid"},
		{"plain http", "http://panel.example.com/api", "session.error.subscription_url_insecure"},
		{"ok", "https://panel.example.com/api", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSession(t, &fakePanel{}, &fakeFetcher{}, nil)
			serr := s.SetSubscriptionURL(tc.url)
			if tc.want == "" {
				if serr != nil {
					t.Fatalf("应当接受，实际 %v", serr)
				}
				return
			}
			if serr == nil {
				t.Fatalf("应当拒绝 %q", tc.url)
			}
			if serr.MessageKey != tc.want {
				t.Errorf("MessageKey = %q，期望 %q", serr.MessageKey, tc.want)
			}
		})
	}
}

// 订阅链接是凭据：不得出现在 SessionState / 诊断输出里。
func TestSubscriptionURLNeverLeaksIntoStateOrSecretsOutput(t *testing.T) {
	const token = "VERY-SECRET-TOKEN"
	s := newTestSession(t, &fakePanel{}, &fakeFetcher{}, nil)
	if serr := s.SetSubscriptionURL("https://panel.example.com/api/sub?token=" + token); serr != nil {
		t.Fatalf("SetSubscriptionURL: %v", serr)
	}

	// 让订阅成功一次，STATE 才可能带上节点信息。
	ff := &fakeFetcher{results: []*subscription.FetchResult{{Nodes: []*domain.Node{node("A", "a.example.com", 443)}}}}
	s.fetch = ff
	if _, serr := s.RefreshSubscription(context.Background()); serr != nil {
		t.Fatalf("RefreshSubscription: %v", serr)
	}

	blob, err := json.Marshal(s.SubscriptionInfo())
	if err != nil {
		t.Fatalf("marshal subscription info: %v", err)
	}
	if strings.Contains(string(blob), token) {
		t.Errorf("订阅信息里出现了 token：%s", blob)
	}

	// Secrets() 是给脱敏器用的，它经手 token 是应该的；
	// 但**诊断用的 String()** 不行。
	if s := s.String(); strings.Contains(s, token) {
		t.Errorf("String() 里出现了订阅 token：%s", s)
	}
}
