// Package core 管理核心（Xray / sing-box）子进程的生命周期。
//
// 职责边界（设计基线 §5.2）：
//   - 本包**只**负责：原生配置检查、启动/停止、进程健康、日志转发；
//   - 不负责：编译配置（由 shared/compiler 负责）、接管系统网络（由 platform 负责）、
//     决定何时连接（由 runtime/coordinator 负责）。
//
// 命令行依据（已核实的源码事实）：
//   - Xray：`xray run -test -c <file>` 做配置检查，输出 "Configuration OK."，退出码 0；
//     配置错误退出码为 23（src/references/Xray-core-main/main/run.go:59、:86-92）。
//     运行态为 `xray run -c <file>`，响应 SIGINT/SIGTERM 优雅退出（同文件 :104-108）。
//   - sing-box：`sing-box check -c <file>` 检查配置；`sing-box run -c <file>` 运行。
//     全局参数含 -c/--config 与 --disable-color
//     （src/references/sing-box-testing/cmd/sing-box/cmd.go:34-37、cmd_check.go:Use="check"）。
package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"proxyclient/shared/secrets"
)

// Kind 标识核心类型，取值与 compiler.Core、coreupdater.Core 一致。
type Kind string

const (
	KindXray    Kind = "xray"
	KindSingBox Kind = "sing-box"
)

// Valid 判断核心标识是否受支持。
func (k Kind) Valid() bool { return k == KindXray || k == KindSingBox }

// commandSpec 描述某个核心的命令行形态。
type commandSpec struct {
	// checkArgs 返回配置检查的参数（不含可执行文件本身）。
	checkArgs func(configPath string) []string
	// runArgs 返回运行态的参数。
	runArgs func(configPath string) []string
	// configErrorExitCodes 是该核心在配置错误时使用的特殊退出码。
	configErrorExitCodes []int
}

// commandTable 是核心命令行的事实表。
var commandTable = map[Kind]commandSpec{
	KindXray: {
		// -test 只检查配置并退出；配置错误时退出码为 23。
		checkArgs:            func(p string) []string { return []string{"run", "-test", "-c", p} },
		runArgs:              func(p string) []string { return []string{"run", "-c", p} },
		configErrorExitCodes: []int{23},
	},
	KindSingBox: {
		checkArgs: func(p string) []string { return []string{"check", "-c", p} },
		// --disable-color 让日志便于解析。
		runArgs: func(p string) []string { return []string{"run", "--disable-color", "-c", p} },
	},
}

// Options 配置 Adapter。
type Options struct {
	// Scrubber 用于在日志转发前清除凭据；为 nil 时不进行清洗。
	//
	// 强烈建议传入：核心日志与错误信息可能无意间带出节点凭据。
	Scrubber *secrets.Scrubber
	// LogLines 是每个核心保留的日志行数上限（环形缓冲）。
	// 为 0 时使用默认值 500。
	LogLines int
	// StopGrace 是停止时的优雅等待时间；为 0 时使用默认 5 秒。
	StopGrace time.Duration
}

// Adapter 管理单个核心的进程。
//
// 同一时刻只应有一个运行中的进程；重复 Start 会报错而不是静默覆盖。
type Adapter struct {
	kind       Kind
	binaryPath string
	opts       Options

	mu      sync.Mutex
	proc    *Process
	stopped bool
}

// NewAdapter 构造核心适配器。
func NewAdapter(kind Kind, binaryPath string, opts Options) (*Adapter, error) {
	if !kind.Valid() {
		return nil, fmt.Errorf("core: unknown core %q", kind)
	}
	if strings.TrimSpace(binaryPath) == "" {
		return nil, errors.New("core: binary path is required")
	}
	if opts.LogLines <= 0 {
		opts.LogLines = 500
	}
	if opts.StopGrace <= 0 {
		opts.StopGrace = 5 * time.Second
	}
	return &Adapter{kind: kind, binaryPath: binaryPath, opts: opts}, nil
}

// Kind 返回核心类型。
func (a *Adapter) Kind() Kind { return a.kind }

// BinaryPath 返回核心二进制路径。
func (a *Adapter) BinaryPath() string { return a.binaryPath }

