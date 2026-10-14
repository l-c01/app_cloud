package coreupdater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBinary 返回一段可校验的假二进制内容。
func fakeBinary(tag string) []byte {
	return []byte("#!/bin/sh\necho " + tag + "\n")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newAssetServer 提供下载端点，并返回产物元数据。
func newAssetServer(t *testing.T, content []byte) (*httptest.Server, Asset) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/core" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	t.Cleanup(srv.Close)

	return srv, Asset{
		Version: "1.2.3",
		OS:      "linux",
		Arch:    "amd64",
		URL:     srv.URL + "/core",
		SHA256:  sha256Hex(content),
		Size:    int64(len(content)),
	}
}

func newInstaller(t *testing.T) *Installer {
	t.Helper()
	return &Installer{
		Dir:    t.TempDir(),
		GOOS:   "linux",
		GOARCH: "amd64",
	}
}

func TestAssetValidateRequiresSHA256(t *testing.T) {
	// 契约：没有可用摘要的产物不得被安装（这是最关键的一道防线）。
	a := Asset{Version: "1.0.0", OS: "linux", Arch: "amd64", URL: "https://example.com/core"}
	if err := a.Validate(); err == nil {
		t.Fatal("asset without sha256 must be rejected")
	}

	a.SHA256 = "not-a-hash"
	if err := a.Validate(); err == nil {
		t.Fatal("malformed sha256 must be rejected")
	}

	a.SHA256 = strings.Repeat("a", 64)
	if err := a.Validate(); err != nil {
		t.Fatalf("valid asset rejected: %v", err)
	}
}

func TestManifestValidateRejectsIncompleteEntries(t *testing.T) {
	empty := Manifest{}
	if err := empty.Validate(); err == nil {
		t.Error("empty manifest must be rejected")
	}

	unknownCore := Manifest{Releases: map[Core]Release{
		Core("mihomo"): {Version: "1", Assets: []Asset{{Version: "1", OS: "linux", Arch: "amd64", URL: "u", SHA256: strings.Repeat("a", 64)}}},
	}}
	if err := unknownCore.Validate(); err == nil {
		t.Error("unknown core must be rejected")
	}

	noAssets := Manifest{Releases: map[Core]Release{CoreSingBox: {Version: "1"}}}
	if err := noAssets.Validate(); err == nil {
		t.Error("release without assets must be rejected")
	}
}

func TestInstallDownloadsVerifiesAndPlacesBinary(t *testing.T) {
	content := fakeBinary("sing-box 1.2.3")
	srv, asset := newAssetServer(t, content)
	_ = srv

	inst := newInstaller(t)
	manifest := Manifest{Releases: map[Core]Release{
		CoreSingBox: {Version: asset.Version, Assets: []Asset{asset}},
	}}

	got, err := inst.Install(context.Background(), manifest, CoreSingBox)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got.Version != "1.2.3" {
		t.Errorf("installed version = %q", got.Version)
	}

	path, _ := inst.Path(CoreSingBox)
	onDisk, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("installed binary unreadable: %v", readErr)
	}
	if string(onDisk) != string(content) {
		t.Error("installed content differs from the downloaded asset")
	}

	// 临时文件必须被清理。
	if _, statErr := os.Stat(path + downloadSuffix); statErr == nil {
		t.Error("download temp file was left behind")
	}

	ok, err := inst.Installed(CoreSingBox)
	if err != nil || !ok {
		t.Errorf("Installed = %v, %v", ok, err)
	}
}

func TestInstallRejectsSHA256MismatchAndKeepsExistingVersion(t *testing.T) {
	// 先安装一个可用版本。
	good := fakeBinary("good")
	srv, goodAsset := newAssetServer(t, good)
	inst := newInstaller(t)
	manifest := Manifest{Releases: map[Core]Release{
		CoreSingBox: {Version: goodAsset.Version, Assets: []Asset{goodAsset}},
	}}
	if _, err := inst.Install(context.Background(), manifest, CoreSingBox); err != nil {
		t.Fatalf("initial Install: %v", err)
	}
	srv.Close()

	// 再尝试安装一个摘要不匹配的"新版本"。
	bad := fakeBinary("tampered")
	badSrv, badAsset := newAssetServer(t, bad)
	defer badSrv.Close()
	badAsset.SHA256 = strings.Repeat("0", 64) // 故意写错

	tampered := Manifest{Releases: map[Core]Release{
		CoreSingBox: {Version: "9.9.9", Assets: []Asset{badAsset}},
	}}

	if _, err := inst.Install(context.Background(), tampered, CoreSingBox); err == nil {
		t.Fatal("install must fail on sha256 mismatch")
	} else if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("error should mention sha256, got: %v", err)
	}

	// 关键：现有版本必须完好，不能被半成品覆盖。
	path, _ := inst.Path(CoreSingBox)
	onDisk, _ := os.ReadFile(path)
	if string(onDisk) != string(good) {
		t.Error("existing working core was damaged by a failed update")
	}
}

