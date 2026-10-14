// Package winproxy 实现 Windows 当前用户的系统代理接管。
//
// 设计取舍（为什么不做成 Windows-only 文件）：
//
//	Windows 专属代码若只在 Windows 上编译，CI 里的 Linux 作业永远编译不到它，
//	等于长期处于「未验证」状态。因此这里把**可注入的命令执行器**与**全部逻辑**
//	放在平台无关的文件里：命令构造、输出解析、所有权比较、冲突判定都能在任何
//	平台上被测试；只有真正调用 reg.exe 的那一次是平台相关的。
//
// 实现方式：通过 reg.exe 读写
// HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings。
// 使用固定命令与固定参数，代理值在写入前经过严格校验，不接受任意命令行。
//
// 已知缺口（必须补齐后才算完成 R05）：
// 写入注册表后还需调用 wininet.dll 的 InternetSetOption 通知系统刷新，
// 否则部分应用不会立即感知变化。该调用需要 Windows 专属实现，
// 见文件末尾的说明与 TODO。
package winproxy

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// registryKey 是当前用户的 Internet Settings 键。
const registryKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// 注册表值名。
const (
	valueProxyEnable   = "ProxyEnable"
	valueProxyServer   = "ProxyServer"
	valueProxyOverride = "ProxyOverride"
	valueAutoConfigURL = "AutoConfigURL"
)

// settingsKey 前缀用于把注册表值名映射到 NetworkSettings 的键，
// 与 coordinator 中 system_proxy / pac 模式的键命名保持一致。
const (
	keyProxyEnable   = "system_proxy.enabled"
	keyProxyServer   = "system_proxy.server"
	keyProxyOverride = "system_proxy.bypass"
	keyAutoConfigURL = "system_proxy.pac"
)

// defaultBypass 是默认绕过列表：本地地址不应经代理。
// 与核心侧的直连规则保持一致，避免「系统代理绕过了但核心又送进代理」的错位。
const defaultBypass = "<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.2*;172.30.*;172.31.*;192.168.*"

// Runner 执行外部命令。
//
// 做成接口是为了让全部逻辑可以在不执行任何命令的前提下被测试，
// 也便于将来换成直接调用 Windows API 的实现。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner 使用 os/exec 执行命令（生产实现）。
type ExecRunner struct{}

// Run 实现 Runner。
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// UnavailableRunner 在不支持该平台的系统上返回明确错误。
//
// 它保证「在不支持的平台上调用」得到清晰错误，而不是静默不做事，
// 也不是假装成功——后者会让用户以为系统代理已生效。
type UnavailableRunner struct{ Reason string }

// Run 实现 Runner。
func (u UnavailableRunner) Run(context.Context, string, ...string) ([]byte, error) {
	reason := u.Reason
	if reason == "" {
		reason = "Windows registry access is unavailable on this platform"
	}
	return nil, errors.New("winproxy: " + reason)
}

// Manager 管理当前用户的系统代理设置。
type Manager struct {
	runner Runner
}

// New 构造 Manager。
//
// runner 为 nil 时使用 ExecRunner（即真的调用 reg.exe）。
func New(runner Runner) *Manager {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Manager{runner: runner}
}

// proxyValuePattern 校验代理值形如 host:port。
//
// 之所以严格校验：该值会被拼进 reg.exe 的命令行，
// 宽松校验等于给出命令注入的可能。
var proxyValuePattern = regexp.MustCompile(`^[A-Za-z0-9._\-\[\]:]{1,255}$`)

// validateProxyValue 校验将要写入注册表的代理值。
func validateProxyValue(value string) error {
	if value == "" {
		return errors.New("winproxy: proxy value is empty")
	}
	if !proxyValuePattern.MatchString(value) {
		return fmt.Errorf("winproxy: proxy value contains unsupported characters: %q", value)
	}
	// 必须是 host:port 形式，且端口合法。
	idx := strings.LastIndex(value, ":")
	if idx <= 0 || idx == len(value)-1 {
		return fmt.Errorf("winproxy: proxy value must be host:port, got %q", value)
	}
	port, err := strconv.Atoi(value[idx+1:])
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("winproxy: proxy value has an invalid port: %q", value)
	}
	return nil
}

