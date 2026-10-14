// Package netmgr 把具体平台的网络接管实现适配到 coordinator.NetworkManager。
//
// 分层理由：
//   - coordinator 只依赖 4 个抽象接口，不知道 Windows 注册表或 GNOME 设置的存在；
//   - 平台后端各自实现 Backend，互不干扰；
//   - 这一层负责「模式 → 后端」的分派、键的翻译与结果转换。
//
// 安全约定：本层只做有限、可枚举的设置读写，不执行任意命令、不写任意文件。
package netmgr

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"proxyclient/runtime/coordinator"
)

// 接入方式常量，与 client-config.json 的 trafficEntry 取值一致。
const (
	ModeSystemProxy = "system_proxy"
	ModeTUN         = "tun"
	ModePAC         = "pac"
	ModeLocalProxy  = "local_proxy"
)

// 网络设置的通用键。
//
// 各后端负责把这些键映射到自己的表示（注册表值名、gsettings 键等）。
// 用统一键而不是让上层理解每个平台，是为了让「所有权比较」与「冲突报告」
// 能在平台无关的代码里完成。
const (
	KeySystemProxyEnabled = "system_proxy.enabled"
	KeySystemProxyServer  = "system_proxy.server"
	KeySystemProxyBypass  = "system_proxy.bypass"
	KeySystemProxyPAC     = "system_proxy.pac"
	KeyTUNEnabled         = "tun.enabled"
	KeyTUNDevice          = "tun.device"
)

// NoteKind 是适配层给出的提示类别。
type NoteKind string

const (
	// NoteUnsupported 表示当前平台/后端不支持该接入方式。
	NoteUnsupported NoteKind = "unsupported"
	// NotePartial 表示支持但能力有限（例如只影响 GNOME 桌面应用）。
	NotePartial NoteKind = "partial"
	// NoteNoop 表示该接入方式不需要改动系统设置。
	NoteNoop NoteKind = "noop"
)

// Note 是一条能力提示，用于界面诚实说明接管范围。
//
// 本地化约定（见 design/architecture/i18n-convention.md）：
//   - MessageKey 与 MessageArgs 是界面文案的唯一来源；
//   - Message 只是内部诊断文本，**不得**直接显示给用户。
//
// 设计基线明确要求：桌面代理必须说明接管范围，
// 不能让用户误以为「所有应用的流量都已进入代理」。
type Note struct {
	Kind        NoteKind
	Mode        string
	MessageKey  string
	MessageArgs []any
	Message     string
	// Reason 是后端给出的「为何不可用」的额外说明（仅诊断用，非文案）。
	Reason string
}

// noteReasonOrNone 返回提示里的原因说明，没有时给出占位。
func noteReasonOrNone(n Note) string {
	if n.Reason == "" {
		return "no reason given"
	}
	return n.Reason
}

// newNote 构造一条带文案键的提示。
func newNote(kind NoteKind, mode, messageKey string, args ...any) Note {
	return Note{
		Kind:        kind,
		Mode:        mode,
		MessageKey:  messageKey,
		MessageArgs: args,
		Message:     fmt.Sprintf("netmgr note: kind=%s mode=%s key=%s", kind, mode, messageKey),
	}
}

// Result 是一次接管的返回值。
type Result struct {
	Note Note
}

// Backend 是平台后端的契约。
type Backend interface {
	// Name 返回后端标识，用于诊断与日志。
	Name() string
	// SupportedModes 返回该后端支持的接入方式。
	SupportedModes() []string
	// Snapshot 读取当前网络设置；只返回**确实存在**的键。
	Snapshot(ctx context.Context) (map[string]string, error)
	// Apply 依据模式与通用键接管网络，返回实际写入的值。
	Apply(ctx context.Context, mode string, values map[string]string) (map[string]string, error)
	// Restore 恢复设置，并做所有权校验。
	//
	// 约定：当前值等于 applied 里的期望值才写回 original；
	// 不等则记入 Conflicts 且**不覆盖**。
	Restore(ctx context.Context, original, applied map[string]string) (RestoreResult, error)
}

