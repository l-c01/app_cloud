package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"proxyclient/shared/domain"
)

// maxBodyBytes 限制订阅响应体大小，防止异常订阅耗尽内存。
const maxBodyBytes = 8 << 20 // 8 MiB

// Format 是订阅内容的格式分类。
type Format string

const (
	// FormatURIList 是 Base64 编码的节点 URI 列表（Xboard 的 general 系列 flag）。
	FormatURIList Format = "uri_list_base64"
	// FormatClashYAML 是 Clash / ClashMeta YAML。
	FormatClashYAML Format = "clash_yaml"
	// FormatSingBoxJSON 是 sing-box 配置 JSON。
	FormatSingBoxJSON Format = "singbox_json"
	// FormatUnknown 无法识别。
	FormatUnknown Format = "unknown"
)

// UserInfo 是服务端通过 subscription-userinfo 响应头下发的账户用量。
//
// 事实来源：src/references/Xboard-master/app/Protocols/General.php:53
// 形如：upload=123; download=456; total=789; expire=1700000000
type UserInfo struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   int64
	Present  bool
}

// Used 返回已用流量。
func (u UserInfo) Used() int64 { return u.Upload + u.Download }

// FetchResult 是一次订阅拉取的完整结果。
type FetchResult struct {
	// Nodes 是成功解析出的节点。
	Nodes []*domain.Node
	// Format 是探测到的订阅格式。
	Format Format
	// UserInfo 来自响应头；Present 为 false 时表示服务端未提供。
	UserInfo UserInfo
	// Warnings 记录解析过程中可容忍的问题（单条 URI 失败等）。
	Warnings []string
	// RejectedCount 是因解析失败被跳过的条目数。
	RejectedCount int
}

// Error 是订阅操作的失败，带稳定分类。
type Error struct {
	// Kind 取值：unavailable / unauthorized / network / too_large / unsupported_format / parse。
	Kind    string
	Message string
	// HTTPStatus 服务端状态码；网络层失败时为 0。
	HTTPStatus int
}

func (e *Error) Error() string {
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("subscription: %s (http %d): %s", e.Kind, e.HTTPStatus, e.Message)
	}
	return fmt.Sprintf("subscription: %s: %s", e.Kind, e.Message)
}

// 错误分类常量。
const (
	KindUnavailable       = "unavailable"
	KindUnauthorized      = "unauthorized"
	KindNetwork           = "network"
	KindTooLarge          = "too_large"
	KindUnsupportedFormat = "unsupported_format"
	KindParse             = "parse"
)

// Fetcher 拉取订阅内容。
//
// 它不持有订阅 URL（URL 含 token，由调用方从 SecretStore 取出后传入），
// 也不写日志，避免凭据泄漏。
type Fetcher struct {
	http *http.Client
	// UserAgent 是声明给服务端的客户端标识。
	//
	// 服务端依据 flag 或 User-Agent 决定输出格式（ClientController::getClientInfo），
	// 因此调用方应显式声明，避免拿到需要额外转换的格式。
	UserAgent string
	// Flag 通过查询参数显式声明格式（优先生效）。
	Flag string
}

// NewFetcher 构造拉取器。hc 为 nil 时使用默认客户端（30 秒超时）。
func NewFetcher(hc *http.Client) *Fetcher {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Fetcher{http: hc}
}

// Fetch 拉取并解析订阅。
//
// 返回的 *Error 分类语义：
//   - KindUnauthorized：HTTP 401/403 且响应体为空，表示订阅不可用
//     （套餐到期、流量用尽或封禁；服务端返回 403 空 body）。
//   - KindTooLarge：响应体超过上限。
//   - KindUnsupportedFormat：格式已识别但当前尚未实现解析（见 ParseResult 说明）。
//   - KindParse：格式识别失败或全部条目解析失败。
func (f *Fetcher) Fetch(ctx context.Context, subscribeURL string) (*FetchResult, *Error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subscribeURL, nil)
	if err != nil {
		return nil, &Error{Kind: KindNetwork, Message: "build request failed: " + err.Error()}
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	if f.Flag != "" {
		q := req.URL.Query()
		q.Set("flag", f.Flag)
		req.URL.RawQuery = q.Encode()
	}

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, &Error{Kind: KindNetwork, Message: "request failed: " + err.Error()}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if readErr != nil {
		return nil, &Error{Kind: KindNetwork, Message: "read response failed: " + readErr.Error(), HTTPStatus: resp.StatusCode}
	}
	if int64(len(body)) > maxBodyBytes {
		return nil, &Error{Kind: KindTooLarge, Message: "subscription body exceeds the size limit", HTTPStatus: resp.StatusCode}
	}

	// 服务端在用户不可用时返回 403 且 body 为空；这必须映射为具体业务原因，
	// 不能显示为「网络错误」。
	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) && len(strings.TrimSpace(string(body))) == 0 {
		return nil, &Error{
			Kind:       KindUnavailable,
			Message:    "subscription is unavailable (expired, out of quota, or banned)",
			HTTPStatus: resp.StatusCode,
		}
	}
	if resp.StatusCode >= 400 {
		return nil, &Error{
			Kind:       KindUnauthorized,
			Message:    "unexpected status from subscription endpoint",
			HTTPStatus: resp.StatusCode,
		}
	}

	res := &FetchResult{
		UserInfo: ParseUserInfoHeader(resp.Header.Get("subscription-userinfo")),
	}

	format := DetectFormat(body)
	res.Format = format

	switch format {
	case FormatURIList:
		nodes, warnings, rejected, perr := ParseBase64URIList(body)
		if perr != nil {
			return nil, &Error{Kind: KindParse, Message: perr.Error()}
		}
		res.Nodes = nodes
		res.Warnings = warnings
		res.RejectedCount = rejected
	case FormatClashYAML:
		return nil, &Error{
			Kind:    KindUnsupportedFormat,
			Message: "clash YAML detected but not implemented yet; request flag=general to get the URI list instead",
		}
	case FormatSingBoxJSON:
		return nil, &Error{
			Kind:    KindUnsupportedFormat,
			Message: "sing-box JSON detected but not implemented yet; request flag=general to get the URI list instead",
		}
	default:
		return nil, &Error{Kind: KindParse, Message: "unrecognized subscription format"}
	}

	if len(res.Nodes) == 0 {
		return nil, &Error{Kind: KindParse, Message: "subscription contained no parseable nodes"}
	}
	return res, nil
}