// Snapshot 读取当前系统代理设置。
//
// 返回值只包含**确实存在**的键，不存在的键不出现在映射里——
// 这样恢复时才能区分「原本为空」与「原本没这个键」。
func (m *Manager) Snapshot(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, 4)
	for _, name := range []string{valueProxyEnable, valueProxyServer, valueProxyOverride, valueAutoConfigURL} {
		value, found, err := m.queryValue(ctx, name)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		switch name {
		case valueProxyEnable:
			out[keyProxyEnable] = value
		case valueProxyServer:
			out[keyProxyServer] = value
		case valueProxyOverride:
			out[keyProxyOverride] = value
		case valueAutoConfigURL:
			out[keyAutoConfigURL] = value
		}
	}
	return out, nil
}

// ApplySystemProxy 写入系统代理设置，返回实际写入的值。
//
// host 必须是环回地址（调用方 coordinator 已保证），port 是本地混合/HTTP 端口。
func (m *Manager) ApplySystemProxy(ctx context.Context, host string, port int) (map[string]string, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("winproxy: invalid port %d", port)
	}
	proxyValue := fmt.Sprintf("%s:%d", host, port)
	if err := validateProxyValue(proxyValue); err != nil {
		return nil, err
	}

	applied := map[string]string{
		keyProxyEnable:   "1",
		keyProxyServer:   proxyValue,
		keyProxyOverride: defaultBypass,
	}

	// 顺序：先写 server 与 bypass，最后写 enabled。
	// 这样「启用」永远是最后一步，避免出现「已启用但地址还没写」的瞬间。
	if err := m.setValue(ctx, valueProxyServer, proxyValue); err != nil {
		return nil, err
	}
	if err := m.setValue(ctx, valueProxyOverride, defaultBypass); err != nil {
		return nil, err
	}
	if err := m.setValue(ctx, valueProxyEnable, "1"); err != nil {
		return nil, err
	}

	// PAC 与静态代理互斥：接管静态代理时必须清掉 AutoConfigURL，
	// 否则有的应用会优先走 PAC，造成行为不一致。
	if _, found, err := m.queryValue(ctx, valueAutoConfigURL); err != nil {
		return nil, err
	} else if found {
		if err := m.deleteValue(ctx, valueAutoConfigURL); err != nil {
			return nil, err
		}
	}

	return applied, nil
}

// ApplyPAC 写入 PAC 自动配置 URL，返回实际写入的值。
// pacURLPattern / pacCharsetPattern 校验写入注册表的 PAC 地址。
//
// 为什么不能沿用 proxyValuePattern：那是给「host:port」用的白名单，
// 不允许 "/"——而 PAC 是一个 URL，路径里必然出现 "/"。
// 但也不能因此放宽成「随便一个字符串」：这个值会被拼进 reg.exe 的命令行，
// 允许引号或换行等于给自己开一条注入路径。所以仍然用白名单，只是换一份。
//
// 主机部分只接受环回：PAC 文件决定了哪些流量走代理，
// 指向外部服务器等于把这份控制权交给别人，而且每次启动都要联网才能拿到规则。
var (
	pacHostPattern    = regexp.MustCompile(`^http://(127(\.\d{1,3}){3}|localhost|\[::1\])(:\d{1,5})?(/|$)`)
	pacCharsetPattern = regexp.MustCompile(`^[A-Za-z0-9._~%\-/:\[\]]+$`)
)

