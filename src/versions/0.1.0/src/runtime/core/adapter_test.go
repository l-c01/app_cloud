package core

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"proxyclient/shared/secrets"
)

// TestCommandTableMatchesVerifiedCLI 锁定已核实的核心命令行事实。
//
// 依据：
//   - Xray：main/run.go:59（-test 只检查配置）、:26（run [-c config.json]）
//   - sing-box：cmd/sing-box/cmd.go:34（-c/--config）、cmd_check.go（check 子命令）
//
// 如果有人「顺手调整」了参数顺序或漏掉 -test，这个测试会失败。
func TestCommandTableMatchesVerifiedCLI(t *testing.T) {
	xray, ok := commandTable[KindXray]
	if !ok {
		t.Fatal("xray command spec missing")
	}
	if got := strings.Join(xray.checkArgs("/tmp/c.json"), " "); got != "run -test -c /tmp/c.json" {
		t.Errorf("xray check args = %q", got)
	}
	if got := strings.Join(xray.runArgs("/tmp/c.json"), " "); got != "run -c /tmp/c.json" {
		t.Errorf("xray run args = %q", got)
	}
	// Xray 用退出码 23 表示配置错误（main/run.go:86）。
	if len(xray.configErrorExitCodes) != 1 || xray.configErrorExitCodes[0] != 23 {
		t.Errorf("xray config error codes = %v, want [23]", xray.configErrorExitCodes)
	}

	sb, ok := commandTable[KindSingBox]
	if !ok {
		t.Fatal("sing-box command spec missing")
	}
	if got := strings.Join(sb.checkArgs("/tmp/c.json"), " "); got != "check -c /tmp/c.json" {
		t.Errorf("sing-box check args = %q", got)
	}
	if got := strings.Join(sb.runArgs("/tmp/c.json"), " "); got != "run --disable-color -c /tmp/c.json" {
		t.Errorf("sing-box run args = %q", got)
	}
}

func TestKindValid(t *testing.T) {
	for _, k := range []Kind{KindXray, KindSingBox} {
		if !k.Valid() {
			t.Errorf("%q should be valid", k)
		}
	}
	if Kind("mihomo").Valid() {
		t.Error("unknown kind must be invalid")
	}
}

func TestNewAdapterValidation(t *testing.T) {
	if _, err := NewAdapter("mihomo", "/bin/x", Options{}); err == nil {
		t.Error("unknown core must be rejected")
	}
	if _, err := NewAdapter(KindXray, "   ", Options{}); err == nil {
		t.Error("empty binary path must be rejected")
	}

	a, err := NewAdapter(KindXray, "/usr/bin/xray", Options{})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	if a.Kind() != KindXray || a.BinaryPath() != "/usr/bin/xray" {
		t.Errorf("adapter = %v / %v", a.Kind(), a.BinaryPath())
	}
	// 未指定时应填入默认值。
	if a.opts.LogLines != 500 {
		t.Errorf("LogLines = %d, want 500", a.opts.LogLines)
	}
	if a.opts.StopGrace != 5*time.Second {
		t.Errorf("StopGrace = %v, want 5s", a.opts.StopGrace)
	}
}

func TestStartRejectsSecondProcess(t *testing.T) {
	a, err := NewAdapter(KindXray, "/usr/bin/xray", Options{})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	// 直接占位，模拟「已有进程在跑」的状态。
	a.proc = &Process{done: make(chan struct{})}

	if _, err := a.Start(context.Background(), "/tmp/c.json"); err == nil {
		t.Fatal("starting a second process must be rejected, not silently replace it")
	}
}

func TestCheckConfigMissingBinaryIsNotAConfigError(t *testing.T) {
	// 环境问题与配置问题必须区分：前者不是配置错误，用户提示与重试策略不同。
	a, err := NewAdapter(KindXray, "/nonexistent/xray-binary", Options{})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	checkErr := a.CheckConfig(context.Background(), "/tmp/c.json")
	if checkErr == nil {
		t.Fatal("expected an error")
	}
	if _, isConfig := checkErr.(*ConfigError); isConfig {
		t.Error("a missing binary must not be reported as a config error")
	}
}

