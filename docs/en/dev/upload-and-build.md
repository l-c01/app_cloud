# Uploading and building

For maintainers and anyone who wants to build this from source.

**Your first build is the first time this code is compiled.** Nothing here
has been through a compiler before. Expect the first CI run to report
something; that is the process working, not the process failing.

---

## 1. Getting the source onto GitHub

### Create an empty repository

On GitHub, create a repository **without** a README, `.gitignore` or
licence: those files already exist here and pre-creating them causes a
push conflict.

### Push

The working tree is already a Git repository with tags.

```bash
git remote add origin https://github.com/<owner>/<repo>.git
git push -u origin main
git push origin --tags
```

GitHub no longer accepts password authentication for pushes. Use a
**personal access token** with `repo` scope when prompted for a password.

### What is not uploaded

`.gitignore` excludes `src/references/**` (third-party source snapshots
used for the design audit) and generated output. The reference snapshots
are recorded in `design/audit/source-register.md` and must be re-fetched
separately if you need them.

---

## 2. First CI run

A push triggers `CI checks` automatically. You can also run it manually
from the Actions tab.

It runs these jobs **in parallel**:

| Job | Checks |
|---|---|
| Static pre-checks | Unused Go imports; cross-package symbol resolution; C++ structure; workflow structure; ASCII-only PowerShell |
| Go formatting | `gofmt` |
| Go build / vet / test (Linux) | Build, `go mod tidy` consistency, `vet`, tests, race detector |
| Go build / test (Windows) | Same, on Windows (files behind `//go:build windows` are not compiled on Linux at all) |
| Desktop core and tests | CMake configure, build `pcx_core`, run `ctest` |
| Desktop GUI build (Linux) | Build the wxWidgets executable with `-Werror` |
| Design JSON, config schema and i18n | Design JSON, config against schema, i18n key consistency |

Parallel jobs are deliberate. A formatting failure is the cheapest thing
to fix; if it stopped the whole pipeline at step one, it would hide the
real compile errors. Running them side by side shows everything at once.

### The three most likely first failures

**1. `Go formatting`** — expected, and fixable in one command.

No `gofmt` exists on the development machine, so manual alignment may be
incomplete. Either download the `gofmt-fixes` artifact that job produces
and run `git apply gofmt.patch`, or run `gofmt -w .` in
`src/versions/0.1.0/src` locally and commit.

**2. Compile errors or failing tests** — read the file and line from the
log. Around 12,000 lines of Go and 5,600 lines of C++ have never been
compiled; some breakage is likely.

**3. `Design JSON, config schema and i18n`** — the i18n check names the
exact key and the language it is missing from.

---

## 3. Verifying target architectures

Actions → **Verify target architectures** → Run workflow.

This cross-compiles the shared Go module for every target platform and
architecture:

```
windows/amd64    windows/386     windows/arm64
linux/amd64      linux/386       linux/arm64
android/arm64    android/arm(v7) android/386
```

It produces a compile verdict per target; it does **not** produce
installers. This is the only manual build entry that can succeed before
the GUI and Android front ends are fully wired.

---

## 4. Building packages

Seven manual entries exist under Actions: `Build all`, `Build Windows`,
`Build Android`, `Build Linux`, `Build Docker`, `Build by architecture`,
`Build common`.

Each package contains the GUI **and** the background service. The service
is not optional: every operation that touches system networking or core
processes lives there, so a GUI-only package would open a window that can
do nothing. The build fails if the service binary is missing.

### What will and will not work

| Target | State |
|---|---|
| windows-amd64, linux-amd64 | Toolchain path is wired; requires wxWidgets via vcpkg / apt |
| windows-386, windows-arm64 | Same, different vcpkg triplet |
| linux-386 | Needs i386 multiarch packages |
| linux-arm64 | Needs an `ubuntu-24.04-arm` runner (free for public repositories only) |
| android-* | Builds an APK, but it does not proxy traffic yet |
| docker-* | **Fails by design**: the Dockerfile is deliberately absent |

Two hard rules already enforced:

- A **release** build is refused when the core versions are not pinned in
  `versions.lock.json`. Placeholder pins must never enter a release.
- An Android **release** build fails when signing secrets are missing. A
  random release key is never generated: it would make future updates
  impossible.

---

## 5. Signing keys (release only)

Android, as repository secrets:

| Secret | Contents |
|---|---|
| `ANDROID_KEYSTORE_BASE64` | Base64 of the keystore file |
| `ANDROID_KEYSTORE_PASSWORD` | Keystore password |
| `ANDROID_KEY_ALIAS` | Key alias |

Windows code signing is optional.

> Never commit these, and never paste them into a chat or an issue.

---

## 6. Filling in your own project details

Edit `design/client-config.json`:

| Field | Meaning |
|---|---|
| `project.repositoryUrl` | Your repository URL |
| `project.license` | Licence identifier; **do not add GPL code before choosing** (see ADR-0002) |
| `branding.applicationName` | Currently `default` |
| `branding.displayNames.*` | Localised names; empty falls back to `applicationName` |
| `branding.applicationId` | Stable desktop identity, reverse-DNS |
| `branding.androidPackageId` | Android package name, reverse-DNS. **Cannot change after release.** Empty uses a clearly-marked internal test identity |

Validate locally:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
./tools/scripts/validate-client-config.ps1
./tools/scripts/check-i18n-keys.ps1
```

---

## 7. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `failed to push some refs` | The remote has commits you do not (usually a pre-created README) | `git pull --rebase origin main`, then push |
| Actions shows nothing after an upload | Workflow file is not on the default branch, or the path filter did not match | Confirm `.github/workflows/ci.yml` is on `main` |
| Every job fails immediately | Often `checkout` or `setup-go` | Read the first few lines of the log |
| Windows GUI build times out | vcpkg compiles wxWidgets from source on first run | The runner caches vcpkg binaries to the Actions cache; later runs are fast |
| `Release build refused: core pins are unresolved` | Expected until `versions.lock.json` is filled in | Pin the core versions deliberately |

---

## 8. Suggested order

1. Create the empty repository.
2. Push source and tags.
3. Wait for `CI checks`; fix what it reports (see section 2).
4. Run **Verify target architectures** to confirm every architecture compiles.
5. Fill in `design/client-config.json`.
6. Only then attempt package builds.

---

## 9. See also

- [Architecture](architecture.md)
- [Verification](verification.md) — what has and has not been checked
- [deploy/packaging/README.md](../../../src/versions/0.1.0/deploy/packaging/README.md) — package contents and core distribution rules
