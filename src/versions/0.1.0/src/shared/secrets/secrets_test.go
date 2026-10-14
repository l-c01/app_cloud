package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactEmptyAndShort(t *testing.T) {
	if got := Redact(""); got != "" {
		t.Errorf("Redact(\"\") = %q, want empty", got)
	}
	// 短值整体替换：保留首尾会让大部分内容暴露。
	for _, v := range []string{"a", "abcdefgh", "Bearer xyz12"} {
		got := Redact(v)
		if got != strings.Repeat("*", 8) {
			t.Errorf("Redact(%q) = %q, want full mask", v, got)
		}
	}
}

func TestRedactKeepsEdgesForLongValues(t *testing.T) {
	// 契约：保留前 4 与后 4，中间等长掩码。
	value := "Bearer abcdefghijklmnop" // 24 字符
	got := Redact(value)

	if !strings.HasPrefix(got, "Bear") {
		t.Errorf("prefix lost: %q", got)
	}
	if !strings.HasSuffix(got, "mnop") {
		t.Errorf("suffix lost: %q", got)
	}
	if strings.Contains(got, "abcdefgh") {
		t.Errorf("middle should be masked: %q", got)
	}
	// 长度必须与原文一致，便于日志对齐排查。
	if len([]rune(got)) != len([]rune(value)) {
		t.Errorf("length changed: got %d, want %d", len([]rune(got)), len([]rune(value)))
	}
}

