package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"proxyclient/shared/domain"
)

// 本文件是**真实流量贯通测试** / real traffic-flow test.
//
// 它回答一个此前没有答案的问题：我们生成的配置，除了能被核心接受之外，
// **流量真的能穿过它吗？**
//
// 做法：把真实的核心同时当服务端与客户端跑起来。
//
//	本地 HTTP 服务   ← 目标
//	sing-box 服务端  ← 手写的服务端配置（shadowsocks 入站）
//	sing-box 客户端  ← **我们编译器生成的配置**（shadowsocks 出站）
//
// 然后用 HTTP 代理去请求本地目标，断言拿到正确响应。
// 这样验证的是完整的链路：入站 → 路由 → 出站 → 服务端 → 目标，
// 而中间真正跑的是我们生成的配置。
//
// 为什么需要它：`sing-box check` 只证明**字段名对**。字段名对而
// 语义错（例如把 HTTP 入站的类型写成了 socks，或者路由兜底指错）
// 同样会让配置通过校验，但流量走不通。官方的 check 不可能覆盖这一点。
//
// 需要真实二进制，因此默认跳过：
//
//	$env:SINGBOX_BIN = '<sing-box 路径>'
//	go test ./shared/compiler/ -run TestRealTraffic
func TestRealTrafficFlowsThroughGeneratedConfig(t *testing.T) {
	bin := realSingBoxPath(t)

	// ---- 1) 本地目标服务（普通 HTTP，直连即可到达）----
	target := startLocalHTTPServer(t, "proxyclient-traffic-ok")
	targetAddr := target.Addr().String()

	// ---- 2) 找一个空闲端口给 sing-box 服务端与客户端入站 ----
	serverPort := freePort(t)
	clientPort := freePort(t)

	// ---- 3) 手写服务端配置：shadowsocks 入站 ----
	//
	// 服务端只负责「把收到的流量转出去」，因此出站就是 direct。
	// 用 shadowsocks 而不是 TLS 系协议：本地自签名证书会让测试
	// 变成在测证书而不是在测链路。
	const ssPassword = "traffic-test-password"
	serverCfg := map[string]any{
		"log": map[string]any{"level": "info"},
		"inbounds": []any{
			map[string]any{
				"type":        "shadowsocks",
				"tag":         "ss-in",
				"listen":      "127.0.0.1",
				"listen_port": serverPort,
				"method":      "aes-256-gcm",
				"password":    ssPassword,
			},
		},
		"outbounds": []any{
			map[string]any{"type": "direct", "tag": "direct"},
		},
	}

	serverDir := t.TempDir()
	serverPath := filepath.Join(serverDir, "server.json")
	writeJSON(t, serverPath, serverCfg)
	assertCoreAccepts(t, bin, serverPath)

	// ---- 4) 客户端配置：**由我们的编译器生成** ----
	node := &domain.Node{
		Name:     "local-ss",
		Protocol: domain.ProtocolShadowsocks,
		Server:   "127.0.0.1",
		Port:     serverPort,
		Method:   "aes-256-gcm",
		Password: ssPassword,
	}
	node.ComputeID()

	// 全局代理模式：本次要验证的是「流量能不能过去」，
	// 而规则模式会先把流量按规则分类，把问题引到规则上。
	opts := domain.DefaultOptions()
	req := &Request{
		Core:   CoreSingBox,
		Nodes:  []*domain.Node{node},
		Policy: PolicyGlobal,
		Inbound: InboundOptions{
			Listen:    "127.0.0.1",
			HTTPPort:  clientPort,
			SocksPort: freePort(t),
		},
		LogLevel:       "info",
		SelectedNodeID: node.ID,
		Options:        &opts,
	}
	out, _, err := CompileSingBox(req)
	if err != nil {
		t.Fatalf("CompileSingBox: %v", err)
	}

	clientDir := t.TempDir()
	clientPath := filepath.Join(clientDir, "client.json")
	if err := os.WriteFile(clientPath, out, 0o600); err != nil {
		t.Fatalf("写客户端配置: %v", err)
	}
	assertCoreAccepts(t, bin, clientPath)

	// ---- 5) 跑起来 ----
	serverLog := startCore(t, bin, serverPath)
	clientLog := startCore(t, bin, clientPath)

	// ---- 6) 等客户端入站可用 ----
	proxyAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(clientPort))
	waitForTCP(t, proxyAddr, clientLog)

	// ---- 7) 通过代理请求本地目标 ----
	//
	// 目标地址写成**域名以外的形式**是不行的：这里用 127.0.0.1:port，
	// 而 sing-box 会把它当 IP 直连出去（服务端再连目标）。
	proxyURL := "http://" + proxyAddr
	targetURL := fmt.Sprintf("http://%s/", targetAddr)

	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) { return url.Parse(proxyURL) },
		},
	}
	resp, err := client.Get(targetURL)
	if err != nil {
		t.Fatalf("通过代理请求失败：%v\n--- 客户端日志 ---\n%s\n--- 服务端日志 ---\n%s",
			err, clientLog.String(), serverLog.String())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200；响应体 %q", resp.StatusCode, body)
	}
	if got := string(body); got != "proxyclient-traffic-ok" {
		t.Errorf("响应体 = %q，期望 %q", got, "proxyclient-traffic-ok")
	}

	// ---- 8) 断言流量**确实经过了服务端** ----
	//
	// 只看「拿到了正确响应」是不够的：客户端的 direct 兜底也能让请求成功，
	// 那样这条测试会在「代理根本没生效」时依然变绿。
	// 服务端日志里出现该出站标签，才证明请求真的走了我们生成的出站。
	//
	// 等待日志刷新：核心是异步写日志的，读到旧内容会让测试变得不稳定。
	if !waitForSubstring(serverLog, "ss-in", 5*time.Second) {
		t.Errorf("服务端日志里没有出现 ss-in 的入站记录：请求可能没有真的走代理。\n"+
			"--- 服务端日志 ---\n%s", serverLog.String())
	}

	// 目标服务也应当确实收到过一次请求。
	if n := targetHits(target); n == 0 {
		t.Errorf("目标服务没有收到请求")
	}
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// sharedLog 是并发安全的日志缓冲区。
//
// 核心在后台线程写日志，与测试线程并发，因此必须加锁——
// 不加锁会让 `go test -race` 报错，而那种报错看起来像核心的问题。
type sharedLog struct {
	mu  sync.Mutex
	buf []byte
}

