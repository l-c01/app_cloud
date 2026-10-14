// Package coordinator 实现单次连接事务。
//
// 设计约束（设计基线 §5.2、§7.2）：
//   - **同一时刻只有一个活动连接事务**，不并发提交多个核心或多套网络图；
//   - 每个操作带 generation（代次），过期的异步结果不得覆盖新状态；
//   - 启动前快照网络设置，核心就绪后才接管网络；
//   - 使用持久化恢复日志记录「原值 / 我们写入的值 / 资源所有者 / 阶段」；
//   - 恢复时**只恢复本客户端拥有的设置**，并与当前值比较：
//     若已被用户或其他软件修改，则报告冲突而不强行写回；
//   - 不能承诺进程被强杀后依赖核心自身完成清理。
//
// 本包不直接调用操作系统、不直接启动进程、不直接编译配置：
// 它通过下面四个接口协作，平台与运行期实现由调用方注入。
package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"proxyclient/shared/compiler"
)

// State 是连接状态机的状态。
//
// 取值与设计基线 §7.2 的状态机一致，便于与文档、日志、UI 直接对照。
type State string

const (
	StateStopped          State = "stopped"
	StateValidating       State = "validating"
	StateStartingCore     State = "starting_core"
	StatePreparingNetwork State = "preparing_network"
	StateConnecting       State = "connecting"
	StateRunning          State = "running"
	StateStopping         State = "stopping"
	StateRestoring        State = "restoring"
	StateRecovering       State = "recovering"
	StateFailed           State = "failed"
	StateRecoveryRequired State = "recovery_required"
)

// ErrBusy 表示已有连接事务在进行中。
var ErrBusy = errors.New("coordinator: another connection transaction is in progress")

// ErrSuperseded 表示该步骤所属的代次已过期，结果必须被丢弃。
//
// 出现它通常意味着用户快速切换了节点或核心；调用方应把它当作正常取消处理，
// 而不是错误提示。
var ErrSuperseded = errors.New("coordinator: operation superseded by a newer generation")

// RecoveryError 表示连接事务失败后，网络设置未能恢复到原始状态。
//
// 这是本包最需要用户介入的错误：系统网络可能仍指向一个已经停止的本地代理，
// 用户会表现为「浏览器打不开任何网页」。因此：
//   - 状态被置为 StateRecoveryRequired；
//   - 错误里同时保留原始失败原因与恢复失败原因，两者都要能查到。
type RecoveryError struct {
	// Stage 是事务失败时所在的阶段。
	Stage string
	// Cause 是导致事务失败的原始错误。
	Cause error
	// RecoveryErr 是恢复网络时发生的错误。
	RecoveryErr error
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("coordinator: %s: %v (network recovery failed: %v)", e.Stage, e.Cause, e.RecoveryErr)
}

// Unwrap 返回原始失败原因，便于 errors.Is / errors.As 判断。
func (e *RecoveryError) Unwrap() error { return e.Cause }

// MessageKeyFor 把本包的错误映射到界面文案键。
//
// 存在理由：本包对外暴露的是哨兵值与具名错误类型的混合，
// 界面不该逐个做类型断言。返回空串表示不是本包的可本地化错误。
func MessageKeyFor(err error) string {
	if err == nil {
		return ""
	}
	var re *RecoveryError
	if errors.As(err, &re) {
		// 恢复失败比原始失败更严重，优先按它提示用户。
		return "coordinator.recovery_failed"
	}
	switch {
	case errors.Is(err, ErrBusy):
		return "coordinator.busy"
	case errors.Is(err, ErrSuperseded):
		return "coordinator.superseded"
	default:
		return ""
	}
}

// CompiledConfig 是编译产物。
type CompiledConfig struct {
	Core compiler.Core
	// Path 是配置文件在磁盘上的位置。
	Path   string
	Report *compiler.Report
}

// Compiler 负责把编译请求落成配置文件。
//
// 实现应同时完成「编译」与「写出文件」，因为核心需要的是文件路径。
type Compiler interface {
	CompileToFile(ctx context.Context, req *compiler.Request, dir string) (*CompiledConfig, error)
}

