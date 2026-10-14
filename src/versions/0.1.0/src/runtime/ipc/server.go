package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// maxMessageBytes 限制单条报文的长度，防止异常客户端耗尽服务内存。
const maxMessageBytes = 8 << 20 // 8 MiB

// eventQueueSize 是每个连接的事件队列长度。
//
// 队列满时丢弃最旧的事件并累加丢弃计数，而不是阻塞服务：
// 慢消费者不应拖垮整个服务（设计基线 §6.2 的背压要求）。
const eventQueueSize = 256

// Handler 实现具体的操作。
//
// 实现方负责参数解析与业务；本包负责线协议、版本校验、令牌校验与背压。
type Handler interface {
	Handle(ctx context.Context, op Operation, params json.RawMessage) (any, *RPCError)
}

// HandlerFunc 便于用函数实现 Handler。
type HandlerFunc func(ctx context.Context, op Operation, params json.RawMessage) (any, *RPCError)

// Handle 实现 Handler。
func (f HandlerFunc) Handle(ctx context.Context, op Operation, params json.RawMessage) (any, *RPCError) {
	return f(ctx, op, params)
}

// ListenerFactory 创建本地监听器。
//
// 之所以做成可注入：Unix 上使用 unix socket，Windows 上应注入命名管道实现
// （Go 标准库没有命名管道，需要平台层提供）。默认实现使用 unix socket。
type ListenerFactory func(address string) (net.Listener, error)

// UnixListenerFactory 使用 Unix 域套接字。
func UnixListenerFactory(address string) (net.Listener, error) {
	return net.Listen("unix", address)
}

// Options 配置 Server。
type Options struct {
	Handler Handler
	// SocketPath 是监听地址（unix socket 路径）。
	SocketPath string
	// HandlePath 是写入连接句柄的文件路径；留空表示不写文件。
	HandlePath string
	// ListenerFactory 允许替换监听实现；为 nil 时使用 UnixListenerFactory。
	ListenerFactory ListenerFactory
	// Token 为空时自动生成。
	Token string
	// CallTimeout 是单次调用的上限；默认 30 秒。
	CallTimeout time.Duration
}

// Server 提供本地 IPC 服务。
type Server struct {
	opts   Options
	token  string
	handle Handle

	mu       sync.Mutex
	ln       net.Listener
	conns    map[*conn]struct{}
	closed   bool
	closeErr error
}

// NewServer 构造服务。
func NewServer(opts Options) (*Server, error) {
	if opts.Handler == nil {
		return nil, errors.New("ipc: handler is required")
	}
	if opts.SocketPath == "" {
		return nil, errors.New("ipc: socket path is required")
	}
	if opts.ListenerFactory == nil {
		opts.ListenerFactory = UnixListenerFactory
	}
	if opts.CallTimeout <= 0 {
		opts.CallTimeout = 30 * time.Second
	}

	token := opts.Token
	if token == "" {
		var err error
		token, err = NewToken()
		if err != nil {
			return nil, err
		}
	}

	return &Server{
		opts:  opts,
		token: token,
		conns: make(map[*conn]struct{}),
	}, nil
}

// Listen 开始监听并准备连接句柄。
func (s *Server) Listen() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ln != nil {
		return errors.New("ipc: already listening")
	}

	// 清理可能残留的 socket 文件，否则 bind 会失败。
	if err := removeIfExists(s.opts.SocketPath); err != nil {
		return fmt.Errorf("ipc: remove stale socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.opts.SocketPath), 0o700); err != nil {
		return fmt.Errorf("ipc: create socket directory: %w", err)
	}

	ln, err := s.opts.ListenerFactory(s.opts.SocketPath)
	if err != nil {
		return fmt.Errorf("ipc: listen on %s: %w", s.opts.SocketPath, err)
	}
	s.ln = ln

	s.handle = Handle{
		Network:         ln.Addr().Network(),
		Address:         ln.Addr().String(),
		Token:           s.token,
		ProtocolVersion: ProtocolVersion,
		SchemaVersion:   SchemaVersion,
		ServerPID:       os.Getpid(),
	}

	if s.opts.HandlePath != "" {
		if err := writeHandleFile(s.opts.HandlePath, s.handle); err != nil {
			_ = ln.Close()
			s.ln = nil
			return err
		}
	}
	return nil
}

