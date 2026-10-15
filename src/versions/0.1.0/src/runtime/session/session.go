package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"proxyclient/shared/compiler"
	"proxyclient/shared/domain"
	"proxyclient/shared/panel"
	"proxyclient/shared/secrets"
	"proxyclient/shared/subscription"
)

// Subscriber 是本包对面板客户端的最小依赖。
//
// 用接口而不是直接依赖 *panel.Client，原因有二：
//   - 测试里可以注入假面板，不必起 HTTP 服务；
//   - 面板接口将来若增加端点，本包不该被迫一起改。
//
// 注意 Login 的返回值只用于取凭据，**不得**被写进日志。
type Subscriber interface {
	Login(ctx context.Context, creds panel.Credentials) (*panel.Session, *panel.Error)
	GetSubscriptionDescriptor(ctx context.Context, session string) (*panel.SubscriptionDescriptor, *panel.Error)
	GetAccount(ctx context.Context, session string) (*panel.Account, *panel.Error)
}

// Fetcher 是本包对订阅拉取器的最小依赖。
type Fetcher interface {
	Fetch(ctx context.Context, subscribeURL string) (*subscription.FetchResult, *subscription.Error)
}

// Config 是构造 Session 所需的全部依赖。
type Config struct {
	// PanelURL 是用户填写的面板地址，例如 https://panel.example.com。
	PanelURL string
	// Store 是凭据存储。为 nil 时使用 secrets.NewUnavailableStore()：
	// 那样仍可用，但**不会记住任何凭据**，界面必须据此提示用户。
	Store secrets.Store
	// HTTPClient 供面板与订阅共用；为 nil 时各自用默认值。
	HTTPClient *http.Client
	// AllowInsecureHTTP 允许 http:// 面板地址，仅供本地测试。
	// 打开时界面必须明确展示「正在使用不安全连接」。
	AllowInsecureHTTP bool
	// UserAgent 用于订阅请求。
	UserAgent string
	// Now 便于测试注入时间；为 nil 时用 time.Now。
	Now func() time.Time
}

// Session 持有一次「面板会话 + 订阅 + 选项」的完整状态。
//
// 并发约定：所有导出方法都加锁。IPC 服务是多连接并发的，
// 而「登录」与「拉订阅」可能同时被两个界面窗口触发。
type Session struct {
	mu    sync.Mutex
	cfg   Config
	store *secrets.NamespacedStore
	panel Subscriber
	fetch Fetcher
	now   func() time.Time

	// sessionToken 是面板会话凭据（形如 "Bearer xxx"）。
	sessionToken string
	// subscriptionURL 是完整订阅链接（含 token）——**本身就是凭据**。
	subscriptionURL string
	// nodes 是最近一次成功拉取的节点。
	nodes []*domain.Node
	// warnings 是最近一次解析的可容忍问题。
	warnings []string
	// rejectedCount 是最近一次被跳过的条目数。
	rejectedCount int
	// userInfo 是服务端下发的用量。
	userInfo subscription.UserInfo
	// selectedNodeID 是用户选定的节点。
	selectedNodeID string
	// account 是最近一次取到的账户信息（可能为 nil）。
	account *panel.Account
	// planName 来自订阅描述（getSubscribe），不是 user/info。
	planName string
	// options 是界面上的选项模型。
	options domain.Options
	// lastSync 是最近一次成功拉取订阅的时间。
	lastSync time.Time
	// loggedIn 表示是否已持有会话。
	loggedIn bool
}