// RunningCore 是一个已启动的核心。
type RunningCore interface {
	// Ready 等待核心真正就绪（例如本地监听端口可连接）。
	// 注意：就绪不等于代理可用，仍需后续健康检查。
	Ready(ctx context.Context) error
	// Stop 停止核心并等待其退出。
	Stop(ctx context.Context) error
	// Logs 返回最近的（已脱敏）日志，用于失败诊断。
	Logs() []string
}

// CoreRunner 负责配置检查与启动核心。
type CoreRunner interface {
	CheckConfig(ctx context.Context, core compiler.Core, configPath string) error
	Start(ctx context.Context, core compiler.Core, configPath string) (RunningCore, error)
}

// NetworkSettings 是网络设置的原值/写入值集合。
//
// 用字符串映射而不是强类型：不同平台接管的项完全不同
// （系统代理开关、路由表项、DNS 服务器、PAC URL 等），
// 强行统一类型只会让平台实现变得别扭。
type NetworkSettings map[string]string

// NetworkPlan 描述本次要接管的网络设置。
type NetworkPlan struct {
	// Mode 是接入方式（system_proxy / tun / pac / local_proxy）。
	Mode string
	// Values 是客户端希望写入的设置。
	Values NetworkSettings
	// Owner 标识这些设置属于本客户端，用于恢复时校验所有权。
	Owner string
}

// RestoreConflict 描述一处「当前值已不再是我们写入的值」的冲突。
//
// 设计基线要求：冲突时报告需处理，不强行写回。
type RestoreConflict struct {
	Key      string
	Expected string // 我们当初写入的值
	Actual   string // 当前实际值（已被他人修改）
}

// RestoreResult 是网络恢复的结果。
type RestoreResult struct {
	// Restored 是成功恢复的键。
	Restored []string
	// Conflicts 是需要用户处理的冲突键。
	Conflicts []RestoreConflict
	// Missing 是原本就不存在、无需恢复的键。
	Missing []string
}

// NetworkManager 负责快照、接管与恢复网络设置。
//
// 实现方（platform 层）必须是**有限、可枚举**的操作集合；
// 不得执行任意命令或写任意文件。
type NetworkManager interface {
	// Snapshot 记录当前网络设置，作为恢复依据。
	Snapshot(ctx context.Context) (NetworkSettings, error)
	// Apply 接管网络设置，返回实际写入的值（可能与请求略有差异，例如系统归一化）。
	Apply(ctx context.Context, plan NetworkPlan) (NetworkSettings, error)
	// Restore 恢复设置。
	//
	// 实现约定：逐个键比较当前值与期望值（期望值为客户端当初写入的值）。
	// 相等才写回原值；不等则记入 Conflicts，**不要覆盖**。
	// 原值不存在（例如键本来就没有）记入 Missing。
	Restore(ctx context.Context, original, applied NetworkSettings) (RestoreResult, error)
}

