// Package ipc 实现桌面 UI 与后台服务之间的本地 IPC。
//
// 设计约束（设计基线 §6.2）：
//   - 通过**本地**通道通信（Unix socket / 命名管道），不开放公网 HTTP 管理端口；
//   - 握手时校验 protocolVersion 与 schemaVersion，版本不匹配要明确拒绝而不是尽力解析；
//   - 统一错误结构：code / messageKey / fieldPath / retryable / operationId / detailsRedacted；
//   - 错误里**不得**包含完整 token 或其它凭据；
//   - 事件带序号，有背压与丢弃计数，避免慢消费者拖垮服务。
package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ProtocolVersion 是线协议的版本。
//
// 不兼容变更必须递增它；服务与客户端都会在握手时拒绝不匹配的对方。
const ProtocolVersion = 1

// SchemaVersion 是载荷结构的版本，与 ProtocolVersion 独立演进。
//
// 两者分开的原因：可以只改请求/响应的字段结构（SchemaVersion）而不动线协议
// （例如仍用同一套请求-响应封装）。
const SchemaVersion = "1.0.0"

// Operation 是操作名。
//
// 取值与设计基线 §6.2 的接口清单一一对应；新增操作必须同时更新文档与能力声明。
type Operation string

const (
	OpGetCapabilities Operation = "GetCapabilities"
	OpValidateProfile Operation = "ValidateProfile"
	OpCompileProfile  Operation = "CompileProfile"
	OpStart           Operation = "Start"
	OpStop            Operation = "Stop"
	OpGetRuntimeState Operation = "GetRuntimeState"
	OpSwitchNode      Operation = "SwitchNode"
	OpSetNetworkMode  Operation = "SetNetworkMode"
	OpWatchEvents     Operation = "WatchEvents"
	OpRecoverNetwork  Operation = "RecoverNetwork"
)

// KnownOperations 返回全部已知操作，用于能力声明与校验。
func KnownOperations() []Operation {
	return []Operation{
		OpGetCapabilities, OpValidateProfile, OpCompileProfile,
		OpStart, OpStop, OpGetRuntimeState, OpSwitchNode,
		OpSetNetworkMode, OpWatchEvents, OpRecoverNetwork,
	}
}

// Known 判断操作名是否受支持。
func (o Operation) Known() bool {
	for _, known := range KnownOperations() {
		if o == known {
			return true
		}
	}
	return false
}

// Request 是一次调用请求。
type Request struct {
	// ID 由客户端生成，用于把响应与请求配对；事件推送不含 ID。
	ID string `json:"id"`
	// Operation 是操作名。
	Operation Operation `json:"operation"`
	// ProtocolVersion 与 SchemaVersion 由客户端声明，服务端校验。
	ProtocolVersion int    `json:"protocolVersion"`
	SchemaVersion   string `json:"schemaVersion"`
	// Token 是本地访问令牌，防止同机其它用户/进程调用本服务。
	//
	// 它不是用户凭据，只用于本地通道的访问控制，因此可以出现在本地 IPC 报文中；
	// 但它绝不能被写入日志或诊断包。
	Token string `json:"token,omitempty"`
	// Params 是操作参数；结构与操作对应。
	Params json.RawMessage `json:"params,omitempty"`
}

// Response 是一次调用响应，或一条事件推送。
type Response struct {
	ID        string          `json:"id,omitempty"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *RPCError       `json:"error,omitempty"`
	Event     *Event          `json:"event,omitempty"`
	// 服务端把自己的版本回填，便于客户端诊断不匹配问题。
	ProtocolVersion int    `json:"protocolVersion"`
	SchemaVersion   string `json:"schemaVersion"`
}

// Event 是服务端主动推送的事件。
//
// Seq 单调递增；客户端据此发现丢事件（背压丢弃时 Seq 会跳号）。
type Event struct {
	Seq     uint64          `json:"seq"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
	// Dropped 是本次推送前因背压被丢弃的事件数。
	// 它让客户端知道「你看到的不是全部」，而不是静默地少几条日志。
	Dropped uint64 `json:"dropped,omitempty"`
}

