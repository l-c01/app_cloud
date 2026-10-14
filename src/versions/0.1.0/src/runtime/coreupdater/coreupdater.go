// Package coreupdater 实现核心二进制的安装、校验、替换与回滚。
//
// 对应决策 ADR-0003：本项目**不编译核心**，使用官方预构建二进制，
// 由客户端内置的更新能力管理。关键约束：
//
//  1. 只按**锁定清单**升级，绝不追踪 latest——否则不可复现且不可审计；
//  2. SHA256 校验是强制的，缺失或不匹配一律拒绝安装；
//  3. 先下载到临时文件并校验，再**原子替换**，避免半个文件变成可执行文件；
//  4. 保留上一版本用于回滚，更新失败不影响现有可用状态；
//  5. 不执行下载内容中的任何脚本，不把二进制放到可被当前目录劫持的位置。
package coreupdater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Core 标识被管理的核心，取值与 compiler.Core 一致，但此处不依赖该包以避免耦合。
type Core string

const (
	CoreXray    Core = "xray"
	CoreSingBox Core = "sing-box"
)

// Valid 判断核心标识是否受支持。
func (c Core) Valid() bool { return c == CoreXray || c == CoreSingBox }

// 文件名约定（在安装目录内）。
const (
	// previousSuffix 是回滚用的上一版本后缀。
	previousSuffix = ".previous"
	// downloadSuffix 是下载中的临时文件后缀。
	downloadSuffix = ".download"
)

// maxAssetBytes 限制单个核心产物大小，防止异常响应耗尽磁盘。
const maxAssetBytes = 256 << 20 // 256 MiB

// Asset 描述一个可下载的核心产物。
type Asset struct {
	// Version 是核心版本号（例如 "25.9.11" 或 "1.12.0"）。
	Version string
	// OS 与 Arch 使用 Go 的 runtime 命名（windows/linux、amd64/386/arm64）。
	OS   string
	Arch string
	// URL 是官方 Release 的下载地址。
	URL string
	// SHA256 是产物的小写十六进制摘要。**不得为空**。
	SHA256 string
	// Size 是预期字节数；为 0 时不校验大小。
	Size int64
}

// Matches 判断产物是否匹配当前运行平台。
func (a Asset) Matches(goos, goarch string) bool {
	return a.OS == goos && a.Arch == goarch
}

// Validate 校验产物元数据是否完整。
func (a Asset) Validate() error {
	if strings.TrimSpace(a.Version) == "" {
		return errors.New("coreupdater: asset version is empty")
	}
	if strings.TrimSpace(a.URL) == "" {
		return errors.New("coreupdater: asset URL is empty")
	}
	if !isHexSHA256(a.SHA256) {
		// 这是最关键的一条：没有可用摘要就不允许下载。
		return fmt.Errorf("coreupdater: asset %s@%s has no valid sha256", a.OS, a.Version)
	}
	if strings.TrimSpace(a.OS) == "" || strings.TrimSpace(a.Arch) == "" {
		return errors.New("coreupdater: asset platform is incomplete")
	}
	return nil
}

// Release 是某个核心的一个版本及其各平台产物。
type Release struct {
	Version string
	Assets  []Asset
}

// AssetFor 返回匹配指定平台的产物。
func (r Release) AssetFor(goos, goarch string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Matches(goos, goarch) {
			return a, true
		}
	}
	return Asset{}, false
}

// Manifest 是受管理的核心版本清单。
//
// 它对应 deploy/targets/versions.lock.json 中的锁定信息；
// 由构建阶段生成或随应用分发，运行时不从网络获取"最新版本列表"。
type Manifest struct {
	Releases map[Core]Release
}

// Validate 校验清单中每个核心都至少有一个带摘要的产物。
func (m Manifest) Validate() error {
	if len(m.Releases) == 0 {
		return errors.New("coreupdater: manifest is empty")
	}
	for core, rel := range m.Releases {
		if !core.Valid() {
			return fmt.Errorf("coreupdater: manifest contains unknown core %q", core)
		}
		if len(rel.Assets) == 0 {
			return fmt.Errorf("coreupdater: manifest for %s has no assets", core)
		}
		for _, a := range rel.Assets {
			if err := a.Validate(); err != nil {
				return fmt.Errorf("coreupdater: %s: %w", core, err)
			}
		}
	}
	return nil
}

// Installer 负责把核心安装到指定目录。
type Installer struct {
	// Dir 是核心的安装目录（例如 <appdata>/cores）。
	Dir string
	// HTTPClient 用于下载；为 nil 时使用带超时的默认客户端。
	HTTPClient *http.Client
	// GOOS / GOARCH 允许覆盖平台，便于测试；留空使用 runtime 值。
	GOOS   string
	GOARCH string
}

