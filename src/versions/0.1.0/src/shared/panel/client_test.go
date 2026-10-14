package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newStub 启动一个返回固定状态码与响应体的测试服务器。
func newStub(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginSuccessParsesBothTokens(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		defer func() { _ = r.Body.Close() }()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","message":"","data":{"auth_data":"Bearer tok123","token":"sub456","is_admin":false},"error":null}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	s, perr := c.Login(context.Background(), Credentials{Email: "a@b.com", Password: "pw"})
	if perr != nil {
		t.Fatalf("Login: %v", perr)
	}

	// 契约：端点与方法是固定事实（panel-api-contract.md 3.1）。
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != pathLogin {
		t.Errorf("path = %q, want %q", gotPath, pathLogin)
	}
	if gotBody["email"] != "a@b.com" || gotBody["password"] != "pw" {
		t.Errorf("body = %v, want email/password", gotBody)
	}
	// 契约：auth_data 与 token 语义不同，必须分别落在两个字段。
	if s.SessionToken != "Bearer tok123" {
		t.Errorf("SessionToken = %q, want %q", s.SessionToken, "Bearer tok123")
	}
	if s.SubscriptionToken != "sub456" {
		t.Errorf("SubscriptionToken = %q, want %q", s.SubscriptionToken, "sub456")
	}
}

func TestLoginRejectsEmptyCredentials(t *testing.T) {
	c, err := NewClient("https://panel.example.com", false)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, perr := c.Login(context.Background(), Credentials{}); perr == nil {
		t.Fatal("expected error for empty credentials")
	} else if perr.Kind != KindInvalidCredentials {
		t.Errorf("kind = %q, want %q", perr.Kind, KindInvalidCredentials)
	}
}

func TestLoginBadCredentialsClassified(t *testing.T) {
	// Xboard 对错误凭据返回业务 code 400（HTTP 取前三位）。
	srv := newStub(t, http.StatusBadRequest, `{"status":"fail","message":"邮箱或密码错误","data":null,"error":null}`)
	c, err := NewClient(srv.URL, true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, perr := c.Login(context.Background(), Credentials{Email: "a@b.com", Password: "bad"})
	if perr == nil {
		t.Fatal("expected error")
	}
	if perr.Kind != KindInvalidCredentials {
		t.Errorf("kind = %q, want %q", perr.Kind, KindInvalidCredentials)
	}
	if perr.Retryable {
		t.Error("credential failure must not be retryable")
	}
	// 服务端文案只保留在 ServerMessage，不作为程序判断依据。
	if perr.ServerMessage == "" {
		t.Error("ServerMessage should preserve the server text for diagnostics")
	}
}

func TestGetSubscriptionDescriptorUsesSessionVerbatim(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","message":"","data":{"subscribe_url":"https://p.example.com/api/v1/client/subscribe?token=x&flag=sing-box","token":"x","uuid":"u-1","email":"a@b.com","u":10,"d":20,"transfer_enable":1000},"error":null}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	d, perr := c.GetSubscriptionDescriptor(context.Background(), "Bearer session-1")
	if perr != nil {
		t.Fatalf("GetSubscriptionDescriptor: %v", perr)
	}
	// 契约：Authorization 头原样传递，不重新拼接前缀。
	if gotAuth != "Bearer session-1" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer session-1")
	}
	// 契约：subscribe_url 直接使用，不由客户端拼接。
	want := "https://p.example.com/api/v1/client/subscribe?token=x&flag=sing-box"
	if d.SubscribeURL != want {
		t.Errorf("SubscribeURL = %q, want %q", d.SubscribeURL, want)
	}
	if got := d.UsedBytes(); got != 30 {
		t.Errorf("UsedBytes = %d, want 30", got)
	}
}

func TestSessionWithoutBearerPrefixRejected(t *testing.T) {
	c, err := NewClient("https://panel.example.com", false)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// 传入裸 token 而不是完整的 "Bearer ..."：必须显式报错，不得静默拼双前缀。
	_, perr := c.GetAccount(context.Background(), "raw-token")
	if perr == nil {
		t.Fatal("expected error")
	}
	if perr.Kind != KindUnauthorized {
		t.Errorf("kind = %q, want %q", perr.Kind, KindUnauthorized)
	}
}

func TestGetAccountParsesQuotaSemantics(t *testing.T) {
	srv := newStub(t, http.StatusOK, `{"status":"success","message":"","data":{"email":"a@b.com","uuid":"u-1","transfer_enable":1000,"u":400,"d":100,"expired_at":0,"banned":0,"plan_id":7,"balance":1.5,"avatar_url":"https://cdn.example.com/x"},"error":null}`)
	c, err := NewClient(srv.URL, true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	a, perr := c.GetAccount(context.Background(), "Bearer s")
	if perr != nil {
		t.Fatalf("GetAccount: %v", perr)
	}
	if a.UsedBytes() != 500 {
		t.Errorf("UsedBytes = %d, want 500", a.UsedBytes())
	}
	if a.RemainingBytes() != 500 {
		t.Errorf("RemainingBytes = %d, want 500", a.RemainingBytes())
	}
	// 契约：expired_at = 0 表示长期有效，不是 1970 年过期。
	if a.IsExpired(1_700_000_000) {
		t.Error("expired_at=0 must mean never expires")
	}
	if a.IsBanned() {
		t.Error("banned=0 must mean not banned")
	}
}

func TestUnauthorizedAndRateLimitedClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   ErrorKind
	}{
		{"unauthorized", http.StatusUnauthorized, KindUnauthorized},
		{"forbidden", http.StatusForbidden, KindForbidden},
		{"rate limited", http.StatusTooManyRequests, KindRateLimited},
		{"server error", http.StatusInternalServerError, KindServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newStub(t, tc.status, `{"status":"fail","message":"x","data":null,"error":null}`)
			c, err := NewClient(srv.URL, true)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			_, perr := c.GetAccount(context.Background(), "Bearer s")
			if perr == nil {
				t.Fatal("expected error")
			}
			if perr.Kind != tc.want {
				t.Errorf("kind = %q, want %q", perr.Kind, tc.want)
			}
		})
	}
}