// New 构造一个会话。
//
// 它不发起任何网络请求：登录与拉订阅都是显式动作。
// 这样做的原因是「打开软件就连面板」会让离线用户看到一个莫名其妙的错误，
// 而他们只是还没登录。
func New(cfg Config) (*Session, error) {
	if strings.TrimSpace(cfg.PanelURL) == "" {
		return nil, newError(KindInvalidConfig, "session.error.panel_url_required").
			withField("panelUrl")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if cfg.Store == nil {
		cfg.Store = secrets.NewUnavailableStore()
	}
	if strings.TrimSpace(cfg.UserAgent) == "" {
		cfg.UserAgent = "proxyclient"
	}

	// 面板地址先规范化再作为命名空间：凭据按服务器隔离，
	// 换面板不会读到上一台服务器的凭据。
	client, err := panel.NewClient(cfg.PanelURL, cfg.AllowInsecureHTTP, panel.WithHTTPClient(cfg.HTTPClient))
	if err != nil {
		return nil, newError(KindInvalidConfig, "session.error.panel_url_invalid").
			withField("panelUrl").
			withDetail("%v", err).
			withCause(err)
	}

	ns, err := secrets.NewNamespaced(cfg.Store, normalizeNamespace(cfg.PanelURL))
	if err != nil {
		return nil, newError(KindInvalidConfig, "session.error.panel_url_invalid").
			withField("panelUrl").
			withDetail("%v", err).
			withCause(err)
	}

	fetcher := subscription.NewFetcher(cfg.HTTPClient)
	fetcher.UserAgent = cfg.UserAgent
	// 显式向 Xboard 请求节点 URI 列表（flag=general）。
	//
	// 不请求的话服务端可能返回 Clash YAML，而那个格式本版本尚未实现；
	// 与其让用户遇到「格式不支持」，不如一开始就要求服务端给能用的格式。
	fetcher.Flag = "general"

	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &Session{
		cfg:     cfg,
		store:   ns,
		panel:   client,
		fetch:   fetcher,
		now:     nowFn,
		options: domain.DefaultOptions(),
	}, nil
}

// normalizeNamespace 把面板地址化成命名空间。
//
// 去掉 scheme 的大小写差异、结尾斜杠与默认端口，避免同一台面板
// 因为写法不同而分裂成两个命名空间——那会让用户「明明登录过却要重新登录」。
func normalizeNamespace(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.ToLower(s)
	return s
}

// ---------------------------------------------------------------------------
// 登录 / 登出
// ---------------------------------------------------------------------------

// Login 用邮箱与密码登录，并保存会话凭据。
//
// 成功后**不会**自动拉取订阅：拉取是另一个显式动作，因为它可能很慢，
// 而且失败原因（面板不可达 vs 订阅不可用）应当分别呈现。
func (s *Session) Login(ctx context.Context, email, password string) (*AccountInfo, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(email) == "" || password == "" {
		return nil, newError(KindInvalidConfig, "session.error.credentials_required").
			withField("credentials")
	}

	sess, perr := s.panel.Login(ctx, panel.Credentials{Email: email, Password: password})
	if perr != nil {
		return nil, classifyPanelError(perr, "session.error.login_failed")
	}
	if sess == nil || sess.SessionToken == "" {
		return nil, newError(KindUnauthorized, "session.error.login_failed").
			withDetail("panel returned an empty session token")
	}

	s.sessionToken = sess.SessionToken
	s.loggedIn = true

	// 凭据落盘。存不下不是致命错误（用户这次仍能用），
	// 但必须让调用方知道「下次启动要重新登录」。
	var storeErr *Error
	if err := s.store.Set(secrets.KeyPanelSession, sess.SessionToken); err != nil {
		storeErr = newError(KindStorageUnavailable, "session.error.store_failed").
			withDetail("%v", err).
			withCause(err)
	}
	if sess.SubscriptionToken != "" {
		_ = s.store.Set(secrets.KeySubscriptionToken, sess.SubscriptionToken)
	}

	// 立刻取一次账户信息：登录成功但账户被封/过期是常见情况，
	// 这时候告诉用户「登录成功」会误导。
	acct, aerr := s.panel.GetAccount(ctx, s.sessionToken)
	if aerr != nil {
		// 取账户失败不影响已经拿到的会话，但会话是否可用存疑，
		// 因此只在错误确实是「未授权」时才让登录失败。
		if aerr.Kind == panel.KindUnauthorized {
			s.sessionToken = ""
			s.loggedIn = false
			_ = s.store.Delete(secrets.KeyPanelSession)
			return nil, classifyPanelError(aerr, "session.error.unauthorized")
		}
	} else {
		s.account = acct
	}

	info := s.accountInfoLocked()
	if storeErr != nil {
		// 把「凭据没存下」作为警告随结果返回，而不是让登录失败。
		info.Warnings = append(info.Warnings, storeErr.MessageKey)
	}
	return info, nil
}

// Logout 清除内存与持久化的凭据。
//
// 节点与订阅链接一并清掉：它们都是从凭据派生的，留着没有意义，
// 而且订阅链接本身就是凭据。
func (s *Session) Logout() *Error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionToken = ""
	s.subscriptionURL = ""
	s.nodes = nil
	s.warnings = nil
	s.rejectedCount = 0
	s.userInfo = subscription.UserInfo{}
	s.selectedNodeID = ""
	s.account = nil
	s.loggedIn = false
	s.lastSync = time.Time{}

	var firstErr error
	for _, k := range []secrets.Key{
		secrets.KeyPanelSession,
		secrets.KeySubscriptionURL,
		secrets.KeySubscriptionToken,
		secrets.KeyNodeUUID,
	} {
		if err := s.store.Delete(k); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return newError(KindStorageUnavailable, "session.error.store_failed").
			withDetail("%v", firstErr).
			withCause(firstErr)
	}
	return nil
}

