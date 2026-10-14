package netmgr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"proxyclient/runtime/coordinator"
)

// gsettingsRunner 是 GNOME 后端的假执行器：内存里维护一份设置。
type gsettingsRunner struct {
	values map[string]string
	calls  []string
	fail   error
	// unavailable 模拟非 GNOME 环境（任何调用都失败）。
	unavailable bool
}

func newGsettingsRunner() *gsettingsRunner {
	return &gsettingsRunner{values: map[string]string{
		"org.gnome.system.proxy mode":           "none",
		"org.gnome.system.proxy autoconfig-url": "",
		"org.gnome.system.proxy ignore-hosts":   "localhost",
		"org.gnome.system.proxy.http host":      "",
		"org.gnome.system.proxy.http port":      "0",
		"org.gnome.system.proxy.https host":     "",
		"org.gnome.system.proxy.https port":     "0",
		"org.gnome.system.proxy.socks host":     "",
		"org.gnome.system.proxy.socks port":     "0",
	}}
}

func (g *gsettingsRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	g.calls = append(g.calls, call)
	if g.unavailable {
		return nil, errors.New("gsettings not found")
	}
	if g.fail != nil {
		return nil, g.fail
	}
	if len(args) < 3 || name != "gsettings" {
		return nil, errors.New("unexpected command")
	}
	verb, schema, key := args[0], args[1], args[2]
	id := schema + " " + key

	switch verb {
	case "get":
		v, ok := g.values[id]
		if !ok {
			return nil, errors.New("no such key")
		}
		// 还原成 gsettings 的真实输出形态（字符串带引号）。
		if strings.HasSuffix(key, "port") {
			return []byte(v + "\n"), nil
		}
		if key == "ignore-hosts" {
			if v == "" {
				return []byte("@as []\n"), nil
			}
			parts := strings.Split(v, ",")
			for i := range parts {
				parts[i] = "'" + strings.TrimSpace(parts[i]) + "'"
			}
			return []byte("[" + strings.Join(parts, ", ") + "]\n"), nil
		}
		return []byte("'" + v + "'\n"), nil
	case "set":
		if len(args) < 4 {
			return nil, errors.New("set needs a value")
		}
		raw := args[3]
		// 去掉 GVariant 的外层引号/括号，存进内存。
		switch {
		case strings.HasPrefix(raw, "["):
			inner := strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
			var items []string
			for _, p := range strings.Split(inner, ",") {
				item := strings.Trim(strings.TrimSpace(p), "'")
				if item != "" {
					items = append(items, item)
				}
			}
			g.values[id] = strings.Join(items, ",")
		case strings.HasPrefix(raw, "'"):
			g.values[id] = strings.Trim(raw, "'")
		default:
			g.values[id] = raw
		}
		return nil, nil
	default:
		return nil, errors.New("unsupported gsettings verb")
	}
}

func (g *gsettingsRunner) findCall(substr string) string {
	for _, c := range g.calls {
		if strings.Contains(c, substr) {
			return c
		}
	}
	return ""
}

// ---- Manager ----

