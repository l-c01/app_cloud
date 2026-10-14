package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 端点路径常量。事实来源：src/references/Xboard-master/app/Http/Routes/V1/**。
// 修改这些常量等同于修改契约，必须同步 design/contracts/panel-api-contract.md。
const (
	pathAPIPrefix   = "/api"
	pathLogin       = "/api/v1/passport/auth/login"
	pathSubscribe   = "/api/v1/user/getSubscribe"
	pathAccountInfo = "/api/v1/user/info"
	pathGuestConfig = "/api/v1/guest/comm/config"
)

// maxResponseBytes 限制单个响应体大小，防止异常或恶意响应耗尽内存。
const maxResponseBytes = 4 << 20 // 4 MiB

// Client 是 Xboard 面板的只读/低风险操作客户端。
//
// 本类型不持有凭据：会话由调用方在每次请求时显式传入，
// 以便调用方把凭据集中在 SecretStore 中管理。
type Client struct {
	baseURL *url.URL
	http    *http.Client
	headers map[string]string
}

// Option 用于配置 Client。
type Option func(*Client) error

// WithHTTPClient 注入自定义 HTTP 客户端（用于测试或代理设置）。
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error {
		if hc == nil {
			return errors.New("panel: nil http client")
		}
		c.http = hc
		return nil
	}
}

// WithTimeout 设置请求超时。默认 15 秒。
func WithTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return errors.New("panel: timeout must be positive")
		}
		c.http.Timeout = d
		return nil
	}
}

// WithCustomHeaders 追加公共非敏感请求头。
//
// 安全约束：拒绝授权类请求头。认证必须走参数传入的会话，
// 不允许通过公共配置注入凭据（见 design/schemas/client-config.schema.json 对 customHeaders 的约束）。
func WithCustomHeaders(h map[string]string) Option {
	return func(c *Client) error {
		for k, v := range h {
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "authorization", "cookie", "proxy-authorization":
				return errors.New("panel: refusing credential-bearing header: " + k)
			}
			c.headers[k] = v
		}
		return nil
	}
}

// NewClient 构造面板客户端。
//
// baseURL 是用户填写的面板地址。约束：
//   - 必须是绝对 URL；
//   - 不允许在 URL 中携带用户名或密码（拒绝 http://user:pass@host）；
//   - 默认要求 https；仅当 allowInsecureHTTP 为 true 时允许 http（供本地测试），
//     调用方必须把该状态在界面上明确展示。
func NewClient(baseURL string, allowInsecureHTTP bool, opts ...Option) (*Client, error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return nil, errors.New("panel: empty base URL")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("panel: invalid base URL: " + err.Error())
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, errors.New("panel: base URL must be absolute, e.g. https://panel.example.com")
	}
	if u.User != nil {
		return nil, errors.New("panel: base URL must not contain credentials")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		// OK
	case "http":
		if !allowInsecureHTTP {
			return nil, errors.New("panel: plain HTTP requires explicit insecure testing mode")
		}
	default:
		return nil, errors.New("panel: unsupported scheme: " + u.Scheme)
	}

	// 去掉查询与片段，避免与端点路径冲突。
	u.RawQuery = ""
	u.Fragment = ""

	c := &Client{
		baseURL: u,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
		headers: make(map[string]string),
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// BaseURL 返回规范化后的面板基础地址（不含查询与片段）。
func (c *Client) BaseURL() string { return c.baseURL.String() }

// endpoint 把端点路径拼到基础地址上，保留用户在基础地址中填写的路径前缀。
func (c *Client) endpoint(p string) string {
	u := *c.baseURL
	base := strings.TrimSuffix(u.Path, "/")
	// 允许用户在 base 中填 /panel 之类的前缀。
	u.Path = base + p
	return u.String()
}

// do 执行一次请求并把响应解析为信封。
//
// authHeader 为空表示匿名请求；否则必须已是完整的 "Bearer xxx" 形式。
// 返回值：成功时为解析后的信封，失败时为 *Error。
//
// 所有错误都经 newError 构造，保证一定带上 MessageKey：
// 界面据此本地化，Message 只用于日志诊断。
func (c *Client) do(ctx context.Context, op, method, p string, body any, authHeader string) (*envelope, *Error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, newError(op, KindUnknown, 0, "").
				withDetail("encode request failed: %v", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(p), reader)
	if err != nil {
		return nil, newError(op, KindUnknown, 0, "").
			withDetail("build request failed: %v", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if authHeader != "" {
		if !hasBearerPrefix(authHeader) {
			// 明确报错而不是静默拼一个双前缀：这通常意味着配置被手工改坏。
			//
			// 分类用 KindUnauthorized 而不是 KindInvalidSession：
			// 这两者引导用户做的事不同——KindUnauthorized 让界面提示
			// 「请重新登录」，而重新登录确实会重新写入一份格式正确的凭据，
			// 问题就此消失。KindInvalidSession 的文案指向「会话已失效」，
			// 会让人以为是服务端的问题，于是反复重试而不是重新登录。
			return nil, newError(op, KindUnauthorized, 0, "").
				withDetail("session token must already include the 'Bearer ' prefix")
		}
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// 调用方主动取消：不是故障，重试没有意义。
		if errors.Is(err, context.Canceled) {
			e := newError(op, KindUnknown, 0, "").withDetail("request canceled by caller")
			e.Retryable = false
			return nil, e
		}
		return nil, newError(op, KindNetworkUnreachable, 0, "").
			withDetail("request failed: %v", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	// 多读 1 字节用于判断是否被截断：静默截断会伪装成「不是 Xboard」，属误导性错误。
	limited := io.LimitReader(resp.Body, maxResponseBytes+1)
	rawBody, readErr := io.ReadAll(limited)
	if readErr != nil {
		return nil, newError(op, KindNetworkUnreachable, 0, "").
			withDetail("read response failed: %v", readErr)
	}
	if int64(len(rawBody)) > maxResponseBytes {
		e := newError(op, KindUnknown, resp.StatusCode, "").
			withDetail("response exceeds the size limit")
		e.Retryable = false
		return nil, e
	}

	// 订阅类端点返回非 JSON，本包的 4 个接口都应返回 JSON 信封。
	// 非 JSON 说明地址不是 Xboard，或中间有拦截页面。
	trimmed := bytes.TrimSpace(rawBody)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, newError(op, KindNotXboard, resp.StatusCode, "").
			withDetail("response is not a JSON object; the address may not be an Xboard panel")
	}

	var env envelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, newError(op, KindNotXboard, resp.StatusCode, "").
			withDetail("response is not a valid Xboard envelope: %v", err)
	}

	// 信封结构校验：status 必须存在且为已知值。
	if env.Status != "success" && env.Status != "fail" {
		return nil, newError(op, KindNotXboard, resp.StatusCode, "").
			withDetail("response envelope has an unexpected status field")
	}

	if env.isSuccess() {
		return &env, nil
	}

	// 服务端返回失败：分类后把服务端文案保存在 ServerMessage 里供诊断，
	// 但界面文案仍由 MessageKey 决定（服务端文案可能随语言与版本变化）。
	kind := classifyHTTP(op, resp.StatusCode)
	return nil, newError(op, kind, resp.StatusCode, env.Message)
}

// classifyHTTP 把 HTTP 状态码映射为错误分类。
//
// 事实：Xboard 的 HTTP 状态码取自业务 code 的前三位（app/Helpers/ApiResponse.php:54）。
// 因此 401/403 直接对应未登录/被拒绝，429 对应限流，5xx 对应服务端错误。
func classifyHTTP(op string, status int) ErrorKind {
	switch {
	case status == http.StatusUnauthorized:
		return KindUnauthorized
	case status == http.StatusForbidden:
		// 登录与改密场景下 403 语义更接近凭据问题。
		if op == opLogin {
			return KindInvalidCredentials
		}
		return KindForbidden
	case status == http.StatusTooManyRequests:
		return KindRateLimited
	case status >= 500:
		return KindServerError
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		if op == opLogin {
			return KindInvalidCredentials
		}
		return KindUnknown
	default:
		return KindUnknown
	}
}

// localizedMessage 已删除。
//
// 原先它把错误分类直接映射成硬编码中文，导致英文界面在出错时显示中文。
// 现在由 newError 统一填入 MessageKey，界面用 i18n.Bundle 渲染。
// 见 design/architecture/i18n-convention.md。

// 操作名常量，用于错误定位与分类。
const (
	opLogin     = "login"
	opSubscribe = "getSubscriptionDescriptor"
	opAccount   = "getAccount"
	opDiscover  = "discoverPanel"
)