// Restore 尝试用已保存的凭据恢复会话。
//
// 返回 (nil, nil) 表示「没有可恢复的凭据」，这不是错误——
// 首次启动就是这个情形，界面应当显示登录页而不是报错。
func (s *Session) Restore(ctx context.Context) (*AccountInfo, *Error) {
	s.mu.Lock()
	token, err := s.store.Get(secrets.KeyPanelSession)
	s.mu.Unlock()

	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return nil, nil
		}
		return nil, newError(KindStorageUnavailable, "session.error.store_read_failed").
			withDetail("%v", err).
			withCause(err)
	}
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}

	s.mu.Lock()
	s.sessionToken = token
	s.loggedIn = true
	if u, uerr := s.store.Get(secrets.KeySubscriptionURL); uerr == nil {
		s.subscriptionURL = u
	}
	s.mu.Unlock()

	// 校验会话是否仍然有效。失败即视为未登录并把坏凭据清掉，
	// 否则界面会一直显示「已登录」而每个操作都失败。
	acct, aerr := s.panel.GetAccount(ctx, token)
	if aerr != nil {
		if aerr.Kind == panel.KindUnauthorized || aerr.Kind == panel.KindInvalidCredentials {
			_ = s.Logout()
			return nil, nil
		}
		return nil, classifyPanelError(aerr, "session.error.account_failed")
	}

	s.mu.Lock()
	s.account = acct
	info := s.accountInfoLocked()
	s.mu.Unlock()
	return info, nil
}

// LoggedIn 报告当前是否持有会话。
func (s *Session) LoggedIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loggedIn
}

// ---------------------------------------------------------------------------
// 订阅
// ---------------------------------------------------------------------------