func TestConfigErrorFormatting(t *testing.T) {
	e := &ConfigError{
		Core:     KindXray,
		ExitCode: 23,
		Output:   "failed to load config\nsecond line\nthird line",
		Message:  "核心拒绝了该配置",
	}
	got := e.Error()
	if !strings.Contains(got, "exit 23") {
		t.Errorf("error should carry the exit code: %q", got)
	}
	if !strings.Contains(got, "failed to load config") {
		t.Errorf("error should carry the core output: %q", got)
	}
	// 只取第一行，避免错误消息被多行输出淹没。
	if strings.Contains(got, "third line") {
		t.Errorf("error should only include the first output line: %q", got)
	}
}

func TestRingBufferWrapsAndKeepsOrder(t *testing.T) {
	r := newRingBuffer(3)

	if got := r.snapshot(); len(got) != 0 {
		t.Errorf("fresh buffer should be empty, got %v", got)
	}

	r.add("a")
	r.add("b")
	if got := r.snapshot(); strings.Join(got, ",") != "a,b" {
		t.Errorf("snapshot = %v", got)
	}

	// 写满并覆盖：容量 3，写入 a..e 后应保留 c,d,e 且顺序正确。
	r.add("c")
	r.add("d")
	r.add("e")
	if got := r.snapshot(); strings.Join(got, ",") != "c,d,e" {
		t.Errorf("after wrap snapshot = %v, want c,d,e", got)
	}
}

func TestRingBufferTail(t *testing.T) {
	r := newRingBuffer(5)
	for _, s := range []string{"1", "2", "3", "4"} {
		r.add(s)
	}
	if got := r.tail(2); strings.Join(got, ",") != "3,4" {
		t.Errorf("tail(2) = %v", got)
	}
	// n 超过长度时返回全部。
	if got := r.tail(100); len(got) != 4 {
		t.Errorf("tail(100) = %v", got)
	}
	// n <= 0 时返回全部，避免调用方拿到空结果。
	if got := r.tail(0); len(got) != 4 {
		t.Errorf("tail(0) = %v", got)
	}
}

func TestWaitForListenSucceedsOnLiveListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if err := WaitForListen(context.Background(), ln.Addr().String(), time.Second); err != nil {
		t.Errorf("WaitForListen on a live listener: %v", err)
	}
}

func TestWaitForListenTimesOutOnClosedPort(t *testing.T) {
	// 先占一个端口再关掉，确保该端口上没有监听者。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	start := time.Now()
	err = WaitForListen(context.Background(), addr, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error for a closed port")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("timeout was not honoured: %v", elapsed)
	}
}

func TestWaitForListenHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	if err := WaitForListen(ctx, addr, 5*time.Second); err == nil {
		t.Fatal("expected an error after cancellation")
	}
}

// 日志转发必须脱敏：核心输出可能无意间带出凭据。
func TestScrubberIsAppliedToCapturedLogs(t *testing.T) {
	scrubber := secrets.NewScrubber()
	secret := "subscription-token-value-123"
	scrubber.Add(secret)

	a, err := NewAdapter(KindXray, "/usr/bin/xray", Options{Scrubber: scrubber})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	got := a.scrub("connecting with " + secret + " now")
	if strings.Contains(got, secret) {
		t.Errorf("credential leaked through the log path: %q", got)
	}
}

// 未配置清洗器时不应篡改内容（明确记录该行为，避免误以为默认安全）。
func TestWithoutScrubberLogsAreUnchanged(t *testing.T) {
	a, err := NewAdapter(KindXray, "/usr/bin/xray", Options{})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	const in = "plain text"
	if got := a.scrub(in); got != in {
		t.Errorf("scrub changed text without a scrubber: %q", got)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("  a  \n b \n"); got != "a" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("single"); got != "single" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("   "); got != "" {
		t.Errorf("firstLine = %q", got)
	}
}