// platform 返回生效的平台标识。
func (i *Installer) platform() (string, string) {
	goos, goarch := i.GOOS, i.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

// client 返回生效的 HTTP 客户端。
func (i *Installer) client() *http.Client {
	if i.HTTPClient != nil {
		return i.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// executableName 返回核心在目标平台上的文件名。
func executableName(core Core, goos string) string {
	if goos == "windows" {
		return string(core) + ".exe"
	}
	return string(core)
}

// Path 返回核心二进制的完整路径。
func (i *Installer) Path(core Core) (string, error) {
	if !core.Valid() {
		return "", fmt.Errorf("coreupdater: unknown core %q", core)
	}
	if strings.TrimSpace(i.Dir) == "" {
		return "", errors.New("coreupdater: install directory is empty")
	}
	goos, _ := i.platform()
	return filepath.Join(i.Dir, executableName(core, goos)), nil
}

// Installed 报告核心是否已经存在于安装目录。
func (i *Installer) Installed(core Core) (bool, error) {
	p, err := i.Path(core)
	if err != nil {
		return false, err
	}
	info, statErr := os.Stat(p)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, statErr
	}
	return !info.IsDir(), nil
}

// ErrNoMatchingAsset 表示清单里没有匹配当前平台的产物。
var ErrNoMatchingAsset = errors.New("coreupdater: no asset matches this platform")

// Install 从清单中选择匹配当前平台的产物并安装。
//
// 流程：定位产物 → 下载到临时文件 → 校验 SHA256 → 备份现有文件 → 原子替换。
// 任一步失败都不会破坏现有可用版本。
func (i *Installer) Install(ctx context.Context, manifest Manifest, core Core) (Asset, error) {
	if !core.Valid() {
		return Asset{}, fmt.Errorf("coreupdater: unknown core %q", core)
	}
	if err := manifest.Validate(); err != nil {
		return Asset{}, err
	}

	release, ok := manifest.Releases[core]
	if !ok {
		return Asset{}, fmt.Errorf("coreupdater: manifest has no entry for %s", core)
	}

	goos, goarch := i.platform()
	asset, ok := release.AssetFor(goos, goarch)
	if !ok {
		return Asset{}, fmt.Errorf("%w: core=%s os=%s arch=%s", ErrNoMatchingAsset, core, goos, goarch)
	}

	if err := i.installAsset(ctx, core, asset); err != nil {
		// 统一包一层：调用方不必区分是下载、校验还是替换失败，
		// 界面显示的键都是「内核安装失败」，细节在 Cause 里保留。
		var ie *InstallError
		if errors.As(err, &ie) {
			return Asset{}, err
		}
		return Asset{}, &InstallError{Core: core, Version: asset.Version, Cause: err}
	}
	return asset, nil
}

// InstallError 表示内核安装失败（下载、校验或替换任一环节）。
//
// 本地化约定：界面用 coreupdater.install_failed 提示，
// 具体的失败环节由 Cause 提供，用于日志与诊断。
type InstallError struct {
	Core    Core
	Version string
	Cause   error
}

func (e *InstallError) Error() string {
	return fmt.Sprintf("coreupdater: install %s@%s: %v", e.Core, e.Version, e.Cause)
}

// Unwrap 保留底层错误，便于 errors.Is 判断具体原因
// （例如 ErrChecksumMismatch）。
func (e *InstallError) Unwrap() error { return e.Cause }

// installAsset 执行实际的下载与替换。
func (i *Installer) installAsset(ctx context.Context, core Core, asset Asset) error {
	dest, err := i.Path(core)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("coreupdater: create install dir: %w", err)
	}

	// 临时文件必须与目标同目录，保证 rename 是同一文件系统内的原子操作。
	tmp := dest + downloadSuffix
	defer func() { _ = os.Remove(tmp) }()

	if err := i.download(ctx, asset, tmp); err != nil {
		return err
	}

	// 备份现有版本，供回滚使用。
	if _, statErr := os.Stat(dest); statErr == nil {
		if err := os.Rename(dest, dest+previousSuffix); err != nil {
			return fmt.Errorf("coreupdater: keep previous version: %w", err)
		}
	}

	if err := os.Rename(tmp, dest); err != nil {
		// 替换失败时尽量把旧版本放回去，避免留下"没有核心"的状态。
		if _, backErr := os.Stat(dest + previousSuffix); backErr == nil {
			_ = os.Rename(dest+previousSuffix, dest)
		}
		return fmt.Errorf("coreupdater: activate new version: %w", err)
	}

	// 在类 Unix 系统上确保可执行位。
	if r, _ := i.platform(); r != "windows" {
		if err := os.Chmod(dest, 0o755); err != nil {
			return fmt.Errorf("coreupdater: set executable bit: %w", err)
		}
	}
	return nil
}

// download 下载并校验产物，写入 path。
func (i *Installer) download(ctx context.Context, asset Asset, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return fmt.Errorf("coreupdater: build download request: %w", err)
	}

	resp, err := i.client().Do(req)
	if err != nil {
		return fmt.Errorf("coreupdater: download failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("coreupdater: download returned http %d", resp.StatusCode)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("coreupdater: create temp file: %w", err)
	}

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxAssetBytes+1)

	written, copyErr := io.Copy(io.MultiWriter(f, hasher), limited)
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("coreupdater: write download: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("coreupdater: close download: %w", closeErr)
	}
	if written > maxAssetBytes {
		return fmt.Errorf("coreupdater: asset exceeds the %d byte limit", int64(maxAssetBytes))
	}
	if asset.Size > 0 && written != asset.Size {
		return fmt.Errorf("coreupdater: size mismatch: got %d, want %d", written, asset.Size)
	}

	// SHA256 校验：不匹配一律拒绝安装，且不保留半成品。
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, asset.SHA256) {
		// 用哨兵错误包裹：界面需要单独提示「下载可能被篡改或损坏」，
		// 这与其它安装失败的含义完全不同。
		return fmt.Errorf("%w: got %s, want %s", ErrChecksumMismatch, got, asset.SHA256)
	}
	return nil
}