// RefreshSubscription 取订阅链接并拉取节点。
//
// 两步都做，因为它们总是一起发生：面板给的链接可能变化，
// 而订阅内容随时可能变。分成两个公开方法只会让调用方漏掉一步。
func (s *Session) RefreshSubscription(ctx context.Context) (*SubscriptionInfo, *Error) {
	s.mu.Lock()
	if !s.loggedIn || s.sessionToken == "" {
		s.mu.Unlock()
		return nil, newError(KindNotAuthenticated, "session.error.not_authenticated")
	}
	token := s.sessionToken
	knownURL := s.subscriptionURL
	s.mu.Unlock()

	subURL := knownURL
	if subURL == "" {
		desc, perr := s.panel.GetSubscriptionDescriptor(ctx, token)
		if perr != nil {
			return nil, classifyPanelError(perr, "session.error.subscribe_descriptor_failed")
		}
		subURL = desc.SubscribeURL

		s.mu.Lock()
		s.subscriptionURL = subURL
		if desc.Plan != nil {
			s.planName = desc.Plan.Name
		}
		s.mu.Unlock()

		// 订阅链接是凭据，必须存进安全存储，不能写进普通配置文件。
		_ = s.store.Set(secrets.KeySubscriptionURL, subURL)
		if desc.SubscriptionToken != "" {
			_ = s.store.Set(secrets.KeySubscriptionToken, desc.SubscriptionToken)
		}
		if desc.UUID != "" {
			_ = s.store.Set(secrets.KeyNodeUUID, desc.UUID)
		}
	}

	res, ferr := s.fetch.Fetch(ctx, subURL)
	if ferr != nil {
		return nil, classifySubscriptionError(ferr)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes = res.Nodes
	s.warnings = res.Warnings
	s.rejectedCount = res.RejectedCount
	s.userInfo = res.UserInfo
	s.lastSync = s.now()

	// 重新拉取后原先选中的节点可能已经不在列表里。
	//
	// 这不是罕见情况：订阅里换了入口、节点改名都会发生。
	// 必须显式清掉选择，而不是留着一个不存在的 ID 让编译器
	// 悄悄回退到第一个节点——那会让界面显示 A 而实际连 B。
	if s.selectedNodeID != "" && s.findNodeLocked(s.selectedNodeID) == nil {
		s.selectedNodeID = ""
		// 只有当列表非空时才提示；空列表会另外报 no_nodes。
		if len(s.nodes) > 0 {
			s.warnings = append(s.warnings, "session.warning.selected_node_gone")
		}
	}

	return s.subscriptionInfoLocked(), nil
}

// SubscriptionInfo 描述一次订阅拉取的结果。
type SubscriptionInfo struct {
	// NodeCount 是可用节点数。
	NodeCount int `json:"nodeCount"`
	// RejectedCount 是解析失败被跳过的条目数。
	RejectedCount int `json:"rejectedCount"`
	// Warnings 是可容忍问题的文案键列表。
	Warnings []string `json:"warnings,omitempty"`
	// SelectedNodeID 是当前选中节点；为空表示未选。
	SelectedNodeID string `json:"selectedNodeId,omitempty"`
	// LastSyncUnix 是最近一次成功拉取的时间。
	LastSyncUnix int64 `json:"lastSyncUnix,omitempty"`
	// Usage 是服务端下发的用量。
	Usage UsageInfo `json:"usage"`
}

// UsageInfo 是账户用量。
//
// 字段都来自订阅响应头，服务端可能不提供；Present 为 false 时
// 界面必须隐藏这一块，而不是显示 0——「用量 0」与「不知道用量」是两件事。
type UsageInfo struct {
	Present    bool  `json:"present"`
	Upload     int64 `json:"upload"`
	Download   int64 `json:"download"`
	Total      int64 `json:"total"`
	ExpireUnix int64 `json:"expireUnix,omitempty"`
	Remaining  int64 `json:"remaining,omitempty"`
}

// subscriptionInfoLocked 组装订阅结果。调用方必须已持锁。
func (s *Session) subscriptionInfoLocked() *SubscriptionInfo {
	u := UsageInfo{
		Present:    s.userInfo.Present,
		Upload:     s.userInfo.Upload,
		Download:   s.userInfo.Download,
		Total:      s.userInfo.Total,
		ExpireUnix: s.userInfo.Expire,
	}
	if u.Total > 0 {
		u.Remaining = u.Total - s.userInfo.Used()
		if u.Remaining < 0 {
			u.Remaining = 0
		}
	}
	info := &SubscriptionInfo{
		NodeCount:      len(s.nodes),
		RejectedCount:  s.rejectedCount,
		SelectedNodeID: s.selectedNodeID,
		Usage:          u,
	}
	// 复制切片：调用方拿到的结果不应随后续刷新而改变。
	info.Warnings = append(info.Warnings, s.warnings...)
	if !s.lastSync.IsZero() {
		info.LastSyncUnix = s.lastSync.Unix()
	}
	return info
}

// ---------------------------------------------------------------------------
// 节点
// ---------------------------------------------------------------------------

// NodeSummary 是一个节点的界面表示。
//
// **刻意不含任何凭据字段**：节点里有 password / uuid，
// 而这份结构会被原样送进 IPC 报文交给界面。
type NodeSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Server   string `json:"server"`
	Port     int    `json:"port"`
	// Selected 标记当前选中项，省得界面自己比对。
	Selected bool `json:"selected"`
	// DelayMillis 是延迟测试结果；尚未测过时为 nil。
	DelayMillis *int `json:"delayMillis,omitempty"`
}

