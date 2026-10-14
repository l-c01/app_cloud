// Package secrets 提供凭据存储抽象与统一的脱敏规则。
//
// 设计约束（设计基线 §9、R15）：
//   - 明文凭据绝不落盘、绝不写日志、绝不进入导出配置；
//   - 桌面用系统凭据设施（Windows DPAPI/Credential Manager、Linux Secret Service），
//     Android 用 Keystore；没有可用钥匙环时只保留会话或明确要求用户选择加密保险库，
//     **不得退回明文**；
//   - 切换面板服务器时必须隔离账号与缓存，不把旧凭据发给新主机。
//
// 本包只定义契约与内存实现；平台实现由 platform 层提供。
package secrets

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// ErrNotFound 表示指定键没有存储值。
var ErrNotFound = errors.New("secrets: key not found")

// ErrUnavailable 表示本平台缺少可用的安全存储。
//
// 调用方收到该错误时**不得**降级为明文保存，而应提示用户：
// 要么仅保留本次会话，要么显式启用加密保险库。
var ErrUnavailable = errors.New("secrets: secure storage is unavailable on this platform")

// MessageKeyFor 把本包的哨兵错误映射到界面文案键。
//
// 存在理由：这两个错误是 errors.New 的哨兵值，本身没有地方挂文案键。
// 界面拿到 error 后调用本函数即可得到键；返回空串表示不是本包的可本地化错误，
// 界面按自己的兜底逻辑处理。
//
// 注意：不要用 err.Error() 的文本去匹配，也不要把这里返回的键当成错误分类。
func MessageKeyFor(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUnavailable):
		return "secrets.unavailable"
	case errors.Is(err, ErrNotFound):
		return "secrets.not_found"
	default:
		return ""
	}
}

// Key 是凭据的逻辑键。
//
// 使用固定枚举而非自由字符串，避免拼写错误导致凭据写错位置。
type Key string

const (
	// KeyPanelSession 是面板会话凭据（登录返回的 auth_data，形如 "Bearer xxx"）。
	//
	// 命名说明：凭据键统一用 "cred." 前缀，**刻意不使用** "panel." 等包名前缀。
	//
	// 原因：多语言文案键的约定是 "<包名>.<主题>"（见
	// design/architecture/i18n-convention.md），自动校验脚本据此识别文案键。
	// 若凭据键也叫 "panel.xxx"，就会被误判成缺失的文案键。
	// 两类标识符必须能在命名上区分，否则自动检查永远需要例外清单。
	KeyPanelSession Key = "cred.panel.session"
	// KeySubscriptionURL 是该用户的完整订阅链接（含 token）。
	KeySubscriptionURL Key = "cred.subscription.url"
	// KeySubscriptionToken 是订阅 token。
	KeySubscriptionToken Key = "cred.panel.subscribe_token"
	// KeyNodeUUID 是协议用户标识（uuid）。
	KeyNodeUUID Key = "cred.panel.node_uuid"
	// KeyAccountPassword 仅在用户明确选择"记住密码"时使用；默认不保存。
	KeyAccountPassword Key = "cred.panel.password"
)

// Store 是凭据存储契约。
//
// 实现必须保证：
//   - 值以操作系统提供的保护机制加密保存；
//   - 不提供"列出全部值"的接口（减少泄漏面）；
//   - 服务器切换时由调用方按命名空间清理，见 NamespacedStore。
type Store interface {
	// Set 保存或覆盖一个值。
	Set(key Key, value string) error
	// Get 读取一个值；不存在时返回 ErrNotFound。
	Get(key Key) (string, error)
	// Delete 删除一个值；不存在时不算错误。
	Delete(key Key) error
	// Available 报告本平台是否具备安全存储能力。
	Available() bool
}

// NamespacedStore 给凭据加上服务器命名空间前缀，实现账号隔离。
//
// 这样切换面板时，旧服务器的凭据不会被新主机读取到；
// 同时可以用 ClearNamespace 一次性清理某台服务器的全部凭据。
type NamespacedStore struct {
	inner Store
	// namespace 通常是规范化后的面板地址（不含凭据）。
	namespace string
}

// NewNamespaced 包装一个 Store 并绑定命名空间。
func NewNamespaced(inner Store, namespace string) (*NamespacedStore, error) {
	if inner == nil {
		return nil, errors.New("secrets: nil store")
	}
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return nil, errors.New("secrets: empty namespace")
	}
	return &NamespacedStore{inner: inner, namespace: ns}, nil
}

// scoped 返回带命名空间的真实键。
func (n *NamespacedStore) scoped(key Key) Key {
	return Key(n.namespace + "|" + string(key))
}

// Set 实现 Store。
func (n *NamespacedStore) Set(key Key, value string) error {
	return n.inner.Set(n.scoped(key), value)
}

// Get 实现 Store。
func (n *NamespacedStore) Get(key Key) (string, error) {
	return n.inner.Get(n.scoped(key))
}

// Delete 实现 Store。
func (n *NamespacedStore) Delete(key Key) error {
	return n.inner.Delete(n.scoped(key))
}

// Available 实现 Store。
func (n *NamespacedStore) Available() bool { return n.inner.Available() }

