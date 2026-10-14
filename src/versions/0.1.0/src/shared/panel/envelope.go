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
	// KindUnknown 未归类的失败。
	KindUnknown ErrorKind = "unknown"
)

// Error 是面板操作失败的结构化错误。
//
// 调用方应依据 Kind 决策，而不是解析 Message。
// ServerMessage 仅供诊断展示，可能包含服务端本地化文案。
type Error struct {
	Kind          ErrorKind
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
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("panel: %s failed: kind=%s http=%d: %s", e.Op, e.Kind, e.HTTPStatus, e.Message)
	}
	return fmt.Sprintf("panel: %s failed: kind=%s: %s", e.Op, e.Kind, e.Message)
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
