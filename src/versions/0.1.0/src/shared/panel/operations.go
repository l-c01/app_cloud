package panel

import (
	"context"
	"encoding/json"
	"errors"
)

// Login 使用邮箱与密码登录，换取会话凭据。
//
// 对应：POST /api/v1/passport/auth/login
// 事实来源：src/references/Xboard-master/app/Http/Controllers/V1/Passport/AuthController.php:72-85
//
// 注意：
//   - 该操作非幂等，服务端每次登录都会新建一条 token 记录（app/Services/AuthService.php:21-25）。
//     调用方应避免无意义地重复登录。
//   - 返回的 Session 必须交给 SecretStore 保存，不得写入日志。
func (c *Client) Login(ctx context.Context, creds Credentials) (*Session, *Error) {
	if creds.Email == "" || creds.Password == "" {
		return nil, &Error{Op: opLogin, Kind: KindInvalidCredentials, Message: "email and password are required"}
	}

	body := map[string]string{
		"email":    creds.Email,
		"password": creds.Password,
	}

	env, perr := c.do(ctx, opLogin, "POST", pathLogin, body, "")
	if perr != nil {
		return nil, perr
	}

	var s Session
	if err := json.Unmarshal(env.Data, &s); err != nil {
		return nil, &Error{
			Op:            opLogin,
			Kind:          KindUnknown,
			Message:       "unexpected login response shape",
			ServerMessage: env.Message,
		}
	}
	if s.SessionToken == "" {
		return nil, &Error{
			Op:      opLogin,
			Kind:    KindUnknown,
			Message: "login succeeded but no session token was returned",
		}
	}
	return &s, nil
}

// GetSubscriptionDescriptor 获取当前用户的订阅链接。
//
// 对应：GET /api/v1/user/getSubscribe
// 事实来源：src/references/Xboard-master/app/Http/Controllers/V1/User/UserController.php:131-162
//
// session 必须是 Login 返回的 SessionToken（完整 "Bearer ..." 形式）。
//
// 实现约定：返回的 SubscribeURL 必须直接用于拉取订阅内容，
// 不要自行拼接 "/api/v1/client/subscribe?token=..."——服务端的拼接方式可能包含额外参数。
func (c *Client) GetSubscriptionDescriptor(ctx context.Context, session string) (*SubscriptionDescriptor, *Error) {
	env, perr := c.do(ctx, opSubscribe, "GET", pathSubscribe, nil, session)
	if perr != nil {
		return nil, perr
	}

	var d SubscriptionDescriptor
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return nil, &Error{
			Op:            opSubscribe,
			Kind:          KindUnknown,
			Message:       "unexpected subscription response shape",
			ServerMessage: env.Message,
		}
	}
	if d.SubscribeURL == "" {
		return nil, &Error{
			Op:      opSubscribe,
			Kind:    KindUnknown,
			Message: "panel returned an empty subscription URL",
		}
	}
	return &d, nil
}

// GetAccount 获取账户信息，同时用于校验会话是否仍然有效。
//
// 对应：GET /api/v1/user/info
// 事实来源：src/references/Xboard-master/app/Http/Controllers/V1/User/UserController.php:88-114
//
// 该端点无副作用，是会话探测的首选；失败分类为 KindUnauthorized 时应引导重新登录。
func (c *Client) GetAccount(ctx context.Context, session string) (*Account, *Error) {
	env, perr := c.do(ctx, opAccount, "GET", pathAccountInfo, nil, session)
	if perr != nil {
		return nil, perr
	}

	var a Account
	if err := json.Unmarshal(env.Data, &a); err != nil {
		return nil, &Error{
			Op:            opAccount,
			Kind:          KindUnknown,
			Message:       "unexpected account response shape",
			ServerMessage: env.Message,
		}
	}
	return &a, nil
}

// DiscoverPanel 探测给定地址是否为 Xboard 面板。
//
// 对应：GET /api/v1/guest/comm/config
// 事实来源：src/references/Xboard-master/app/Http/Controllers/V1/Guest/CommController.php
//
// 该请求**不携带任何凭据**。返回的字段可被服务端插件增删，
// 因此只做信封级校验，业务字段由调用方按需读取。
//
// 失败时 error.Kind 可用以区分：
//   - KindNotXboard：地址可达但不是 Xboard（非 JSON 或信封不符）；
//   - KindNetworkUnreachable：DNS/连接/TLS 失败；
//   - KindServerError：服务端 5xx。
func (c *Client) DiscoverPanel(ctx context.Context) (*PanelIdentity, *Error) {
	env, perr := c.do(ctx, opDiscover, "GET", pathGuestConfig, nil, "")
	if perr != nil {
		return nil, perr
	}

	id := &PanelIdentity{}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		// 面板未返回配置数据，但信封正确：仍视为识别成功。
		id.Raw = map[string]any{}
		return id, nil
	}
	if err := json.Unmarshal(env.Data, &id.Raw); err != nil {
		// data 不是对象（例如是数组或标量）：说明不是预期的 guest config 结构。
		return nil, &Error{
			Op:      opDiscover,
			Kind:    KindNotXboard,
			Message: "panel config is not a JSON object",
		}
	}
	return id, nil
}

// AsError 把 error 还原为 *Error，便于调用方在错误链中使用 errors.As 之外的简写。
// 若不是 *Error 则返回 nil。
func AsError(err error) *Error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	return nil
}
