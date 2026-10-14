// Command proxyclient-service 是后台服务进程。
//
// 架构位置：桌面 GUI 只做界面，所有涉及系统网络与核心进程的操作都在这里。
// 两者通过本地 IPC 通信（见 runtime/ipc）。
//
// 为什么必须独立成进程（而不是把逻辑放进 GUI 进程）：
//   - 修改系统代理、启动核心、恢复网络这些操作需要在 GUI 崩溃或被强杀后
//     仍然可被恢复。GUI 进程本身就是最可能崩溃的那一个。
//   - Windows 上无法向普通进程发送优雅终止信号（见设计基线），
//     因此网络恢复必须由**独立于界面生命周期**的一方负责。
//
// 本进程的启动顺序刻意如此：
//  1. 解析参数与日志
//  2. 组装协调器（编译器 / 核心运行器 / 网络接管 / 恢复日志）
//  3. **先做一次恢复**：上次进程可能被强杀，网络设置还指着已停止的代理
//  4. 启动 IPC 服务
//  5. 等待信号，优雅退出
//
// 第 3 步必须在第 4 步之前：否则界面一连上就会看到一份「已连接」的
// 陈旧状态，而系统网络其实还指着早已退出的核心。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"proxyclient/platform/journalfile"
	"proxyclient/platform/netmgr"
	"proxyclient/runtime/coordinator"
	"proxyclient/runtime/core"
	"proxyclient/runtime/coreupdater"
	"proxyclient/runtime/ipc"
	"proxyclient/shared/compiler"
	"proxyclient/shared/secrets"
)

// version 由构建时注入；未注入时为 "dev"。
var version = "dev"

func main() {
	if err := run(); err != nil {
		// 用 log.Fatal 而不是 panic：服务启动失败是可预期的运维情况，
		// 用户需要看到一行可读的原因，而不是一段栈。
		log.Fatalf("proxyclient-service: %v", err)
	}
}

type options struct {
	stateDir    string
	coreBinDir  string
	owner       string
	showVersion bool
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.stateDir, "state-dir", defaultStateDir(),
		"directory for the connection handle, recovery journal and compiled configs")
	flag.StringVar(&opts.coreBinDir, "core-dir", defaultCoreDir(),
		"directory containing the installed core binaries")
	flag.StringVar(&opts.owner, "owner", "proxyclient",
		"identifier written into network settings so recovery can prove ownership")
	flag.BoolVar(&opts.showVersion, "version", false, "print the version and exit")
	flag.Parse()
	return opts
}

func defaultStateDir() string {
	// 与桌面端 IpcClient::DefaultStatePath 的目录规则保持一致：
	// 不一致会让界面永远连不上服务，且看起来像服务没启动。
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "proxyclient")
		}
	}
	// 优先运行期目录：它随会话创建、权限收紧、重启后自动清理，
	// 适合放含本地访问令牌的句柄文件。
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "proxyclient")
	}
	if configHome := os.Getenv("XDG_CONFIG_HOME"); configHome != "" {
		return filepath.Join(configHome, "proxyclient")
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "proxyclient")
	}
	return "."
}

func defaultCoreDir() string {
	if dir := os.Getenv("PCX_CORE_DIR"); dir != "" {
		return dir
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			return filepath.Join(appData, "proxyclient", "cores")
		}
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".local", "share", "proxyclient", "cores")
	}
	return "cores"
}