// DetectFormat 探测订阅正文格式。
//
// 探测顺序刻意固定：先看是否为 JSON 对象，再看是否为 YAML 文档，
// 最后才尝试 base64 节点 URI 列表，避免把 base64 误判为其它格式。
func DetectFormat(body []byte) Format {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return FormatUnknown
	}

	if strings.HasPrefix(trimmed, "{") {
		var probe map[string]any
		if err := json.Unmarshal([]byte(trimmed), &probe); err == nil {
			// sing-box 配置的显著特征：包含 outbounds 数组。
			if _, ok := probe["outbounds"]; ok {
				return FormatSingBoxJSON
			}
			return FormatSingBoxJSON
		}
		return FormatUnknown
	}

	// YAML 文档的常见特征：顶层出现 proxies: 或 proxy-groups:
	if strings.Contains(trimmed, "proxies:") || strings.Contains(trimmed, "proxy-groups:") {
		return FormatClashYAML
	}

	// 节点 URI 列表被整体 base64 编码；解码后应出现 "://"。
	if decoded, err := decodeBase64Loose(trimmed); err == nil {
		if strings.Contains(decoded, "://") {
			return FormatURIList
		}
	}

	// 少数服务端直接返回明文 URI 列表。
	if strings.Contains(trimmed, "://") {
		return FormatURIList
	}
	return FormatUnknown
}

// ParseBase64URIList 解析 Base64 编码的节点 URI 列表。
//
// 容错策略：单条 URI 解析失败时跳过并记入 warnings，而不是让整个订阅失败——
// 否则一个坏节点会导致用户完全无法使用。失败条数单独统计，便于在界面与报告里展示。
func ParseBase64URIList(body []byte) (nodes []*domain.Node, warnings []string, rejected int, err error) {
	trimmed := strings.TrimSpace(string(body))

	text := trimmed
	if decoded, derr := decodeBase64Loose(trimmed); derr == nil && strings.Contains(decoded, "://") {
		text = decoded
	} else if !strings.Contains(trimmed, "://") {
		return nil, nil, 0, fmt.Errorf("content is not a base64 URI list")
	}

	lines := splitURILines(text)
	if len(lines) == 0 {
		return nil, nil, 0, fmt.Errorf("uri list is empty")
	}
	if len(lines) > maxNodes {
		return nil, nil, 0, fmt.Errorf("uri list exceeds the %d node limit", maxNodes)
	}

	for _, line := range lines {
		node, perr := ParseURI(line)
		if perr != nil {
			rejected++
			if len(warnings) < 50 { // 限制告警数量，避免报告膨胀
				warnings = append(warnings, perr.Error())
			}
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, warnings, rejected, nil
}

// splitURILines 按行拆分 URI 列表。
//
// 服务端用 "\r\n" 分隔（General.php:73 等），但现实中的订阅可能用 "\n"，
// 甚至把多条挤在一行，因此这里同时按换行与空白切分。
func splitURILines(text string) []string {
	raw := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ParseUserInfoHeader 解析 subscription-userinfo 响应头。
//
// 格式：upload=<n>; download=<n>; total=<n>; expire=<n>
// 缺失或无法解析的字段为 0；Present 表示该头是否存在。
func ParseUserInfoHeader(header string) UserInfo {
	var info UserInfo
	if strings.TrimSpace(header) == "" {
		return info
	}
	info.Present = true

	for _, part := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "upload":
			info.Upload = n
		case "download":
			info.Download = n
		case "total":
			info.Total = n
		case "expire":
			info.Expire = n
		}
	}
	return info
}
