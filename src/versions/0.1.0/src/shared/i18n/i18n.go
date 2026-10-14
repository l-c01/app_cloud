// Package i18n 提供多语言文案的加载、语言协商与格式化。
//
// 设计约束（设计基线 §4.3、R10）：
//   - 默认跟随系统语言，允许用户手动覆盖；
//   - 缺键时必须**可见地**暴露问题（返回键名），而不是返回空串或英文兜底，
//     否则界面会出现难以察觉的空白；
//   - 占位符用位置参数 {0}、{1}，避免命名参数在不同语言间顺序不一致带来歧义；
//   - 复数通过 "key.one" / "key.other" 约定表达，找不到时回退到 "key"。
package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// FallbackLanguage 是最终兜底语言。
//
// 与 client-config.json 的 fallbackLanguage 默认值一致。
const FallbackLanguage = "en"

// SchemaVersion 是语言资源文件的版本。
const SchemaVersion = "1.0.0"

// Catalog 是单个语言的文案表。
type Catalog struct {
	SchemaVersion string            `json:"schemaVersion"`
	Language      string            `json:"language"`
	DisplayName   string            `json:"displayName"`
	Messages      map[string]string `json:"messages"`
}

// Validate 校验语言资源的基本结构。
func (c *Catalog) Validate() error {
	if c == nil {
		return fmt.Errorf("i18n: catalog is nil")
	}
	if strings.TrimSpace(c.Language) == "" {
		return fmt.Errorf("i18n: catalog has no language code")
	}
	if c.SchemaVersion != "" && c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("i18n: unsupported catalog schema version %q (want %q)",
			c.SchemaVersion, SchemaVersion)
	}
	if len(c.Messages) == 0 {
		return fmt.Errorf("i18n: catalog %q contains no messages", c.Language)
	}
	return nil
}

// Bundle 是已加载的语言集合。
type Bundle struct {
	catalogs map[string]*Catalog
	fallback string
}

// NewBundle 构造空的语言集合。
//
// fallback 为空时使用 FallbackLanguage。
func NewBundle(fallback string) *Bundle {
	if strings.TrimSpace(fallback) == "" {
		fallback = FallbackLanguage
	}
	return &Bundle{
		catalogs: make(map[string]*Catalog),
		fallback: Normalize(fallback),
	}
}

// Fallback 返回兜底语言代码。
func (b *Bundle) Fallback() string { return b.fallback }

// LoadJSON 从 JSON 数据加载一个语言。
func (b *Bundle) LoadJSON(data []byte) error {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("i18n: parse catalog: %w", err)
	}
	return b.Add(&c)
}

// Add 把一个已解析的语言加入集合。
func (b *Bundle) Add(c *Catalog) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b.catalogs[Normalize(c.Language)] = c
	return nil
}

