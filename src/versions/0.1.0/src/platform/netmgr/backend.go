package netmgr

import (
	"context"
	"fmt"
)

// NoopBackend 是不接管任何设置的占位后端。
//
// 用途：
//   - 平台实现尚未提供时（例如 Android 走 VpnService 而非本层）；
//   - 在不支持系统级接管的桌面上（例如没有 Known 的会话总线）。
//
// 关键约定：它**不支持**任何接入方式，因此 Apply 会明确失败，
// 而不是假装成功。用户会看到「本平台未实现系统代理接管」，
// 而不是以为代理已生效却实际未生效。
type NoopBackend struct {
	// Reason 解释为何不可用，会出现在错误与提示里。
	Reason string
}

// Name 实现 Backend。
func (NoopBackend) Name() string { return "noop" }

// SupportedModes 实现 Backend：不支持任何模式。
func (NoopBackend) SupportedModes() []string { return nil }

// Snapshot 实现 Backend：返回空快照。
func (NoopBackend) Snapshot(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

// Apply 实现 Backend：明确失败。
func (n NoopBackend) Apply(_ context.Context, mode string, _ map[string]string) (map[string]string, error) {
	reason := n.Reason
	if reason == "" {
		reason = "this platform has no system network takeover implementation"
	}
	return nil, fmt.Errorf("netmgr: refusing to take over %q: %s", mode, reason)
}

// Restore 实现 Backend：无需恢复。
func (NoopBackend) Restore(context.Context, map[string]string, map[string]string) (RestoreResult, error) {
	return RestoreResult{}, nil
}

// staticBackend 用于测试与演示：记录调用并返回预设结果。
//
// 它放在非测试文件里，是因为「可注入的假后端」也是运行期需要的
// （例如诊断模式下的干跑）。
type staticBackend struct {
	name       string
	modes      []string
	snapshot   map[string]string
	applied    map[string]string
	restore    RestoreResult
	snapshotFn func(context.Context) (map[string]string, error)
	applyFn    func(context.Context, string, map[string]string) (map[string]string, error)
	restoreFn  func(context.Context, map[string]string, map[string]string) (RestoreResult, error)

	applyCalls   int
	restoreCalls int
	lastMode     string
}

func (s *staticBackend) Name() string { return s.name }

func (s *staticBackend) SupportedModes() []string { return s.modes }

func (s *staticBackend) Snapshot(ctx context.Context) (map[string]string, error) {
	if s.snapshotFn != nil {
		return s.snapshotFn(ctx)
	}
	return s.snapshot, nil
}

func (s *staticBackend) Apply(ctx context.Context, mode string, values map[string]string) (map[string]string, error) {
	s.applyCalls++
	s.lastMode = mode
	if s.applyFn != nil {
		return s.applyFn(ctx, mode, values)
	}
	if s.applied != nil {
		return s.applied, nil
	}
	return values, nil
}

func (s *staticBackend) Restore(ctx context.Context, original, applied map[string]string) (RestoreResult, error) {
	s.restoreCalls++
	if s.restoreFn != nil {
		return s.restoreFn(ctx, original, applied)
	}
	return s.restore, nil
}
