package coordinator

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"proxyclient/shared/compiler"
)

// ---- 假实现 ----

type fakeCompiler struct {
	err   error
	calls int
}

func (f *fakeCompiler) CompileToFile(_ context.Context, req *compiler.Request, dir string) (*CompiledConfig, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &CompiledConfig{Core: req.Core, Path: dir + "/config.json", Report: &compiler.Report{}}, nil
}

type fakeRunning struct {
	readyErr  error
	stopErr   error
	stopCalls int
	// order 非空时，Stop 会把自己的动作追加进去。
	// 有了它就不需要再定义第二个 RunningCore 实现来记录调用顺序：
	// fakeCoreRunner.running 的类型是固定的，换一种实现就没法赋值。
	order *[]string
}

func (f *fakeRunning) Ready(context.Context) error { return f.readyErr }
func (f *fakeRunning) Stop(context.Context) error {
	f.stopCalls++
	if f.order != nil {
		*f.order = append(*f.order, "stop-core")
	}
	return f.stopErr
}
func (f *fakeRunning) Logs() []string { return nil }

type fakeCoreRunner struct {
	checkErr   error
	startErr   error
	running    *fakeRunning
	checkCalls int
	startCalls int
}

func (f *fakeCoreRunner) CheckConfig(context.Context, compiler.Core, string) error {
	f.checkCalls++
	return f.checkErr
}

func (f *fakeCoreRunner) Start(context.Context, compiler.Core, string) (RunningCore, error) {
	f.startCalls++
	if f.startErr != nil {
		return nil, f.startErr
	}
	if f.running == nil {
		f.running = &fakeRunning{}
	}
	return f.running, nil
}

type fakeNetwork struct {
	snapshotErr error
	applyErr    error
	restoreErr  error
	conflicts   []RestoreConflict

	snapshot NetworkSettings
	applied  NetworkSettings

	snapshotCalls int
	applyCalls    int
	restoreCalls  int

	restoreOriginal NetworkSettings
	restoreApplied  NetworkSettings
}

func (f *fakeNetwork) Snapshot(context.Context) (NetworkSettings, error) {
	f.snapshotCalls++
	if f.snapshotErr != nil {
		return nil, f.snapshotErr
	}
	if f.snapshot == nil {
		f.snapshot = NetworkSettings{"system_proxy.http": "original-value"}
	}
	return f.snapshot, nil
}

func (f *fakeNetwork) Apply(_ context.Context, plan NetworkPlan) (NetworkSettings, error) {
	f.applyCalls++
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	f.applied = plan.Values
	return plan.Values, nil
}