// ClearNamespace 清理本命名空间下的全部已知键。
//
// 注意：这不是"删除所有数据"，而是逐个删除本包定义的键，
// 避免误删同一存储中属于其它服务器或其它应用的数据。
func (n *NamespacedStore) ClearNamespace() error {
	var firstErr error
	for _, key := range AllKeys() {
		if err := n.inner.Delete(n.scoped(key)); err != nil && !errors.Is(err, ErrNotFound) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// AllKeys 返回本包定义的全部键。
func AllKeys() []Key {
	return []Key{
		KeyPanelSession,
		KeySubscriptionURL,
		KeySubscriptionToken,
		KeyNodeUUID,
		KeyAccountPassword,
	}
}

// MemoryStore 是仅用于测试与临时会话的内存实现。
//
// 它**不是**安全存储：进程退出即丢失，且不做加密。
// 生产环境在没有安全存储时必须明确告知用户，而不是用它顶替。
type MemoryStore struct {
	mu     sync.RWMutex
	values map[Key]string
	// available 允许测试模拟"平台不支持安全存储"的情况。
	available bool
}

// NewMemoryStore 构造一个可用状态的内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{values: make(map[Key]string), available: true}
}

// NewUnavailableStore 构造一个报告为不可用的存储，用于测试降级路径。
func NewUnavailableStore() *MemoryStore {
	return &MemoryStore{values: make(map[Key]string), available: false}
}

// Set 实现 Store。
func (m *MemoryStore) Set(key Key, value string) error {
	if !m.available {
		return ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

// Get 实现 Store。
func (m *MemoryStore) Get(key Key) (string, error) {
	if !m.available {
		return "", ErrUnavailable
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.values[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Delete 实现 Store。
func (m *MemoryStore) Delete(key Key) error {
	if !m.available {
		return ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

// Available 实现 Store。
func (m *MemoryStore) Available() bool { return m.available }

// 脱敏参数。
const (
	// redactKeep 是首尾各保留的可见字符数。
	redactKeep = 4
	// redactMinLen 是启用"中间掩码"所需的最短长度；
	// 比这更短的值整体替换，避免因保留首尾而暴露大部分内容。
	redactMinLen = 12
	// redactMask 是掩码字符。
	redactMask = "*"
)

// Redact 对敏感字符串做脱敏，用于日志、诊断包与错误消息。
//
// 规则：
//   - 空串返回空串；
//   - 长度 < 12 时整体替换为固定占位，避免暴露大部分内容；
//   - 否则保留前 4 与后 4 个字符，中间用 * 替换。
//
// 本函数是**纯函数且不记录任何内容**，可以安全地在错误路径调用。
func Redact(value string) string {
	if value == "" {
		return ""
	}
	// 按字符（而非字节）计算长度，避免切断多字节字符。
	n := utf8.RuneCountInString(value)
	if n < redactMinLen {
		return strings.Repeat(redactMask, 8)
	}
	runes := []rune(value)
	return string(runes[:redactKeep]) + strings.Repeat(redactMask, n-redactKeep*2) + string(runes[n-redactKeep:])
}

// RedactURL 对可能含凭据的 URL 做脱敏。
//
// 订阅链接里的 token 通常出现在查询参数中，直接按整体字符串脱敏会丢掉
// 主机名等有用信息，因此这里保留 scheme 与 host，只替换路径与查询部分。
func RedactURL(raw string) string {
	if raw == "" {
		return ""
	}
	idx := strings.Index(raw, "://")
	if idx < 0 {
		return Redact(raw)
	}
	rest := raw[idx+3:]
	slash := strings.IndexAny(rest, "/?#")
	if slash < 0 {
		// 只有 scheme://host，没有路径，按整体规则处理。
		return raw
	}
	return raw[:idx+3+slash] + "/" + strings.Repeat(redactMask, 8)
}

// Scrubber 用已知的敏感字面量清洗文本。
//
// 用途：核心进程输出、HTTP 错误、第三方库报错都可能无意间带出凭据。
// 在写入日志或诊断包之前，用本类型把这些字面量替换掉。
//
// 它不能替代"根本不记录凭据"的设计，而是最后一道防线。
type Scrubber struct {
	mu       sync.RWMutex
	literals []string
}

// NewScrubber 构造清洗器。
func NewScrubber() *Scrubber { return &Scrubber{} }

// Add 登记一个需要清洗的字面量。
//
// 过短的值（少于 6 个字符）会被忽略：它们极易与正常文本误匹配，
// 造成日志难以阅读，而且本身也不构成有效的凭据。
func (s *Scrubber) Add(secret string) {
	if len(secret) < 6 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.literals {
		if existing == secret {
			return
		}
	}
	s.literals = append(s.literals, secret)
}

// Scrub 替换文本中出现的全部已登记字面量。
//
// 长字面量优先替换，避免短字面量先命中导致长字面量残留片段。
func (s *Scrubber) Scrub(text string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.literals) == 0 || text == "" {
		return text
	}

	ordered := make([]string, len(s.literals))
	copy(ordered, s.literals)
	// 简单插入排序：字面量数量很少，不值得引入排序依赖之外的复杂度。
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && len(ordered[j]) > len(ordered[j-1]); j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}

	out := text
	for _, literal := range ordered {
		out = strings.ReplaceAll(out, literal, Redact(literal))
	}
	return out
}

// Count 返回已登记的字面量数量，便于诊断。
func (s *Scrubber) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.literals)
}

// FromSession 把一次登录得到的凭据登记到清洗器。
//
// 调用方在完成登录后应当立即调用它，使后续所有日志都被覆盖。
func (s *Scrubber) FromSession(sessionToken, subscriptionToken, subscribeURL, nodeUUID string) {
	for _, v := range []string{sessionToken, subscriptionToken, subscribeURL, nodeUUID} {
		if strings.TrimSpace(v) != "" {
			s.Add(v)
		}
	}
}

// ValidateKey 校验键是否在本包定义的集合内，防止调用方写入未预期的位置。
func ValidateKey(key Key) error {
	for _, known := range AllKeys() {
		if key == known {
			return nil
		}
	}
	return fmt.Errorf("secrets: unknown key %q", key)
}
