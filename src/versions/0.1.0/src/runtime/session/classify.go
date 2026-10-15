package session

import (
	"proxyclient/shared/panel"
	"proxyclient/shared/subscription"
)

// 本文件把两个下游包的**错误词汇表**翻译成会话层的分类。
//
// 为什么不把下游的错误直接透出去：它们的分类是按「谁发现的」划分的
// （panel 区分 KindUnauthorized 与 KindInvalidSession，subscription 区分
// KindParse 与 KindUnsupportedFormat），而界面需要的分类是按
// **「用户该做什么」**划分的——重新登录、检查网络、换个节点、联系管理员。
// 两者不是一一对应，所以必须有人做这次翻译；放在这里比放在界面里好，
// 因为界面是两套（桌面 + Android），翻译只能有一份。

// classifyPanelError 翻译面板错误。
//
// fallbackKey 是分类无法更精确时使用的文案键。它由调用方给出，
// 因为同一个底层面板错误在不同动作下含义不同：
// KindUnauthorized 在「登录」时是「账号密码不对」，
// 在「刷新订阅」时却是「会话过期了，请重新登录」。
func classifyPanelError(err *panel.Error, fallbackKey string) *Error {
	if err == nil {
		return newError(KindUnknownKind, fallbackKey)
	}

	out := &Error{MessageKey: fallbackKey, Cause: err}
	if err.Message != "" {
		// 面板的诊断文本不含凭据（panel 包保证），可以留着便于定位。
		out.Message = err.Message
	}
	if err.HTTPStatus > 0 {
		out.MessageArgs = []any{err.HTTPStatus}
	}

	switch err.Kind {
	case panel.KindInvalidCredentials:
		out.Kind = KindUnauthorized
		out.MessageKey = "session.error.invalid_credentials"
	case panel.KindUnauthorized, panel.KindInvalidSession:
		out.Kind = KindUnauthorized
		// 保留 fallback：登录与刷新订阅对「未授权」的措辞不同。
		if err.Kind == panel.KindInvalidSession {
			out.MessageKey = "session.error.session_expired"
		}
	case panel.KindForbidden:
		out.Kind = KindUnauthorized
		out.MessageKey = "session.error.forbidden"
	case panel.KindSubscriptionUnavailable:
		out.Kind = KindNoSubscription
		out.MessageKey = "session.error.subscription_unavailable"
	case panel.KindNetworkUnreachable:
		out.Kind = KindNetwork
		out.MessageKey = "session.error.network"
	case panel.KindNotXboard:
		// 地址能通但不是面板：这不是网络问题，改网络设置没有用。
		out.Kind = KindInvalidConfig
		out.MessageKey = "session.error.not_xboard"
		out.FieldPath = "panelUrl"
	case panel.KindRateLimited:
		out.Kind = KindNetwork
		out.MessageKey = "session.error.rate_limited"
	case panel.KindServerError:
		out.Kind = KindNetwork
		out.MessageKey = "session.error.server_error"
	default:
		out.Kind = KindNetwork
	}

	// 面板自己已经给了文案键时以它为准：它比本层的兜底更具体。
	if err.MessageKey != "" {
		out.MessageKey = err.MessageKey
	}
	return out
}

// classifySubscriptionError 翻译订阅拉取错误。
func classifySubscriptionError(err *subscription.Error) *Error {
	if err == nil {
		return newError(KindUnknownKind, "session.error.subscribe_failed")
	}

	out := &Error{
		MessageKey:  "session.error.subscribe_failed",
		MessageArgs: err.MessageArgs,
		Cause:       err,
	}
	if err.Message != "" {
		out.Message = err.Message
	}

	switch err.Kind {
	case subscription.KindUnauthorized:
		out.Kind = KindUnauthorized
	case subscription.KindUnavailable:
		out.Kind = KindNoSubscription
	case subscription.KindNetwork:
		out.Kind = KindNetwork
	case subscription.KindTooLarge:
		out.Kind = KindNetwork
	case subscription.KindUnsupportedFormat:
		// 订阅格式不支持是**本端的实现缺口**，不是用户配置错误。
		// 分类独立出来，界面才能给出「换个订阅格式」这类可执行建议，
		// 而不是让用户去反复检查订阅地址。
		out.Kind = KindUnsupportedFormat
	case subscription.KindParse:
		out.Kind = KindNoNodes
	default:
		out.Kind = KindUnknownKind
	}

	// 下游的文案键更具体（例如 subscription.no_nodes），优先采用。
	if err.MessageKey != "" {
		out.MessageKey = err.MessageKey
	}
	return out
}

// KindUnknownKind 表示错误确实无法归类。
//
// 它必须与「网络错误」分开：把未知错误说成网络问题会让用户
// 反复重试一个永远不会成功的操作。
const KindUnknownKind = "unknown"
