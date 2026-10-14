// Package journalfile 提供基于文件的恢复日志实现。
//
// 它实现 coordinator.Journal：记录「原值 / 我们写入的值 / 所有者 / 阶段」，
// 使进程被强杀后仍能在下次启动恢复网络设置。
//
// 关键要求（设计基线 §7.2）：
//   - 写入必须是**先写临时文件再改名**的原子操作，
//     否则崩溃可能留下半截 JSON，导致恢复信息不可读——那正是最需要它的时刻；
//   - 文件权限必须收紧（含网络设置的原值，可能暴露内网代理地址等信息）；
//   - 内容必须可被人类读懂，便于用户手动排障。
package journalfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"proxyclient/runtime/coordinator"
)

// 文件权限：仅属主可读写。
const filePerm = 0o600

// dirPerm 是日志目录权限。
const dirPerm = 0o700

// Store 是基于文件的恢复日志。
type Store struct {
	path string
	// now 允许测试注入时间。
	now func() time.Time
}

// New 构造恢复日志存储。
//
// path 应是应用数据目录下的一个文件路径；目录会自动创建。
func New(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("journalfile: path is required")
	}
	return &Store{path: path, now: time.Now}, nil
}

// Path 返回日志文件路径。
func (s *Store) Path() string { return s.path }

// Save 原子写入一条恢复记录。
//
// 流程：确保目录存在 → 写临时文件（0600）→ fsync → 改名覆盖。
// fsync 不能省略：否则掉电后可能得到长度正确但内容为空的文件。
func (s *Store) Save(entry coordinator.JournalEntry) error {
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = s.now()
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("journalfile: encode entry: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("journalfile: create directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".journal-*.tmp")
	if err != nil {
		return fmt.Errorf("journalfile: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// 任何失败路径都要清理临时文件，避免目录里堆积垃圾。
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(filePerm); err != nil {
		cleanup()
		return fmt.Errorf("journalfile: set temp permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("journalfile: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("journalfile: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("journalfile: close temp file: %w", err)
	}

	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("journalfile: activate journal: %w", err)
	}
	return nil
}

// Load 读取恢复记录。
//
// 文件不存在时返回 (nil, nil)：这表示「没有需要恢复的东西」，不是错误。
func (s *Store) Load() (*coordinator.JournalEntry, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("journalfile: read journal: %w", err)
	}

	// 空文件按「无记录」处理：可能是上次崩溃留下了空文件。
	if len(data) == 0 {
		return nil, nil
	}

	var entry coordinator.JournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		// 损坏的日志不能静默忽略：它意味着可能有一份网络设置没被恢复。
		// 返回错误，让上层的 Recover 流程把它暴露给用户。
		return nil, fmt.Errorf("journalfile: journal is corrupt (%s): %w", s.path, err)
	}
	return &entry, nil
}

// Clear 删除恢复记录。
//
// 文件不存在不算错误（保持幂等）。
func (s *Store) Clear() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("journalfile: clear journal: %w", err)
	}
	return nil
}

// Exists 报告是否存在恢复记录。
func (s *Store) Exists() bool {
	_, err := os.Stat(s.path)
	return err == nil
}

// Inspect 返回日志文件的原始内容，供诊断界面展示。
//
// 它不解析、不校验，只把内容原样交给用户——排障时原始文本比结构化视图更有用。
func (s *Store) Inspect() (string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("journalfile: inspect journal: %w", err)
	}
	return string(data), nil
}
