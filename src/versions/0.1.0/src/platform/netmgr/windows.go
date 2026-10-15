package netmgr

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"proxyclient/platform/winproxy"
)

// WindowsBackend 通过注册表与 wininet 接管 Windows 的当前用户代理设置。
//
// **这个适配层此前完全不存在**：platform/winproxy 里早已有完整的实现
// （注册表读写、所有权校验、注入防护，以及写入后的 wininet 刷新通知），
// 但没有任何代码 import 它。于是服务在 Windows 上落到 NoopBackend，
// 系统代理接管其实是**死代码**——用户在 Windows 上点「连接」，
// 得到的是「本平台尚未实现系统代理接管」。
//
// 一个只有测试、没有调用点的实现是最容易骗过人的状态：
// 它有测试覆盖、CI 是绿的、代码也真的写对了，
// 但功能从端到端看根本不存在。判断「某功能是否可用」要顺着
// **调用链**查到起点，而不是看那个包里的测试是否通过。
//
// 限制（必须如实告知用户，见 Note）：
//   - 只影响读取系统代理设置（WinINET / WinHTTP 默认设置）的应用；
//     自己管理连接的程序（部分游戏、自带网络栈的软件）不会走代理；
//   - 这是**当前用户**的设置，不是全机器设置（写 HKCU 而非 HKLM）。
type WindowsBackend struct {
	proxy *winproxy.Manager
}

// NewWindowsBackend 构造 Windows 后端。
//
// runner 为 nil 时 winproxy 会用 ExecRunner，即真的调用 reg.exe。
func NewWindowsBackend(runner Runner) *WindowsBackend {
	var wpRunner winproxy.Runner
	if runner != nil {
		// Runner 与 winproxy.Runner 的方法签名完全相同
		// （Run(ctx, name, args...) ([]byte, error)）。
		// netmgr 在这里充当适配者，避免 winproxy 反过来依赖本包——
		// 依赖方向必须是「平台后端 → 通用契约」，不是反过来。
		wpRunner = runnerAdapter{inner: runner}
	}
	return &WindowsBackend{proxy: winproxy.New(wpRunner)}
}

// runnerAdapter 把 netmgr.Runner 适配成 winproxy.Runner。
type runnerAdapter struct{ inner Runner }

func (r runnerAdapter) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.inner.Run(ctx, name, args...)
}

// Name 实现 Backend。
func (*WindowsBackend) Name() string { return "windows-registry" }

// SupportedModes 实现 Backend。
//
// TUN 不在其中：它由核心侧的 TUN 入站提供，不需要改系统代理设置。
// 把它列进来会让界面显示一个点了没反应的选项。
func (*WindowsBackend) SupportedModes() []string {
	return []string{ModeSystemProxy, ModePAC}
}

// UnsupportedReason 实现 unsupportedReasoner：说明为何不支持某个模式。
func (*WindowsBackend) UnsupportedReason(mode string) string {
	switch mode {
	case ModeTUN:
		return "TUN mode is provided by the core's tun inbound, not by the system proxy settings"
	case ModeLocalProxy:
		return "local proxy mode changes no system settings by design"
	default:
		return fmt.Sprintf("the Windows backend does not take over %q", mode)
	}
}

// Snapshot 实现 Backend。
func (b *WindowsBackend) Snapshot(ctx context.Context) (map[string]string, error) {
	snap, err := b.proxy.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return translateFromWinproxy(snap), nil
}

// Apply 实现 Backend。
func (b *WindowsBackend) Apply(ctx context.Context, mode string, values map[string]string) (map[string]string, error) {
	switch mode {
	case ModeSystemProxy:
		return b.applySystemProxy(ctx, values)
	case ModePAC:
		return b.applyPAC(ctx, values)
	default:
		return nil, fmt.Errorf("netmgr: windows backend does not support mode %q", mode)
	}
}

// applySystemProxy 从通用键里取出 host:port 并写入注册表。
func (b *WindowsBackend) applySystemProxy(ctx context.Context, values map[string]string) (map[string]string, error) {
	// coordinator 可能用两种键名给出代理地址；两个都认。
	server := strings.TrimSpace(values[KeySystemProxyServer])
	if server == "" {
		server = strings.TrimSpace(values["system_proxy.http"])
	}
	if server == "" {
		return nil, fmt.Errorf("netmgr: %s is required for system proxy mode", KeySystemProxyServer)
	}

	host, port, err := splitHostPort(server)
	if err != nil {
		return nil, err
	}

	applied, err := b.proxy.ApplySystemProxy(ctx, host, port)
	if err != nil {
		return nil, err
	}

	// 通知失败的降级提示单独带走，不混进 applied：
	// applied 是「写入了哪些键值」，会被用于将来的恢复，
	// 往里塞一个不是设置的东西会让恢复时去注册表找一个不存在的键。
	if nerr := b.proxy.NotifyError(); nerr != nil {
		return translateFromWinproxy(applied), &TakeoverNotice{
			Mode:       ModeSystemProxy,
			MessageKey: "netmgr.notify_failed",
			Cause:      nerr,
		}
	}
	return translateFromWinproxy(applied), nil
}