// RestoreResult 是恢复结果（与 coordinator.RestoreResult 对应）。
type RestoreResult struct {
	Restored  []string
	Conflicts []Conflict
	Missing   []string
}

// Conflict 描述一处所有权冲突。
type Conflict struct {
	Key      string
	Expected string
	Actual   string
}

// unsupportedReasoner 允许后端解释「为何不支持某个模式」。
//
// 存在理由：只告诉用户「不支持」没有可操作性。
// NoopBackend 能说明「本平台尚未实现系统代理接管」，
// 这对判断是自己配错了还是软件还没做完至关重要。
type unsupportedReasoner interface {
	UnsupportedReason(mode string) string
}

// Manager 实现 coordinator.NetworkManager。
type Manager struct {
	backend Backend
	// notes 记录最近一次接管给出的提示，供界面展示。
	notes []Note
}

// New 构造适配层。
//
// backend 为 nil 时使用 NoopBackend：它不接管任何设置，
// 并明确声明所有接入方式均不受支持——而不是假装成功。
func New(backend Backend) *Manager {
	if backend == nil {
		backend = NoopBackend{Reason: "no platform backend was provided"}
	}
	return &Manager{backend: backend}
}

// Backend 返回当前后端。
func (m *Manager) Backend() Backend { return m.backend }

// Notes 返回最近一次接管给出的提示。
func (m *Manager) Notes() []Note {
	out := make([]Note, len(m.notes))
	copy(out, m.notes)
	return out
}

// Snapshot 实现 coordinator.NetworkManager。
func (m *Manager) Snapshot(ctx context.Context) (coordinator.NetworkSettings, error) {
	raw, err := m.backend.Snapshot(ctx)
	if err != nil {
		// 读取失败必须向上传播：返回空快照会让上层以为系统本来没有设置，
		// 从而跳过恢复，把用户原始配置丢掉。
		return nil, fmt.Errorf("netmgr: snapshot via %s: %w", m.backend.Name(), err)
	}
	return toSettings(raw), nil
}

// Apply 实现 coordinator.NetworkManager。
func (m *Manager) Apply(ctx context.Context, plan coordinator.NetworkPlan) (coordinator.NetworkSettings, error) {
	mode := plan.Mode
	if mode == "" {
		mode = ModeLocalProxy
	}

	if !m.supports(mode) {
		// 不支持时明确报错，并给出可展示的提示——绝不静默不做事。
		note := newNote(NoteUnsupported, mode, "netmgr.mode_unsupported", m.backend.Name(), mode)

		// 后端若愿意解释原因，就把它带上：只说「不支持」没有可操作性。
		// 注意顺序：必须在 append 之前填好 Reason，否则记录到的是不含原因的副本。
		if r, ok := m.backend.(unsupportedReasoner); ok {
			if reason := r.UnsupportedReason(mode); reason != "" {
				note.Reason = reason
			}
		}

		m.notes = append(m.notes, note)
		return nil, fmt.Errorf("netmgr: backend %s does not support mode %s; system networking was left unchanged (%s)",
			m.backend.Name(), mode, noteReasonOrNone(note))
	}

	if mode == ModeLocalProxy {
		// 本地代理只启动本地端口，不改动任何系统设置。
		m.notes = append(m.notes, newNote(NoteNoop, mode, "netmgr.local_proxy_noop"))
		return coordinator.NetworkSettings{}, nil
	}

	applied, err := m.backend.Apply(ctx, mode, toRaw(plan.Values))
	if err != nil {
		return nil, fmt.Errorf("netmgr: apply %s via %s: %w", mode, m.backend.Name(), err)
	}

	m.notes = append(m.notes, m.takeoverNote(mode))
	return toSettings(applied), nil
}