func (f *fakeNetwork) Restore(_ context.Context, original, applied NetworkSettings) (RestoreResult, error) {
	f.restoreCalls++
	f.restoreOriginal = original
	f.restoreApplied = applied
	if f.restoreErr != nil {
		return RestoreResult{}, f.restoreErr
	}
	keys := make([]string, 0, len(applied))
	for k := range applied {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return RestoreResult{Restored: keys, Conflicts: f.conflicts}, nil
}

type fakeJournal struct {
	entry  *JournalEntry
	saves  int
	clears int
}

func (f *fakeJournal) Save(e JournalEntry) error {
	copied := e
	f.entry = &copied
	f.saves++
	return nil
}

func (f *fakeJournal) Load() (*JournalEntry, error) { return f.entry, nil }

func (f *fakeJournal) Clear() error {
	f.entry = nil
	f.clears++
	return nil
}

// ---- 测试夹具 ----

type fixture struct {
	coord   *Coordinator
	comp    *fakeCompiler
	core    *fakeCoreRunner
	network *fakeNetwork
	journal *fakeJournal
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		comp:    &fakeCompiler{},
		core:    &fakeCoreRunner{},
		network: &fakeNetwork{},
		journal: &fakeJournal{},
	}

	c, err := New(Options{
		Compiler:  f.comp,
		Core:      f.core,
		Network:   f.network,
		Journal:   f.journal,
		ConfigDir: t.TempDir(),
		Owner:     "proxyclient-test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.coord = c
	return f
}

func systemProxyRequest() ConnectRequest {
	return ConnectRequest{
		Compile: &compiler.Request{
			Core:           compiler.CoreSingBox,
			SelectedNodeID: "node-1",
			Inbound: compiler.InboundOptions{
				Listen:    "127.0.0.1",
				MixedPort: 7890,
			},
			Policy: compiler.PolicyGlobal,
		},
		Mode: "system_proxy",
	}
}

// ---- 测试 ----

func TestNewValidatesRequiredDependencies(t *testing.T) {
	base := Options{
		Compiler:  &fakeCompiler{},
		Core:      &fakeCoreRunner{},
		Network:   &fakeNetwork{},
		Journal:   &fakeJournal{},
		ConfigDir: "/tmp/x",
	}
	cases := []struct {
		name   string
		mutate func(*Options)
	}{
		{"no compiler", func(o *Options) { o.Compiler = nil }},
		{"no core", func(o *Options) { o.Core = nil }},
		{"no network", func(o *Options) { o.Network = nil }},
		{"no journal", func(o *Options) { o.Journal = nil }},
		{"no config dir", func(o *Options) { o.ConfigDir = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.mutate(&o)
			if _, err := New(o); err == nil {
				t.Error("expected a construction error")
			}
		})
	}

	// 默认值应被填入。
	c, err := New(base)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.opts.Owner == "" {
		t.Error("Owner should default")
	}
	if c.opts.ReadyTimeout <= 0 || c.opts.StopTimeout <= 0 {
		t.Error("timeouts should default")
	}
}

func TestConnectSuccessReachesRunning(t *testing.T) {
	f := newFixture(t)

	if err := f.coord.Connect(context.Background(), systemProxyRequest()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	st := f.coord.Status()
	if st.State != StateRunning {
		t.Errorf("state = %q, want running", st.State)
	}
	if st.Core != compiler.CoreSingBox {
		t.Errorf("core = %q", st.Core)
	}
	if st.ActiveNode != "node-1" {
		t.Errorf("active node = %q", st.ActiveNode)
	}
	if f.network.applyCalls != 1 {
		t.Errorf("apply calls = %d, want 1", f.network.applyCalls)
	}
	if f.comp.calls != 1 || f.core.checkCalls != 1 || f.core.startCalls != 1 {
		t.Errorf("unexpected call counts: compile=%d check=%d start=%d",
			f.comp.calls, f.core.checkCalls, f.core.startCalls)
	}
	// 运行态应保留恢复日志，以便崩溃后仍能恢复网络。
	if f.journal.entry == nil {
		t.Fatal("journal should be retained while running")
	}
	if len(f.journal.entry.Applied) == 0 {
		t.Error("journal should record the applied network settings")
	}
	if f.journal.entry.Owner != "proxyclient-test" {
		t.Errorf("journal owner = %q", f.journal.entry.Owner)
	}
}

// 回归保护：编译阶段失败绝不能触碰网络，也绝不能覆盖已有的恢复日志。
//
// 曾经的缺陷：失败路径无条件写一条空日志，会把上一次崩溃遗留的恢复记录冲掉，
// 导致真正需要恢复的网络设置永久丢失。
func TestConnectCompileFailureDoesNotTouchNetworkOrJournal(t *testing.T) {
	f := newFixture(t)
	f.comp.err = errors.New("compile exploded")

	// 模拟上一次崩溃遗留的恢复记录。
	leftover := &JournalEntry{
		Generation: 7,
		Owner:      "proxyclient-test",
		Original:   NetworkSettings{"system_proxy.http": "clean"},
		Applied:    NetworkSettings{"system_proxy.http": "stale-proxy"},
	}
	f.journal.entry = leftover

	err := f.coord.Connect(context.Background(), systemProxyRequest())
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "compile config") {
		t.Errorf("error = %v, want it to mention the compile stage", err)
	}

	if f.network.applyCalls != 0 || f.network.restoreCalls != 0 {
		t.Errorf("network must not be touched: apply=%d restore=%d",
			f.network.applyCalls, f.network.restoreCalls)
	}
	if f.journal.saves != 0 {
		t.Errorf("journal must not be overwritten at this stage: saves=%d", f.journal.saves)
	}
	if f.journal.entry == nil || f.journal.entry.Applied["system_proxy.http"] != "stale-proxy" {
		t.Error("the leftover recovery record was lost")
	}
	if f.coord.Status().State != StateFailed {
		t.Errorf("state = %q, want failed", f.coord.Status().State)
	}
}

// 编译报告里存在错误级条目时必须中止：那意味着有节点被静默排除。
func TestConnectRejectsConfigWhenReportHasErrors(t *testing.T) {
	f := newFixture(t)

	report := &compiler.Report{}
	report.Errorf(nil, "protocol", "compiler.protocol_unsupported", "节点协议不受支持")
	if !report.HasErrors() {
		t.Fatal("fixture precondition: the report should contain an error")
	}

	c, err := New(Options{
		Compiler:  &reportCompiler{report: report},
		Core:      f.core,
		Network:   f.network,
		Journal:   f.journal,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	connectErr := c.Connect(context.Background(), systemProxyRequest())
	if connectErr == nil {
		t.Fatal("expected a failure when the compile report contains errors")
	}
	if !strings.Contains(connectErr.Error(), "compile report contains errors") {
		t.Errorf("error = %v", connectErr)
	}
	// 报告有错误时不得启动核心，也不得接管网络。
	if f.core.startCalls != 0 {
		t.Errorf("core must not start: startCalls=%d", f.core.startCalls)
	}
	if f.network.applyCalls != 0 {
		t.Errorf("network must not be touched: applyCalls=%d", f.network.applyCalls)
	}
}

// 无错误条目时应正常继续（确保上面的拦截不是因为「报告非空」而误伤）。
func TestConnectProceedsWhenReportIsClean(t *testing.T) {
	f := newFixture(t)
	f.comp.err = nil // 默认编译器返回空报告

	if err := f.coord.Connect(context.Background(), systemProxyRequest()); err != nil {
		t.Fatalf("Connect with a clean report should succeed: %v", err)
	}
	if f.coord.Status().State != StateRunning {
		t.Errorf("state = %q, want running", f.coord.Status().State)
	}
}

type reportCompiler struct {
	report *compiler.Report
	err    error
}

func (r *reportCompiler) CompileToFile(_ context.Context, req *compiler.Request, dir string) (*CompiledConfig, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &CompiledConfig{Core: req.Core, Path: dir + "/config.json", Report: r.report}, nil
}

func TestConnectCoreConfigCheckFailure(t *testing.T) {
	f := newFixture(t)
	f.core.checkErr = &fakeConfigError{}

	err := f.coord.Connect(context.Background(), systemProxyRequest())
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "core rejected the config") {
		t.Errorf("error = %v", err)
	}
	// 配置检查在启动之前，因此不应启动进程，也不应改动网络。
	if f.core.startCalls != 0 {
		t.Errorf("core should not be started: startCalls=%d", f.core.startCalls)
	}
	if f.network.applyCalls != 0 {
		t.Errorf("network must not be touched: applyCalls=%d", f.network.applyCalls)
	}
}

type fakeConfigError struct{}

func (fakeConfigError) Error() string { return "config rejected" }

func TestConnectReadyFailureStopsCore(t *testing.T) {
	f := newFixture(t)
	f.core.running = &fakeRunning{readyErr: errors.New("port never opened")}

	err := f.coord.Connect(context.Background(), systemProxyRequest())
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("error = %v", err)
	}
	// 核心已启动但未就绪：必须把它停掉，不能留下孤儿进程。
	if f.core.running.stopCalls == 0 {
		t.Error("a core that failed to become ready must be stopped")
	}
	if f.network.applyCalls != 0 {
		t.Errorf("network must not be taken over before the core is ready: applyCalls=%d", f.network.applyCalls)
	}
}

// 网络接管失败：核心必须被停掉，不能留下孤儿进程。
//
// 注意：Apply 失败时尚未产生 applied 集合，因此无需恢复网络，
// 但核心已经启动，必须回收。
func TestConnectNetworkApplyFailureStopsCore(t *testing.T) {
	f := newFixture(t)
	f.network.applyErr = errors.New("cannot write proxy settings")

	err := f.coord.Connect(context.Background(), systemProxyRequest())
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "apply network settings") {
		t.Errorf("error = %v", err)
	}
	if f.core.running == nil || f.core.running.stopCalls == 0 {
		t.Error("core must be stopped after a network failure")
	}
	if f.coord.Status().State != StateFailed {
		t.Errorf("state = %q, want failed", f.coord.Status().State)
	}
}

// 恢复失败时不能假装已回到干净状态，必须要求用户介入。
func TestConnectRecoveryFailureRequiresUserAttention(t *testing.T) {
	f := newFixture(t)
	f.network.restoreErr = errors.New("permission denied while restoring")

	// 先让连接成功（此时网络已接管），再由 Disconnect 触发恢复失败。
	if err := f.coord.Connect(context.Background(), systemProxyRequest()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	err := f.coord.Disconnect(context.Background())
	if err == nil {
		t.Fatal("expected disconnect to report the restore failure")
	}
	if !strings.Contains(err.Error(), "restore network") {
		t.Errorf("error = %v", err)
	}
	st := f.coord.Status()
	if st.State != StateRecoveryRequired {
		t.Errorf("state = %q, want recovery_required", st.State)
	}
	if !strings.Contains(st.LastError, "restore") {
		t.Errorf("lastError should explain the restore failure: %q", st.LastError)
	}
}

func TestConcurrentTransactionsAreRejected(t *testing.T) {
	f := newFixture(t)
	// 直接占用 busy 标志，模拟事务进行中。
	f.coord.mu.Lock()
	f.coord.busy = true
	f.coord.mu.Unlock()

	if err := f.coord.Connect(context.Background(), systemProxyRequest()); !errors.Is(err, ErrBusy) {
		t.Errorf("Connect error = %v, want ErrBusy", err)
	}
	if err := f.coord.Disconnect(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("Disconnect error = %v, want ErrBusy", err)
	}
}

func TestDisconnectRestoresNetworkAndClearsJournal(t *testing.T) {
	f := newFixture(t)
	if err := f.coord.Connect(context.Background(), systemProxyRequest()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := f.coord.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	st := f.coord.Status()
	if st.State != StateStopped {
		t.Errorf("state = %q, want stopped", st.State)
	}
	if f.network.restoreCalls != 1 {
		t.Errorf("restore calls = %d, want 1", f.network.restoreCalls)
	}
	// 恢复时必须同时提供原值与我们写入的值，供实现做所有权判断。
	if f.network.restoreOriginal["system_proxy.http"] != "original-value" {
		t.Errorf("restore original = %v", f.network.restoreOriginal)
	}
	if f.network.restoreApplied["system_proxy.http"] == "" {
		t.Errorf("restore applied = %v", f.network.restoreApplied)
	}
	if f.core.running == nil || f.core.running.stopCalls == 0 {
		t.Error("core must be stopped on disconnect")
	}
	if f.journal.entry != nil {
		t.Error("journal should be cleared after a clean disconnect")
	}
}

func TestDisconnectIsIdempotent(t *testing.T) {
	f := newFixture(t)
	if err := f.coord.Connect(context.Background(), systemProxyRequest()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := f.coord.Disconnect(context.Background()); err != nil {
		t.Fatalf("first Disconnect: %v", err)
	}
	// 幂等停止：再次调用不应报错（设计基线要求）。
	if err := f.coord.Disconnect(context.Background()); err != nil {
		t.Errorf("second Disconnect should be a no-op: %v", err)
	}
}

func TestDisconnectStopsCoreBeforeRestoringNetwork(t *testing.T) {
	// 顺序很重要：先停核心再恢复网络，否则会在一段时间内把系统指向不存在的代理。
	f := newFixture(t)
	order := make([]string, 0, 2)

	// 必须让 Start 直接返回这个可记录顺序的核心，这样 Connect 与 Disconnect
	// 操作的是同一个实例。若在 Connect 之后再替换，Disconnect 用的仍是旧实例。
	f.core.running = &fakeRunning{order: &order}
	net := &orderTrackingNetwork{inner: f.network, order: &order}

	c, err := New(Options{
		Compiler:  f.comp,
		Core:      f.core,
		Network:   net,
		Journal:   f.journal,
		ConfigDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.Connect(context.Background(), systemProxyRequest()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	order = order[:0]
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	if len(order) < 2 {
		t.Fatalf("expected both stop and restore to run, got %v", order)
	}
	if order[0] != "stop-core" || order[1] != "restore-network" {
		t.Errorf("order = %v, want stop-core before restore-network", order)
	}
}

type orderTrackingNetwork struct {
	inner *fakeNetwork
	order *[]string
}

func (o *orderTrackingNetwork) Snapshot(ctx context.Context) (NetworkSettings, error) {
	return o.inner.Snapshot(ctx)
}

func (o *orderTrackingNetwork) Apply(ctx context.Context, plan NetworkPlan) (NetworkSettings, error) {
	return o.inner.Apply(ctx, plan)
}

func (o *orderTrackingNetwork) Restore(ctx context.Context, original, applied NetworkSettings) (RestoreResult, error) {
	*o.order = append(*o.order, "restore-network")
	return o.inner.Restore(ctx, original, applied)
}

func TestRecoverWithoutJournalIsNoop(t *testing.T) {
	f := newFixture(t)
	result, err := f.coord.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil when there is nothing to recover", result)
	}
	if f.network.restoreCalls != 0 {
		t.Error("network must not be touched when there is nothing to recover")
	}
}

func TestRecoverRestoresLeftoverSettings(t *testing.T) {
	f := newFixture(t)
	f.journal.entry = &JournalEntry{
		Generation: 3,
		Owner:      "proxyclient-test",
		Original:   NetworkSettings{"system_proxy.http": "clean"},
		Applied:    NetworkSettings{"system_proxy.http": "127.0.0.1:7890"},
	}

	result, err := f.coord.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result == nil || len(result.Restored) == 0 {
		t.Errorf("expected restored keys, got %v", result)
	}
	if f.network.restoreCalls != 1 {
		t.Errorf("restore calls = %d, want 1", f.network.restoreCalls)
	}
	if f.journal.entry != nil {
		t.Error("journal should be cleared after a successful recovery")
	}
	if f.coord.Status().State != StateStopped {
		t.Errorf("state = %q, want stopped", f.coord.Status().State)
	}
}

func TestRecoverSkipsWhenCrashHappenedBeforeNetworkTakeover(t *testing.T) {
	f := newFixture(t)
	// 有日志但没有 applied：说明崩溃发生在接管网络之前。
	f.journal.entry = &JournalEntry{Generation: 1, Owner: "proxyclient-test"}

	result, err := f.coord.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
	if f.network.restoreCalls != 0 {
		t.Error("no restore should happen when nothing was applied")
	}
}

func TestRecoverSurfacesConflictsWithoutOverwriting(t *testing.T) {
	// 契约：当前值已被他人修改时，只报告冲突，不强行写回。
	f := newFixture(t)
	f.network.conflicts = []RestoreConflict{{
		Key:      "system_proxy.http",
		Expected: "127.0.0.1:7890",
		Actual:   "10.0.0.1:8080",
	}}
	f.journal.entry = &JournalEntry{
		Generation: 4,
		Owner:      "proxyclient-test",
		Original:   NetworkSettings{"system_proxy.http": "clean"},
		Applied:    NetworkSettings{"system_proxy.http": "127.0.0.1:7890"},
	}

	result, err := f.coord.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result == nil || len(result.Conflicts) != 1 {
		t.Fatalf("expected one conflict, got %v", result)
	}
	// 冲突必须出现在状态里，供 UI 提示用户处理。
	st := f.coord.Status()
	if len(st.Conflicts) != 1 || st.Conflicts[0].Key != "system_proxy.http" {
		t.Errorf("status conflicts = %v", st.Conflicts)
	}
}

func TestLocalProxyModeDoesNotTouchSystemSettings(t *testing.T) {
	// local_proxy 只启动本地端口，不应接管任何系统设置。
	f := newFixture(t)
	req := systemProxyRequest()
	req.Mode = "local_proxy"

	if err := f.coord.Connect(context.Background(), req); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if f.network.applyCalls != 1 {
		t.Fatalf("apply calls = %d", f.network.applyCalls)
	}
	if len(f.network.applied) != 0 {
		t.Errorf("local_proxy must not write system settings, got %v", f.network.applied)
	}
}

func TestPlanForModeProducesExpectedValues(t *testing.T) {
	req := &compiler.Request{
		Inbound: compiler.InboundOptions{Listen: "127.0.0.1", MixedPort: 7890},
	}

	sp := planForMode("system_proxy", req)
	if sp["system_proxy.http"] != "127.0.0.1:7890" {
		t.Errorf("system_proxy.http = %q", sp["system_proxy.http"])
	}
	// socks 与 http 必须指向**同一个**地址。
	//
	// 核心只监听一个 mixed 端口，因此两个键写不同地址就会让其中一条
	// 指向没人监听的端口——表现是「浏览器能上网、走 SOCKS 的程序连不上」。
	// 这条断言把「两者必须一致」钉住，而不是各断言各的。
	if sp["system_proxy.socks"] != "127.0.0.1:7890" {
		t.Errorf("system_proxy.socks = %q，期望与 http 相同的 127.0.0.1:7890", sp["system_proxy.socks"])
	}
	if sp["system_proxy.http"] != sp["system_proxy.socks"] {
		t.Errorf("http 与 socks 必须以同一个 mixed 端口接管，实际 %q / %q",
			sp["system_proxy.http"], sp["system_proxy.socks"])
	}

	if len(planForMode("local_proxy", req)) != 0 {
		t.Error("local_proxy should not take over anything")
	}
	if len(planForMode("unknown-mode", req)) != 0 {
		t.Error("unknown mode should not take over anything")
	}
	if _, ok := planForMode("tun", req)["tun.enabled"]; !ok {
		t.Error("tun mode should declare its key")
	}
}

func TestLoopbackNeverWritesWildcardAddress(t *testing.T) {
	// 系统代理指向 0.0.0.0 是无意义的；监听所有网卡时也必须回落到环回地址。
	cases := []struct {
		listen string
		want   string
	}{
		{"127.0.0.1", "127.0.0.1"},
		{"", "127.0.0.1"},
		{"0.0.0.0", "127.0.0.1"},
		{"::", "127.0.0.1"},
	}
	for _, tc := range cases {
		req := &compiler.Request{Inbound: compiler.InboundOptions{Listen: tc.listen}}
		if got := loopback(req); got != tc.want {
			t.Errorf("loopback(%q) = %q, want %q", tc.listen, got, tc.want)
		}
	}
	if got := loopback(nil); got != "127.0.0.1" {
		t.Errorf("loopback(nil) = %q", got)
	}
}

func TestConnectRejectsNilRequest(t *testing.T) {
	f := newFixture(t)
	if err := f.coord.Connect(context.Background(), ConnectRequest{}); err == nil {
		t.Error("a nil compile request must be rejected")
	}
}