// applyPAC 写入 PAC 自动配置 URL。
func (b *WindowsBackend) applyPAC(ctx context.Context, values map[string]string) (map[string]string, error) {
	pacURL := strings.TrimSpace(values[KeySystemProxyPAC])
	if pacURL == "" {
		pacURL = strings.TrimSpace(values["system_proxy.pac_url"])
	}
	if pacURL == "" {
		return nil, fmt.Errorf("netmgr: %s is required for pac mode", KeySystemProxyPAC)
	}

	applied, err := b.proxy.ApplyPAC(ctx, pacURL)
	if err != nil {
		return nil, err
	}
	if nerr := b.proxy.NotifyError(); nerr != nil {
		return translateFromWinproxy(applied), &TakeoverNotice{
			Mode:       ModePAC,
			MessageKey: "netmgr.notify_failed",
			Cause:      nerr,
		}
	}
	return translateFromWinproxy(applied), nil
}

// Restore 实现 Backend：恢复并做所有权校验。
func (b *WindowsBackend) Restore(ctx context.Context, original, applied map[string]string) (RestoreResult, error) {
	res, err := b.proxy.Restore(ctx, translateToWinproxy(original), translateToWinproxy(applied))
	if err != nil {
		return RestoreResult{}, err
	}

	out := RestoreResult{Restored: res.Restored, Missing: res.Missing}
	for _, c := range res.Conflicts {
		out.Conflicts = append(out.Conflicts, Conflict{
			Key:      c.Key,
			Expected: c.Expected,
			Actual:   c.Actual,
		})
	}
	// 恢复同样要通知系统；失败也只是降级提示。
	// 不通知时「断开连接」后应用仍以为该走代理，表现为「断开之后没网」。
	if nerr := b.proxy.NotifyError(); nerr != nil {
		return out, &TakeoverNotice{
			Mode:       "restore",
			MessageKey: "netmgr.notify_failed",
			Cause:      nerr,
		}
	}
	return out, nil
}

// TakeoverNotice 表示「接管已成功，但有一项需要注意」。
//
// 它不是失败：注册表已经改对了。存在的理由是这类降级**必须可见**——
// 静默吞掉时用户只看到「有些应用没走代理」，而永远不知道为什么。
type TakeoverNotice struct {
	Mode       string
	MessageKey string
	Cause      error
}

func (e *TakeoverNotice) Error() string {
	return fmt.Sprintf("netmgr: takeover of %q succeeded with a notice (%s): %v",
		e.Mode, e.MessageKey, e.Cause)
}

// Unwrap 让调用方能取到底层原因。
func (e *TakeoverNotice) Unwrap() error { return e.Cause }

// ---------------------------------------------------------------------------
// 键名翻译
// ---------------------------------------------------------------------------

// 通用键与 winproxy 键的映射。
//
// 两套键名故意不同：通用键属于平台无关的协调层（"system_proxy.enabled"），
// 而 winproxy 的键名贴着注册表语义（"proxy.enable"）。
// 让它们同名会掩盖「这一层在做翻译」这件事，
// 而翻译正是最容易出错、也最需要被单独测试的地方。
func translateFromWinproxy(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[genericKeyFor(k)] = v
	}
	return out
}

func translateToWinproxy(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[winproxyKeyFor(k)] = v
	}
	return out
}

// winproxy 的键名（与 winproxy 包内的常量保持一致）。
const (
	wpKeyProxyEnable   = "proxy.enable"
	wpKeyProxyServer   = "proxy.server"
	wpKeyProxyOverride = "proxy.override"
	wpKeyAutoConfigURL = "proxy.auto_config_url"
)

func genericKeyFor(k string) string {
	switch k {
	case wpKeyProxyEnable:
		return KeySystemProxyEnabled
	case wpKeyProxyServer:
		return KeySystemProxyServer
	case wpKeyProxyOverride:
		return KeySystemProxyBypass
	case wpKeyAutoConfigURL:
		return KeySystemProxyPAC
	default:
		return k
	}
}

func winproxyKeyFor(k string) string {
	switch k {
	case KeySystemProxyEnabled:
		return wpKeyProxyEnable
	case KeySystemProxyServer:
		return wpKeyProxyServer
	case KeySystemProxyBypass:
		return wpKeyProxyOverride
	case KeySystemProxyPAC:
		return wpKeyAutoConfigURL
	default:
		return k
	}
}

// splitHostPort 把 "127.0.0.1:7890" 拆成 host 与 port。
func splitHostPort(value string) (string, int, error) {
	idx := strings.LastIndex(value, ":")
	if idx <= 0 || idx == len(value)-1 {
		return "", 0, fmt.Errorf("netmgr: proxy address must be host:port, got %q", value)
	}
	host := value[:idx]
	port, err := strconv.Atoi(value[idx+1:])
	if err != nil {
		return "", 0, fmt.Errorf("netmgr: invalid port in %q: %w", value, err)
	}
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("netmgr: port out of range in %q", value)
	}
	return host, port, nil
}
