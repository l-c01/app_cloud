package netmgr

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Runner 执行外部命令。
//
// 与 winproxy 一样做成接口：全部逻辑（键映射、值编码、所有权比较）
// 都可以在不执行任何命令的前提下被测试。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner 使用 os/exec（生产实现）。
type ExecRunner struct{}

// Run 实现 Runner。
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// valueKind 是 gsettings 值的类型。
type valueKind int

const (
	kindString valueKind = iota
	kindInt
	kindStringArray
)

// gsettingsKey 描述一个受管理的 gsettings 键。
type gsettingsKey struct {
	schema string
	key    string
	kind   valueKind
}

// gnomeKeys 是受管理的 gsettings 键集合。
//
// 刻意只覆盖代理相关的最小集合，不做「遍历全部设置」这种事。
var gnomeKeys = []gsettingsKey{
	{"org.gnome.system.proxy", "mode", kindString},
	{"org.gnome.system.proxy", "autoconfig-url", kindString},
	{"org.gnome.system.proxy", "ignore-hosts", kindStringArray},
	{"org.gnome.system.proxy.http", "host", kindString},
	{"org.gnome.system.proxy.http", "port", kindInt},
	{"org.gnome.system.proxy.https", "host", kindString},
	{"org.gnome.system.proxy.https", "port", kindInt},
	{"org.gnome.system.proxy.socks", "host", kindString},
	{"org.gnome.system.proxy.socks", "port", kindInt},
}

// settingsName 返回该键在通用映射里的名字。
//
// 用 "schema key" 形式，保证不同 schema 下同名键不会互相覆盖。
func (k gsettingsKey) settingsName() string {
	return k.schema + " " + k.key
}

// GNOMEBackend 通过 gsettings 接管 GNOME 桌面代理设置。
//
// 重要限制：gsettings 只影响读取 GNOME 代理设置的应用。
// 不遵循该设置的命令行工具与自绘代理逻辑的软件不会经过代理——
// 因此它属于「部分接管」，界面必须如实说明。
type GNOMEBackend struct {
	runner Runner
}

// NewGNOMEBackend 构造 GNOME 后端。
func NewGNOMEBackend(runner Runner) *GNOMEBackend {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &GNOMEBackend{runner: runner}
}

// Name 实现 Backend。
func (*GNOMEBackend) Name() string { return "gnome-gsettings" }

// SupportedModes 实现 Backend。
//
// TUN 不在此列：它由核心侧提供，不需要通过桌面设置接管。
func (*GNOMEBackend) SupportedModes() []string {
	return []string{ModeSystemProxy, ModePAC}
}

// Available 报告 gsettings 是否可用（非 GNOME 桌面通常不可用）。
func (g *GNOMEBackend) Available(ctx context.Context) bool {
	if _, err := g.runner.Run(ctx, "gsettings", "get", "org.gnome.system.proxy", "mode"); err != nil {
		return false
	}
	return true
}

// Snapshot 实现 Backend：读取全部受管理键。
func (g *GNOMEBackend) Snapshot(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(gnomeKeys))
	for _, k := range gnomeKeys {
		value, found, err := g.read(ctx, k)
		if err != nil {
			// 读取失败不能当作「不存在」：那会让上层以为系统本来没有设置，
			// 从而跳过恢复。
			return nil, fmt.Errorf("netmgr: read %s: %w", k.settingsName(), err)
		}
		if found {
			out[k.settingsName()] = value
		}
	}
	return out, nil
}

// Apply 实现 Backend。
func (g *GNOMEBackend) Apply(ctx context.Context, mode string, values map[string]string) (map[string]string, error) {
	switch mode {
	case ModeSystemProxy:
		return g.applySystemProxy(ctx, values)
	case ModePAC:
		return g.applyPAC(ctx, values)
	default:
		return nil, fmt.Errorf("netmgr: gnome backend does not support mode %q", mode)
	}
}