// ListNodes 返回当前节点集合的界面表示。
func (s *Session) ListNodes() []NodeSummary {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]NodeSummary, 0, len(s.nodes))
	for _, n := range s.nodes {
		if n == nil {
			continue
		}
		out = append(out, NodeSummary{
			ID:       n.ID,
			Name:     n.Name,
			Protocol: string(n.Protocol),
			Server:   n.Server,
			Port:     n.Port,
			Selected: n.ID == s.selectedNodeID,
		})
	}
	return out
}

// NodeIDByRef 把界面上的一种「节点引用」解析成节点 ID。
//
// 界面拿到的可能是名字（Clash 风格的界面习惯用名字），也可能是 ID。
// 允许两种是为了让界面不必自己维护名字到 ID 的映射——那份映射会在
// 订阅刷新后变旧，而变旧的映射会让「点了一个节点却连到另一个」。
//
// 名字重复时返回错误而不是随便选一个：订阅里同名节点很常见
// （同一入口的不同倍率），猜错会让用户连到不想连的节点。
func (s *Session) NodeIDByRef(ref string) (string, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", newError(KindNodeNotFound, "session.error.node_not_found").
			withField("node")
	}
	// 先按 ID 精确匹配。
	if s.findNodeLocked(ref) != nil {
		return ref, nil
	}
	// 再按名字匹配。
	var matches []*domain.Node
	for _, n := range s.nodes {
		if n != nil && n.Name == ref {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		return "", newError(KindNodeNotFound, "session.error.node_not_found").
			withField("node").
			withArgs(ref).
			withDetail("no node with id or name %q", ref)
	case 1:
		return matches[0].ID, nil
	default:
		return "", newError(KindInvalidConfig, "session.error.node_name_ambiguous").
			withField("node").
			withArgs(ref).
			withDetail("name %q matches %d nodes; select by id instead", ref, len(matches))
	}
}

// SelectNode 选定一个节点并持久化。
//
// ref 可以是节点 ID 或名字；重名会报错而不是猜（见 NodeIDByRef）。
func (s *Session) SelectNode(ref string) (string, *Error) {
	id, err := s.NodeIDByRef(ref)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedNodeID = id
	return id, nil
}

// SelectNodeByID 只按 ID 选定，不做名字回退。
//
// 给「快速切换」这类已知 ID 的场景用：它不会因为订阅里恰好有个
// 同名节点而选错。
func (s *Session) SelectNodeByID(id string) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.findNodeLocked(id) == nil {
		return newError(KindNodeNotFound, "session.error.node_not_found").
			withField("node").
			withArgs(id).
			withDetail("no node with id %q", id)
	}
	s.selectedNodeID = id
	return nil
}

// SelectedNodeID 返回当前选中的节点 ID。
func (s *Session) SelectedNodeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectedNodeID
}

