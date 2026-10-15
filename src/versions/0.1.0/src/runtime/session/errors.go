// Package session 把「面板凭据 → 可连接的编译请求」这条链路收成一个对象。
//
// 为什么需要它：在这之前，协议层（shared/panel）、订阅层（shared/subscription）
// 与编译器（shared/compiler）各自都有实现与测试，但**没有人把它们串起来**。
// 界面拿到的是一份孤立的 harness 请求，而不是「用户登录后从订阅拿到的节点」。
// 一个「能用的客户端」必须把这条链路接通，而链路上有若干必须一起考虑的约束：
//
//   - 凭据只能进 secrets.Store，且**不得**出现在日志、错误或 IPC 报文里；
//   - 订阅链接本身就是凭据（含 token），因此它的脱敏规则与面板会话不同；
//   - 节点集合在「重新拉取订阅」时会整体替换，此时**已选节点可能消失**，
//     必须显式处理而不是让编译器回退到第一个节点（用户会以为选中没生效）；
//   - 选项模型（domain.Options）与节点集合要一起进入编译请求。
//
// 本包**不做网络接管、不启动核心**——那是 runtime/coordinator 的职责。
// 它的输出是一份 *compiler.Request，边界清晰到可以单独测试。
package session

import (
	"errors"
	"fmt"
	"strings"
)

// Error 是一次会话操作的失败，带稳定分类与文案键。
//
// 本地化约定（见 design/architecture/i18n-convention.md）：
// MessageKey 与 MessageArgs 是界面文案的唯一来源；Message 只是内部诊断文本。
// **Message 里绝不能放凭据**，哪怕是「诊断用」——它会被写进日志。
type Error struct {
	// Kind 是稳定分类，调用方据此分支，不解析 Message。
	Kind string
	// MessageKey 供 i18n 查表。
	MessageKey  string
	MessageArgs []any
	// Message 是内部诊断文本，不含凭据。
	Message string
	// FieldPath 便于界面把错误定位到具体输入框。
	FieldPath string
	// Cause 是底层错误，供 errors.Is / errors.As 使用。
	Cause error
}

func (e *Error) Error() string {
	if e.FieldPath != "" {
		return fmt.Sprintf("session: %s (%s): %s", e.Kind, e.FieldPath, e.detail())
	}
	return fmt.Sprintf("session: %s: %s", e.Kind, e.detail())
}

func (e *Error) detail() string {
	if e.Message != "" {
		return e.Message
	}
	if e.MessageKey != "" {
		return e.MessageKey
	}
	return e.Kind
}

// Unwrap 让 errors.Is / errors.As 能穿透到底层错误。
func (e *Error) Unwrap() error { return e.Cause }

// 稳定分类。刻意用字符串而不是 iota：
// 它们会进入 IPC 错误码与日志，需要可读且不随枚举顺序变化。
const (
	// KindInvalidConfig 表示调用方给的配置不成立（地址为空、格式错误等）。
	KindInvalidConfig = "invalid_config"
	// KindNotAuthenticated 表示还没登录，或会话已失效。
	KindNotAuthenticated = "not_authenticated"
	// KindAlreadyAuthenticated 表示已登录时又要求登录。
	KindAlreadyAuthenticated = "already_authenticated"
	// KindNoSubscription 表示尚未取得订阅（还没拉取，或订阅链接为空）。
	KindNoSubscription = "no_subscription"
	// KindNoNodes 表示订阅里没有任何可用节点。
	KindNoNodes = "no_nodes"
	// KindNodeNotFound 表示指定的节点不在当前节点集合里。
	KindNodeNotFound = "node_not_found"
	// KindUnsupportedFormat 表示订阅格式当前不支持（Clash YAML / sing-box JSON）。
	KindUnsupportedFormat = "unsupported_format"
	// KindStorageUnavailable 表示平台安全存储不可用，无法保存凭据。
	KindStorageUnavailable = "storage_unavailable"
	// KindNetwork 表示网络层失败。
	KindNetwork = "network"
	// KindUnauthorized 表示凭据被服务端拒绝。
	KindUnauthorized = "unauthorized"
)

// newError 构造一个带分类与文案键的错误。
func newError(kind, messageKey string) *Error {
	return &Error{Kind: kind, MessageKey: messageKey}
}

func (e *Error) withField(path string) *Error {
	e.FieldPath = path
	return e
}

func (e *Error) withDetail(format string, args ...any) *Error {
	e.Message = fmt.Sprintf(format, args...)
	return e
}

func (e *Error) withArgs(args ...any) *Error {
	e.MessageArgs = args
	return e
}

func (e *Error) withCause(err error) *Error {
	e.Cause = err
	return e
}

// MessageKeyFor 把错误映射到界面文案键。
//
// 与 MessageKeyFor 在别的包里的同名函数一致：界面不该逐个做类型断言。
// 返回空串表示不是本包的可本地化错误。
func MessageKeyFor(err error) string {
	if err == nil {
		return ""
	}
	var se *Error
	if !errors.As(err, &se) {
		return ""
	}
	return se.MessageKey
}

// ScrubURL 把订阅链接里的 token 抹掉，用于日志与诊断。
//
// 订阅链接**本身就是凭据**：它形如
// https://host/api/v1/client/subscribe?token=SECRET，
// 拿到它就能拉取该用户的全部节点。因此任何要落盘或进日志的场合
// 都必须先经过这里。
//
// 与 secrets.RedactURL 的分工：那个函数处理任意带凭据的 URL，
// 这个函数还额外处理 **路径里带 token** 的形态
// （/subscribe/SECRET 而不是 ?token=SECRET），面板两种都出现过。
func ScrubURL(raw string) string {
	if raw == "" {
		return ""
	}
	out := raw
	// ?token=xxx / &token=xxx
	if i := strings.Index(out, "?"); i >= 0 {
		base, query := out[:i], out[i+1:]
		query = scrubQuery(query)
		if query == "" {
			out = base
		} else {
			out = base + "?" + query
		}
	}
	return scrubPathToken(out)
}

// scrubQuery 把查询串里像 token 的参数值替换成占位符。
func scrubQuery(query string) string {
	parts := strings.Split(query, "&")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		key := p
		if i := strings.Index(p, "="); i >= 0 {
			key = p[:i]
		}
		switch strings.ToLower(key) {
		case "token", "password", "passwd", "secret", "auth", "key", "access_token":
			kept = append(kept, key+"=***")
		default:
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "&")
}

// scrubPathToken 处理 /subscribe/SECRET 这种把 token 放在路径里的形态。
//
// 只在段看起来像 token（足够长且不含点，即不像文件名或版本号）时才抹掉。
// 过宽的规则会把正常的路径也糊掉，那会让诊断信息变得没用。
func scrubPathToken(raw string) string {
	u := raw
	// 保留 scheme://host 部分。
	rest := u
	prefix := ""
	if i := strings.Index(u, "://"); i >= 0 {
		prefix = u[:i+3]
		rest = u[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			prefix += rest[:j]
			rest = rest[j:]
		} else {
			return u
		}
	}
	segs := strings.Split(rest, "/")
	for i, s := range segs {
		if i == 0 || s == "" {
			continue
		}
		if looksLikeToken(s) {
			segs[i] = "***"
		}
	}
	return prefix + strings.Join(segs, "/")
}

// looksLikeToken 判断一个路径段是否像凭据。
func looksLikeToken(s string) bool {
	if len(s) < 16 {
		return false
	}
	if strings.ContainsAny(s, ".@:") {
		return false
	}
	// 十六进制或 base64url 风格的长串。
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}