func run() error {
	opts := parseFlags()
	if opts.showVersion {
		fmt.Println(version)
		return nil
	}

	if err := os.MkdirAll(opts.stateDir, 0o700); err != nil {
		return fmt.Errorf("create the state directory: %w", err)
	}

	// 日志绝不写入凭据：核心输出与配置路径都可能间接带出凭据，
	// 因此统一过一遍清洗器。
	scrubber := secrets.NewScrubber()

	// ---------------------------------------------------------------
	// 组装依赖
	// ---------------------------------------------------------------
	journal, err := journalfile.New(filepath.Join(opts.stateDir, "recovery.json"))
	if err != nil {
		return fmt.Errorf("open the recovery journal: %w", err)
	}

	network := netmgr.New(platformBackend())
	compilerAdapter := &configCompiler{dir: filepath.Join(opts.stateDir, "configs")}

	runner, err := newCoreRunner(opts.coreBinDir, scrubber)
	if err != nil {
		return err
	}

	coord, err := coordinator.New(coordinator.Options{
		Compiler:  compilerAdapter,
		Core:      runner,
		Network:   network,
		Journal:   journal,
		ConfigDir: compilerAdapter.dir,
		Owner:     opts.owner,
	})
	if err != nil {
		return fmt.Errorf("create the coordinator: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---------------------------------------------------------------
	// 启动恢复
	//
	// 必须在启动 IPC 之前：上次进程可能被强杀，系统网络还指着
	// 一个已经停止的本地代理。用户此时的表现是「浏览器打不开任何网页」。
	// ---------------------------------------------------------------
	recoverCtx, cancelRecover := context.WithTimeout(ctx, 30*time.Second)
	if result, err := coord.Recover(recoverCtx); err != nil {
		// 恢复失败不阻止服务启动：服务本身仍能提供诊断信息，
		// 而直接退出会让用户完全无从下手。
		log.Printf("network recovery did not complete cleanly: %v", err)
	} else if result != nil && (len(result.Restored) > 0 || len(result.Conflicts) > 0) {
		log.Printf("network recovery: restored=%v conflicts=%d",
			result.Restored, len(result.Conflicts))
	}
	cancelRecover()

	// ---------------------------------------------------------------
	// IPC 服务
	// ---------------------------------------------------------------
	handler := &serviceHandler{
		coordinator: coord,
		network:     network,
		compiler:    compilerAdapter,
		runner:      runner,
		scrubber:    scrubber,
		version:     version,
	}

	// 句柄文件与套接字放在同一目录（该目录权限已收紧为 0700）。
	socketPath := filepath.Join(opts.stateDir, "service.sock")
	handlePath := filepath.Join(opts.stateDir, "service.json")

	server, err := ipc.NewServer(ipc.Options{
		Handler:    handler,
		SocketPath: socketPath,
		HandlePath: handlePath,
	})
	if err != nil {
		return fmt.Errorf("create the IPC server: %w", err)
	}
	defer func() { _ = server.Close() }()

	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	log.Printf("proxyclient-service %s listening (platform=%s)", version, runtime.GOOS)

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("IPC server: %w", err)
		}
	case <-ctx.Done():
		log.Print("shutting down; stopping the core and restoring the network")
	}

	// 退出前主动断开：这会停止核心并恢复网络设置。
	//
	// 不能依赖系统在进程退出时清理：Windows 上无法向普通进程发送优雅终止信号，
	// 而系统网络设置不会随进程消失而自动恢复。
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()
	if err := coord.Disconnect(shutdownCtx); err != nil {
		// 退出路径上的失败必须显式记录：它意味着网络可能没恢复。
		log.Printf("disconnect during shutdown failed: %v", err)
		return err
	}
	return nil
}

// serviceHandler 实现 ipc.Handler。
type serviceHandler struct {
	coordinator *coordinator.Coordinator
	network     *netmgr.Manager
	compiler    *configCompiler
	runner      *coreRunner
	scrubber    *secrets.Scrubber
	version     string

	// lastMode 记录最近一次使用的接入方式，供 SetNetworkMode 与重连使用。
	lastMode string
}

// startParams 是 Start 操作的参数。
type startParams struct {
	// Compile 是编译请求。
	Compile *compiler.Request `json:"compile"`
	// Mode 是接入方式；空值表示只启动本地代理，不接管系统网络。
	Mode string `json:"mode"`
	// ReadyAddress 是核心就绪后应当监听的地址（例如 127.0.0.1:7890）。
	//
	// 留空表示**不校验就绪**。这时服务会在结果里明确说明
	// 「就绪未验证」，而不是假装核心已经可用。
	ReadyAddress string `json:"readyAddress"`
}

