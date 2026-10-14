# Verification

Two questions: **what has been checked**, and **what those checks do not
prove**.

The second matters more. A report that only says "everything passed" carries
no information, because it does not tell you which conclusions you *cannot*
draw from it.

> **The single most important sentence: none of the code has ever been
> compiled.**
> The development machine has no Go, C++, JDK or Android SDK toolchain.
> Every compile and every unit test happens in GitHub Actions.

---

## 1. Checks that run without a toolchain

These need no compiler, so they run both locally and in CI.

| Check | Script | Proves | Does **not** prove |
|---|---|---|---|
| Unused Go imports | `check-go-imports.ps1` | No package is imported and unused (Go refuses to compile those) | Not that it compiles: type errors and undefined identifiers are invisible to it |
| Cross-package Go symbols | `check-go-symbols.ps1` | Every `pkg.Symbol` reference points at a declaration that exists | Nothing about argument or return types |
| C++ structural consistency | `check-cpp-structure.ps1` | CMake-referenced files exist, quoted includes resolve, headers have guards, brackets balance | Nothing about types or templates; **not a compile** |
| Workflow structure | `check-workflows.ps1` | No tabs, required top-level keys, indentation outside block scalars, local reusable references exist and declare `on.workflow_call` | Not that they are valid YAML, and certainly not that they run |
| IPC contract | `check-ipc-contract.ps1` | Protocol version, operation names, error codes and wire field names are identical in Go and C++ | Not behavioural equivalence — only that the definitions match |
| Message keys | `check-i18n-keys.ps1` | Referenced keys exist in **every** catalog, with matching placeholder counts | Nothing about translation quality |
| Design JSON syntax | `validate-json.ps1` | 12 design JSON files are valid JSON | Not semantics |
| Config vs schema | `validate-client-config.ps1` | The shipped config matches its schema | Not that the values are sensible |

One principle runs through all of them: **prefer a missed problem to a
false alarm**. A false positive teaches people to ignore the output, at
which point the check has stopped working entirely.

---

## 2. Most recent local run

> Date: 2026-10-15 · Version: `v0.1.0-alpha.4`

| Check | Result |
|---|---|
| Unused Go imports | **PASS** — 38 files with imports, 0 unused |
| Cross-package Go symbols | **PASS** — 14 internal packages, 305 references all resolved |
| C++ structural consistency | **PASS** — 28 files, no structural problems |
| Workflow structure | **PASS** — 13 files, 4 reusable workflows identified |
| IPC contract | **PASS** — versions 1 / 1.0.0 match; 10 operation names; all 10 Go error codes present in C++; all 44 Go wire fields parsed by the C++ side |
| Message keys | **PASS** — 109 referenced keys present in both catalogs with matching placeholders; 0 unreferenced |
| Design JSON syntax | **PASS** — 12 files |
| Config vs schema | **PASS** — 0 problems |
| PowerShell ASCII-only | **PASS** — no non-ASCII bytes in any `.ps1` |

Size: Go 39 files / 12,300 lines · C++ 28 files / 5,609 lines ·
Kotlin 6 files / 844 lines · 163 version-controlled files.

---

## 3. Checks that can only run in CI

With no toolchain locally, the following have **never run on this machine**.
A conclusion exists only once they have run in GitHub Actions.

| Check | Where | Status |
|---|---|---|
| `gofmt` | `ci.yml` → format | **not_run** |
| `go build ./...` | `ci.yml` → go-checks-linux / windows | **not_run** |
| `go mod tidy` consistency | same | **not_run** |
| `go vet ./...` | same | **not_run** |
| `go test ./...` | same | **not_run** |
| `go test -race` | same | **not_run** |
| CMake configure and build `pcx_core` | `ci.yml` → desktop-core | **not_run** |
| C++ unit tests (`ctest`) | same | **not_run** |
| Desktop GUI build | `ci.yml` → desktop-gui | **not_run** |
| Cross-compilation for all targets | `verify-targets.yml` | **not_run** |
| Package builds | `build-*.yml` | **not_run** |
| Android APK build | `reusable-android.yml` | **not_run** |

**`not_run` is never written as "passed".** The design baseline makes this
a hard rule: an unrun check has exactly one legitimate status, and it is
"not run".

---

## 4. Verification that has not been done at all

| Not done | Consequence |
|---|---|
| Run on any real device | No runtime behaviour is confirmed |
| Connected to any real Xboard panel | Every field mapping in the panel adapter comes from reading the reference source |
| Started a real Xray / sing-box process | Command-line arguments and exit codes come from reading the core source |
| Had a core check any generated config | Whether the compiler's output is accepted is entirely unknown |
| Confirmed system network takeover on real hardware | Neither the Windows registry writes nor the GNOME writes have ever executed |
| Exercised core download and SHA256 verification | The lock file is unpinned, so that path has never run |
| Exercised Android's VpnService | No APK has been installed on a device or emulator |

In short: **this is a complete design that has not yet met reality.**

---

## 5. Known design-level risks

These are not "not done yet" — they are places where the work is finished
and may still be wrong:

| Risk | Detail |
|---|---|
| Capability tables are unvalidated | `shared/compiler` marks them `verifiedAgainst: "unverified"`; they come from reading core sources, not from testing. The UI says so. |
| Core versions are not pinned | `versions.lock.json` is a placeholder. Release builds are refused; test builds bundle no cores. |
| vcpkg first build is slow | The Windows GUI build compiles wxWidgets from source the first time and may exceed the default timeout. |
| arm64 runner availability | `ubuntu-24.04-arm` is free for public repositories only. |
| Cross-language contract checks definitions only | The two sides agree on the *definitions*; that says nothing about behaviour. |
| Single-instance locking varies by platform | `wxSingleInstanceChecker` behaviour has not been tested anywhere. |

---

## 6. Reproducing these checks

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
cd <repository root>

./tools/scripts/check-go-imports.ps1
./tools/scripts/check-go-symbols.ps1
./tools/scripts/check-cpp-structure.ps1
./tools/scripts/check-workflows.ps1
./tools/scripts/check-ipc-contract.ps1
./tools/scripts/check-i18n-keys.ps1
./tools/scripts/validate-json.ps1 -Path design
./tools/scripts/validate-client-config.ps1
```

For compilation and tests, push and read the Actions tab.

---

## 7. See also

- [Architecture](architecture.md)
- [Upload and build](upload-and-build.md)
- [design/plan/STATUS.md](../../../design/plan/STATUS.md)
- [design/plan/environment-constraints.md](../../../design/plan/environment-constraints.md)