// Restore 实现 coordinator.NetworkManager。
func (m *Manager) Restore(ctx context.Context, original, applied coordinator.NetworkSettings) (coordinator.RestoreResult, error) {
	if len(applied) == 0 {
		// 没有接管过任何设置：无需恢复。
		return coordinator.RestoreResult{}, nil
	}

	result, err := m.backend.Restore(ctx, toRaw(original), toRaw(applied))
	if err != nil {
		return coordinator.RestoreResult{}, fmt.Errorf("netmgr: restore via %s: %w", m.backend.Name(), err)
	}

	out := coordinator.RestoreResult{
		Restored: append([]string(nil), result.Restored...),
		Missing:  append([]string(nil), result.Missing...),
	}
	for _, c := range result.Conflicts {
		out.Conflicts = append(out.Conflicts, coordinator.RestoreConflict{
			Key:      c.Key,
			Expected: c.Expected,
			Actual:   c.Actual,
		})
	}
	// 排序让输出稳定，便于比对与测试。
	sort.Strings(out.Restored)
	sort.Strings(out.Missing)
	sort.Slice(out.Conflicts, func(i, j int) bool { return out.Conflicts[i].Key < out.Conflicts[j].Key })
	return out, nil
}

// supports 判断后端是否支持某接入方式。
func (m *Manager) supports(mode string) bool {
	if mode == ModeLocalProxy {
		return true // 不需要后端能力
	}
	for _, supported := range m.backend.SupportedModes() {
		if supported == mode {
			return true
		}
	}
	return false
}

// takeoverNote 给出该模式的接管范围提示。
//
// 这些文案刻意直白地点出限制，因为「以为所有流量都走了代理」
// 是这个类型软件最常见、也最危险的误解。
func (m *Manager) takeoverNote(mode string) Note {
	switch mode {
	case ModeSystemProxy:
		return newNote(NotePartial, mode, "netmgr.scope.system_proxy")
	case ModePAC:
		return newNote(NotePartial, mode, "netmgr.scope.pac")
	case ModeTUN:
		return newNote(NotePartial, mode, "netmgr.scope.tun")
	default:
		return newNote(NoteNoop, mode, "netmgr.scope.none")
	}
}

// toSettings 把通用映射转为 coordinator 的类型。
func toSettings(raw map[string]string) coordinator.NetworkSettings {
	if raw == nil {
		return coordinator.NetworkSettings{}
	}
	out := make(coordinator.NetworkSettings, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	return out
}

// toRaw 把 coordinator 的类型转为通用映射。
func toRaw(settings coordinator.NetworkSettings) map[string]string {
	if settings == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(settings))
	for k, v := range settings {
		out[k] = v
	}
	return out
}

// ValidateMode 校验接入方式取值。
func ValidateMode(mode string) error {
	switch mode {
	case ModeSystemProxy, ModeTUN, ModePAC, ModeLocalProxy:
		return nil
	case "":
		return errors.New("netmgr: mode is empty")
	default:
		return fmt.Errorf("netmgr: unknown mode %q", mode)
	}
}

// ParseHostPort 从 "host:port" 形式的键值里解析出主机与端口。
//
// 供后端实现复用，避免各自写一份解析逻辑。
// 端口解析刻意用 strconv.Atoi 而非 fmt.Sscanf：后者会把 "7890abc"
// 解析成 7890 而不报错，等于放过了格式错误的值。
func ParseHostPort(value string) (string, int, error) {
	idx := strings.LastIndex(value, ":")
	if idx <= 0 || idx == len(value)-1 {
		return "", 0, fmt.Errorf("netmgr: %q is not host:port", value)
	}
	host := strings.Trim(value[:idx], "[]")
	port, err := strconv.Atoi(value[idx+1:])
	if err != nil {
		return "", 0, fmt.Errorf("netmgr: %q has an invalid port", value)
	}
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("netmgr: %q has an out-of-range port", value)
	}
	return host, port, nil
}
