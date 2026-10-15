package session

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"proxyclient/shared/domain"
)

// Manager 持有「当前是哪台面板的会话」，并在面板地址变化时切换。
//
// 为什么需要它：Session 在构造时就绑定了面板地址（凭据按地址做命名空间隔离），
// 而用户可以在界面上随时改地址。没有这一层的话，调用方要么在每次登录时
// 重建会话（丢掉已拉取的节点），要么就得自己判断地址有没有变——
// 而判断错了的后果是**把新面板的凭据写进旧面板的命名空间**，
// 那会让两台服务器的登录状态互相污染，且很难排查。
//
// Manager 是并发安全的：IPC 服务同时服务多个界面窗口。
type Manager struct {
	mu  sync.Mutex
	cfg Config
	cur *Session
	// panelURL 是当前会话绑定的地址（规范化前，保留原始写法便于回显）。
	panelURL string
}

// NewManager 构造管理器。此时还没有会话——面板地址要等用户填写。
func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg}
}

// PanelURL 返回当前面板地址；未设置时为空串。
func (m *Manager) PanelURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.panelURL
}

// Session 返回当前会话；未设置面板地址时返回 nil。
//
// 调用方必须先判空：`New` 需要一个面板地址，而首次启动时并没有。
func (m *Manager) Session() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur
}

// EnsureSession 返回绑定到 panelURL 的会话，必要时新建。
//
// 地址没变时复用现有会话，这样已拉取的节点与用户选择都保留下来；
// 地址变了则新建——**绝不复用**，否则新面板的凭据会写进旧面板的命名空间。
//
// 地址比较用规范化后的形式（见 normalizeNamespace），这样
// "https://a.com/" 与 "https://A.com" 被视为同一台，不会因为写法
// 不同而让用户「刚登录又要重新登录」。
func (m *Manager) EnsureSession(panelURL string, allowInsecureHTTP bool) (*Session, *Error) {
	raw := strings.TrimSpace(panelURL)
	if raw == "" {
		return nil, newError(KindInvalidConfig, "session.error.panel_url_required").
			withField("panelUrl")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cur != nil && normalizeNamespace(m.panelURL) == normalizeNamespace(raw) &&
		m.cur.cfg.AllowInsecureHTTP == allowInsecureHTTP {
		return m.cur, nil
	}

	cfg := m.cfg
	cfg.PanelURL = raw
	cfg.AllowInsecureHTTP = allowInsecureHTTP

	s, err := New(cfg)
	if err != nil {
		// New 返回的可能是本包的 *Error，也可能是 panel 包在
		// 构造客户端时给的普通 error（例如地址格式非法）。
		// 前者要原样带出去以保留具体分类，后者统一归为「地址无效」——
		// 在构造阶段，除了地址问题没有别的可能。
		var se *Error
		if errors.As(err, &se) {
			return nil, se
		}
		return nil, newError(KindInvalidConfig, "session.error.panel_url_invalid").
			withField("panelUrl").
			withDetail("%v", err).
			withCause(err)
	}
	m.cur = s
	m.panelURL = raw
	return s, nil
}

// SetHTTPClient 在构造会话之前注入 HTTP 客户端。
//
// 存在的理由：服务进程想让面板请求与订阅请求共用一份带超时与代理
// 设置的客户端，而那必须在建会话之前给进去。
func (m *Manager) SetHTTPClient(hc *http.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.HTTPClient = hc
}

// Forget 丢弃当前会话（例如用户改地址后要求重新登录）。
//
// 它**不**删除安全存储里的凭据：那是 Logout 的职责。
// 两者分开是因为「忘掉内存里的会话」与「清除保存的凭据」
// 对用户的含义完全不同。
func (m *Manager) Forget() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cur = nil
	m.panelURL = ""
}

// SecureStorage 报告本平台是否具备安全存储能力。
//
// 界面据此提示「本次可用，但不会记住登录」：不提示的话，
// 用户会以为下次启动仍是登录状态，然后在那天发现要重新登录。
func (m *Manager) SecureStorage() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.Store == nil {
		return false
	}
	return m.cfg.Store.Available()
}

// ErrNotConfigured 是「还没设置面板地址」时的标准错误。
//
// 单独提供它而不是让调用方自己构造，是为了让所有入口给出**同一句话**。
// 同一种情况在不同操作里显示不同文案，会让用户以为遇到了多个问题。
func ErrNotConfigured() *Error {
	return newError(KindInvalidConfig, "session.error.panel_url_required").
		withField("panelUrl").
		withDetail("no panel session is configured yet")
}

// TrafficEntry 返回会话选项里配置的流量接入方式。
//
// 为什么需要它：协调器按接入方式决定要接管哪些系统设置，而
// **空字符串会落到「不接管任何设置」**——核心起来了、状态显示「已连接」，
// 但系统代理根本没被改动，流量一点都没走代理，而且看起来一切正常。
//
// 界面只发「连接」，不发接入方式，因此这个值必须由服务端从选项模型里取。
//
// 会话尚未建立、或选项里的值为空时，回退到**出厂默认**而不是返回空串：
// 返回空串等于悄悄退化成「不接管」，那正是这条注释要防的事。
func (m *Manager) TrafficEntry() string {
	m.mu.Lock()
	sess := m.cur
	m.mu.Unlock()

	fallback := domain.DefaultOptions().Inbound.TrafficEntry
	if sess == nil {
		return fallback
	}
	if entry := sess.Options().Inbound.TrafficEntry; entry != "" {
		return entry
	}
	return fallback
}