type switchNodeParams struct {
	// Compile 是带新选中节点的完整编译请求。
	//
	// 之所以要求完整请求而不是只给一个节点 ID：服务端不持有节点列表
	// （那是客户端的资产），只给 ID 会让服务必须维护一份副本，
	// 两份副本不一致时会出现「切换到了不存在的节点」。
	Compile *compiler.Request `json:"compile"`
}

type setModeParams struct {
	Mode string `json:"mode"`
}

// Handle 实现 ipc.Handler。
func (h *serviceHandler) Handle(ctx context.Context, op ipc.Operation, params json.RawMessage) (any, *ipc.RPCError) {
	switch op {
	case ipc.OpGetCapabilities:
		return h.capabilities(), nil

	case ipc.OpValidateProfile:
		return h.validateProfile(params)

	case ipc.OpCompileProfile:
		return h.compileProfile(params)

	case ipc.OpStart:
		return h.start(ctx, params)

	case ipc.OpStop:
		if err := h.coordinator.Disconnect(ctx); err != nil {
			return nil, h.internal("stop", err)
		}
		return map[string]any{"ok": true}, nil

	case ipc.OpGetRuntimeState:
		return h.runtimeState(), nil

	case ipc.OpSwitchNode:
		return h.switchNode(ctx, params)

	case ipc.OpSetNetworkMode:
		return h.setNetworkMode(params)

	case ipc.OpRecoverNetwork:
		result, err := h.coordinator.Recover(ctx)
		if err != nil {
			return nil, h.internal("recoverNetwork", err)
		}
		return result, nil

	case ipc.OpWatchEvents:
		// 事件是服务端主动推送的，不是请求-响应操作。
		// 明确拒绝而不是返回一个空结果：空结果会让客户端以为订阅成功了。
		return nil, &ipc.RPCError{
			Code:       ipc.CodeInvalidParams,
			MessageKey: "ipc.watch_events_not_a_call",
			Message:    "WatchEvents is pushed by the service, not called",
			Retryable:  false,
		}

	default:
		return nil, &ipc.RPCError{
			Code:       ipc.CodeUnknownOperation,
			MessageKey: "ipc.unknown_operation",
			Message:    fmt.Sprintf("unknown operation: %s", op),
			Retryable:  false,
		}
	}
}

func (h *serviceHandler) capabilities() ipc.Capabilities {
	operations := make([]string, 0, len(ipc.KnownOperations()))
	for _, known := range ipc.KnownOperations() {
		if known == ipc.OpWatchEvents {
			continue
		}
		operations = append(operations, string(known))
	}

	cores := make([]ipc.CoreCapability, 0, 2)
	for _, kind := range h.runner.kinds() {
		path, err := h.runner.binaryPath(kind)
		installed := err == nil && path != ""
		coreVersion := ""
		if installed {
			coreVersion = h.runner.probeVersion(kind)
		}
		cores = append(cores, ipc.CoreCapability{
			Name:      string(kind),
			Version:   coreVersion,
			Installed: installed,
			// 能力表尚未针对锁定版本实测（G4 的待办）。
			// 界面据此如实提示，而不是假装这些选项一定可用。
			VerifiedAgainst: "unverified",
		})
	}

	return ipc.Capabilities{
		Platform:        runtime.GOOS,
		ProtocolVersion: ipc.ProtocolVersion,
		SchemaVersion:   ipc.SchemaVersion,
		Operations:      operations,
		Cores:           cores,
		TrafficEntries:  h.network.Backend().SupportedModes(),
		// 平台层是否真能接管系统网络，由后端自己回答。
		NetworkTakeover: len(h.network.Backend().SupportedModes()) > 0,
		// 应用内核心更新尚未接线。
		CoreUpdate: false,
	}
}

func (h *serviceHandler) runtimeState() ipc.RuntimeState {
	status := h.coordinator.Status()
	state := ipc.RuntimeState{
		State:      string(status.State),
		Generation: status.Generation,
		Core:       string(status.Core),
		ActiveNode: status.ActiveNode,
		Mode:       status.Mode,
		LastError:  h.scrubber.Scrub(status.LastError),
	}
	for _, conflict := range status.Conflicts {
		state.Conflicts = append(state.Conflicts, ipc.ConflictInfo{
			Key:      conflict.Key,
			Expected: conflict.Expected,
			Actual:   conflict.Actual,
		})
	}
	return state
}