// applySystemProxy 写入静态代理设置。
func (g *GNOMEBackend) applySystemProxy(ctx context.Context, values map[string]string) (map[string]string, error) {
	httpValue := values[KeySystemProxyServer]
	if httpValue == "" {
		// 兼容 coordinator 生成的键名（system_proxy.http）。
		httpValue = values["system_proxy.http"]
	}
	host, port, err := ParseHostPort(httpValue)
	if err != nil {
		return nil, err
	}

	ignoreHosts := values[KeySystemProxyBypass]
	if ignoreHosts == "" {
		ignoreHosts = values["system_proxy.bypass"]
	}
	if ignoreHosts == "" {
		ignoreHosts = "localhost,127.0.0.0/8,::1"
	}

	applied := map[string]string{}

	// 先写 host/port，最后写 mode：避免出现「已切到 manual 但地址还没写」的瞬间。
	httpHostKey := gsettingsKey{"org.gnome.system.proxy.http", "host", kindString}
	if err := g.write(ctx, httpHostKey, host, applied); err != nil {
		return nil, err
	}
	httpPortKey := gsettingsKey{"org.gnome.system.proxy.http", "port", kindInt}
	if err := g.write(ctx, httpPortKey, strconv.Itoa(port), applied); err != nil {
		return nil, err
	}

	// https 与 http 共用同一代理（本地核心同时处理两种）。
	httpsHostKey := gsettingsKey{"org.gnome.system.proxy.https", "host", kindString}
	if err := g.write(ctx, httpsHostKey, host, applied); err != nil {
		return nil, err
	}
	httpsPortKey := gsettingsKey{"org.gnome.system.proxy.https", "port", kindInt}
	if err := g.write(ctx, httpsPortKey, strconv.Itoa(port), applied); err != nil {
		return nil, err
	}

	// SOCKS 端口如果提供，也一并设置。
	if socksValue := values["system_proxy.socks"]; socksValue != "" {
		socksHost, socksPort, perr := ParseHostPort(socksValue)
		if perr != nil {
			return nil, perr
		}
		socksHostKey := gsettingsKey{"org.gnome.system.proxy.socks", "host", kindString}
		if err := g.write(ctx, socksHostKey, socksHost, applied); err != nil {
			return nil, err
		}
		socksPortKey := gsettingsKey{"org.gnome.system.proxy.socks", "port", kindInt}
		if err := g.write(ctx, socksPortKey, strconv.Itoa(socksPort), applied); err != nil {
			return nil, err
		}
	}

	ignoreKey := gsettingsKey{"org.gnome.system.proxy", "ignore-hosts", kindStringArray}
	if err := g.write(ctx, ignoreKey, ignoreHosts, applied); err != nil {
		return nil, err
	}

	// 静态代理与 PAC 互斥。
	autoconfigKey := gsettingsKey{"org.gnome.system.proxy", "autoconfig-url", kindString}
	if err := g.write(ctx, autoconfigKey, "", applied); err != nil {
		return nil, err
	}

	modeKey := gsettingsKey{"org.gnome.system.proxy", "mode", kindString}
	if err := g.write(ctx, modeKey, "manual", applied); err != nil {
		return nil, err
	}

	return applied, nil
}

// applyPAC 切换到 PAC 模式。
func (g *GNOMEBackend) applyPAC(ctx context.Context, values map[string]string) (map[string]string, error) {
	pacURL := values[KeySystemProxyPAC]
	if pacURL == "" {
		pacURL = values["system_proxy.pac"]
	}
	if pacURL == "" {
		return nil, errors.New("netmgr: PAC mode requires a PAC URL")
	}
	if !strings.HasPrefix(pacURL, "http://127.0.0.1:") && !strings.HasPrefix(pacURL, "http://localhost:") {
		// PAC 必须由本客户端的本地服务提供；不接受任意外部地址。
		return nil, fmt.Errorf("netmgr: PAC URL must point at the local PAC server, got %q", pacURL)
	}
	if err := validateVariantString(pacURL); err != nil {
		return nil, err
	}

	applied := map[string]string{}
	autoconfigKey := gsettingsKey{"org.gnome.system.proxy", "autoconfig-url", kindString}
	if err := g.write(ctx, autoconfigKey, pacURL, applied); err != nil {
		return nil, err
	}

	modeKey := gsettingsKey{"org.gnome.system.proxy", "mode", kindString}
	if err := g.write(ctx, modeKey, "auto", applied); err != nil {
		return nil, err
	}
	return applied, nil
}

// Restore 实现 Backend：带所有权校验。
//
// 当前值等于 applied 里的期望值才写回 original；不等则记入冲突且不覆盖。
func (g *GNOMEBackend) Restore(ctx context.Context, original, applied map[string]string) (RestoreResult, error) {
	var result RestoreResult

	for name, expected := range applied {
		k, ok := lookupGNOMEKey(name)
		if !ok {
			continue // 不认识的键：跳过而不是猜
		}

		current, found, err := g.read(ctx, k)
		if err != nil {
			return result, err
		}

		if !found || current != expected {
			actual := current
			if !found {
				actual = ""
			}
			result.Conflicts = append(result.Conflicts, Conflict{Key: name, Expected: expected, Actual: actual})
			continue
		}

		if originalValue, had := original[name]; had {
			if err := g.write(ctx, k, originalValue, nil); err != nil {
				return result, err
			}
			result.Restored = append(result.Restored, name)
			continue
		}
		result.Missing = append(result.Missing, name)
	}

	return result, nil
}