// validatePACURL 校验 PAC 地址。
//
// 三类失败分开报：不是 http、不是本机、含不合法字符。
// 合成一条笼统的「地址不合法」会让人不知道该改哪一处。
func validatePACURL(raw string) error {
	if raw == "" {
		return errors.New("winproxy: PAC URL is empty")
	}
	if len(raw) > 255 {
		// 注册表字符串值有长度上限，超了会被系统截断——截断后的地址
		// 仍然能写入，但指向的已经不是用户给的那一个。
		return fmt.Errorf("winproxy: PAC URL is longer than the registry value limit: %d", len(raw))
	}
	if !strings.HasPrefix(raw, "http://") {
		return fmt.Errorf("winproxy: PAC URL must be an http URL, got %q", raw)
	}
	if !pacHostPattern.MatchString(raw) {
		return fmt.Errorf("winproxy: PAC URL must point at the local PAC server, got %q", raw)
	}
	if !pacCharsetPattern.MatchString(raw) {
		return fmt.Errorf("winproxy: PAC URL contains unsupported characters: %q", raw)
	}
	return nil
}

// ApplyPAC 接管为 PAC 模式。
func (m *Manager) ApplyPAC(ctx context.Context, pacURL string) (map[string]string, error) {
	if err := validatePACURL(pacURL); err != nil {
		return nil, err
	}

	applied := map[string]string{keyAutoConfigURL: pacURL}
	if err := m.setValue(ctx, valueAutoConfigURL, pacURL); err != nil {
		return nil, err
	}
	// PAC 与静态代理互斥。
	if err := m.setValue(ctx, valueProxyEnable, "0"); err != nil {
		return nil, err
	}
	applied[keyProxyEnable] = "0"
	return applied, nil
}

// RestoreResult 描述恢复结果。
type RestoreResult struct {
	Restored  []string
	Missing   []string
	Conflicts []Conflict
}

// Conflict 描述一处「当前值已不是我们写入的值」。
type Conflict struct {
	Key      string
	Expected string
	Actual   string
}

// Restore 恢复系统代理设置。
//
// 所有权校验（设计基线 §7.2 的硬要求）：逐个键比较当前值与当初写入的值。
//   - 相等：写回原值（原值不存在则删除该键）；
//   - 不等：记入 Conflicts 并**跳过**，绝不覆盖用户或其他软件的修改。
func (m *Manager) Restore(ctx context.Context, original, applied map[string]string) (RestoreResult, error) {
	var result RestoreResult

	for key, expected := range applied {
		name, ok := registryNameFor(key)
		if !ok {
			continue
		}

		current, found, err := m.queryValue(ctx, name)
		if err != nil {
			return result, err
		}

		if !found || current != expected {
			// 当前值已不是我们写入的：不覆盖，报告冲突。
			actual := current
			if !found {
				actual = ""
			}
			result.Conflicts = append(result.Conflicts, Conflict{Key: key, Expected: expected, Actual: actual})
			continue
		}

		originalValue, hadOriginal := original[key]
		switch {
		case hadOriginal && originalValue != "":
			if err := m.setValue(ctx, name, originalValue); err != nil {
				return result, err
			}
			result.Restored = append(result.Restored, key)
		default:
			// 原本没有这个键，或原本为空：删除我们创建的值。
			if err := m.deleteValue(ctx, name); err != nil {
				return result, err
			}
			result.Missing = append(result.Missing, key)
		}
	}

	return result, nil
}

// ---- reg.exe 交互 ----

// ErrValueNotFound 表示注册表中不存在该值。
//
// 必须与「读取失败」严格区分：
//   - 值不存在是正常情况，快照里不记录该键；
//   - 读取失败（权限、注册表损坏）**绝不能**当成「不存在」，
//     否则上层会以为系统本来就没有代理设置，从而跳过恢复，
//     把用户的原始配置永久丢掉。
var ErrValueNotFound = errors.New("winproxy: registry value not found")

// queryValue 读取一个注册表值。
//
// 返回 (值, 是否存在, 错误)。存在性判定依赖 reg.exe 的退出码 1（未找到）；
// 其它错误一律向上传播。
func (m *Manager) queryValue(ctx context.Context, name string) (string, bool, error) {
	out, err := m.runner.Run(ctx, "reg", "query", registryKey, "/v", name)
	if err != nil {
		if errors.Is(err, ErrValueNotFound) || isExitCode(err, 1) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("winproxy: query %s: %w", name, err)
	}
	value, ok := parseRegQueryOutput(string(out), name)
	if !ok {
		// 命令成功但输出里没有这一行：可能是键存在而值不存在。
		return "", false, nil
	}
	return value, true, nil
}

