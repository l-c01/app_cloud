// Package panel 实现 Xboard 面板的用户端接口适配。
//
// 范围（见 design/contracts/panel-api-contract.md）：
// 客户端核心链路是「登录 → 取订阅链接 → 编译核心配置 → 连接」，面板只承担前两步。
// 本包只实现 4 个接口：登录、取订阅链接、账户信息、面板身份探测。
// 工单、订单支付、邀请返佣、礼品卡、知识库等不在范围内，不应在本包出现。
//
// 安全约束：
//   - 登录返回的 auth_data 是会话凭据，token 是订阅 token，两者语义不同，不得混用。
//   - 订阅链接、订阅 token、账户 uuid 一律视为秘密，调用方必须交给 SecretStore 保存，
//     不得写入日志、崩溃报告或导出配置。
//   - 本包不负责持久化，只负责请求与解析。
package panel

import (
	"encoding/json"
	"fmt"
	"strings"
)

// envelope 是 Xboard 的统一响应信封。
//
// 事实来源：src/references/Xboard-master/app/Helpers/ApiResponse.php:44-55
// 响应体不含 code 字段（源码中被注释掉），HTTP 状态码取自内部 code 的前三位。
type envelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Error   json.RawMessage `json:"error"`
}

// isSuccess 判断信封是否表示成功。
//
// 注意：不能只看 HTTP 状态码。服务端成功时也可能带非 200 的状态，
// 失败时 HTTP 码来自业务 code，必须同时读 status。
func (e envelope) isSuccess() bool {
	return e.Status == "success"
}

// ErrorKind 是客户端对面板失败的分类结果。
//
// 设计意图：服务端的 message 文案会随面板语言与版本变化，
// 禁止用文案做程序判断，必须映射为稳定的分类。
type ErrorKind string

const (
	// KindUnauthorized 会话缺失或失效（HTTP 401/403 来自面板 API）。
	KindUnauthorized ErrorKind = "unauthorized"
	// KindForbidden 服务端明确拒绝（HTTP 403，通常为权限或状态不允许）。
	KindForbidden ErrorKind = "forbidden"
	// KindRateLimited 触发服务端限流（HTTP 429），调用方需退避。
	KindRateLimited ErrorKind = "rate_limited"
	// KindServerError 服务端内部错误（HTTP 5xx），面板业务暂不可用。
	KindServerError ErrorKind = "server_error"
	// KindNetworkUnreachable 网络层不可达（DNS、连接超时、TLS 失败）。
	KindNetworkUnreachable ErrorKind = "network_unreachable"
	// KindNotXboard 地址可达但不是 Xboard（响应不是 JSON 或信封结构不符）。
	KindNotXboard ErrorKind = "not_xboard"
	// KindInvalidCredentials 账号或密码错误。
	KindInvalidCredentials ErrorKind = "invalid_credentials"
	// KindSubscriptionUnavailable 订阅不可用（套餐到期、流量用尽、被封禁）。
	// 对应订阅接口返回 403 且 body 为空的情形。
	KindSubscriptionUnavailable ErrorKind = "subscription_unavailable"
	// KindInvalidSession 服务端明确回话说这个会话无效。
	//
	// 与 KindUnauthorized 的区别在**是谁发现的**：
	// KindInvalidSession 是服务端判定的（例如面板返回了一个表示会话无效的业务码），
	// KindUnauthorized 是本端判定或 HTTP 401 —— 包括「本地存的会话串缺少
	// "Bearer " 前缀」这种情形：调用方拼错了或配置被改坏，重新登录即可修好。
	KindInvalidSession ErrorKind = "invalid_session"
	// KindUnknown 未归类的失败。
	KindUnknown ErrorKind = "unknown"
)