// RPCError 是统一错误结构。
//
// 约定：
//   - Code 是稳定的机器可读码；客户端据此分支，不解析 Message；
//   - MessageKey 供 i18n 查表，界面文案由客户端本地化；
//   - FieldPath 用于字段级错误（例如配置里某一项不兼容）；
//   - DetailsRedacted 只放**已脱敏**的补充信息，禁止放原始凭据。
type RPCError struct {
	Code            string            `json:"code"`
	MessageKey      string            `json:"messageKey"`
	Message         string            `json:"message,omitempty"`
	FieldPath       string            `json:"fieldPath,omitempty"`
	Retryable       bool              `json:"retryable"`
	OperationID     string            `json:"operationId,omitempty"`
	DetailsRedacted map[string]string `json:"detailsRedacted,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.FieldPath != "" {
		return fmt.Sprintf("ipc: %s (%s): %s", e.Code, e.FieldPath, e.Message)
	}
	return fmt.Sprintf("ipc: %s: %s", e.Code, e.Message)
}

// 稳定错误码。
const (
	CodeMalformed        = "malformed_request"
	CodeVersionMismatch  = "version_mismatch"
	CodeUnauthorized     = "unauthorized"
	CodeUnknownOperation = "unknown_operation"
	CodeInvalidParams    = "invalid_params"
	CodeBusy             = "busy"
	CodeNotRunning       = "not_running"
	CodeInternal         = "internal_error"
	CodeTimeout          = "timeout"
	CodeBackpressure     = "backpressure"
)

// ErrVersionMismatch 便于调用方用 errors.Is 判断。
var ErrVersionMismatch = errors.New("ipc: protocol or schema version mismatch")

// ErrUnauthorized 表示本地令牌不正确。
var ErrUnauthorized = errors.New("ipc: unauthorized")

// checkHandshake 校验协议与结构版本。
//
// 版本不匹配时拒绝服务，而不是尽力解析——尽力解析会把「字段缺失」变成难以定位的运行时错误。
func checkHandshake(protocolVersion int, schemaVersion string) *RPCError {
	if protocolVersion != ProtocolVersion {
		return &RPCError{
			Code:       CodeVersionMismatch,
			MessageKey: "ipc.version_mismatch.protocol",
			Message: fmt.Sprintf("协议版本不匹配：服务为 %d，客户端为 %d",
				ProtocolVersion, protocolVersion),
			DetailsRedacted: map[string]string{
				"serverProtocol": fmt.Sprint(ProtocolVersion),
				"clientProtocol": fmt.Sprint(protocolVersion),
			},
		}
	}
	if schemaVersion != SchemaVersion {
		return &RPCError{
			Code:       CodeVersionMismatch,
			MessageKey: "ipc.version_mismatch.schema",
			Message: fmt.Sprintf("结构版本不匹配：服务为 %s，客户端为 %s",
				SchemaVersion, schemaVersion),
			DetailsRedacted: map[string]string{
				"serverSchema": SchemaVersion,
				"clientSchema": schemaVersion,
			},
		}
	}
	return nil
}

// Handle 是客户端连接服务所需的全部信息。
//
// 它会被写到状态文件里；**Token 字段是本地访问控制凭据**，
// 状态文件必须以仅当前用户可读的权限创建。
type Handle struct {
	// Network 与 Address 供 net.Dial 使用（如 "unix" + socket 路径）。
	Network string `json:"network"`
	Address string `json:"address"`
	// Token 是本地访问令牌。
	Token string `json:"token"`
	// ProtocolVersion 与 SchemaVersion 供客户端预先判断兼容性。
	ProtocolVersion int    `json:"protocolVersion"`
	SchemaVersion   string `json:"schemaVersion"`
	// ServerPID 便于诊断与清理残留进程。
	ServerPID int `json:"serverPid"`
}

// Validate 校验句柄可用性。
func (h Handle) Validate() error {
	if strings.TrimSpace(h.Network) == "" {
		return errors.New("ipc: handle has no network")
	}
	if strings.TrimSpace(h.Address) == "" {
		return errors.New("ipc: handle has no address")
	}
	if strings.TrimSpace(h.Token) == "" {
		return errors.New("ipc: handle has no access token")
	}
	return nil
}

// NewToken 生成一个本地访问令牌。
//
// 使用 crypto/rand；长度 32 字节（64 个十六进制字符）足以抵御本地暴力猜测。
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("ipc: generate access token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Capabilities 是 GetCapabilities 的返回值。
//
// 客户端据此决定哪些界面元素可用，而不是假设服务一定支持某项能力（设计基线 §4.2）。
type Capabilities struct {
	Platform        string   `json:"platform"`
	ProtocolVersion int      `json:"protocolVersion"`
	SchemaVersion   string   `json:"schemaVersion"`
	Operations      []string `json:"operations"`
	// Cores 是已安装且可用的核心。
	Cores []CoreCapability `json:"cores"`
	// TrafficEntries 是本平台支持的接入方式。
	TrafficEntries []string `json:"trafficEntries"`
	// NetworkTakeover 表示平台层是否真的能够接管系统网络。
	NetworkTakeover bool `json:"networkTakeover"`
	// CoreUpdate 表示是否支持应用内核心更新。
	CoreUpdate bool `json:"coreUpdate"`
}

// CoreCapability 描述单个核心的可用性。
type CoreCapability struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Installed 为 false 时 Version 通常为空。
	Installed bool `json:"installed"`
	// VerifiedAgainst 记录能力表所依据的核心版本；未实测时为 "unverified"。
	VerifiedAgainst string `json:"verifiedAgainst"`
}

// RuntimeState 是 GetRuntimeState 的返回值，对应 coordinator.Status 的可序列化形式。
type RuntimeState struct {
	State      string `json:"state"`
	Generation uint64 `json:"generation"`
	Core       string `json:"core,omitempty"`
	ActiveNode string `json:"activeNode,omitempty"`
	Mode       string `json:"mode,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	// Conflicts 是上次网络恢复时发现、需要用户处理的冲突。
	Conflicts []ConflictInfo `json:"conflicts,omitempty"`
}

// ConflictInfo 描述一处网络设置冲突。
type ConflictInfo struct {
	Key string `json:"key"`
	// Expected 是我们当初写入的值；Actual 是当前实际值。
	// 两者都不含凭据，可以安全地展示给用户。
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}