// JournalEntry 是持久化的恢复记录。
//
// 它必须能在进程崩溃后被重新读取，因此字段都是可序列化的值类型。
type JournalEntry struct {
	Generation uint64          `json:"generation"`
	Stage      State           `json:"stage"`
	Mode       string          `json:"mode"`
	Core       compiler.Core   `json:"core"`
	CorePID    int             `json:"corePid"`
	ConfigPath string          `json:"configPath"`
	Owner      string          `json:"owner"`
	Original   NetworkSettings `json:"original"`
	Applied    NetworkSettings `json:"applied"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// Journal 负责恢复记录的持久化。
type Journal interface {
	Save(entry JournalEntry) error
	Load() (*JournalEntry, error)
	Clear() error
}

// Options 配置 Coordinator。
type Options struct {
	Compiler Compiler
	Core     CoreRunner
	Network  NetworkManager
	Journal  Journal
	// ConfigDir 是编译产物写入目录。
	ConfigDir string
	// Owner 标识本客户端，写入网络设置与日志。
	// 默认 "proxyclient"。
	Owner string
	// ReadyTimeout 是等待核心就绪的上限；默认 10 秒。
	ReadyTimeout time.Duration
	// StopTimeout 是停止核心的上限；默认 10 秒。
	StopTimeout time.Duration
	// Clock 允许测试注入时间；为 nil 时使用 time.Now。
	Clock func() time.Time
}

// Status 是运行状态快照，供 UI 展示。
type Status struct {
	State      State         `json:"state"`
	Generation uint64        `json:"generation"`
	Core       compiler.Core `json:"core"`
	ActiveNode string        `json:"activeNode"`
	Mode       string        `json:"mode"`
	LastError  string        `json:"lastError,omitempty"`
	// Conflicts 表示上次恢复时发现的冲突，需要用户处理。
	Conflicts []RestoreConflict `json:"conflicts,omitempty"`
}

// Coordinator 驱动连接事务。
type Coordinator struct {
	opts Options

	mu         sync.Mutex
	generation uint64
	state      State
	lastError  string
	core       compiler.Core
	activeNode string
	mode       string
	conflicts  []RestoreConflict
	running    RunningCore
	// busy 表示有事务在进行；用于拒绝并发事务。
	busy bool
}

// New 构造 Coordinator。
func New(opts Options) (*Coordinator, error) {
	if opts.Compiler == nil {
		return nil, errors.New("coordinator: compiler is required")
	}
	if opts.Core == nil {
		return nil, errors.New("coordinator: core runner is required")
	}
	if opts.Network == nil {
		return nil, errors.New("coordinator: network manager is required")
	}
	if opts.Journal == nil {
		return nil, errors.New("coordinator: journal is required")
	}
	if opts.ConfigDir == "" {
		return nil, errors.New("coordinator: config dir is required")
	}
	if opts.Owner == "" {
		opts.Owner = "proxyclient"
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = 10 * time.Second
	}
	if opts.StopTimeout <= 0 {
		opts.StopTimeout = 10 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Coordinator{opts: opts, state: StateStopped}, nil
}

// Status 返回当前状态快照。
func (c *Coordinator) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		State:      c.state,
		Generation: c.generation,
		Core:       c.core,
		ActiveNode: c.activeNode,
		Mode:       c.mode,
		LastError:  c.lastError,
		Conflicts:  append([]RestoreConflict(nil), c.conflicts...),
	}
}

// setState 更新状态；同时校验代次。
func (c *Coordinator) setState(gen uint64, state State) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.generation {
		return ErrSuperseded
	}
	c.state = state
	return nil
}

// currentGeneration 返回当前代次。
func (c *Coordinator) currentGeneration() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

// ConnectRequest 是一次连接请求。
type ConnectRequest struct {
	// Compile 是编译请求（节点、选定节点、接入方式对应的本地入口等）。
	Compile *compiler.Request
	// Mode 是接入方式；空值表示只启动本地代理，不接管系统网络。
	Mode string
}

// Connect 执行一次完整的连接事务。
//
// 流程：validating → starting_core → preparing_network → connecting → running。
// 任一步失败都会进入 recovering，尝试恢复网络并停止核心；
// 恢复失败则进入 recovery_required，要求用户介入。
func (c *Coordinator) Connect(ctx context.Context, req ConnectRequest) error {
	if req.Compile == nil {
		return errors.New("coordinator: compile request is required")
	}

	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return ErrBusy
	}
	c.busy = true
	c.generation++
	gen := c.generation
	c.state = StateValidating
	c.lastError = ""
	c.conflicts = nil
	c.mode = req.Mode
	c.core = req.Compile.Core
	c.activeNode = req.Compile.SelectedNodeID
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
	}()

	var (
		compiled *CompiledConfig
		running  RunningCore
		snapshot NetworkSettings
		applied  NetworkSettings
		entry    JournalEntry
	)

	// 失败路径统一走这里，保证「要么成功，要么恢复到已知状态」。
	//
	// 关键点：只有在**确实改过网络**时才写恢复日志并恢复。
	// 如果在编译/启动阶段就失败却写一条空日志，会覆盖上一次崩溃遗留的记录，
	// 导致真正需要恢复的网络设置永久丢失。
	fail := func(stage string, cause error) error {
		_ = c.setState(gen, StateRecovering)

		var recoveryErr error
		if len(applied) > 0 {
			entry.Stage = StateRecovering
			entry.Applied = applied
			c.recordJournal(&entry, gen, StateRecovering, running, req.Mode)

			result, err := c.opts.Network.Restore(ctx, snapshot, applied)
			if len(result.Conflicts) > 0 {
				c.mu.Lock()
				c.conflicts = result.Conflicts
				c.mu.Unlock()
			}
			recoveryErr = err
		}

		if running != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), c.opts.StopTimeout)
			if err := running.Stop(stopCtx); err != nil && recoveryErr == nil {
				recoveryErr = fmt.Errorf("stop core: %w", err)
			}
			cancel()
		}

		c.mu.Lock()
		c.running = nil
		c.lastError = cause.Error()
		c.mu.Unlock()

		if recoveryErr != nil {
			// 恢复失败：必须让用户知道并介入，不能假装已回到干净状态。
			_ = c.setState(gen, StateRecoveryRequired)
			failure := &RecoveryError{Stage: stage, Cause: cause, RecoveryErr: recoveryErr}
			c.mu.Lock()
			c.lastError = failure.Error()
			c.mu.Unlock()
			return failure
		}

		// 恢复到干净状态后才清除日志；否则保留给下次启动的 Recover 处理。
		if len(applied) > 0 {
			_ = c.opts.Journal.Clear()
		}
		_ = c.setState(gen, StateFailed)
		return fmt.Errorf("coordinator: %s: %w", stage, cause)
	}

	// --- 1) validating：编译并做原生配置检查 ---
	//
	// 这一段不会改动系统网络，因此失败时不需要恢复，也不应触碰恢复日志。
	compiled, err := c.opts.Compiler.CompileToFile(ctx, req.Compile, c.opts.ConfigDir)
	if err != nil {
		return fail("compile config", err)
	}
	if compiled.Report != nil && compiled.Report.HasErrors() {
		// 编译报告里有错误级条目时不应继续：那意味着有节点被排除。
		return fail("compile report contains errors",
			fmt.Errorf("%d compilation error(s)", len(compiled.Report.Errors())))
	}
	if err := c.opts.Core.CheckConfig(ctx, compiled.Core, compiled.Path); err != nil {
		return fail("core rejected the config", err)
	}
	if err := c.setState(gen, StateStartingCore); err != nil {
		return err
	}

	// 从这里开始可能启动进程与改动网络，先落一条日志。
	entry = JournalEntry{
		Generation: gen,
		Stage:      StateStartingCore,
		Mode:       req.Mode,
		Core:       compiled.Core,
		ConfigPath: compiled.Path,
		Owner:      c.opts.Owner,
		CreatedAt:  c.opts.Clock(),
	}
	c.recordJournal(&entry, gen, StateStartingCore, nil, req.Mode)

	// --- 2) starting_core：启动核心并等待就绪 ---
	running, err = c.opts.Core.Start(ctx, compiled.Core, compiled.Path)
	if err != nil {
		return fail("start core", err)
	}

	readyCtx, cancelReady := context.WithTimeout(ctx, c.opts.ReadyTimeout)
	readyErr := running.Ready(readyCtx)
	cancelReady()
	if readyErr != nil {
		return fail("core did not become ready", readyErr)
	}

	c.mu.Lock()
	c.running = running
	c.mu.Unlock()

	// --- 3) preparing_network：核心就绪后才接管网络 ---
	//
	// 顺序很重要：先核心后网络，避免网络已接管但核心不可用导致断网。
	if req.Mode != "" {
		if err := c.setState(gen, StatePreparingNetwork); err != nil {
			return err
		}

		snapshot, err = c.opts.Network.Snapshot(ctx)
		if err != nil {
			return fail("snapshot network settings", err)
		}

		plan := NetworkPlan{
			Mode:   req.Mode,
			Values: planForMode(req.Mode, req.Compile),
			Owner:  c.opts.Owner,
		}
		// 先写日志再改网络：崩溃后能据此恢复。
		entry.Original = snapshot
		entry.Stage = StatePreparingNetwork
		c.recordJournal(&entry, gen, StatePreparingNetwork, running, req.Mode)

		applied, err = c.opts.Network.Apply(ctx, plan)
		if err != nil {
			return fail("apply network settings", err)
		}

		entry.Applied = applied
		c.recordJournal(&entry, gen, StatePreparingNetwork, running, req.Mode)
	}

	// --- 4) connecting → running ---
	if err := c.setState(gen, StateConnecting); err != nil {
		return err
	}
	entry.Stage = StateRunning
	// 进入运行态后清除日志：此后若崩溃，网络恢复由 Recover 依据日志处理，
	// 而日志在成功提交后不再需要——但立即清除会丢失崩溃窗口的保护，
	// 因此这里保留日志，由 Disconnect 或下次 Recover 清除。
	c.recordJournal(&entry, gen, StateRunning, running, req.Mode)

	return c.setState(gen, StateRunning)
}

// Disconnect 停止连接并恢复网络。
//
// 幂等：已停止时再次调用不报错（设计基线要求「幂等停止」）。
func (c *Coordinator) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return ErrBusy
	}
	c.busy = true
	c.generation++
	gen := c.generation
	c.state = StateStopping
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
	}()

	c.mu.Lock()
	running := c.running
	c.mu.Unlock()

	// --- 1) stopping：先停核心，再恢复网络 ---
	//
	// 反过来做会让系统在一段时间内指向一个不存在的代理，造成断网。
	if running != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), c.opts.StopTimeout)
		stopErr := running.Stop(stopCtx)
		cancel()
		if stopErr != nil {
			// 停止失败仍要继续恢复网络，否则会留下指向死代理的系统设置。
			c.mu.Lock()
			c.lastError = stopErr.Error()
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.running = nil
		c.mu.Unlock()
	}

	if err := c.setState(gen, StateRestoring); err != nil {
		return err
	}

	// --- 2) restoring：按恢复日志还原网络 ---
	if _, err := c.restoreFromJournal(ctx); err != nil {
		_ = c.setState(gen, StateRecoveryRequired)
		// 必须在 setState **之后**写：setState 会清空 lastError。
		//
		// 恢复失败意味着系统网络可能仍指向一个已经退出的代理，
		// 用户的表现是「完全没网」。只给一个 recovery_required 状态
		// 而不给原因，用户无从判断该改什么，界面上也只能显示一句空话。
		c.mu.Lock()
		c.lastError = err.Error()
		c.mu.Unlock()
		return fmt.Errorf("coordinator: restore network: %w", err)
	}

	_ = c.opts.Journal.Clear()

	if err := c.setState(gen, StateStopped); err != nil {
		return err
	}
	return nil
}

// Recover 在启动时调用，用于处理上次运行遗留的恢复记录。
//
// 典型场景：进程被强杀，网络设置仍指向已不存在的代理。
// 返回的恢复结果里的冲突需要用户处理。
func (c *Coordinator) Recover(ctx context.Context) (*RestoreResult, error) {
	entry, err := c.opts.Journal.Load()
	if err != nil {
		return nil, fmt.Errorf("coordinator: load recovery journal: %w", err)
	}
	if entry == nil || len(entry.Applied) == 0 {
		// 崩溃发生在接管网络之前，没有需要恢复的设置。
		if err := c.opts.Journal.Clear(); err != nil {
			return nil, err
		}
		return nil, nil
	}

	c.mu.Lock()
	c.generation++
	gen := c.generation
	c.state = StateRecovering
	c.mu.Unlock()

	result, restoreErr := c.restoreFromJournal(ctx)
	if restoreErr != nil {
		_ = c.setState(gen, StateRecoveryRequired)
		// 必须把原因写进 lastError，而且要在 setState 之后写——
		// setState 会清空 lastError。
		//
		// 恢复失败意味着系统网络可能仍指向一个已经退出的代理，
		// 用户的表现是「完全没网」。只给一个「需要修复」的状态
		// 而不给原因，用户无从判断该改什么。
		c.mu.Lock()
		c.lastError = restoreErr.Error()
		c.mu.Unlock()
		return result, fmt.Errorf("coordinator: restore leftover network settings: %w", restoreErr)
	}
	if err := c.opts.Journal.Clear(); err != nil {
		return result, err
	}
	_ = c.setState(gen, StateStopped)
	return result, nil
}

// restoreFromJournal 依据持久化记录恢复网络设置。
//
// 返回 nil 表示没有需要恢复的设置。
// 发现的冲突会被记录到状态中，供 UI 提示用户处理；
// 冲突**不会**被强行覆盖（这是 NetworkManager 的职责约定）。
func (c *Coordinator) restoreFromJournal(ctx context.Context) (*RestoreResult, error) {
	stored, err := c.opts.Journal.Load()
	if err != nil {
		return nil, fmt.Errorf("load recovery journal: %w", err)
	}
	if stored == nil || len(stored.Applied) == 0 {
		return nil, nil
	}

	result, err := c.opts.Network.Restore(ctx, stored.Original, stored.Applied)
	if len(result.Conflicts) > 0 {
		c.mu.Lock()
		c.conflicts = result.Conflicts
		c.mu.Unlock()
	}
	// NetworkManager.Restore 返回的是值而不是指针，因此这里取地址返回。
	//
	// 返回 nil 只发生在「日志里没有待恢复项」时，语义是「无需恢复」——
	// 这与「恢复执行了但没有冲突」不是一回事，调用方据此区分两种情况。
	return &result, err
}

// recordJournal 写入（或更新）恢复记录。
//
// 写入失败只记录状态而不中断事务：恢复记录是尽力而为的保障，
// 但缺了它会导致崩溃后无法恢复，因此调用方应把它视为需要上报的问题。
func (c *Coordinator) recordJournal(entry *JournalEntry, gen uint64, stage State, running RunningCore, mode string) {
	if entry == nil {
		return
	}
	entry.Generation = gen
	entry.Stage = stage
	entry.Mode = mode
	entry.Owner = c.opts.Owner
	entry.UpdatedAt = c.opts.Clock()
	if running != nil {
		entry.CorePID = 0 // 由具体实现填充；此处保留字段以便持久化。
	}
	_ = c.opts.Journal.Save(*entry)
}

// planForMode 依据接入方式给出要接管的网络设置。
//
// 注意：这里只写出「本客户端需要接管的最小集合」。
// 具体键名由平台层解释；未识别的模式返回空集合，表示不接管网络
// （例如 local_proxy 只启动本地端口，不改系统设置）。
func planForMode(mode string, req *compiler.Request) NetworkSettings {
	switch mode {
	case "system_proxy":
		values := NetworkSettings{
			"system_proxy.http":  fmt.Sprintf("%s:%d", loopback(req), req.Inbound.HTTPPort),
			"system_proxy.socks": fmt.Sprintf("%s:%d", loopback(req), req.Inbound.SocksPort),
		}
		return values
	case "pac":
		// PAC URL 的端口由平台层确定，这里只声明需要接管的键。
		return NetworkSettings{"system_proxy.pac": ""}
	case "tun":
		return NetworkSettings{"tun.enabled": "true"}
	case "local_proxy":
		return NetworkSettings{}
	default:
		return NetworkSettings{}
	}
}

// loopback 返回本地监听地址；开启局域网访问时不能把 0.0.0.0 写进系统代理。
func loopback(req *compiler.Request) string {
	if req == nil {
		return "127.0.0.1"
	}
	host := req.Inbound.Listen
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "127.0.0.1"
	}
	return host
}