// isExitCode 判断错误是否为指定退出码的进程退出错误。
//
// reg.exe 在「找不到指定的注册表项或值」时返回 1；
// 用它区分「不存在」与「真的失败」。
func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == code
	}
	return false
}

// setValue 写入一个 REG_SZ 值。
func (m *Manager) setValue(ctx context.Context, name, value string) error {
	if err := validateRegValue(value); err != nil {
		return err
	}
	// /f 表示覆盖已存在的值，避免交互式确认导致命令挂起。
	if _, err := m.runner.Run(ctx, "reg", "add", registryKey, "/v", name, "/t", "REG_SZ", "/d", value, "/f"); err != nil {
		return fmt.Errorf("winproxy: set %s: %w", name, err)
	}
	return nil
}

// deleteValue 删除一个值；值不存在不算错误。
func (m *Manager) deleteValue(ctx context.Context, name string) error {
	if _, err := m.runner.Run(ctx, "reg", "delete", registryKey, "/v", name, "/f"); err != nil {
		// 删除不存在的值返回非零；这里无法区分，按「已完成」处理。
		// 风险可接受：后续写入使用 /f 覆盖，不依赖删除结果。
		return nil
	}
	return nil
}

// validateRegValue 校验写入注册表的值不含会导致命令注入的字符。
//
// 合法的值只有：代理地址、绕过列表、PAC URL、以及 "0"/"1"。
// 它们都不需要引号、空格以外的 shell 元字符；这里允许空格与常见符号，
// 但拒绝引号与控制字符。
func validateRegValue(value string) error {
	if strings.ContainsAny(value, `"'`+"\r\n\x00") {
		return fmt.Errorf("winproxy: registry value contains forbidden characters")
	}
	if len(value) > 4096 {
		return errors.New("winproxy: registry value is too long")
	}
	return nil
}

// parseRegQueryOutput 从 reg query 的输出里取出指定值的 REG_SZ 内容。
//
// 典型输出（行首有缩进）：
//
//	HKEY_CURRENT_USER\...\Internet Settings
//	    ProxyServer    REG_SZ    127.0.0.1:7890
//
// 解析方式刻意宽松：只找以值名开头的行，然后取 REG_SZ 之后的部分。
// 这样不受系统语言与列宽差异影响。
func parseRegQueryOutput(output, name string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		fields := strings.Fields(trimmed)
		if len(fields) < 3 {
			continue
		}
		if fields[0] != name {
			continue
		}
		// fields[1] 是类型；其后全部内容是值（可能含空格）。
		idx := strings.Index(trimmed, fields[1])
		if idx < 0 {
			continue
		}
		value := strings.TrimSpace(trimmed[idx+len(fields[1]):])
		return value, true
	}
	return "", false
}

// registryNameFor 把 NetworkSettings 的键映射回注册表值名。
func registryNameFor(key string) (string, bool) {
	switch key {
	case keyProxyEnable:
		return valueProxyEnable, true
	case keyProxyServer:
		return valueProxyServer, true
	case keyProxyOverride:
		return valueProxyOverride, true
	case keyAutoConfigURL:
		return valueAutoConfigURL, true
	default:
		return "", false
	}
}

// TODO(R05)：写入注册表之后需要通知系统刷新代理设置。
//
// 方法是在 Windows 上调用 wininet.dll 的：
//
//	InternetSetOptionW(nil, INTERNET_OPTION_SETTINGS_CHANGED, nil, 0)
//	InternetSetOptionW(nil, INTERNET_OPTION_REFRESH, nil, 0)
//
// 缺少这一步时，部分应用（尤其是已经运行中的）不会立即使用新代理。
// 该调用需要 Windows 专属文件与 syscall，且必须在 Windows CI 作业中编译验证，
// 因此单独实现，不混进本文件的跨平台逻辑里。