func newSharedLog() *sharedLog { return &sharedLog{} }

func (l *sharedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	return len(p), nil
}

func (l *sharedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("写配置 %s: %v", path, err)
	}
}

// startCore 启动一个核心进程，输出收进内存供失败时打印。
//
// 进程由 t.Cleanup 结束，避免测试失败时留下后台进程。
func startCore(t *testing.T, bin, cfgPath string) *sharedLog {
	t.Helper()
	log := newSharedLog()
	cmd := exec.Command(bin, "run", "-c", cfgPath)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动核心失败：%v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	return log
}

// assertCoreAccepts 先确认核心接受配置，让后续失败更容易定位。
func assertCoreAccepts(t *testing.T, bin, cfgPath string) {
	t.Helper()
	cmd := exec.Command(bin, "check", "-c", cfgPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("核心拒绝了配置 %s：%v", cfgPath, err)
	}
}

// freePort 取一个当前空闲的端口。
//
// 存在「取了之后、用之前被别人占掉」的竞态，但在这个测试里可以接受：
// 真被占掉时核心会启动失败，错误信息足够明确。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitForTCP 等到某个地址可以连接为止。
func waitForTCP(t *testing.T, addr string, log *sharedLog) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("等待 %s 可用超时\n--- 核心日志 ---\n%s", addr, log.String())
}

func waitForSubstring(log *sharedLog, want string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(log.String(), want) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return strings.Contains(log.String(), want)
}

// startLocalHTTPServer 起一个本地 HTTP 服务，记录被访问次数。
func startLocalHTTPServer(t *testing.T, body string) *countingServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起本地服务: %v", err)
	}
	cs := &countingServer{listener: ln}
	mux := &countingHandler{body: body, hits: &cs.hits}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return cs
}

type countingServer struct {
	listener net.Listener
	// hits 由 HTTP 处理协程写、测试线程读，用原子计数器避免数据竞争。
	hits atomic.Int64
}

func (s *countingServer) Addr() net.Addr { return s.listener.Addr() }

func targetHits(s *countingServer) int64 { return s.hits.Load() }

type countingHandler struct {
	body string
	hits *atomic.Int64
}

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.hits.Add(1)
	_, _ = io.WriteString(w, h.body)
}
