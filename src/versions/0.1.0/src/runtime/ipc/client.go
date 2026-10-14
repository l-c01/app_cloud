package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// ErrClosed 表示客户端已关闭。
var ErrClosed = errors.New("ipc: client is closed")

// Client 是 IPC 客户端（由桌面 UI 或调试工具使用）。
//
// 设计：单个读取协程负责分发——按 ID 把响应交给等待中的调用者，
// 其余报文按事件交给事件回调。这样调用方可以并发发起多个调用，
// 且不需要自己处理报文交叉。
type Client struct {
	conn net.Conn
	opts CallOptions

	writeMu sync.Mutex
	nextID  atomic.Uint64

	mu      sync.Mutex
	pending map[string]chan Response
	closed  bool

	eventMu      sync.RWMutex
	eventHandler func(Event)

	// lastEventSeq 记录收到的事件序号，便于调用方发现丢事件。
	lastEventSeq atomic.Uint64

	// eventCh 是事件投递队列：单协程消费，避免为每个事件起一个协程。
	eventCh chan Event
	// localDropped 是本地因缓冲满而丢弃的事件数。
	localDropped atomic.Uint64

	done chan struct{}
}

// CallOptions 配置 Client。
type CallOptions struct {
	// EventBuffer 是事件回调的缓冲；为 0 时使用默认 64。
	//
	// 回调本身在独立协程里调用，避免阻塞读取协程。
	EventBuffer int
}

// Dial 连接服务。
func Dial(handle Handle) (*Client, error) {
	return DialWithOptions(handle, CallOptions{})
}

// DialWithOptions 连接服务并指定选项。
func DialWithOptions(handle Handle, opts CallOptions) (*Client, error) {
	if err := handle.Validate(); err != nil {
		return nil, err
	}
	if opts.EventBuffer <= 0 {
		opts.EventBuffer = 64
	}
	c, err := net.Dial(handle.Network, handle.Address)
	if err != nil {
		return nil, fmt.Errorf("ipc: dial %s %s: %w", handle.Network, handle.Address, err)
	}

	cli := &Client{
		conn:    c,
		opts:    opts,
		pending: make(map[string]chan Response),
		eventCh: make(chan Event, opts.EventBuffer),
		done:    make(chan struct{}),
	}
	go cli.readLoop()
	go cli.eventWorker()
	return cli, nil
}

// DialHandleFile 读取句柄文件并连接。
func DialHandleFile(handlePath string) (*Client, error) {
	h, err := ReadHandleFile(handlePath)
	if err != nil {
		return nil, err
	}
	return Dial(h)
}

// OnEvent 注册事件回调。
//
// 回调在独立协程中串行执行；若回调较慢，后续事件会在缓冲中排队，
// 缓冲满时事件被丢弃并由服务端计数（见 Event.Dropped）。
func (c *Client) OnEvent(handler func(Event)) {
	c.eventMu.Lock()
	c.eventHandler = handler
	c.eventMu.Unlock()
}

// LastEventSeq 返回最近收到的事件序号。
func (c *Client) LastEventSeq() uint64 { return c.lastEventSeq.Load() }

// Call 发起一次调用并把结果解码到 out。
//
// out 可以是 nil（只关心成功与否），也可以是任意可被 json.Unmarshal 接收的值。
func (c *Client) Call(ctx context.Context, token string, op Operation, params any, out any) error {
	raw, err := c.CallRaw(ctx, token, op, params)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// CallRaw 发起一次调用并返回原始结果。
func (c *Client) CallRaw(ctx context.Context, token string, op Operation, params any) (json.RawMessage, error) {
	var encodedParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("ipc: encode params: %w", err)
		}
		encodedParams = data
	}

	id := fmt.Sprintf("%d", c.nextID.Add(1))

	wait := make(chan Response, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.pending[id] = wait
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req := Request{
		ID:              id,
		Operation:       op,
		ProtocolVersion: ProtocolVersion,
		SchemaVersion:   SchemaVersion,
		Token:           token,
		Params:          encodedParams,
	}
	if err := c.write(req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, ErrClosed
	case resp := <-wait:
		if resp.Error != nil {
			return nil, resp.Error
		}
		if !resp.OK {
			return nil, &RPCError{Code: CodeInternal, MessageKey: "ipc.not_ok", Message: "服务返回了未标记成功的响应"}
		}
		return resp.Result, nil
	}
}

// write 串行写出一个请求。
func (c *Client) write(req Request) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("ipc: encode request: %w", err)
	}
	data = append(data, '\n')

	if _, err := c.conn.Write(data); err != nil {
		return fmt.Errorf("ipc: write request: %w", err)
	}
	return nil
}