func TestRedactDoesNotSplitMultibyteRunes(t *testing.T) {
	// 按字符而非字节截断，避免产生非法 UTF-8。
	value := "订阅链接令牌内容需要足够长才能触发保留首尾的规则"
	got := Redact(value)
	if !strings.HasPrefix(got, "订阅链接") {
		t.Errorf("prefix = %q", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("redaction produced an invalid rune: %q", got)
	}
}

func TestRedactURLCanBeLoggedSafety(t *testing.T) {
	// 订阅链接含 token：脱敏后必须保留主机名以便排查，但不得保留 token。
	raw := "https://panel.example.com/api/v1/client/subscribe?token=abcdef123456"
	got := RedactURL(raw)

	if !strings.Contains(got, "panel.example.com") {
		t.Errorf("host should be preserved: %q", got)
	}
	if strings.Contains(got, "abcdef123456") {
		t.Errorf("token leaked: %q", got)
	}
	if strings.Contains(got, "subscribe") {
		t.Errorf("path should be masked: %q", got)
	}
}

func TestRedactURLHandlesOddInputs(t *testing.T) {
	if got := RedactURL(""); got != "" {
		t.Errorf("empty = %q", got)
	}
	// 没有 scheme 时退回普通脱敏。
	if got := RedactURL("abcdefghijklmnopqrst"); strings.Contains(got, "ijklmnop") {
		t.Errorf("non-URL input leaked: %q", got)
	}
	// 只有 scheme://host、没有路径时原样返回（不含秘密）。
	if got := RedactURL("https://panel.example.com"); got != "https://panel.example.com" {
		t.Errorf("host-only URL altered: %q", got)
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	s := NewMemoryStore()
	if !s.Available() {
		t.Fatal("memory store should report available")
	}
	if err := s.Set(KeyPanelSession, "Bearer x"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(KeyPanelSession)
	if err != nil || got != "Bearer x" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := s.Delete(KeyPanelSession); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(KeyPanelSession); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	// 删除不存在的键不算错误。
	if err := s.Delete(KeyPanelSession); err != nil {
		t.Errorf("delete missing key should not fail: %v", err)
	}
}

func TestUnavailableStoreRefusesInsteadOfDegradingToPlaintext(t *testing.T) {
	// 契约：平台没有安全存储时必须报错，绝不退回明文保存。
	s := NewUnavailableStore()
	if s.Available() {
		t.Fatal("should report unavailable")
	}
	if err := s.Set(KeyPanelSession, "Bearer x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Set error = %v, want ErrUnavailable", err)
	}
	if _, err := s.Get(KeyPanelSession); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get error = %v, want ErrUnavailable", err)
	}
}

func TestNamespacedStoreIsolatesServers(t *testing.T) {
	// 契约：切换服务器不能读到旧服务器的凭据。
	inner := NewMemoryStore()

	a, err := NewNamespaced(inner, "https://panel-a.example.com")
	if err != nil {
		t.Fatalf("NewNamespaced: %v", err)
	}
	b, err := NewNamespaced(inner, "https://panel-b.example.com")
	if err != nil {
		t.Fatalf("NewNamespaced: %v", err)
	}

	if err := a.Set(KeyPanelSession, "Bearer from-a"); err != nil {
		t.Fatalf("a.Set: %v", err)
	}
	if _, err := b.Get(KeyPanelSession); !errors.Is(err, ErrNotFound) {
		t.Errorf("server B must not see server A's credential, got %v", err)
	}

	got, err := a.Get(KeyPanelSession)
	if err != nil || got != "Bearer from-a" {
		t.Fatalf("a.Get = %q, %v", got, err)
	}
}

func TestNamespacedStoreClearNamespaceOnlyRemovesItsOwnKeys(t *testing.T) {
	inner := NewMemoryStore()
	a, _ := NewNamespaced(inner, "https://a.example.com")
	b, _ := NewNamespaced(inner, "https://b.example.com")

	_ = a.Set(KeyPanelSession, "A")
	_ = a.Set(KeySubscriptionToken, "A-token")
	_ = b.Set(KeyPanelSession, "B")

	if err := a.ClearNamespace(); err != nil {
		t.Fatalf("ClearNamespace: %v", err)
	}

	if _, err := a.Get(KeyPanelSession); !errors.Is(err, ErrNotFound) {
		t.Error("A's session should be cleared")
	}
	if got, err := b.Get(KeyPanelSession); err != nil || got != "B" {
		t.Errorf("B's credential must survive: %q, %v", got, err)
	}
}

func TestNewNamespacedRejectsEmptyInputs(t *testing.T) {
	if _, err := NewNamespaced(nil, "x"); err == nil {
		t.Error("nil store must be rejected")
	}
	if _, err := NewNamespaced(NewMemoryStore(), "  "); err == nil {
		t.Error("empty namespace must be rejected")
	}
}

func TestScrubberReplacesLongestFirst(t *testing.T) {
	s := NewScrubber()
	// 短字面量是长字面量的前缀：必须优先替换长的，否则会残留片段。
	s.Add("Bearer abc123")
	s.Add("Bearer abc123def456")

	text := "request failed: Bearer abc123def456 (also Bearer abc123)"
	got := s.Scrub(text)

	if strings.Contains(got, "abc123def456") {
		t.Errorf("long secret leaked: %q", got)
	}
	if strings.Contains(got, "abc123") {
		t.Errorf("short secret leaked: %q", got)
	}
}

func TestScrubberIgnoresTooShortLiterals(t *testing.T) {
	s := NewScrubber()
	// 少于 6 字符的字面量极易误匹配正常文本，应被忽略。
	s.Add("abc")
	if s.Count() != 0 {
		t.Errorf("short literal should be ignored, count = %d", s.Count())
	}
	if got := s.Scrub("abcdef"); got != "abcdef" {
		t.Errorf("text should be untouched: %q", got)
	}
}

func TestScrubberDeduplicates(t *testing.T) {
	s := NewScrubber()
	s.Add("secret-value-1")
	s.Add("secret-value-1")
	if s.Count() != 1 {
		t.Errorf("count = %d, want 1", s.Count())
	}
}

func TestScrubberFromSession(t *testing.T) {
	s := NewScrubber()
	s.FromSession("Bearer session-token", "sub-token-value", "https://p.example.com/sub?token=x", "uuid-value-123")
	if s.Count() != 4 {
		t.Errorf("count = %d, want 4", s.Count())
	}
	// 空值不应被登记。
	s2 := NewScrubber()
	s2.FromSession("", "", "", "")
	if s2.Count() != 0 {
		t.Errorf("empty values must not be registered, count = %d", s2.Count())
	}
}

func TestValidateKey(t *testing.T) {
	if err := ValidateKey(KeyPanelSession); err != nil {
		t.Errorf("known key rejected: %v", err)
	}
	if err := ValidateKey(Key("panel.made_up")); err == nil {
		t.Error("unknown key must be rejected")
	}
}
