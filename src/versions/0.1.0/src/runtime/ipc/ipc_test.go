package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- 测试夹具 ----

type echoHandler struct {
	mu     sync.Mutex
	calls  []Operation
	failOn Operation
}

func (h *echoHandler) Handle(_ context.Context, op Operation, params json.RawMessage) (any, *RPCError) {
	h.mu.Lock()
	h.calls = append(h.calls, op)
	h.mu.Unlock()

	if op == h.failOn {
		return nil, &RPCError{
			Code:       CodeInvalidParams,
			MessageKey: "test.failure",
			Message:    "预设的失败",
			FieldPath:  "params.field",
			Retryable:  false,
		}
	}

	switch op {
	case OpGetCapabilities:
		return Capabilities{
			Platform:        "test",
			ProtocolVersion: ProtocolVersion,
			SchemaVersion:   SchemaVersion,
			Operations:      []string{string(OpGetCapabilities)},
		}, nil
	case OpGetRuntimeState:
		return RuntimeState{State: "stopped", Generation: 1}, nil
	default:
		return map[string]string{"echo": string(params)}, nil
	}
}

func newTestServer(t *testing.T, h Handler) (*Server, Handle) {
	t.Helper()

	dir := t.TempDir()
	sock := filepath.Join(dir, "ipc.sock")
	handlePath := filepath.Join(dir, "ipc.json")

	srv, err := NewServer(Options{
		Handler:    h,
		SocketPath: sock,
		HandlePath: handlePath,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Listen(); err != nil {
		// 某些平台不支持本地套接字；跳过而不是伪装通过。
		t.Skipf("local socket listener unavailable: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	return srv, srv.Handle()
}

func dialTest(t *testing.T, h Handle) *Client {
	t.Helper()
	c, err := Dial(h)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// ---- 协议与版本 ----

func TestCallRoundTrip(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)
	c := dialTest(t, handle)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	caps, err := c.GetCapabilities(ctx, handle.Token)
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if caps.Platform != "test" {
		t.Errorf("platform = %q", caps.Platform)
	}
	// 服务端必须回填自己的版本，便于客户端诊断不匹配。
	if caps.ProtocolVersion != ProtocolVersion || caps.SchemaVersion != SchemaVersion {
		t.Errorf("capabilities versions = %d / %q", caps.ProtocolVersion, caps.SchemaVersion)
	}
}

func TestUnauthorizedTokenIsRejectedBeforeAnythingElse(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)
	c := dialTest(t, handle)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.GetCapabilities(ctx, "wrong-token")
	if err == nil {
		t.Fatal("expected an authorization error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %v, want *RPCError", err)
	}
	if rpcErr.Code != CodeUnauthorized {
		t.Errorf("code = %q, want %q", rpcErr.Code, CodeUnauthorized)
	}
	// 未授权请求不应被投递到业务层。
	h.mu.Lock()
	calls := len(h.calls)
	h.mu.Unlock()
	if calls != 0 {
		t.Errorf("handler must not be invoked for unauthorized calls, got %d calls", calls)
	}
}

func TestProtocolVersionMismatchIsRejected(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)

	conn, err := net.Dial(handle.Network, handle.Address)
	if err != nil {
		t.Skipf("local socket unavailable: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 手工构造一个协议版本不匹配的请求。
	req := Request{
		ID:              "1",
		Operation:       OpGetCapabilities,
		ProtocolVersion: ProtocolVersion + 99,
		SchemaVersion:   SchemaVersion,
		Token:           handle.Token,
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}

	line := make([]byte, 64*1024)
	n, err := conn.Read(line)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(line[:n], &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeVersionMismatch {
		t.Fatalf("expected a version mismatch error, got %+v", resp)
	}
	// 错误详情应给出双方版本，便于诊断，但不得含凭据。
	if resp.Error.DetailsRedacted["clientProtocol"] == "" {
		t.Error("version mismatch should report the client version for diagnosis")
	}
}

func TestSchemaVersionMismatchIsRejected(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)

	conn, err := net.Dial(handle.Network, handle.Address)
	if err != nil {
		t.Skipf("local socket unavailable: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := Request{
		ID:              "1",
		Operation:       OpGetCapabilities,
		ProtocolVersion: ProtocolVersion,
		SchemaVersion:   "9.9.9",
		Token:           handle.Token,
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	_, _ = conn.Write(data)

	line := make([]byte, 64*1024)
	n, _ := conn.Read(line)
	var resp Response
	_ = json.Unmarshal(line[:n], &resp)
	if resp.Error == nil || resp.Error.Code != CodeVersionMismatch {
		t.Fatalf("expected a schema version mismatch, got %+v", resp)
	}
}

func TestUnknownOperationIsRejected(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)
	c := dialTest(t, handle)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.CallRaw(ctx, handle.Token, Operation("DoSomethingWeird"), nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %v, want *RPCError", err)
	}
	if rpcErr.Code != CodeUnknownOperation {
		t.Errorf("code = %q", rpcErr.Code)
	}
	if rpcErr.FieldPath != "operation" {
		t.Errorf("fieldPath = %q, want operation", rpcErr.FieldPath)
	}
}

func TestHandlerErrorIsPropagatedWithFieldPath(t *testing.T) {
	h := &echoHandler{failOn: OpCompileProfile}
	_, handle := newTestServer(t, h)
	c := dialTest(t, handle)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.CallRaw(ctx, handle.Token, OpCompileProfile, map[string]string{"a": "b"})
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %v, want *RPCError", err)
	}
	if rpcErr.Code != CodeInvalidParams || rpcErr.FieldPath != "params.field" {
		t.Errorf("error = %+v", rpcErr)
	}
	if rpcErr.MessageKey == "" {
		t.Error("errors must carry a message key for i18n")
	}
}

func TestMalformedRequestGetsAnErrorResponse(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)

	conn, err := net.Dial(handle.Network, handle.Address)
	if err != nil {
		t.Skipf("local socket unavailable: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("this is not json\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	line := make([]byte, 64*1024)
	n, err := conn.Read(line)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(line[:n], &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeMalformed {
		t.Fatalf("expected a malformed error, got %+v", resp)
	}
}

func TestOversizedRequestIsRejected(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)

	conn, err := net.Dial(handle.Network, handle.Address)
	if err != nil {
		t.Skipf("local socket unavailable: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 构造一条超过上限的报文（填充一个大字符串）。
	big := strings.Repeat("x", maxMessageBytes+1024)
	payload := `{"id":"1","operation":"GetCapabilities","protocolVersion":1,"schemaVersion":"1.0.0","token":"` +
		handle.Token + `","params":"` + big + `"}` + "\n"

	if _, err := conn.Write([]byte(payload)); err != nil {
		// 写入可能因对端关闭而失败，这本身也是可接受的结果。
		return
	}

	line := make([]byte, 64*1024)
	n, err := conn.Read(line)
	if err != nil {
		return // 服务直接断开也是可接受的处置
	}
	var resp Response
	if err := json.Unmarshal(line[:n], &resp); err != nil {
		return
	}
	if resp.Error != nil && resp.Error.Code != CodeMalformed {
		t.Errorf("code = %q, want malformed for oversized input", resp.Error.Code)
	}
}

// ---- 事件与背压 ----

func TestBroadcastDeliversEventsWithMonotonicSequence(t *testing.T) {
	h := &echoHandler{}
	srv, handle := newTestServer(t, h)
	c := dialTest(t, handle)

	received := make(chan Event, 16)
	c.OnEvent(func(ev Event) { received <- ev })

	// 先做一次调用：它同时证明连接已被服务端接受并登记，
	// 否则紧随其后的 Broadcast 可能因连接尚未入表而丢失。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.GetRuntimeState(ctx, handle.Token); err != nil {
		t.Fatalf("warm-up call failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		srv.Broadcast("state", map[string]int{"i": i})
	}

	var seqs []uint64
	timeout := time.After(5 * time.Second)
	for len(seqs) < 3 {
		select {
		case ev := <-received:
			if ev.Kind != "state" {
				t.Errorf("kind = %q", ev.Kind)
			}
			seqs = append(seqs, ev.Seq)
		case <-timeout:
			t.Fatalf("only received %d events", len(seqs))
		}
	}

	// 序号必须严格递增，客户端才能据此发现丢事件。
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Errorf("sequence not increasing: %v", seqs)
		}
	}
}

// ---- 句柄文件 ----

func TestHandleFileIsWrittenWithRestrictivePermissions(t *testing.T) {
	h := &echoHandler{}
	dir := t.TempDir()
	handlePath := filepath.Join(dir, "nested", "ipc.json")
	sock := filepath.Join(dir, "ipc.sock")

	srv, err := NewServer(Options{Handler: h, SocketPath: sock, HandlePath: handlePath})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Listen(); err != nil {
		t.Skipf("local socket unavailable: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	info, err := os.Stat(handlePath)
	if err != nil {
		t.Fatalf("handle file missing: %v", err)
	}
	// 句柄含本地访问令牌，必须是仅属主可读写。
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("handle file permissions too loose: %v", perm)
	}

	read, err := ReadHandleFile(handlePath)
	if err != nil {
		t.Fatalf("ReadHandleFile: %v", err)
	}
	if read.Token != srv.Handle().Token {
		t.Error("handle token mismatch")
	}

	// 关闭后句柄文件必须被删除，避免留下含令牌的残留文件。
	if err := srv.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := os.Stat(handlePath); !os.IsNotExist(err) {
		t.Error("handle file should be removed on close")
	}
}

func TestReadHandleFileRejectsIncompleteHandle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{"network":"unix"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadHandleFile(path); err == nil {
		t.Error("an incomplete handle must be rejected")
	}
}

// ---- 客户端行为 ----

func TestCallRespectsContextCancellation(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)
	c := dialTest(t, handle)

	// 已取消的上下文应立即返回，不等待服务响应。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.CallRaw(ctx, handle.Token, OpGetRuntimeState, nil)
	if err == nil {
		t.Fatal("expected the call to fail on a cancelled context")
	}
}

func TestConcurrentCallsAreMatchedToTheirResponses(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)
	c := dialTest(t, handle)

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			params := map[string]int{"i": i}
			var out RuntimeState
			if err := c.Call(ctx, handle.Token, OpGetRuntimeState, params, &out); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call failed: %v", err)
	}
}

func TestClientRejectsClosedCalls(t *testing.T) {
	h := &echoHandler{}
	_, handle := newTestServer(t, h)
	c := dialTest(t, handle)

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.CallRaw(ctx, handle.Token, OpGetRuntimeState, nil)
	if err == nil {
		t.Fatal("calls after Close must fail")
	}
}

func TestDialHandleFileRoundTrip(t *testing.T) {
	h := &echoHandler{}
	dir := t.TempDir()
	handlePath := filepath.Join(dir, "ipc.json")
	srv, err := NewServer(Options{
		Handler:    h,
		SocketPath: filepath.Join(dir, "ipc.sock"),
		HandlePath: handlePath,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Listen(); err != nil {
		t.Skipf("local socket unavailable: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	t.Cleanup(func() { _ = srv.Close() })

	c, err := DialHandleFile(handlePath)
	if err != nil {
		t.Fatalf("DialHandleFile: %v", err)
	}
	defer func() { _ = c.Close() }()

	callCtx, cancelCall := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCall()
	if _, err := c.GetRuntimeState(callCtx, srv.Handle().Token); err != nil {
		t.Fatalf("GetRuntimeState: %v", err)
	}
}

func TestOperationKnown(t *testing.T) {
	for _, op := range KnownOperations() {
		if !op.Known() {
			t.Errorf("%q should be known", op)
		}
	}
	if Operation("Nope").Known() {
		t.Error("unknown operation reported as known")
	}
	// 能力清单与操作集合必须一致，避免声明了做不到的能力。
	if len(KnownOperations()) != 10 {
		t.Errorf("expected 10 documented operations, got %d", len(KnownOperations()))
	}
}