// Rollback 把核心回退到上一版本。
//
// 若不存在备份，返回 ErrNoRollbackTarget。
func (i *Installer) Rollback(core Core) error {
	dest, err := i.Path(core)
	if err != nil {
		return err
	}
	prev := dest + previousSuffix

	if _, statErr := os.Stat(prev); statErr != nil {
		if os.IsNotExist(statErr) {
			return ErrNoRollbackTarget
		}
		return statErr
	}

	if _, statErr := os.Stat(dest); statErr == nil {
		// 当前版本改名为 .failed 而不是删除，便于事后诊断。
		_ = os.Remove(dest + ".failed")
		if err := os.Rename(dest, dest+".failed"); err != nil {
			return fmt.Errorf("coreupdater: set aside current version: %w", err)
		}
	}

	if err := os.Rename(prev, dest); err != nil {
		return fmt.Errorf("coreupdater: restore previous version: %w", err)
	}
	return nil
}

// ErrNoRollbackTarget 表示没有可回滚的上一版本。
var ErrNoRollbackTarget = errors.New("coreupdater: no previous version to roll back to")

// ErrChecksumMismatch 表示下载内容的 SHA256 与清单不符。
//
// 单列一类的原因：界面必须把「下载损坏或被篡改」与其它安装失败区分开——
// 前者是安全事件，用户需要知道，而不是看到笼统的「安装失败」。
var ErrChecksumMismatch = errors.New("coreupdater: download checksum mismatch")

// MessageKeyFor 把本包的错误映射到界面文案键。
//
// 存在理由：本包对外的错误既有哨兵值，也有具名类型，
// 界面不该逐个做类型断言。返回空串表示不是本包的可本地化错误。
func MessageKeyFor(err error) string {
	if err == nil {
		return ""
	}
	// 校验失败优先：它比外层的「安装失败」更具体，也更需要用户注意。
	if errors.Is(err, ErrChecksumMismatch) {
		return "coreupdater.sha256_mismatch"
	}
	if errors.Is(err, ErrNoMatchingAsset) {
		return "coreupdater.no_matching_asset"
	}
	if errors.Is(err, ErrNoRollbackTarget) {
		return "coreupdater.no_rollback_target"
	}
	var ie *InstallError
	if errors.As(err, &ie) {
		return "coreupdater.install_failed"
	}
	return ""
}

// HasRollback 报告是否存在可回滚的版本。
func (i *Installer) HasRollback(core Core) bool {
	dest, err := i.Path(core)
	if err != nil {
		return false
	}
	_, statErr := os.Stat(dest + previousSuffix)
	return statErr == nil
}

// versionPattern 匹配版本号，例如 "Xray 25.9.11" 或 "sing-box version 1.12.0"。
var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// ProbeVersion 运行核心的版本命令并解析出版本号。
//
// Xray 使用 `xray version`，sing-box 使用 `sing-box version`，两者输出格式不同，
// 因此这里只做宽松提取：取输出中第一个形如 x.y 或 x.y.z 的片段。
// 解析失败时返回空字符串与错误，调用方应把它显示为"版本未知"，而不是猜测。
func ProbeVersion(ctx context.Context, core Core, binaryPath string) (string, error) {
	if !core.Valid() {
		return "", fmt.Errorf("coreupdater: unknown core %q", core)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "version")
	// 只取标准输出的前若干字节，避免异常二进制产生海量输出。
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("coreupdater: probe %s version: %w", core, err)
	}
	if len(out) > 64<<10 {
		out = out[:64<<10]
	}

	match := versionPattern.Find(out)
	if match == nil {
		return "", fmt.Errorf("coreupdater: cannot parse %s version from its output", core)
	}
	return string(match), nil
}

// isHexSHA256 判断字符串是否为 64 位十六进制摘要。
func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
