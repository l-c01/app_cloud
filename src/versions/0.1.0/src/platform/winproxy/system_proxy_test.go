package winproxy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner 记录命令并返回预设输出。
//
// 用它测试全部逻辑，不需要真的执行 reg.exe —— 因此这些测试在任何平台上都能跑。
type fakeRunner struct {
	calls []string
	// outputs 按命令关键字匹配返回输出；键是子串匹配（例如 "query"）。
	outputs map[string]string
	// errors 按同样的规则决定是否返回错误。
	errors map[string]error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		outputs: make(map[string]string),
		errors:  make(map[string]error),
	}
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)

	for key, err := range f.errors {
		if strings.Contains(call, key) {
			return []byte(f.outputs[key]), err
		}
	}
	for key, out := range f.outputs {
		if strings.Contains(call, key) {
			return []byte(out), nil
		}
	}
	return nil, nil
}

func (f *fakeRunner) lastCall() string {
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeRunner) findCall(substr string) string {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return c
		}
	}
	return ""
}

// regQueryOutput 构造与 reg.exe 实际输出形态一致的文本。
func regQueryOutput(pairs ...string) string {
	var b strings.Builder
	b.WriteString("\nHKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings\n")
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString("    " + pairs[i] + "    REG_SZ    " + pairs[i+1] + "\n")
	}
	return b.String()
}

// ---- Snapshot ----

func TestSnapshotParsesExistingValues(t *testing.T) {
	r := newFakeRunner()
	// 每次 query 都返回同一段文本；解析函数只取需要的行。
	r.outputs["query"] = regQueryOutput(
		"ProxyEnable", "1",
		"ProxyServer", "127.0.0.1:7890",
		"ProxyOverride", "<local>;192.168.*",
	)
	m := New(r)

	got, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got[keyProxyEnable] != "1" {
		t.Errorf("enabled = %q", got[keyProxyEnable])
	}
	if got[keyProxyServer] != "127.0.0.1:7890" {
		t.Errorf("server = %q", got[keyProxyServer])
	}
	if got[keyProxyOverride] != "<local>;192.168.*" {
		t.Errorf("bypass = %q", got[keyProxyOverride])
	}
}

func TestSnapshotOmitsKeysThatDoNotExist(t *testing.T) {
	// 关键在于区分「原本为空」与「原本没有这个键」：恢复行为不同。
	r := newFakeRunner()
	r.outputs["query"] = regQueryOutput("ProxyEnable", "0")
	m := New(r)

	got, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, present := got[keyProxyServer]; present {
		t.Error("a value that does not exist must not appear in the snapshot")
	}
	if _, present := got[keyAutoConfigURL]; present {
		t.Error("AutoConfigURL should be absent")
	}
}

// ---- ApplySystemProxy ----

func TestApplySystemProxyWritesServerBypassThenEnable(t *testing.T) {
	r := newFakeRunner()
	m := New(r)

	applied, err := m.ApplySystemProxy(context.Background(), "127.0.0.1", 7890)
	if err != nil {
		t.Fatalf("ApplySystemProxy: %v", err)
	}

	if applied[keyProxyServer] != "127.0.0.1:7890" {
		t.Errorf("server = %q", applied[keyProxyServer])
	}
	if applied[keyProxyEnable] != "1" {
		t.Errorf("enabled = %q", applied[keyProxyEnable])
	}
	if applied[keyProxyOverride] == "" {
		t.Error("bypass list should be set")
	}

	// 顺序很重要：enabled 必须最后写，避免出现「已启用但地址未写」的瞬间。
	enableIdx, serverIdx := -1, -1
	for i, c := range r.calls {
		if strings.Contains(c, "ProxyEnable") && strings.Contains(c, "/d 1") {
			enableIdx = i
		}
		if strings.Contains(c, "ProxyServer") && strings.Contains(c, "/d 127.0.0.1:7890") {
			serverIdx = i
		}
	}
	if enableIdx < 0 || serverIdx < 0 {
		t.Fatalf("expected both writes, got %v", r.calls)
	}
	if enableIdx < serverIdx {
		t.Errorf("ProxyEnable must be written last (enableIdx=%d serverIdx=%d)", enableIdx, serverIdx)
	}
}

func TestApplySystemProxyUsesForceFlagSoItNeverHangs(t *testing.T) {
	// 缺少 /f 时 reg.exe 会交互式询问是否覆盖，在无人值守场景下会挂住。
	r := newFakeRunner()
	m := New(r)
	if _, err := m.ApplySystemProxy(context.Background(), "127.0.0.1", 7890); err != nil {
		t.Fatalf("ApplySystemProxy: %v", err)
	}
	for _, c := range r.calls {
		if strings.Contains(c, "add") && !strings.Contains(c, "/f") {
			t.Errorf("reg add must use /f: %s", c)
		}
	}
}