// Handle 返回连接句柄（供同进程内直接使用或写入状态文件）。
func (s *Server) Handle() Handle { return s.handle }

// Serve 处理连接直到 ctx 被取消或 Close 被调用。
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()

	if ln == nil {
		return errors.New("ipc: Listen must be called before Serve")
	}

	// ctx 取消时关闭监听，让 Accept 返回。
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			// 单个连接的临时错误不应终止服务。
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return fmt.Errorf("ipc: accept: %w", err)
		}

		conn := newConn(c, s)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return nil
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		go conn.serve()
	}
}

// Broadcast 向所有连接推送一个事件。
//
// 非阻塞：队列满时丢弃最旧事件并累加该连接的丢弃计数。
func (s *Server) Broadcast(kind string, payload any) {
	var raw json.RawMessage
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			// 事件序列化失败不应影响服务运行；记录为一条内部警告事件。
			raw = json.RawMessage(`{"error":"event payload could not be encoded"}`)
		} else {
			raw = encoded
		}
	}

	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		c.enqueue(Event{Kind: kind, Payload: raw})
	}
}

// Close 关闭服务并清理资源。
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	if ln != nil {
		s.closeErr = ln.Close()
	}
	for _, c := range conns {
		c.close()
	}

	// 清理句柄文件与 socket 文件：句柄里含令牌，不能遗留。
	if s.opts.HandlePath != "" {
		_ = os.Remove(s.opts.HandlePath)
	}
	_ = removeIfExists(s.opts.SocketPath)

	return s.closeErr
}

// ---- 连接处理 ----

// conn 是一条客户端连接。
type conn struct {
	netConn net.Conn
	server  *Server

	writeMu sync.Mutex

	evMu      sync.Mutex
	evQueue   []Event
	evSignal  chan struct{}
	evDropped uint64

	seq atomic.Uint64

	closeOnce sync.Once
	closed    chan struct{}
}

func newConn(c net.Conn, s *Server) *conn {
	return &conn{
		netConn:  c,
		server:   s,
		evSignal: make(chan struct{}, 1),
		closed:   make(chan struct{}),
	}
}

func (c *conn) serve() {
	defer func() {
		c.close()
		c.server.mu.Lock()
		delete(c.server.conns, c)
		c.server.mu.Unlock()
	}()

	go c.pumpEvents()

	// 按行读取并强制单条报文长度上限。
	//
	// 用 ReadBytes 而不是 json.Decoder：Decoder 没有尺寸上限，
	// 恶意或异常客户端可以用一条超长报文把服务内存吃光。
	reader := bufio.NewReaderSize(c.netConn, 64*1024)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if len(line) == 0 {
				return // 对端关闭或读取出错
			}
			// 读到数据但未读到换行且出错：视作断开。
			return
		}
		if len(line) > maxMessageBytes {
			c.write(Response{
				OK: false,
				Error: &RPCError{
					Code:       CodeMalformed,
					MessageKey: "ipc.message_too_large",
					Message:    "请求报文超过长度上限",
					DetailsRedacted: map[string]string{
						"limitBytes": fmt.Sprint(maxMessageBytes),
					},
				},
				ProtocolVersion: ProtocolVersion,
				SchemaVersion:   SchemaVersion,
			})
			return
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			// 报文不合法：回一条明确错误后关闭连接。
			// 不继续读是因为无法再可靠地同步报文边界。
			c.write(Response{
				OK: false,
				Error: &RPCError{
					Code:       CodeMalformed,
					MessageKey: "ipc.malformed",
					Message:    "请求不是合法的 JSON 报文",
				},
				ProtocolVersion: ProtocolVersion,
				SchemaVersion:   SchemaVersion,
			})
			return
		}

		resp := c.server.dispatch(&req)
		if resp == nil {
			continue
		}
		if err := c.write(*resp); err != nil {
			return
		}
	}
}