// Languages 返回已加载的语言代码（已排序，便于稳定输出）。
func (b *Bundle) Languages() []string {
	out := make([]string, 0, len(b.catalogs))
	for lang := range b.catalogs {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}

// Has 报告某语言是否已加载。
func (b *Bundle) Has(lang string) bool {
	_, ok := b.catalogs[Normalize(lang)]
	return ok
}

// Negotiate 选择实际使用的语言。
//
// 规则（按优先级）：
//  1. requested 为 "system" 时使用系统语言；
//  2. 精确匹配；
//  3. 主语言匹配（zh-Hans 匹配 zh-CN；en-US 匹配 en）；
//  4. 兜底语言；
//  5. 任意可用语言（保证总能返回一个已加载的语言）。
//
// 返回空串仅当集合里一个语言都没有。
func (b *Bundle) Negotiate(requested string) string {
	if len(b.catalogs) == 0 {
		return ""
	}

	want := Normalize(requested)
	if want == "" || want == "system" {
		want = Normalize(DetectSystemLanguage())
	}

	// 精确匹配。
	if _, ok := b.catalogs[want]; ok {
		return want
	}

	// 主语言匹配。
	if primary := primarySubtag(want); primary != "" {
		if _, ok := b.catalogs[primary]; ok {
			return primary
		}
		candidates := b.Languages()
		for _, lang := range candidates {
			if primarySubtag(lang) == primary {
				return lang
			}
		}
	}

	// 兜底语言。
	if _, ok := b.catalogs[b.fallback]; ok {
		return b.fallback
	}
	if primary := primarySubtag(b.fallback); primary != "" {
		for _, lang := range b.Languages() {
			if primarySubtag(lang) == primary {
				return lang
			}
		}
	}

	// 任意可用语言。
	return b.Languages()[0]
}

// T 取一条文案并按位置参数格式化。
//
// 找不到键时：
//   - 先尝试兜底语言；
//   - 仍然找不到则返回键名本身（可见地暴露问题，便于在界面上发现遗漏）。
func (b *Bundle) T(lang, key string, args ...any) string {
	format, ok := b.lookup(lang, key)
	if !ok {
		if format, ok = b.lookup(b.fallback, key); !ok {
			return key
		}
	}
	return Format(format, args...)
}

// Tn 取一条带复数的文案。
//
// 约定：查找 "key.one"（count == 1）或 "key.other"；两者都缺时回退到 "key"。
// 这只是最小可用的复数支持；若将来需要斯拉夫语系的多样复数形式，
// 必须换成 CLDR 复数规则，不能靠这套约定硬撑。
func (b *Bundle) Tn(lang, key string, count int, args ...any) string {
	suffix := ".other"
	if count == 1 {
		suffix = ".one"
	}
	if format, ok := b.lookup(lang, key+suffix); ok {
		return Format(format, args...)
	}
	if format, ok := b.lookup(b.fallback, key+suffix); ok {
		return Format(format, args...)
	}
	return b.T(lang, key, args...)
}

// lookup 从指定语言取原始格式串。
func (b *Bundle) lookup(lang, key string) (string, bool) {
	c, ok := b.catalogs[Normalize(lang)]
	if !ok {
		return "", false
	}
	v, ok := c.Messages[key]
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// MissingKeys 返回所有已加载语言里都缺失的键。
//
// 用途：CI 或开发期检查代码用到的键是否都已被翻译。
func (b *Bundle) MissingKeys(keys []string) []string {
	var missing []string
	for _, key := range keys {
		found := false
		for _, lang := range b.Languages() {
			if _, ok := b.lookup(lang, key); ok {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

// InconsistentKeys 返回占位符数量在各语言间不一致的键。
//
// 这类不一致会导致某一语言下格式化结果缺参数或多出 {n} 字面量，
// 属于必须修的翻译缺陷。
func (b *Bundle) InconsistentKeys(keys []string) map[string]map[string]int {
	out := make(map[string]map[string]int)
	for _, key := range keys {
		counts := make(map[string]int)
		for _, lang := range b.Languages() {
			format, ok := b.lookup(lang, key)
			if !ok {
				continue
			}
			counts[lang] = CountPlaceholders(format)
		}
		if len(counts) < 2 {
			continue
		}
		var first int
		inconsistent := false
		for _, n := range counts {
			if first == 0 {
				first = n
				continue
			}
			if n != first {
				inconsistent = true
				break
			}
		}
		if inconsistent {
			out[key] = counts
		}
	}
	return out
}

// Format 按位置参数替换 {0}、{1} 等占位符。
//
// 超出参数范围的占位符原样保留：这样界面会显示 "{3}"，
// 让翻译缺陷立刻可见，而不是静默变成空串。
func Format(format string, args ...any) string {
	if len(args) == 0 || !strings.ContainsRune(format, '{') {
		return format
	}
	var sb strings.Builder
	sb.Grow(len(format))

	for i := 0; i < len(format); i++ {
		ch := format[i]
		if ch != '{' {
			sb.WriteByte(ch)
			continue
		}
		// 找右括号。
		end := strings.IndexByte(format[i:], '}')
		if end < 0 {
			sb.WriteByte(ch)
			continue
		}
		idxStr := format[i+1 : i+end]
		idx, err := parseIndex(idxStr)
		if err != nil || idx < 0 || idx >= len(args) {
			// 非法或越界：原样保留，让问题可见。
			sb.WriteString(format[i : i+end+1])
			i += end
			continue
		}
		sb.WriteString(fmt.Sprint(args[idx]))
		i += end
	}
	return sb.String()
}

// CountPlaceholders 统计格式串里的占位符个数（含越界的）。
func CountPlaceholders(format string) int {
	count := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '{' {
			continue
		}
		end := strings.IndexByte(format[i:], '}')
		if end < 0 {
			break
		}
		if _, err := parseIndex(format[i+1 : i+end]); err == nil {
			count++
		}
		i += end
	}
	return count
}

// parseIndex 解析占位符里的数字。
func parseIndex(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty placeholder")
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("placeholder is not numeric")
		}
		n = n*10 + int(s[i]-'0')
		if n > 1000 {
			return 0, fmt.Errorf("placeholder index too large")
		}
	}
	return n, nil
}

// Normalize 规范化语言代码：小写、下划线转连字符、去空白。
func Normalize(lang string) string {
	s := strings.TrimSpace(lang)
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ToLower(s)
	return s
}

// primarySubtag 返回主语言子标签（zh-CN → zh）。
func primarySubtag(lang string) string {
	if idx := strings.IndexByte(lang, '-'); idx > 0 {
		return lang[:idx]
	}
	return lang
}

// DetectSystemLanguage 尽力探测系统语言。
//
// 实现方式：读取环境变量（LC_ALL、LC_MESSAGES、LANG）。
//
// 已知限制：Windows 不通过环境变量暴露界面语言，因此这里会返回空串，
// 由协商流程回退到兜底语言。要正确支持 Windows，需要平台层调用
// GetUserDefaultUILanguage 并把结果注入（见 platform 层）。
func DetectSystemLanguage() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		// 形如 "zh_CN.UTF-8" 或 "en_US"；去掉编码后缀。
		if idx := strings.IndexAny(value, ".@"); idx > 0 {
			value = value[:idx]
		}
		if normalized := Normalize(value); normalized != "" && normalized != "c" && normalized != "posix" {
			return normalized
		}
	}
	return ""
}

// Resolve 是给调用方的一句式入口：加载 JSON 并协商语言。
//
// catalogs 的键是语言代码，值是该语言的 JSON 内容。
func Resolve(requested string, fallback string, catalogs map[string][]byte) (*Bundle, string, error) {
	b := NewBundle(fallback)
	for lang, data := range catalogs {
		if err := b.LoadJSON(data); err != nil {
			return nil, "", fmt.Errorf("i18n: load %s: %w", lang, err)
		}
	}
	return b, b.Negotiate(requested), nil
}