func (s *Session) findNodeLocked(id string) *domain.Node {
	for _, n := range s.nodes {
		if n != nil && n.ID == id {
			return n
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 选项
// ---------------------------------------------------------------------------

// Options 返回当前选项模型的一份副本。
func (s *Session) Options() domain.Options {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneOptions(s.options)
}

// SetOptions 整体替换选项模型。
func (s *Session) SetOptions(opts domain.Options) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options = cloneOptions(opts)
}

// UpdateOptions 在锁内就地修改选项，避免「读-改-写」之间的竞态。
//
// 界面经常只想改一项（比如拖动一个开关），用 SetOptions 就得先读再写，
// 两个窗口同时这样做会丢改动。
func (s *Session) UpdateOptions(mutate func(*domain.Options)) {
	if mutate == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mutate(&s.options)
}

// cloneOptions 深拷贝选项，避免调用方持有内部切片。
//
// 浅拷贝是不够的：Rules / Groups / RuleSets 都是切片，
// 界面改一个元素会直接改到会话内部的状态。
func cloneOptions(o domain.Options) domain.Options {
	c := o
	c.Groups = cloneSlice(o.Groups)
	c.Rules = cloneSlice(o.Rules)
	c.RuleSets = cloneSlice(o.RuleSets)
	c.DNS.Nameservers = cloneSlice(o.DNS.Nameservers)
	c.DNS.FakeIPFilter = cloneSlice(o.DNS.FakeIPFilter)
	c.Inbound.TUN.DNSAddress = cloneSlice(o.Inbound.TUN.DNSAddress)
	c.Inbound.PerApp.Packages = cloneSlice(o.Inbound.PerApp.Packages)
	c.Inbound.Sniffer.Protocols = cloneSlice(o.Inbound.Sniffer.Protocols)
	return c
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}

// ---------------------------------------------------------------------------
// 编译请求
// ---------------------------------------------------------------------------

// BuildRequest 依据当前会话状态构造一次编译请求。
//
// 这是本包的**核心产出**：协调器拿它去编译、写文件、启动核心。
//
// 几点约定：
//   - 没有节点时返回错误，而不是给一个空节点列表让编译器去报错——
//     那个错误会以「节点数据不完整」的形式出现，与真正的原因（没拉订阅）相距很远；
//   - 选中的节点若已不存在，返回错误而不是回退到第一个：静默换节点
//     会让「界面显示 A、实际连 B」，这是最难查的一类问题；
//   - 路由模式与策略在这里对齐：Options.RouteMode 是界面上的三选一，
//     而 Request.Policy 是编译器的入参。两处必须一致，否则界面显示
//     「规则模式」而配置里是全局代理。
func (s *Session) BuildRequest() (*compiler.Request, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.nodes) == 0 {
		return nil, newError(KindNoNodes, "session.error.no_nodes").
			withDetail("no nodes available; refresh the subscription first")
	}

	var selected *domain.Node
	if s.selectedNodeID != "" {
		selected = s.findNodeLocked(s.selectedNodeID)
		if selected == nil {
			return nil, newError(KindNodeNotFound, "session.error.selected_node_gone").
				withField("node").
				withArgs(s.selectedNodeID).
				withDetail("the selected node %q is no longer in the subscription", s.selectedNodeID)
		}
	}

	req := &compiler.Request{
		Core:   compiler.CoreSingBox,
		Nodes:  append([]*domain.Node(nil), s.nodes...),
		Policy: policyForRouteMode(s.options.RouteMode),
		Inbound: compiler.InboundOptions{
			Listen:         s.options.Inbound.Listen,
			AllowLanAccess: s.options.Inbound.AllowLAN,
		},
		LogLevel: s.options.App.LogLevel,
		Options:  optionsPtr(cloneOptions(s.options)),
	}
	if selected != nil {
		req.SelectedNodeID = selected.ID
	}

	// 端口映射：选项模型用的是「混合端口」，而编译请求当前仍接受
	// 分离的 http/socks 端口（见 HANDOFF 2.2b）。
	//
	// 这里暂时让两者取同一个值会让编译器拒绝（它要求两者不同），
	// 因此**只在明确配置了 TUN 时**才把混合端口交给它；
	// 其余情况沿用 0 让调用方自行填。这是一个已知的过渡状态，
	// 等混合入站落地后这里会消失。
	if s.options.Inbound.TUN.Enabled && s.options.Inbound.MixedPort > 0 {
		req.Inbound.HTTPPort = s.options.Inbound.MixedPort
		req.Inbound.SocksPort = s.options.Inbound.MixedPort + 1
	}

	return req, nil
}

// policyForRouteMode 把界面的路由模式映射到编译器的策略。
//
// 两者取值刻意同名（rule/global/direct），但仍是两套类型：
// 领域的取值给界面用，编译器的策略给生成器用。
// 让它们各自演进，比让一个类型同时承担两种职责更安全。
func policyForRouteMode(m domain.RouteMode) compiler.OutboundPolicy {
	switch m {
	case domain.RouteGlobal:
		return compiler.PolicyGlobal
	case domain.RouteDirect:
		return compiler.PolicyDirect
	default:
		return compiler.PolicyRule
	}
}

// optionsPtr 返回选项的指针，供 compiler.Request.Options 使用。
func optionsPtr(o domain.Options) *domain.Options { return &o }

// ---------------------------------------------------------------------------
// 账户
// ---------------------------------------------------------------------------

// AccountInfo 是登录/恢复后返回给界面的账户摘要。
//
// **不含密码、不含 panel 的原始 auth_data**：那些只留在安全存储里。
type AccountInfo struct {
	Email string `json:"email,omitempty"`
	UUID  string `json:"uuid,omitempty"`
	// Banned 表示账户被封。
	Banned bool `json:"banned"`
	// Expired 表示套餐已过期。
	Expired bool `json:"expired"`
	// ExpiredAtUnix 是到期时间；0 表示不过期。
	ExpiredAtUnix int64 `json:"expiredAtUnix,omitempty"`
	// TransferEnable 是套餐总流量。
	TransferEnable int64 `json:"transferEnable,omitempty"`
	// UploadedBytes / DownloadedBytes 是已用流量。
	UploadedBytes   int64 `json:"uploadedBytes,omitempty"`
	DownloadedBytes int64 `json:"downloadedBytes,omitempty"`
	// Balance 是账户余额。
	Balance float64 `json:"balance,omitempty"`
	// PlanName 是套餐名。
	PlanName string `json:"planName,omitempty"`
	// Warnings 是可容忍问题的文案键。
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Session) accountInfoLocked() *AccountInfo {
	info := &AccountInfo{}
	if s.account == nil {
		return info
	}
	a := s.account
	info.Email = a.Email
	info.UUID = a.UUID
	// 用 panel 自己的取值方法，而不是重新解释 Banned 数值与 ExpiredAt 的
	// nil/0 语义——那两条规则已经在那一侧写清楚并有测试，这里再写一遍
	// 只会多一处可能不一致的地方。
	info.Banned = a.IsBanned()
	info.Expired = a.IsExpired(s.now().Unix())
	info.TransferEnable = a.TransferEnable
	info.UploadedBytes = a.UploadedBytes
	info.DownloadedBytes = a.DownloadedBytes
	info.Balance = a.Balance
	if a.ExpiredAt != nil {
		info.ExpiredAtUnix = *a.ExpiredAt
	}
	return info
}

// PlanName 返回最近一次订阅描述里带回来的套餐名。
//
// 套餐信息在 getSubscribe（SubscriptionDescriptor）里，
// 不在 user/info 里，因此它是随拉订阅一起更新的，
// 而不是随取账户信息更新。
func (s *Session) PlanName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.planName
}