// dispatch 校验并执行一次调用。
func (s *Server) dispatch(req *Request) *Response {
	base := Response{
		ID:              req.ID,
		ProtocolVersion: ProtocolVersion,
		SchemaVersion:   SchemaVersion,
	}

	// 1) 令牌校验放在最前：未授权请求不应获得任何版本或能力信息。
	if req.Token != s.token {
		base.Error = &RPCError{
			Code:       CodeUnauthorized,
			MessageKey: "ipc.unauthorized",
			Message:    "本地访问令牌无效",
		}
		return &base
	}

	// 2) 版本握手：不匹配时明确拒绝，而不是尽力解析。
	if verr := checkHandshake(req.ProtocolVersion, req.SchemaVersion); verr != nil {
		base.Error = verr
		return &base
	}

	// 3) 操作名校验。
	if !req.Operation.Known() {
		base.Error = &RPCError{
			Code:       CodeUnknownOperation,
			MessageKey: "ipc.unknown_operation",
			Message:    fmt.Sprintf("未知操作：%s", req.Operation),
			FieldPath:  "operation",
		}
		return &base
	}

	// 4) 执行。带超时，避免一次调用永久占住连接。
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.CallTimeout)
	defer cancel()

	result, rpcErr := s.opts.Handler.Handle(ctx, req.Operation, req.Params)
	if rpcErr != nil {
		base.Error = rpcErr
		return &base
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		base.Error = &RPCError{
			Code:       CodeInternal,
			MessageKey: "ipc.result_encode_failed",
			Message:    "服务端无法序列化返回结果",
			DetailsRedacted: map[string]string{
				"operation": string(req.Operation),
			},
		}
		return &base
	}

	base.OK = true
	base.Result = encoded
	return &base
}

// enqueue 把事件放入队列；队列满时丢弃最旧的事件并计数。
func (c *conn) enqueue(ev Event) {
	c.evMu.Lock()
	if len(c.evQueue) >= eventQueueSize {
		// 丢弃最旧的一条，为新的腾出位置。
		c.evQueue = c.evQueue[1:]
		c.evDropped++
	}
	c.evQueue = append(c.evQueue, ev)
	c.evMu.Unlock()

	select {
	case c.evSignal <- struct{}{}:
	default:
	}
}

// pumpEvents 持续把队列里的事件写给客户端。
func (c *conn) pumpEvents() {
	for {
		select {
		case <-c.closed:
			return
		case <-c.evSignal:
		}

		for {
			c.evMu.Lock()
			if len(c.evQueue) == 0 {
				c.evMu.Unlock()
				break
			}
			ev := c.evQueue[0]
			c.evQueue = c.evQueue[1:]
			dropped := c.evDropped
			c.evDropped = 0
			c.evMu.Unlock()

			ev.Seq = c.seq.Add(1)
			// 把累计丢弃数附在下一条事件上，让客户端知道有内容缺失。
			if dropped > 0 {
				ev.Dropped = dropped
			}

			resp := Response{
				OK:              true,
				Event:           &ev,
				ProtocolVersion: ProtocolVersion,
				SchemaVersion:   SchemaVersion,
			}
			if err := c.write(resp); err != nil {
				return
			}
		}
	}
}

// write 串行写出一个响应，避免事件与响应交叉写坏报文。
func (c *conn) write(resp Response) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if _, err := c.netConn.Write(data); err != nil {
		return err
	}
	return nil
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.netConn.Close()
	})
}

// ---- 文件与路径辅助 ----

// removeIfExists 删除文件（不存在时不算错误）。
func removeIfExists(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// writeHandleFile 以仅当前用户可读的权限写入连接句柄。
//
// 权限很关键：句柄里含本地访问令牌，若其它用户可读，本地访问控制形同虚设。
func writeHandleFile(path string, h Handle) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("ipc: create handle directory: %w", err)
	}

	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("ipc: encode handle: %w", err)
	}

	// 先写临时文件再改名：避免客户端读到写了一半的句柄。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("ipc: write handle: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ipc: activate handle: %w", err)
	}
	return nil
}

// ReadHandleFile 读取连接句柄（供客户端使用）。
func ReadHandleFile(path string) (Handle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Handle{}, fmt.Errorf("ipc: read handle: %w", err)
	}
	var h Handle
	if err := json.Unmarshal(data, &h); err != nil {
		return Handle{}, fmt.Errorf("ipc: decode handle: %w", err)
	}
	if err := h.Validate(); err != nil {
		return Handle{}, err
	}
	return h, nil
}