func (h *serviceHandler) validateProfile(params json.RawMessage) (any, *ipc.RPCError) {
	var req compiler.Request
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, h.badParams("validateProfile", err)
	}
	_, report, err := compiler.Compile(&req)
	if err != nil {
		return nil, h.internal("validateProfile", err)
	}
	// 报告原样返回：界面需要逐条展示哪些节点被排除、为什么,
	// 只回一个布尔值会让用户无从判断。
	return map[string]any{"report": report}, nil
}

func (h *serviceHandler) compileProfile(params json.RawMessage) (any, *ipc.RPCError) {
	var req compiler.Request
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, h.badParams("compileProfile", err)
	}
	compiled, err := h.compiler.CompileToFile(context.Background(), &req, h.compiler.dir)
	if err != nil {
		return nil, h.internal("compileProfile", err)
	}
	return map[string]any{
		"core": string(compiled.Core),
		// 只返回文件名而不是完整路径：完整路径含用户名等本地信息，
		// 而客户端本来也只需要一个引用。
		"path":   filepath.Base(compiled.Path),
		"report": compiled.Report,
	}, nil
}

func (h *serviceHandler) start(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
	var req startParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, h.badParams("start", err)
	}
	if req.Compile == nil {
		return nil, &ipc.RPCError{
			Code:       ipc.CodeInvalidParams,
			MessageKey: "core.config_rejected",
			Message:    "a compile request is required to start",
			Retryable:  false,
		}
	}

	h.runner.setReadyAddress(req.ReadyAddress)
	h.lastMode = req.Mode

	if err := h.coordinator.Connect(ctx, coordinator.ConnectRequest{
		Compile: req.Compile,
		Mode:    req.Mode,
	}); err != nil {
		rpcErr := h.internal("start", err)
		rpcErr.MessageKey = coordinator.MessageKeyFor(err)
		return nil, rpcErr
	}

	readyVerified := req.ReadyAddress != ""
	return map[string]any{
		"ok": true,
		// 明确告诉客户端「就绪是否被验证过」：只看「启动成功」
		// 会把「进程起来了但还没监听」当成可用。
		"readyVerified": readyVerified,
		"state":         h.runtimeState(),
	}, nil
}

func (h *serviceHandler) switchNode(ctx context.Context, params json.RawMessage) (any, *ipc.RPCError) {
	var req switchNodeParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, h.badParams("switchNode", err)
	}
	if req.Compile == nil {
		return nil, &ipc.RPCError{
			Code:       ipc.CodeInvalidParams,
			MessageKey: "core.config_rejected",
			Message:    "switching a node requires a full compile request",
			Retryable:  false,
		}
	}

	// 切换节点是一次完整的重连：断开 → 用新节点重新编译并连接。
	// 不做「热替换」是因为核心不支持在运行中换出站配置，
	// 假装能热替换只会让用户遇到随机的连接中断。
	if err := h.coordinator.Disconnect(ctx); err != nil {
		return nil, h.internal("switchNode/disconnect", err)
	}
	if err := h.coordinator.Connect(ctx, coordinator.ConnectRequest{
		Compile: req.Compile,
		Mode:    h.lastMode,
	}); err != nil {
		rpcErr := h.internal("switchNode/connect", err)
		rpcErr.MessageKey = coordinator.MessageKeyFor(err)
		return nil, rpcErr
	}
	return map[string]any{"ok": true, "state": h.runtimeState()}, nil
}

func (h *serviceHandler) setNetworkMode(params json.RawMessage) (any, *ipc.RPCError) {
	var req setModeParams
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, h.badParams("setNetworkMode", err)
	}
	if err := netmgr.ValidateMode(req.Mode); err != nil {
		return nil, &ipc.RPCError{
			Code:       ipc.CodeInvalidParams,
			MessageKey: "netmgr.mode_unsupported",
			Message:    err.Error(),
			FieldPath:  "mode",
			Retryable:  false,
		}
	}
	// 只记录选择：接入方式在下次 Start 时生效。
	// 立刻切换会中断正在进行的连接，而用户此刻多半只是想改一下设置。
	h.lastMode = req.Mode
	return map[string]any{"ok": true, "mode": req.Mode, "appliesOn": "next start"}, nil
}