// Account 返回最近一次取到的账户摘要。
func (s *Session) Account() *AccountInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountInfoLocked()
}

// Scrubber 返回一个已登记全部已知凭据的日志脱敏器。
//
// 服务进程启动日志转发前应调用它，把节点密码、会话凭据、订阅链接
// 一次性登记进去；否则核心日志里一旦回显凭据就会被写进文件。
func (s *Session) Scrubber() *secrets.Scrubber {
	s.mu.Lock()
	defer s.mu.Unlock()

	sc := secrets.NewScrubber()
	sc.FromSession(s.sessionToken, "", s.subscriptionURL, "")
	for _, n := range s.nodes {
		if n == nil {
			continue
		}
		if n.Password != "" {
			sc.Add(n.Password)
		}
		if n.UUID != "" {
			sc.Add(n.UUID)
		}
	}
	return sc
}

// String 返回会话的可安全打印形式。
//
// 实现它是有意的：默认的 %v 不会打印字段，但 `%+v` 会，
// 而一次调试时的 %+v 就能把会话凭据写进终端历史。
// 提供这个方法让「安全地打印会话」成为可能。
func (s *Session) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("session{panel:%s nodes:%d selected:%q loggedIn:%t}",
		ScrubURL(s.cfg.PanelURL), len(s.nodes), s.selectedNodeID, s.loggedIn)
}