func TestApplySystemProxyRejectsInvalidPort(t *testing.T) {
	m := New(newFakeRunner())
	for _, port := range []int{0, -1, 70000} {
		if _, err := m.ApplySystemProxy(context.Background(), "127.0.0.1", port); err == nil {
			t.Errorf("port %d must be rejected", port)
		}
	}
}

func TestApplySystemProxyClearsPACWhenTakingOverStaticProxy(t *testing.T) {
	// 静态代理与 PAC 互斥；残留的 AutoConfigURL 会让部分应用优先走 PAC。
	r := newFakeRunner()
	r.outputs["query"] = regQueryOutput("AutoConfigURL", "http://127.0.0.1:7892/proxy.pac")
	m := New(r)

	if _, err := m.ApplySystemProxy(context.Background(), "127.0.0.1", 7890); err != nil {
		t.Fatalf("ApplySystemProxy: %v", err)
	}
	if c := r.findCall("delete"); c == "" || !strings.Contains(c, "AutoConfigURL") {
		t.Errorf("AutoConfigURL should be removed, calls=%v", r.calls)
	}
}

// ---- 注入防护 ----

func TestProxyValueRejectsInjectionAttempts(t *testing.T) {
	m := New(newFakeRunner())
	// 这些值若被拼进命令行会变成额外参数或命令。
	// 通过 host 参数注入：host 由 coordinator 保证是环回地址，但这里仍要验证防线。
	bad := []string{
		"127.0.0.1 & calc.exe",
		"127.0.0.1\" /v Evil /d x",
		"127.0.0.1;rm -rf",
		"127.0.0.1|whoami",
	}
	for _, host := range bad {
		if _, err := m.ApplySystemProxy(context.Background(), host, 7890); err == nil {
			t.Errorf("host %q must be rejected", host)
		}
	}
}

func TestValidateProxyValue(t *testing.T) {
	valid := []string{"127.0.0.1:7890", "localhost:1080", "[::1]:8080", "proxy.example.com:3128"}
	for _, v := range valid {
		if err := validateProxyValue(v); err != nil {
			t.Errorf("validateProxyValue(%q) = %v, want nil", v, err)
		}
	}
	invalid := []string{"", "127.0.0.1", "127.0.0.1:", "127.0.0.1:0", "127.0.0.1:99999", "127.0.0.1:abc", "a b:80"}
	for _, v := range invalid {
		if err := validateProxyValue(v); err == nil {
			t.Errorf("validateProxyValue(%q) should fail", v)
		}
	}
}

func TestValidateRegValueRejectsQuotesAndNewlines(t *testing.T) {
	for _, v := range []string{`a"b`, "a'b", "a\nb", "a\rb"} {
		if err := validateRegValue(v); err == nil {
			t.Errorf("value %q must be rejected", v)
		}
	}
	if err := validateRegValue("127.0.0.1:7890;<local>"); err != nil {
		t.Errorf("a normal bypass list should be accepted: %v", err)
	}
}

// ---- PAC ----

func TestApplyPACRequiresLocalURL(t *testing.T) {
	m := New(newFakeRunner())
	if _, err := m.ApplyPAC(context.Background(), "http://example.com/proxy.pac"); err == nil {
		t.Error("an external PAC URL must be rejected")
	}
	if _, err := m.ApplyPAC(context.Background(), "http://127.0.0.1:7892/proxy.pac"); err != nil {
		t.Errorf("a local PAC URL should be accepted: %v", err)
	}
}

func TestApplyPACDisablesStaticProxy(t *testing.T) {
	r := newFakeRunner()
	m := New(r)
	applied, err := m.ApplyPAC(context.Background(), "http://127.0.0.1:7892/proxy.pac")
	if err != nil {
		t.Fatalf("ApplyPAC: %v", err)
	}
	if applied[keyProxyEnable] != "0" {
		t.Errorf("enabled = %q, want 0", applied[keyProxyEnable])
	}
}

// ---- Restore 与所有权校验 ----

func TestRestoreWritesBackOriginalWhenValueIsStillOurs(t *testing.T) {
	r := newFakeRunner()
	r.outputs["query"] = regQueryOutput("ProxyServer", "127.0.0.1:7890")
	m := New(r)

	original := map[string]string{keyProxyServer: "10.0.0.5:3128"}
	applied := map[string]string{keyProxyServer: "127.0.0.1:7890"}

	result, err := m.Restore(context.Background(), original, applied)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(result.Restored) != 1 || result.Restored[0] != keyProxyServer {
		t.Errorf("restored = %v", result.Restored)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("unexpected conflicts: %v", result.Conflicts)
	}
	if c := r.findCall("add"); !strings.Contains(c, "10.0.0.5:3128") {
		t.Errorf("the original value should be written back, calls=%v", r.calls)
	}
}