func TestManagerDispatchesToBackend(t *testing.T) {
	b := &staticBackend{
		name:     "test",
		modes:    []string{ModeSystemProxy},
		applied:  map[string]string{KeySystemProxyEnabled: "1"},
		snapshot: map[string]string{KeySystemProxyEnabled: "0"},
	}
	m := New(b)

	snap, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap[KeySystemProxyEnabled] != "0" {
		t.Errorf("snapshot = %v", snap)
	}

	applied, err := m.Apply(context.Background(), coordinator.NetworkPlan{
		Mode:   ModeSystemProxy,
		Values: coordinator.NetworkSettings{KeySystemProxyServer: "127.0.0.1:7890"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if b.applyCalls != 1 || b.lastMode != ModeSystemProxy {
		t.Errorf("backend not dispatched: calls=%d mode=%q", b.applyCalls, b.lastMode)
	}
	if applied[KeySystemProxyEnabled] != "1" {
		t.Errorf("applied = %v", applied)
	}
}

func TestManagerRejectsUnsupportedModeExplicitly(t *testing.T) {
	// 契约：后端不支持时必须明确报错并给出提示，绝不静默不做事。
	b := &staticBackend{name: "test", modes: []string{ModeSystemProxy}}
	m := New(b)

	_, err := m.Apply(context.Background(), coordinator.NetworkPlan{Mode: ModeTUN})
	if err == nil {
		t.Fatal("an unsupported mode must fail")
	}
	if b.applyCalls != 0 {
		t.Errorf("backend must not be called for an unsupported mode: %d calls", b.applyCalls)
	}

	notes := m.Notes()
	if len(notes) != 1 || notes[0].Kind != NoteUnsupported {
		t.Errorf("expected an unsupported note, got %v", notes)
	}
	if notes[0].Mode != ModeTUN {
		t.Errorf("note mode = %q", notes[0].Mode)
	}
}

func TestLocalProxyModeNeverTouchesTheBackend(t *testing.T) {
	b := &staticBackend{name: "test", modes: []string{ModeSystemProxy}}
	m := New(b)

	applied, err := m.Apply(context.Background(), coordinator.NetworkPlan{Mode: ModeLocalProxy})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("local_proxy must not change settings, got %v", applied)
	}
	if b.applyCalls != 0 {
		t.Errorf("backend must not be called for local_proxy: %d calls", b.applyCalls)
	}
	if notes := m.Notes(); len(notes) != 1 || notes[0].Kind != NoteNoop {
		t.Errorf("expected a noop note, got %v", notes)
	}
}

func TestEmptyModeIsTreatedAsLocalProxy(t *testing.T) {
	b := &staticBackend{name: "test", modes: []string{ModeSystemProxy}}
	m := New(b)
	if _, err := m.Apply(context.Background(), coordinator.NetworkPlan{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if b.applyCalls != 0 {
		t.Errorf("empty mode should not reach the backend")
	}
}

func TestSnapshotFailureIsPropagated(t *testing.T) {
	// 读取失败不能返回空快照，否则上层会以为系统本来没有设置而跳过恢复。
	b := &staticBackend{
		name:  "test",
		modes: []string{ModeSystemProxy},
		snapshotFn: func(context.Context) (map[string]string, error) {
			return nil, errors.New("registry unavailable")
		},
	}
	m := New(b)
	if _, err := m.Snapshot(context.Background()); err == nil {
		t.Fatal("snapshot failure must be propagated")
	}
}

func TestRestoreWithNothingAppliedIsANoop(t *testing.T) {
	b := &staticBackend{name: "test", modes: []string{ModeSystemProxy}}
	m := New(b)

	result, err := m.Restore(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if b.restoreCalls != 0 {
		t.Errorf("backend must not be called when nothing was applied")
	}
	if len(result.Restored)+len(result.Conflicts)+len(result.Missing) != 0 {
		t.Errorf("result = %+v", result)
	}
}

func TestRestoreConvertsResultAndSorts(t *testing.T) {
	b := &staticBackend{
		name:  "test",
		modes: []string{ModeSystemProxy},
		restore: RestoreResult{
			Restored: []string{"z", "a"},
			Missing:  []string{"m", "b"},
			Conflicts: []Conflict{
				{Key: "k2", Expected: "e2", Actual: "a2"},
				{Key: "k1", Expected: "e1", Actual: "a1"},
			},
		},
	}
	m := New(b)

	result, err := m.Restore(context.Background(),
		coordinator.NetworkSettings{KeySystemProxyEnabled: "0"},
		coordinator.NetworkSettings{KeySystemProxyEnabled: "1"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// 稳定排序便于界面对比与测试。
	if result.Restored[0] != "a" || result.Missing[0] != "b" {
		t.Errorf("results not sorted: %+v", result)
	}
	if result.Conflicts[0].Key != "k1" || result.Conflicts[1].Key != "k2" {
		t.Errorf("conflicts not sorted: %+v", result.Conflicts)
	}
}

func TestTakeoverNotesAreHonestAboutScope(t *testing.T) {
	// 设计基线要求界面如实说明接管范围，不能让用户以为所有流量都进了代理。
	b := &staticBackend{name: "test", modes: []string{ModeSystemProxy, ModePAC, ModeTUN}}
	m := New(b)

	for _, mode := range []string{ModeSystemProxy, ModePAC, ModeTUN} {
		if _, err := m.Apply(context.Background(), coordinator.NetworkPlan{Mode: mode}); err != nil {
			t.Fatalf("Apply %s: %v", mode, err)
		}
	}

	notes := m.Notes()
	if len(notes) != 3 {
		t.Fatalf("expected 3 notes, got %d", len(notes))
	}
	for _, n := range notes {
		if n.Kind != NotePartial {
			t.Errorf("mode %s should be reported as partial takeover, got %q", n.Mode, n.Kind)
		}
		// 文案键必须存在：界面据此本地化，Message 只用于诊断，不能当文案用。
		if n.MessageKey == "" {
			t.Errorf("mode %s needs a message key for the UI to render", n.Mode)
		}
	}
	// 系统代理的提示必须是「部分接管」那一类。
	var sysProxyKey string
	for _, n := range notes {
		if n.Mode == ModeSystemProxy {
			sysProxyKey = n.MessageKey
		}
	}
	if sysProxyKey != "netmgr.scope.system_proxy" {
		t.Errorf("system proxy note key = %q, want netmgr.scope.system_proxy", sysProxyKey)
	}
}

func TestUnsupportedNoteCarriesTheArguments(t *testing.T) {
	// 提示需要带上后端名与模式，界面才能拼出完整句子。
	b := &staticBackend{name: "test-backend", modes: []string{ModeSystemProxy}}
	m := New(b)
	if _, err := m.Apply(context.Background(), coordinator.NetworkPlan{Mode: ModeTUN}); err == nil {
		t.Fatal("expected an error for an unsupported mode")
	}

	notes := m.Notes()
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %d", len(notes))
	}
	note := notes[0]
	if note.MessageKey != "netmgr.mode_unsupported" {
		t.Errorf("key = %q", note.MessageKey)
	}
	if len(note.MessageArgs) != 2 {
		t.Fatalf("expected 2 arguments (backend, mode), got %v", note.MessageArgs)
	}
	if note.MessageArgs[0] != "test-backend" || note.MessageArgs[1] != ModeTUN {
		t.Errorf("args = %v", note.MessageArgs)
	}
}

func TestNoopBackendRefusesEverything(t *testing.T) {
	m := New(NoopBackend{Reason: "not implemented on this platform"})
	_, err := m.Apply(context.Background(), coordinator.NetworkPlan{Mode: ModeSystemProxy})
	if err == nil {
		t.Fatal("the noop backend must refuse rather than pretend")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should carry the reason: %v", err)
	}
}

func TestNilBackendFallsBackToNoop(t *testing.T) {
	m := New(nil)
	if m.Backend().Name() != "noop" {
		t.Errorf("backend = %q", m.Backend().Name())
	}
	if _, err := m.Apply(context.Background(), coordinator.NetworkPlan{Mode: ModeSystemProxy}); err == nil {
		t.Error("a nil backend must not silently succeed")
	}
}

// ---- GNOME 后端 ----

func TestGNOMESnapshotReadsAllManagedKeys(t *testing.T) {
	r := newGsettingsRunner()
	r.values["org.gnome.system.proxy mode"] = "manual"
	r.values["org.gnome.system.proxy.http host"] = "127.0.0.1"
	r.values["org.gnome.system.proxy.http port"] = "7890"
	b := NewGNOMEBackend(r)

	snap, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap["org.gnome.system.proxy mode"] != "manual" {
		t.Errorf("mode = %q", snap["org.gnome.system.proxy mode"])
	}
	if snap["org.gnome.system.proxy.http host"] != "127.0.0.1" {
		t.Errorf("host = %q", snap["org.gnome.system.proxy.http host"])
	}
	if snap["org.gnome.system.proxy.http port"] != "7890" {
		t.Errorf("port = %q", snap["org.gnome.system.proxy.http port"])
	}
}

func TestGNOMESnapshotFailureIsPropagated(t *testing.T) {
	r := newGsettingsRunner()
	r.fail = errors.New("dconf is not writable")
	b := NewGNOMEBackend(r)
	if _, err := b.Snapshot(context.Background()); err == nil {
		t.Fatal("a read failure must be propagated, not turned into an empty snapshot")
	}
}

func TestGNOMEApplySystemProxyWritesModeLast(t *testing.T) {
	r := newGsettingsRunner()
	b := NewGNOMEBackend(r)

	applied, err := b.Apply(context.Background(), ModeSystemProxy, map[string]string{
		KeySystemProxyServer: "127.0.0.1:7890",
		"system_proxy.socks": "127.0.0.1:7891",
		KeySystemProxyBypass: "localhost,127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if applied["org.gnome.system.proxy mode"] != "manual" {
		t.Errorf("mode = %q", applied["org.gnome.system.proxy mode"])
	}
	if applied["org.gnome.system.proxy.http host"] != "127.0.0.1" {
		t.Errorf("http host = %q", applied["org.gnome.system.proxy.http host"])
	}
	if applied["org.gnome.system.proxy.http port"] != "7890" {
		t.Errorf("http port = %q", applied["org.gnome.system.proxy.http port"])
	}
	if applied["org.gnome.system.proxy.socks port"] != "7891" {
		t.Errorf("socks port = %q", applied["org.gnome.system.proxy.socks port"])
	}

	// mode 必须最后写：避免出现「已切到 manual 但地址还没写」的瞬间。
	modeIdx, hostIdx := -1, -1
	for i, c := range r.calls {
		if strings.Contains(c, "set org.gnome.system.proxy mode") {
			modeIdx = i
		}
		if strings.Contains(c, "set org.gnome.system.proxy.http host") {
			hostIdx = i
		}
	}
	if modeIdx < 0 || hostIdx < 0 {
		t.Fatalf("expected both writes, got %v", r.calls)
	}
	if modeIdx < hostIdx {
		t.Errorf("mode must be written last (modeIdx=%d hostIdx=%d)", modeIdx, hostIdx)
	}
}

func TestGNOMEApplySystemProxyRejectsBadEndpoint(t *testing.T) {
	b := NewGNOMEBackend(newGsettingsRunner())
	if _, err := b.Apply(context.Background(), ModeSystemProxy, map[string]string{
		KeySystemProxyServer: "not-a-host-port",
	}); err == nil {
		t.Fatal("an invalid endpoint must be rejected")
	}
}

func TestGNOMEApplyPACRequiresLocalURL(t *testing.T) {
	b := NewGNOMEBackend(newGsettingsRunner())
	if _, err := b.Apply(context.Background(), ModePAC, map[string]string{
		KeySystemProxyPAC: "http://example.com/proxy.pac",
	}); err == nil {
		t.Error("an external PAC URL must be rejected")
	}

	applied, err := b.Apply(context.Background(), ModePAC, map[string]string{
		KeySystemProxyPAC: "http://127.0.0.1:7892/proxy.pac",
	})
	if err != nil {
		t.Fatalf("Apply PAC: %v", err)
	}
	if applied["org.gnome.system.proxy mode"] != "auto" {
		t.Errorf("mode = %q, want auto", applied["org.gnome.system.proxy mode"])
	}
}

func TestGNOMERestoreWritesBackOriginal(t *testing.T) {
	r := newGsettingsRunner()
	// 当前值等于我们写入的值：可以安全恢复。
	r.values["org.gnome.system.proxy.http host"] = "127.0.0.1"
	b := NewGNOMEBackend(r)

	result, err := b.Restore(context.Background(),
		map[string]string{"org.gnome.system.proxy.http host": "proxy.corp.example"},
		map[string]string{"org.gnome.system.proxy.http host": "127.0.0.1"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(result.Restored) != 1 {
		t.Fatalf("restored = %v", result.Restored)
	}
	if c := r.findCall("set org.gnome.system.proxy.http host"); !strings.Contains(c, "proxy.corp.example") {
		t.Errorf("original value should be written back, calls=%v", r.calls)
	}
}

func TestGNOMERestoreReportsConflictWithoutOverwriting(t *testing.T) {
	r := newGsettingsRunner()
	// 当前值是别人后来改的，不是我们写入的值。
	r.values["org.gnome.system.proxy.http host"] = "someone.else.example"
	b := NewGNOMEBackend(r)

	result, err := b.Restore(context.Background(),
		map[string]string{"org.gnome.system.proxy.http host": "proxy.corp.example"},
		map[string]string{"org.gnome.system.proxy.http host": "127.0.0.1"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("expected one conflict, got %+v", result)
	}
	if result.Conflicts[0].Actual != "someone.else.example" {
		t.Errorf("conflict actual = %q", result.Conflicts[0].Actual)
	}
	if c := r.findCall("set org.gnome.system.proxy.http host"); c != "" {
		t.Errorf("a conflict must not trigger a write, found: %s", c)
	}
}

func TestGNOMERestoreSkipsUnknownKeys(t *testing.T) {
	b := NewGNOMEBackend(newGsettingsRunner())
	result, err := b.Restore(context.Background(), nil, map[string]string{"tun.enabled": "true"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(result.Restored)+len(result.Conflicts)+len(result.Missing) != 0 {
		t.Errorf("unknown keys should be ignored, got %+v", result)
	}
}

func TestGNOMESupportedModes(t *testing.T) {
	b := NewGNOMEBackend(nil)
	modes := b.SupportedModes()
	want := map[string]bool{ModeSystemProxy: true, ModePAC: true}
	for _, m := range modes {
		if !want[m] {
			t.Errorf("unexpected supported mode %q", m)
		}
	}
	if len(modes) != 2 {
		t.Errorf("modes = %v", modes)
	}
}

func TestGNOMEAvailableDetectsMissingGsettings(t *testing.T) {
	r := newGsettingsRunner()
	r.unavailable = true
	b := NewGNOMEBackend(r)
	if b.Available(context.Background()) {
		t.Error("Available should be false when gsettings is missing")
	}

	if !NewGNOMEBackend(newGsettingsRunner()).Available(context.Background()) {
		t.Error("Available should be true when gsettings works")
	}
}

func TestGNOMEApplyRejectsUnknownMode(t *testing.T) {
	b := NewGNOMEBackend(newGsettingsRunner())
	if _, err := b.Apply(context.Background(), ModeTUN, nil); err == nil {
		t.Error("the GNOME backend must not claim to support TUN")
	}
}

// ---- 编码与解析辅助 ----

func TestVariantEncodingRejectsUnsafeCharacters(t *testing.T) {
	// 参数不经过 shell，因此这里防的是 GVariant 语法被破坏。
	for _, v := range []string{"a'b", `a"b`, "a\\b", "a\nb"} {
		if err := validateVariantString(v); err == nil {
			t.Errorf("value %q should be rejected", v)
		}
	}
	if err := validateVariantString("127.0.0.1"); err != nil {
		t.Errorf("a normal value should be accepted: %v", err)
	}
}

func TestParseHostPort(t *testing.T) {
	host, port, err := ParseHostPort("127.0.0.1:7890")
	if err != nil || host != "127.0.0.1" || port != 7890 {
		t.Fatalf("got %q %d %v", host, port, err)
	}
	if h, p, err := ParseHostPort("[::1]:8080"); err != nil || h != "::1" || p != 8080 {
		t.Errorf("ipv6: got %q %d %v", h, p, err)
	}
	for _, bad := range []string{"", "127.0.0.1", ":8080", "127.0.0.1:", "host:0", "host:99999"} {
		if _, _, err := ParseHostPort(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestValidateMode(t *testing.T) {
	for _, m := range []string{ModeSystemProxy, ModeTUN, ModePAC, ModeLocalProxy} {
		if err := ValidateMode(m); err != nil {
			t.Errorf("mode %q should be valid: %v", m, err)
		}
	}
	for _, m := range []string{"", "smart", "SYSTEM_PROXY"} {
		if err := ValidateMode(m); err == nil {
			t.Errorf("mode %q should be rejected", m)
		}
	}
}

func TestManagerSatisfiesCoordinatorNetworkManager(t *testing.T) {
	// 编译期契约：适配层必须可直接作为 coordinator.NetworkManager 使用。
	var _ coordinator.NetworkManager = (*Manager)(nil)
}