func TestInstallRejectsWrongSize(t *testing.T) {
	content := fakeBinary("x")
	srv, asset := newAssetServer(t, content)
	defer srv.Close()
	asset.Size = int64(len(content)) + 100 // 声明与实际不符

	inst := newInstaller(t)
	manifest := Manifest{Releases: map[Core]Release{
		CoreSingBox: {Version: asset.Version, Assets: []Asset{asset}},
	}}
	if _, err := inst.Install(context.Background(), manifest, CoreSingBox); err == nil {
		t.Fatal("size mismatch must be rejected")
	}
}

func TestInstallReportsMissingAssetForPlatform(t *testing.T) {
	content := fakeBinary("x")
	srv, asset := newAssetServer(t, content)
	defer srv.Close()

	inst := newInstaller(t)
	inst.GOARCH = "arm64" // 清单里只有 amd64

	manifest := Manifest{Releases: map[Core]Release{
		CoreSingBox: {Version: asset.Version, Assets: []Asset{asset}},
	}}
	_, err := inst.Install(context.Background(), manifest, CoreSingBox)
	if err == nil {
		t.Fatal("expected an error for a platform with no matching asset")
	}
	if !errors.Is(err, ErrNoMatchingAsset) {
		t.Errorf("error = %v, want ErrNoMatchingAsset", err)
	}
}

func TestInstallRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	asset := Asset{
		Version: "1.0.0", OS: "linux", Arch: "amd64",
		URL: srv.URL + "/core", SHA256: strings.Repeat("a", 64),
	}
	inst := newInstaller(t)
	manifest := Manifest{Releases: map[Core]Release{
		CoreSingBox: {Version: asset.Version, Assets: []Asset{asset}},
	}}

	if _, err := inst.Install(context.Background(), manifest, CoreSingBox); err == nil {
		t.Fatal("expected failure on http 404")
	}
}

func TestRollbackRestoresPreviousVersion(t *testing.T) {
	inst := newInstaller(t)

	// 第一版。
	v1 := fakeBinary("v1")
	srv1, a1 := newAssetServer(t, v1)
	manifest1 := Manifest{Releases: map[Core]Release{
		CoreSingBox: {Version: "1.0.0", Assets: []Asset{a1}},
	}}
	if _, err := inst.Install(context.Background(), manifest1, CoreSingBox); err != nil {
		t.Fatalf("install v1: %v", err)
	}
	srv1.Close()

	// 第二版。
	v2 := fakeBinary("v2")
	srv2, a2 := newAssetServer(t, v2)
	defer srv2.Close()
	manifest2 := Manifest{Releases: map[Core]Release{
		CoreSingBox: {Version: "2.0.0", Assets: []Asset{a2}},
	}}
	if _, err := inst.Install(context.Background(), manifest2, CoreSingBox); err != nil {
		t.Fatalf("install v2: %v", err)
	}

	if !inst.HasRollback(CoreSingBox) {
		t.Fatal("a previous version should be available for rollback")
	}

	path, _ := inst.Path(CoreSingBox)
	if onDisk, _ := os.ReadFile(path); string(onDisk) != string(v2) {
		t.Fatal("expected v2 to be active after the update")
	}

	if err := inst.Rollback(CoreSingBox); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if onDisk, _ := os.ReadFile(path); string(onDisk) != string(v1) {
		t.Error("rollback did not restore v1")
	}
}

func TestRollbackWithoutTarget(t *testing.T) {
	inst := newInstaller(t)
	if err := inst.Rollback(CoreSingBox); !errors.Is(err, ErrNoRollbackTarget) {
		t.Errorf("error = %v, want ErrNoRollbackTarget", err)
	}
	if inst.HasRollback(CoreSingBox) {
		t.Error("HasRollback should be false on a fresh install dir")
	}
}

func TestPathUsesExeSuffixOnWindows(t *testing.T) {
	inst := &Installer{Dir: t.TempDir(), GOOS: "windows", GOARCH: "amd64"}
	p, err := inst.Path(CoreSingBox)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if filepath.Ext(p) != ".exe" {
		t.Errorf("windows path should end in .exe, got %q", p)
	}
	if filepath.Base(p) != "sing-box.exe" {
		t.Errorf("base = %q", filepath.Base(p))
	}
}

func TestPathRejectsUnknownCoreAndEmptyDir(t *testing.T) {
	inst := &Installer{Dir: t.TempDir()}
	if _, err := inst.Path(Core("mihomo")); err == nil {
		t.Error("unknown core must be rejected")
	}
	empty := &Installer{}
	if _, err := empty.Path(CoreSingBox); err == nil {
		t.Error("empty install dir must be rejected")
	}
}

func TestManifestAssetForMatchesPlatform(t *testing.T) {
	rel := Release{
		Version: "1.0.0",
		Assets: []Asset{
			{Version: "1.0.0", OS: "windows", Arch: "amd64", URL: "w", SHA256: strings.Repeat("a", 64)},
			{Version: "1.0.0", OS: "linux", Arch: "386", URL: "l", SHA256: strings.Repeat("b", 64)},
		},
	}

	if a, ok := rel.AssetFor("linux", "386"); !ok || a.URL != "l" {
		t.Errorf("linux/386 lookup failed: %v %v", a, ok)
	}
	if _, ok := rel.AssetFor("linux", "arm64"); ok {
		t.Error("linux/arm64 should not match")
	}
	if _, ok := rel.AssetFor("windows", "amd64"); !ok {
		t.Error("windows/amd64 should match")
	}
}
