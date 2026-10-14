package journalfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"proxyclient/runtime/coordinator"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "state", "recovery.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func sampleEntry() coordinator.JournalEntry {
	return coordinator.JournalEntry{
		Generation: 12,
		Stage:      coordinator.StatePreparingNetwork,
		Mode:       "system_proxy",
		Core:       "xray",
		ConfigPath: "/data/configs/active.json",
		Owner:      "proxyclient",
		Original: coordinator.NetworkSettings{
			"system_proxy.http":  "",
			"system_proxy.socks": "",
		},
		Applied: coordinator.NetworkSettings{
			"system_proxy.http":  "127.0.0.1:7890",
			"system_proxy.socks": "127.0.0.1:7891",
		},
		CreatedAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
	}
}

func TestLoadReturnsNilWhenNoJournalExists(t *testing.T) {
	// 「没有需要恢复的东西」不是错误。
	s := newStore(t)
	entry, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if entry != nil {
		t.Errorf("entry = %v, want nil", entry)
	}
	if s.Exists() {
		t.Error("Exists should be false")
	}
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	s := newStore(t)
	in := sampleEntry()

	if err := s.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !s.Exists() {
		t.Fatal("journal should exist after Save")
	}

	out, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out == nil {
		t.Fatal("Load returned nil")
	}
	if out.Generation != in.Generation || out.Owner != in.Owner || out.Mode != in.Mode {
		t.Errorf("identity fields differ: %+v", out)
	}
	if out.Applied["system_proxy.http"] != "127.0.0.1:7890" {
		t.Errorf("applied settings differ: %v", out.Applied)
	}
	if out.Original["system_proxy.http"] != "" {
		t.Errorf("original settings differ: %v", out.Original)
	}
	// 未提供 UpdatedAt 时应被自动补齐。
	if out.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be filled in automatically")
	}
}

func TestSaveOverwritesPreviousEntry(t *testing.T) {
	s := newStore(t)

	first := sampleEntry()
	first.Generation = 1
	if err := s.Save(first); err != nil {
		t.Fatalf("Save first: %v", err)
	}

	second := sampleEntry()
	second.Generation = 2
	if err := s.Save(second); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	out, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Generation != 2 {
		t.Errorf("generation = %d, want 2", out.Generation)
	}
}

func TestJournalFileHasRestrictivePermissions(t *testing.T) {
	// 恢复记录含网络设置原值（可能暴露内网代理地址），必须仅属主可读。
	s := newStore(t)
	if err := s.Save(sampleEntry()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("journal permissions too loose: %v", perm)
	}
}

func TestSaveLeavesNoTempFilesBehind(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 3; i++ {
		if err := s.Save(sampleEntry()); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".journal-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestClearIsIdempotent(t *testing.T) {
	s := newStore(t)
	if err := s.Clear(); err != nil {
		t.Errorf("Clear on a missing journal should not fail: %v", err)
	}

	if err := s.Save(sampleEntry()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if s.Exists() {
		t.Error("journal should be gone after Clear")
	}
	if err := s.Clear(); err != nil {
		t.Errorf("second Clear should be a no-op: %v", err)
	}
}

func TestLoadReportsCorruptJournalInsteadOfSilentlyIgnoringIt(t *testing.T) {
	// 损坏的日志意味着可能有一份网络设置没被恢复，不能当作「没有记录」处理。
	s := newStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(s.Path(), []byte(`{"generation": 3, "applied":`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := s.Load(); err == nil {
		t.Fatal("a corrupt journal must be reported")
	}
}

func TestLoadTreatsEmptyFileAsNoJournal(t *testing.T) {
	// 崩溃可能留下长度为零的文件；它等价于「没有记录」。
	s := newStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(s.Path(), nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entry, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if entry != nil {
		t.Errorf("entry = %v, want nil", entry)
	}
}

func TestInspectReturnsRawText(t *testing.T) {
	s := newStore(t)

	// 不存在时返回空串而不是错误。
	text, err := s.Inspect()
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}

	if err := s.Save(sampleEntry()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	text, err = s.Inspect()
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	// 排障时原始文本比结构化视图更有用。
	if !strings.Contains(text, "system_proxy.http") {
		t.Errorf("raw text should contain the settings: %q", text)
	}
	if !json.Valid([]byte(text)) {
		t.Error("journal content should be valid JSON")
	}
}

func TestNewRejectsEmptyPath(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("empty path must be rejected")
	}
}

func TestStoreSatisfiesCoordinatorJournalContract(t *testing.T) {
	// 编译期契约：Store 必须可直接作为 coordinator.Journal 使用。
	var _ coordinator.Journal = (*Store)(nil)
}