// Error 是面板操作失败的结构化错误。
//
// 本地化约定（见 design/architecture/i18n-convention.md）：
//   - MessageKey 与 MessageArgs 是**界面文案的唯一来源**；
//     调用方用 bundle.T(e.MessageKey, e.MessageArgs...) 渲染；
//   - Message 只是内部诊断用的英文兜底，**不得**直接显示给用户。
//
// 这样把「文案」与「错误分类」解耦：分类用于程序判断，文案用于展示。
type Error struct {
	Kind          ErrorKind
	MessageKey    string
	MessageArgs   []any
	Message       string
	HTTPStatus    int
	ServerMessage string
	Retryable     bool
	// Op 是发生失败的操作名，便于定位（例如 "login"）。
	Op string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	detail := e.Message
	if detail == "" {
		detail = e.MessageKey
	}
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("panel: %s failed: kind=%s http=%d: %s", e.Op, e.Kind, e.HTTPStatus, detail)
	}
	return fmt.Sprintf("panel: %s failed: kind=%s: %s", e.Op, e.Kind, detail)
}

// newError 构造一个带文案键的错误。
//
// 所有构造点都走这里，避免某处忘记填 MessageKey 而让界面回退到英文兜底。
func newError(op string, kind ErrorKind, httpStatus int, serverMessage string) *Error {
	key, args := messageKeyFor(kind)
	return &Error{
		Op:            op,
		Kind:          kind,
		MessageKey:    key,
		MessageArgs:   args,
		Message:       diagnosticText(kind, httpStatus),
		HTTPStatus:    httpStatus,
		ServerMessage: serverMessage,
		Retryable:     retryableFor(kind),
	}
}

// messageKeyFor 返回错误分类对应的文案键与参数。
func messageKeyFor(kind ErrorKind) (string, []any) {
	switch kind {
	case KindUnauthorized:
		return "panel.unauthorized", nil
	case KindForbidden:
		return "panel.forbidden", nil
	case KindRateLimited:
		return "panel.rate_limited", nil
	case KindServerError:
		return "panel.server_error", nil
	case KindNetworkUnreachable:
		return "panel.network_unreachable", nil
	case KindNotXboard:
		return "panel.not_xboard", nil
	case KindInvalidCredentials:
		return "panel.invalid_credentials", nil
	case KindSubscriptionUnavailable:
		return "panel.subscription_unavailable", nil
	case KindInvalidSession:
		return "panel.session_malformed", nil
	default:
		return "panel.unknown", nil
	}
}

// withDetail 附加仅用于日志与诊断的细节文本。
//
// 细节**不会**出现在界面上（界面用 MessageKey 渲染），
// 它的作用是在日志里说清「到底哪一步失败了」。
func (e *Error) withDetail(format string, args ...any) *Error {
	e.Message = fmt.Sprintf(format, args...)
	return e
}

// diagnosticText 给出仅用于日志与诊断的英文描述。
//
// 它不会出现在界面上；界面一律用 MessageKey 渲染。
func diagnosticText(kind ErrorKind, httpStatus int) string {
	if httpStatus > 0 {
		return fmt.Sprintf("panel operation failed (kind=%s, http=%d)", kind, httpStatus)
	}
	return fmt.Sprintf("panel operation failed (kind=%s)", kind)
}

// retryableFor 判断某个分类是否值得重试。
//
// 约定：
//   - 限流与服务端错误可重试（需退避）；
//   - 网络不可达可重试；
//   - 认证、凭据、非 Xboard、订阅不可用不可重试（重试无意义，需用户介入）。
func retryableFor(kind ErrorKind) bool {
	switch kind {
	case KindRateLimited, KindServerError, KindNetworkUnreachable, KindUnknown:
		return true
	default:
		return false
	}
}

// hasBearerPrefix 判断字符串是否已经是完整的 Authorization 头值。
//
// 服务端在 auth_data 中已经带上 "Bearer " 前缀，调用方必须原样使用。
// 本函数用于在配置被误改时给出明确错误，而不是静默拼一个双前缀。
func hasBearerPrefix(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "Bearer ")
}