// CheckConfig 调用核心自带的配置检查。
//
// 这是「编译成功」与「核心认可该配置」之间的必要关卡：
// 我们自己的编译器只保证结构正确，字段是否被目标版本接受必须由核心自己判定。
//
// 返回的错误包含核心输出的（已脱敏）诊断，便于定位字段级问题。
func (a *Adapter) CheckConfig(ctx context.Context, configPath string) error {
	spec := commandTable[a.kind]
	args := spec.checkArgs(configPath)

	cmd := exec.CommandContext(ctx, a.binaryPath, args...)
	output, err := cmd.CombinedOutput()

	text := a.scrub(string(output))
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			for _, special := range spec.configErrorExitCodes {
				if code == special {
					return &ConfigError{
						Core:        a.kind,
						ExitCode:    code,
						Output:      text,
						MessageKey:  "core.config_rejected",
						MessageArgs: []any{code},
						Message:     fmt.Sprintf("%s rejected the configuration (exit %d)", a.kind, code),
					}
				}
			}
			return &ConfigError{
				Core:        a.kind,
				ExitCode:    code,
				Output:      text,
				MessageKey:  "core.config_rejected",
				MessageArgs: []any{code},
				Message:     fmt.Sprintf("%s config check failed (exit %d)", a.kind, code),
			}
		}
		return fmt.Errorf("core: run %s config check: %w", a.kind, err)
	}
	return nil
}

// ConfigError 表示核心判定配置不可用。
//
// 与「无法启动进程」区分开：前者是配置问题，后者是环境问题，
// 两者的用户提示与重试策略不同。
//
// 本地化约定（见 design/architecture/i18n-convention.md）：
// MessageKey 与 MessageArgs 是界面文案的唯一来源；Message 只是内部诊断文本。
// Core 与 ExitCode 是结构化的，界面可以据此展示更具体的信息。
type ConfigError struct {
	Core        Kind
	ExitCode    int
	Output      string
	MessageKey  string
	MessageArgs []any
	Message     string
}

func (e *ConfigError) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("core: %s config rejected (exit %d): %s", e.Core, e.ExitCode, e.Message)
	}
	return fmt.Sprintf("core: %s config rejected (exit %d): %s: %s", e.Core, e.ExitCode, e.Message, firstLine(e.Output))
}

// ErrProcessDidNotExit 表示核心进程在被强制结束后仍未退出。
//
// 这是环境问题的信号（进程被占用、处于不可中断状态等），
// 与配置错误完全不同，因此单独成类。
var ErrProcessDidNotExit = errors.New("core: process did not exit after being killed")

// NotReadyError 表示核心进程已启动，但没能在限定时间内监听端口。
//
// 注意：端口可连接**不等于**代理可用（见 WaitForListen 的说明），
// 这个错误只说明连「就绪信号」都没等到。
type NotReadyError struct {
	Address string
	Timeout time.Duration
	Err     error
}

func (e *NotReadyError) Error() string {
	return fmt.Sprintf("core: %s did not start listening within %s: %v", e.Address, e.Timeout, e.Err)
}

// Unwrap 保留底层错误，便于调用方用 errors.Is / errors.As 判断。
func (e *NotReadyError) Unwrap() error { return e.Err }

// MessageKeyFor 把本包的错误映射到界面文案键。
//
// 存在理由：本包既有带结构化字段的错误类型（ConfigError、NotReadyError），
// 也有哨兵值（ErrProcessDidNotExit）。界面不该逐个类型做类型断言，
// 因此统一从这里取键。返回空串表示不是本包的可本地化错误。
//
// ConfigError 的键优先取它自己携带的 MessageKey，
// 因为同一个类型可能对应不同的失败原因。
func MessageKeyFor(err error) string {
	if err == nil {
		return ""
	}

	var ce *ConfigError
	if errors.As(err, &ce) && ce.MessageKey != "" {
		return ce.MessageKey
	}

	var nre *NotReadyError
	if errors.As(err, &nre) {
		return "core.not_ready"
	}

	if errors.Is(err, ErrProcessDidNotExit) {
		return "core.process_did_not_exit"
	}

	return ""
}

// Process 是一个运行中的核心进程。
type Process struct {
	kind    Kind
	cmd     *exec.Cmd
	logs    *ringBuffer
	scrub   func(string) string
	grace   time.Duration
	done    chan struct{}
	waitErr error
	once    sync.Once
}