func TestRestoreReportsConflictAndDoesNotOverwrite(t *testing.T) {
	// 核心契约：当前值已被用户或其他软件修改时，只报告冲突，绝不覆盖。
	r := newFakeRunner()
	r.outputs["query"] = regQueryOutput("ProxyServer", "10.0.0.99:8080") // 已被他人改成别的值
	m := New(r)

	original := map[string]string{keyProxyServer: "10.0.0.5:3128"}
	applied := map[string]string{keyProxyServer: "127.0.0.1:7890"}

	result, err := m.Restore(context.Background(), original, applied)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("expected exactly one conflict, got %v", result.Conflicts)
	}
	conflict := result.Conflicts[0]
	if conflict.Key != keyProxyServer {
		t.Errorf("conflict key = %q", conflict.Key)
	}
	if conflict.Expected != "127.0.0.1:7890" || conflict.Actual != "10.0.0.99:8080" {
		t.Errorf("conflict = %+v", conflict)
	}
	// 绝不能发生任何写入。
	if c := r.findCall("add"); c != "" {
		t.Errorf("a conflict must not trigger a write, but found: %s", c)
	}
}

func TestRestoreDeletesValueThatWeCreated(t *testing.T) {
	// 原本没有这个键：恢复时应删除，而不是写一个空字符串。
	r := newFakeRunner()
	r.outputs["query"] = regQueryOutput("ProxyEnable", "1")
	m := New(r)

	original := map[string]string{} // 原本没有
	applied := map[string]string{keyProxyEnable: "1"}

	result, err := m.Restore(context.Background(), original, applied)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(result.Missing) != 1 {
		t.Errorf("missing = %v", result.Missing)
	}
	if c := r.findCall("delete"); !strings.Contains(c, "ProxyEnable") {
		t.Errorf("the created value should be deleted, calls=%v", r.calls)
	}
}

func TestRestoreSkipsUnknownKeys(t *testing.T) {
	// applied 里出现本包不认识的键时应跳过，而不是报错或猜。
	m := New(newFakeRunner())
	result, err := m.Restore(context.Background(), nil, map[string]string{"tun.enabled": "true"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(result.Restored)+len(result.Missing)+len(result.Conflicts) != 0 {
		t.Errorf("unknown keys should be ignored, got %+v", result)
	}
}

// ---- 输出解析 ----

func TestParseRegQueryOutputToleratesFormatVariations(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string
		found  bool
	}{
		{
			name:   "typical",
			output: "HKEY_CURRENT_USER\\...\\Internet Settings\r\n    ProxyServer    REG_SZ    127.0.0.1:7890\r\n",
			want:   "127.0.0.1:7890",
			found:  true,
		},
		{
			name:   "value containing spaces",
			output: "    ProxyOverride    REG_SZ    <local>; 192.168.*\r\n",
			want:   "<local>; 192.168.*",
			found:  true,
		},
		{
			name:   "different name only",
			output: "    ProxyEnable    REG_SZ    1\r\n",
			want:   "",
			found:  false,
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
			found:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := "ProxyServer"
			if tc.name == "different name only" {
				name = "ProxyServer"
			}
			if tc.name == "value containing spaces" {
				name = "ProxyOverride"
			}
			got, found := parseRegQueryOutput(tc.output, name)
			if found != tc.found {
				t.Fatalf("found = %v, want %v", found, tc.found)
			}
			if found && got != tc.want {
				t.Errorf("value = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnavailableRunnerGivesAClearError(t *testing.T) {
	// 不支持的平台上必须得到明确错误，而不是静默不做事。
	m := New(UnavailableRunner{})
	if _, err := m.ApplySystemProxy(context.Background(), "127.0.0.1", 7890); err == nil {
		t.Fatal("expected an error on an unsupported platform")
	}
}

func TestNewDefaultsToExecRunner(t *testing.T) {
	m := New(nil)
	if _, ok := m.runner.(ExecRunner); !ok {
		t.Errorf("runner = %T, want ExecRunner", m.runner)
	}
}

func TestSnapshotPropagatesRealReadFailures(t *testing.T) {
	// 契约：读取失败**不能**被当成「值不存在」。
	//
	// 若混为一谈，快照会返回空映射，上层就会以为系统本来没有代理设置，
	// 从而跳过恢复，把用户的原始配置永久丢掉。
	r := newFakeRunner()
	r.errors["query"] = errors.New("access denied")
	m := New(r)

	if _, err := m.Snapshot(context.Background()); err == nil {
		t.Fatal("a real read failure must be propagated, not swallowed")
	}
}

func TestSnapshotTreatsNotFoundAsAbsent(t *testing.T) {
	// 值不存在是正常情况：快照里不记录该键，且不报错。
	r := newFakeRunner()
	r.errors["query"] = ErrValueNotFound
	m := New(r)

	got, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("snapshot = %v, want empty", got)
	}
}