func TestNonJSONResponseMeansNotXboard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>login page</html>"))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, perr := c.DiscoverPanel(context.Background())
	if perr == nil {
		t.Fatal("expected error")
	}
	if perr.Kind != KindNotXboard {
		t.Errorf("kind = %q, want %q", perr.Kind, KindNotXboard)
	}
}

func TestUnexpectedEnvelopeStatusMeansNotXboard(t *testing.T) {
	srv := newStub(t, http.StatusOK, `{"status":"weird","message":"","data":{},"error":null}`)
	c, err := NewClient(srv.URL, true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, perr := c.DiscoverPanel(context.Background())
	if perr == nil {
		t.Fatal("expected error")
	}
	if perr.Kind != KindNotXboard {
		t.Errorf("kind = %q, want %q", perr.Kind, KindNotXboard)
	}
}

func TestDiscoverPanelToleratesUnknownFields(t *testing.T) {
	// 契约：guest config 的字段可被插件增删，未知字段不得导致失败。
	srv := newStub(t, http.StatusOK, `{"status":"success","message":"","data":{"unknown_future_field":123,"nested":{"a":[1,2]}},"error":null}`)
	c, err := NewClient(srv.URL, true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	id, perr := c.DiscoverPanel(context.Background())
	if perr != nil {
		t.Fatalf("DiscoverPanel: %v", perr)
	}
	if v, ok := id.Raw["unknown_future_field"]; !ok || v == nil {
		t.Errorf("unknown field should be preserved, got %v", id.Raw)
	}
}

func TestNewClientRejectsUnsafeBaseURLs(t *testing.T) {
	cases := []struct {
		name              string
		url               string
		allowInsecureHTTP bool
		wantErr           bool
	}{
		{"https ok", "https://panel.example.com", false, false},
		{"http rejected by default", "http://panel.example.com", false, true},
		{"http allowed when opted in", "http://panel.example.com", true, false},
		{"credentials in url rejected", "https://user:pass@panel.example.com", false, true},
		{"relative rejected", "/panel", false, true},
		{"empty rejected", "", false, true},
		{"unsupported scheme rejected", "ftp://panel.example.com", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient(tc.url, tc.allowInsecureHTTP)
			if tc.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestCustomHeadersRejectCredentials(t *testing.T) {
	base := "https://panel.example.com"
	for _, h := range []string{"Authorization", "authorization", "Cookie", "Proxy-Authorization"} {
		if _, err := NewClient(base, false, WithCustomHeaders(map[string]string{h: "x"})); err == nil {
			t.Errorf("header %q must be rejected", h)
		}
	}
	// 公共非敏感头应被接受。
	if _, err := NewClient(base, false, WithCustomHeaders(map[string]string{"X-Client-Id": "proxyclient"})); err != nil {
		t.Errorf("public header rejected: %v", err)
	}
}

func TestBaseURLPrefixIsPreserved(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","message":"","data":{},"error":null}`))
	}))
	defer srv.Close()

	// 用户在地址中填写了路径前缀时应保留。
	c, err := NewClient(srv.URL+"/panel/", true)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, perr := c.DiscoverPanel(context.Background()); perr != nil {
		t.Fatalf("DiscoverPanel: %v", perr)
	}
	want := "/panel" + pathGuestConfig
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}