func (h *serviceHandler) badParams(op string, err error) *ipc.RPCError {
	return &ipc.RPCError{
		Code:       ipc.CodeInvalidParams,
		MessageKey: "ipc.malformed",
		Message:    h.scrubber.Scrub(fmt.Sprintf("%s: %v", op, err)),
		Retryable:  false,
	}
}

func (h *serviceHandler) internal(op string, err error) *ipc.RPCError {
	return &ipc.RPCError{
		Code:       ipc.CodeInternal,
		MessageKey: "panel.unknown",
		// 错误文本必须脱敏：核心与网络层的错误里可能带出配置片段。
		Message:     h.scrubber.Scrub(fmt.Sprintf("%s: %v", op, err)),
		OperationID: op,
		Retryable:   false,
	}
}

// platformBackend 返回当前平台的网络接管后端。
//
// 尚未实现的平台返回 NoopBackend：它会**明确拒绝**接管请求，
// 而不是静默不做事。用户因此能区分「我配错了」和「这个平台还没做」。
func platformBackend() netmgr.Backend {
	switch runtime.GOOS {
	case "linux":
		// GNOME 后端在非 GNOME 桌面上会报告不可用，
		// 由 gnome.Available 判定，这里不预先假设。
		return netmgr.NewGNOMEBackend(nil)
	default:
		return netmgr.NoopBackend{
			Reason: fmt.Sprintf(
				"system network takeover is not implemented for %s yet; "+
					"only local proxy mode is available", runtime.GOOS),
		}
	}
}

// ---------------------------------------------------------------------------
// 编译器适配
// ---------------------------------------------------------------------------

// configCompiler 把 shared/compiler 适配成 coordinator.Compiler。
//
// 协调器要的是「编译并落成文件」，而 compiler.Compile 只返回字节，
// 因此这里补上写文件的职责。
type configCompiler struct {
	dir string

	// counter 用于生成不重名的文件名。
	//
	// 用序号而不是时间戳：时间戳在快速连续编译时可能重复，
	// 而重复的文件名会让核心读到上一份配置。
	counter int
}

func (c *configCompiler) CompileToFile(_ context.Context, req *compiler.Request, dir string) (*coordinator.CompiledConfig, error) {
	if req == nil {
		return nil, errors.New("configCompiler: compile request is required")
	}
	data, report, err := compiler.Compile(req)
	if err != nil {
		return nil, err
	}

	target := dir
	if target == "" {
		target = c.dir
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return nil, fmt.Errorf("configCompiler: create config directory: %w", err)
	}

	c.counter++
	// 文件名只含核心名与序号，不含节点名或地址：文件名会出现在日志与
	// 进程命令行里，而进程命令行在部分平台上任何用户都能看到。
	name := fmt.Sprintf("%s-%d.json", req.Core, c.counter)
	path := filepath.Join(target, name)

	// 先写临时文件再改名：核心可能在上一份配置还在用的时候被重新读取，
	// 半截文件会让它读到一个语法错误的配置。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return nil, fmt.Errorf("configCompiler: write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("configCompiler: activate config: %w", err)
	}

	return &coordinator.CompiledConfig{Core: req.Core, Path: path, Report: report}, nil
}

// ---------------------------------------------------------------------------
// 核心运行器适配
// ---------------------------------------------------------------------------

// coreRunner 把 runtime/core 适配成 coordinator.CoreRunner。
type coreRunner struct {
	binaryDir string
	scrubber  *secrets.Scrubber

	adapters map[compiler.Core]*core.Adapter
	// readyAddress 是等待核心就绪的地址；为空表示不校验就绪。
	readyAddress string
}