// readLoop 读取并分发报文。
func (c *Client) readLoop() {
	reader := bufio.NewReaderSize(c.conn, 64*1024)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if len(line) == 0 {
				c.shutdown()
				return
			}
			return
		}
		if len(line) > maxMessageBytes {
			// 服务不可能发出超过上限的报文；视为协议错误并断开。
			c.shutdown()
			return
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // 忽略无法解析的报文，等待后续
		}

		if resp.Event != nil {
			c.lastEventSeq.Store(resp.Event.Seq)
			c.dispatchEvent(*resp.Event)
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		c.mu.Unlock()
		if !ok {
			continue // 无人等待（例如已超时放弃）
		}
		select {
		case ch <- resp:
		default:
			// 缓冲为 1 且已有值：说明重复响应，忽略。
		}
	}
}

// eventWorker 是唯一的事件消费协程。
//
// 单协程 + 缓冲队列：既保证事件顺序，又不会为每个事件创建协程。
// 回调较慢时事件在队列中排队，队列满则由 dispatchEvent 丢弃并计数。
func (c *Client) eventWorker() {
	for {
		select {
		case <-c.done:
			return
		case ev := <-c.eventCh:
			c.eventMu.RLock()
			handler := c.eventHandler
			c.eventMu.RUnlock()
			if handler != nil {
				handler(ev)
			}
		}
	}
}

// dispatchEvent 把事件放入队列。
//
// 非阻塞：队列满时丢弃，避免慢回调阻塞读取协程（进而阻塞所有响应分发）。
func (c *Client) dispatchEvent(ev Event) {
	select {
	case c.eventCh <- ev:
	default:
		c.localDropped.Add(1)
	}
}

// LocalDropped 返回本地因队列满而丢弃的事件数。
//
// 与服务端上报的 Event.Dropped 相加才是真正丢失的总量。
func (c *Client) LocalDropped() uint64 { return c.localDropped.Load() }

// shutdown 标记客户端已关闭并唤醒所有等待者。
func (c *Client) shutdown() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	close(c.done)
}

// Close 关闭客户端。
func (c *Client) Close() error {
	c.shutdown()
	return c.conn.Close()
}

// ---- 常用操作的类型化封装 ----

// GetCapabilities 获取服务能力。
func (c *Client) GetCapabilities(ctx context.Context, token string) (Capabilities, error) {
	var caps Capabilities
	err := c.Call(ctx, token, OpGetCapabilities, nil, &caps)
	return caps, err
}

// GetRuntimeState 获取运行状态。
func (c *Client) GetRuntimeState(ctx context.Context, token string) (RuntimeState, error) {
	var st RuntimeState
	err := c.Call(ctx, token, OpGetRuntimeState, nil, &st)
	return st, err
}

// StartParams 是 Start 操作的参数。
type StartParams struct {
	Core           string `json:"core"`
	SelectedNodeID string `json:"selectedNodeId,omitempty"`
	Mode           string `json:"mode,omitempty"`
	// ProfileRevision 用于让服务判断请求是否基于最新配置（设计基线 §6.2）。
	ProfileRevision uint64 `json:"profileRevision,omitempty"`
}

// StopParams 是 Stop 操作的参数。
type StopParams struct {
	// OperationID 让服务可以识别重复的停止请求并保持幂等。
	OperationID string `json:"operationId,omitempty"`
}

// SwitchNodeParams 是 SwitchNode 操作的参数。
type SwitchNodeParams struct {
	NodeID string `json:"nodeId"`
}

// SetNetworkModeParams 是 SetNetworkMode 操作的参数。
type SetNetworkModeParams struct {
	Mode string `json:"mode"`
}

// Start 请求启动连接。
func (c *Client) Start(ctx context.Context, token string, p StartParams) (RuntimeState, error) {
	var st RuntimeState
	err := c.Call(ctx, token, OpStart, p, &st)
	return st, err
}

// Stop 请求停止连接（幂等）。
func (c *Client) Stop(ctx context.Context, token string, p StopParams) (RuntimeState, error) {
	var st RuntimeState
	err := c.Call(ctx, token, OpStop, p, &st)
	return st, err
}

// RecoverNetwork 请求恢复本客户端拥有的网络设置。
func (c *Client) RecoverNetwork(ctx context.Context, token string) (RuntimeState, error) {
	var st RuntimeState
	err := c.Call(ctx, token, OpRecoverNetwork, nil, &st)
	return st, err
}