// Start 启动核心进程。
//
// 启动前不重复做配置检查：调用方应先调用 CheckConfig，
// 避免在每个连接事务里重复付出核心启动开销。
func (a *Adapter) Start(ctx context.Context, configPath string) (*Process, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.proc != nil {
		return nil, errors.New("core: a process is already running; stop it first")
	}

	spec := commandTable[a.kind]
	cmd := exec.Command(a.binaryPath, spec.runArgs(configPath)...)

	// 让子进程独立于父进程的信号处理，便于我们显式控制停止顺序。
	configureProcess(cmd)

	// 标准输出与标准错误都并入日志：核心的启动失败信息常写在 stderr。
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("core: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("core: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("core: start %s: %w", a.kind, err)
	}

	p := &Process{
		kind:  a.kind,
		cmd:   cmd,
		logs:  newRingBuffer(a.opts.LogLines),
		scrub: a.scrub,
		grace: a.opts.StopGrace,
		done:  make(chan struct{}),
	}

	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); p.consume(stdout) }()
	go func() { defer readers.Done(); p.consume(stderr) }()

	// 等待读取协程结束，确保日志不丢失，再收集退出状态。
	go func() {
		readers.Wait()
		p.waitErr = cmd.Wait()
		close(p.done)
	}()

	a.proc = p
	return p, nil
}

// Stop 停止当前进程。
//
// 先尝试优雅终止，超过 grace 后强制结束。
// 即使优雅终止失败，也必须确保进程不再存活——否则会留下占用端口的孤儿进程。
func (a *Adapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	p := a.proc
	a.proc = nil
	a.mu.Unlock()

	if p == nil {
		return nil
	}
	return p.Stop(ctx)
}

// Running 报告当前是否有进程在运行。
func (a *Adapter) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc == nil {
		return false
	}
	select {
	case <-a.proc.done:
		return false
	default:
		return true
	}
}

// consume 按行读取输出并写入环形缓冲。
func (p *Process) consume(r io.Reader) {
	scanner := bufio.NewScanner(r)
	// 核心日志行可能较长，放宽单行上限（默认 64KiB 在部分配置转储场景不够）。
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		p.logs.add(p.scrub(scanner.Text()))
	}
}

// Done 返回一个在进程退出时关闭的通道。
func (p *Process) Done() <-chan struct{} { return p.done }

// Wait 阻塞直到进程退出，返回退出错误（正常退出为 nil）。
func (p *Process) Wait() error {
	<-p.done
	return p.waitErr
}

// Stop 停止进程。
func (p *Process) Stop(ctx context.Context) error {
	select {
	case <-p.done:
		return nil // 已经退出
	default:
	}

	// 1) 优雅终止：请求核心自行清理（关闭监听端口、恢复资源）。
	_ = terminate(p.cmd)

	grace := p.grace
	if grace <= 0 {
		grace = 5 * time.Second
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case <-p.done:
		return nil
	case <-timer.C:
		// 2) 超时后强制结束。
		_ = p.cmd.Process.Kill()
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
	}

	// 等待进程真正退出，避免返回后仍有存活进程。
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		return ErrProcessDidNotExit
	}
	return nil
}

// Logs 返回当前缓冲的全部日志行（已脱敏）。
func (p *Process) Logs() []string { return p.logs.snapshot() }

// TailLogs 返回最近 n 行日志。
func (p *Process) TailLogs(n int) []string { return p.logs.tail(n) }

// scrub 在配置了清洗器时清除凭据。
func (a *Adapter) scrub(text string) string {
	if a.opts.Scrubber == nil {
		return text
	}
	return a.opts.Scrubber.Scrub(text)
}

// WaitForListen 等待某个地址开始接受连接。
//
// 用途：判断核心是否真正就绪。**仅凭进程存在或端口打开都不能代表代理可用**，
// 因此这个函数只作为「可以进入下一步健康检查」的信号，不是连通性证明。
func WaitForListen(ctx context.Context, address string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)

	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return nil
		}

		if time.Now().After(deadline) {
			return &NotReadyError{Address: address, Timeout: timeout, Err: err}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// firstLine 返回文本的第一行，用于缩短错误消息。
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

// ringBuffer 是固定容量的日志环形缓冲。
//
// 使用环形缓冲而非无界切片：核心在高日志级别下可能产生大量输出，
// 无界缓冲会持续占用内存（设计基线 §9 的轻量要求）。
type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	next  int
	full  bool
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = 500
	}
	return &ringBuffer{lines: make([]string, capacity)}
}

func (r *ringBuffer) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[r.next] = line
	r.next = (r.next + 1) % len(r.lines)
	if r.next == 0 {
		r.full = true
	}
}

// snapshot 返回按时间顺序排列的全部已缓冲日志。
func (r *ringBuffer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		out := make([]string, r.next)
		copy(out, r.lines[:r.next])
		return out
	}
	out := make([]string, 0, len(r.lines))
	out = append(out, r.lines[r.next:]...)
	out = append(out, r.lines[:r.next]...)
	return out
}

// tail 返回最近 n 行。
func (r *ringBuffer) tail(n int) []string {
	all := r.snapshot()
	if n <= 0 || n >= len(all) {
		return all
	}
	return all[len(all)-n:]
}