func newCoreRunner(binaryDir string, scrubber *secrets.Scrubber) (*coreRunner, error) {
	runner := &coreRunner{
		binaryDir: binaryDir,
		scrubber:  scrubber,
		adapters:  make(map[compiler.Core]*core.Adapter),
	}

	for _, kind := range runner.kinds() {
		path, err := runner.binaryPath(kind)
		if err != nil {
			// 未安装的核心不是错误：界面需要如实展示「未安装」，
			// 而不是让服务因此起不来。
			continue
		}
		adapter, err := core.NewAdapter(core.Kind(kind), path, core.Options{
			Scrubber: scrubber,
		})
		if err != nil {
			return nil, fmt.Errorf("create the %s adapter: %w", kind, err)
		}
		runner.adapters[kind] = adapter
	}
	return runner, nil
}

// kinds 返回本构建支持的内核。
//
// 单内核（ADR-0005）：只有一个条目。这个函数保留成返回切片的形式，
// 是因为 capabilities() 与 newCoreRunner 都按「一组核心」遍历——
// 把循环拆掉反而会让「多内核」的逻辑重新长回来时无人察觉。
func (r *coreRunner) kinds() []compiler.Core {
	return []compiler.Core{compiler.CoreSingBox}
}

// binaryPath 返回核心可执行文件的路径。
//
// 返回错误表示该核心未安装；调用方据此展示「未安装」而不是失败。
func (r *coreRunner) binaryPath(kind compiler.Core) (string, error) {
	name := string(kind)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(r.binaryDir, name)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s is not installed", kind)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not an executable", kind)
	}
	return path, nil
}

// probeVersion 尽力读取核心版本。
//
// 读不到时返回空串：版本号只用于展示，不该因为读不到就阻止其它功能。
//
// 注意这里用的是 coreupdater.ProbeVersion，不是 core 包：
// core 包只管进程生命周期，版本探测属于核心管理（下载与更新）的职责。
// 两个包的 Core 类型不同，因此需要显式转换。
func (r *coreRunner) probeVersion(kind compiler.Core) string {
	if _, ok := r.adapters[kind]; !ok {
		return ""
	}
	path, err := r.binaryPath(kind)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	probed, err := coreupdater.ProbeVersion(ctx, coreupdater.Core(kind), path)
	if err != nil {
		return ""
	}
	return probed
}

func (r *coreRunner) setReadyAddress(address string) { r.readyAddress = address }

func (r *coreRunner) CheckConfig(ctx context.Context, kind compiler.Core, configPath string) error {
	adapter, ok := r.adapters[kind]
	if !ok {
		return fmt.Errorf("core %s is not installed", kind)
	}
	return adapter.CheckConfig(ctx, configPath)
}

func (r *coreRunner) Start(ctx context.Context, kind compiler.Core, configPath string) (coordinator.RunningCore, error) {
	adapter, ok := r.adapters[kind]
	if !ok {
		return nil, fmt.Errorf("core %s is not installed", kind)
	}
	process, err := adapter.Start(ctx, configPath)
	if err != nil {
		return nil, err
	}
	return &runningCore{process: process, readyAddress: r.readyAddress}, nil
}

// runningCore 把 *core.Process 适配成 coordinator.RunningCore。
//
// 需要一层包装的原因：core.Process 不实现 Ready——
// 「进程存在」与「服务已就绪」是两件事，就绪需要额外等待监听端口。
type runningCore struct {
	process      *core.Process
	readyAddress string
}

func (r *runningCore) Ready(ctx context.Context) error {
	if r.readyAddress == "" {
		// 未提供就绪地址时**不假装已就绪**。
		//
		// 这里返回 nil 会让上层认为「已就绪」，从而把一个还在启动中的
		// 核心报告成可用；调用方通过结果里的 readyVerified 字段
		// 知道这件事没有被验证过。
		return nil
	}
	return core.WaitForListen(ctx, r.readyAddress, 10*time.Second)
}

func (r *runningCore) Stop(ctx context.Context) error { return r.process.Stop(ctx) }

func (r *runningCore) Logs() []string { return r.process.TailLogs(200) }