// read 读取一个 gsettings 键。
//
// 返回 (值, 是否存在, 错误)。gsettings 对不存在的 schema/键返回非零退出码，
// 但普通读取失败也是非零——因此这里把非零统一视为「不可读」并**报错**，
// 而不是猜测。可用性由 Available 单独判定。
func (g *GNOMEBackend) read(ctx context.Context, k gsettingsKey) (string, bool, error) {
	out, err := g.runner.Run(ctx, "gsettings", "get", k.schema, k.key)
	if err != nil {
		return "", false, err
	}
	value, perr := decodeVariant(strings.TrimSpace(string(out)), k.kind)
	if perr != nil {
		return "", false, perr
	}
	return value, true, nil
}

// write 写入一个 gsettings 键，并记录到 applied（可为 nil）。
func (g *GNOMEBackend) write(ctx context.Context, k gsettingsKey, value string, applied map[string]string) error {
	encoded, err := encodeVariant(value, k.kind)
	if err != nil {
		return err
	}
	if _, err := g.runner.Run(ctx, "gsettings", "set", k.schema, k.key, encoded); err != nil {
		return fmt.Errorf("netmgr: set %s: %w", k.settingsName(), err)
	}
	if applied != nil {
		applied[k.settingsName()] = value
	}
	return nil
}

// lookupGNOMEKey 按通用名找回键定义。
func lookupGNOMEKey(name string) (gsettingsKey, bool) {
	for _, k := range gnomeKeys {
		if k.settingsName() == name {
			return k, true
		}
	}
	return gsettingsKey{}, false
}

// encodeVariant 把值编码成 gsettings 接受的 GVariant 文本。
func encodeVariant(value string, kind valueKind) (string, error) {
	switch kind {
	case kindInt:
		if value == "" {
			// 端口为空时写 0，表示「未设置端口」。
			return "0", nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 65535 {
			return "", fmt.Errorf("netmgr: %q is not a valid port", value)
		}
		return strconv.Itoa(n), nil

	case kindString:
		if err := validateVariantString(value); err != nil {
			return "", err
		}
		return "'" + value + "'", nil

	case kindStringArray:
		// ignore-hosts 用逗号分隔；空值表示空数组。
		parts := strings.Split(value, ",")
		items := make([]string, 0, len(parts))
		for _, p := range parts {
			item := strings.TrimSpace(p)
			if item == "" {
				continue
			}
			if err := validateVariantString(item); err != nil {
				return "", err
			}
			items = append(items, "'"+item+"'")
		}
		return "[" + strings.Join(items, ", ") + "]", nil

	default:
		return "", fmt.Errorf("netmgr: unsupported value kind %d", kind)
	}
}

// decodeVariant 把 gsettings 的输出解析为纯值。
func decodeVariant(raw string, kind valueKind) (string, error) {
	switch kind {
	case kindInt:
		return raw, nil

	case kindString:
		// gsettings 输出带单引号，例如 'manual'。
		if len(raw) >= 2 && strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") {
			return raw[1 : len(raw)-1], nil
		}
		return raw, nil

	case kindStringArray:
		// 形如 ['a', 'b'] 或 @as []；这里拼回逗号分隔形式，
		// 与编码时使用的表示保持一致，便于所有权比较。
		inner := strings.TrimSpace(raw)
		inner = strings.TrimPrefix(inner, "@as ")
		inner = strings.TrimPrefix(inner, "[")
		inner = strings.TrimSuffix(inner, "]")
		if strings.TrimSpace(inner) == "" {
			return "", nil
		}
		parts := strings.Split(inner, ",")
		items := make([]string, 0, len(parts))
		for _, p := range parts {
			item := strings.TrimSpace(p)
			item = strings.TrimPrefix(item, "'")
			item = strings.TrimSuffix(item, "'")
			if item != "" {
				items = append(items, item)
			}
		}
		return strings.Join(items, ","), nil

	default:
		return "", fmt.Errorf("netmgr: unsupported value kind %d", kind)
	}
}

// validateVariantString 拒绝会破坏 GVariant 文本的字符。
//
// 参数是通过 exec 直接传递的（不经过 shell），所以这里防的是
// GVariant 语法层面的破坏，而不是命令注入。
func validateVariantString(value string) error {
	if strings.ContainsAny(value, "'\"\\\r\n\x00") {
		return fmt.Errorf("netmgr: value contains characters that cannot be encoded safely")
	}
	if len(value) > 2048 {
		return errors.New("netmgr: value is too long")
	}
	return nil
}
