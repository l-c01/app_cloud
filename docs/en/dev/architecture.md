# Architecture

For anyone who wants to read or change this project. After reading it you
should be able to answer "which directory does this change belong in?"

Status: the design is complete, and **none of the code has ever been
compiled**. This page describes intent, not verified behaviour. For what
has actually been checked, see [verification.md](verification.md).

---

## 1. Three processes, two front ends

```
┌─────────────────────┐        ┌──────────────────────┐
│  Desktop GUI (C++)  │        │  Android (Kotlin)    │
│  wxWidgets          │        │  Compose + VpnService│
└──────────┬──────────┘        └───────────┬──────────┘
           │ local IPC (JSON)              │ same process
           │ unix socket / named pipe      │
┌──────────▼──────────┐                    │
│  Service (Go)       │                    │
│  cmd/proxyclient-   │                    │
│  service            │                    │
└──────────┬──────────┘                    │
           │ start / stop                  │
┌──────────▼──────────┐        ┌───────────▼──────────┐
│  Core (external)    │        │  Core (external)     │
│  Xray or sing-box   │        │  Xray or sing-box    │
└─────────────────────┘        └──────────────────────┘
```

### Why the desktop side must be two processes

Writes to system networking, starting the core and restoring the network
must survive **a crashed or force-killed interface** — and the interface is
the component most likely to crash.

More concretely: on Windows there is no way to deliver a graceful
termination signal to an ordinary process. If the GUI owned the network
takeover, force-killing it would leave the system proxy pointing at a local
port that no longer exists. The user sees "no web pages open at all" and
has no idea what to change.

So network restoration has to belong to something whose lifetime is
independent of the interface, backed by a **persistent journal**.

Android needs no IPC: `VpnService` and the UI share a process model, and
Android owns the lifecycle.

---

## 2. Directory responsibilities

```
src/versions/0.1.0/
├── deploy/
│   ├── packaging/          packaging contract and core fetch scripts
│   ├── docker/             docker contract (deliberately unimplemented)
│   └── targets/            target manifest (single source of build matrix)
└── src/
    ├── cmd/                executables
    ├── shared/             cross-client pure logic (no platform deps)
    │   ├── panel/          panel API adapter (sign-in, subscription link)
    │   ├── subscription/   subscription fetch and node parsing
    │   ├── domain/         canonical node model
    │   ├── compiler/       dual-core config compiler
    │   ├── secrets/        credential store abstraction and scrubbing
    │   └── i18n/           catalog loading, negotiation, formatting
    ├── runtime/            runtime orchestration
    │   ├── coordinator/    connection transaction state machine
    │   ├── core/           core process lifecycle
    │   ├── coreupdater/    core download, verification and update
    │   └── ipc/            UI ⇄ service local protocol
    ├── platform/           platform-specific implementations
    │   ├── netmgr/         network takeover adapter and backends
    │   ├── winproxy/       Windows system proxy (registry)
    │   └── journalfile/    persistent recovery journal
    └── apps/
        ├── desktop/        C++ GUI
        └── android/        Kotlin GUI
```

**Layering rule: `shared` depends on neither `runtime` nor `platform`.**
Breaking it forces pure logic to carry platform baggage and makes unit
tests require a platform environment.

---

## 3. Four independent dimensions

These are not four ways of saying the same thing:

| Dimension | Values | Meaning |
|---|---|---|
| Interface mode | simple / professional | Presentation only; no behavioural effect |
| Traffic entry | system proxy / PAC / TUN / local ports | How traffic reaches the core |
| Routing policy | rule / global / direct | How the core then splits traffic |
| Android app scope | all / excluded list | Which apps use the proxy |

**Interface mode is the only dimension where switching changes nothing
behaviourally.** Both modes share one configuration and one node set. With
separate state you quickly get "simple mode says connected, professional
mode says disconnected".

---

## 4. The connection transaction

"Connect" is not one operation; it is a transaction with five places to fail:

```
validating          compile the config; let the core do its native check
      ↓
starting_core       start the core process and wait for readiness
      ↓
preparing_network   write system network settings (recording originals)
      ↓
connecting          confirm usable
      ↓
running             done
```

Any failure triggers a rollback, and **before** rolling back, the
original values, the written values and the owner are written to the
recovery journal. That journal must be re-readable after the process is
force-killed.

### Ownership checking: why restore cannot simply write back

Restore compares, per key, the **current** value against **what we wrote**:

- equal → write the original back (delete the key if there was no original)
- different → record a conflict and **skip it**

A difference means a user or another program changed it in the meantime.
Overwriting would erase someone's change, and they would not know who did it.

The same principle appears in `coreupdater`'s SHA256 check, `netmgr`'s
conflict reporting and `coordinator`'s `RecoveryError`: **never hide it,
never decide on the user's behalf.**

---

## 5. Core distribution (ADR-0003)

Cores are **never compiled**; only official prebuilt binaries are downloaded:

1. Every asset is pinned by version **and** SHA256 in `versions.lock.json`.
2. Verification happens **before** the file lands anywhere usable.
3. A mismatch deletes the temporary file and aborts.
4. Replacement backs up first, then renames atomically, so it can roll back.
5. **With unpinned versions, the download refuses to run.** A package that
   silently contains an unverified core looks exactly like a normal one
   while carrying none of the integrity guarantees.

---

## 6. Two cores and the capability table

One client can use Xray or sing-box. Their formats and capabilities differ:

| | Xray | sing-box |
|---|---|---|
| `transport.type` values | per core docs | only `http, ws, quic, grpc, httpupgrade` |
| xhttp / splitHTTP | supported | **not supported** |
| h2 / http | supported | mapped to `http` |
| TCP header obfuscation | supported | not supported |
| REALITY | supported | supported |

The table lives in `shared/compiler/capability.go` and is marked
`verifiedAgainst: "unverified"`: it comes from reading core sources, not
from testing pinned releases. A test asserts that marker exists, so nobody
can quietly reclassify it as verified.

If a table were wrongly marked verified, a user would see an option marked
"supported", the core would refuse to start, and the error would come from
deep inside the core, far from the real cause.

---

## 7. Local IPC

The protocol is implemented twice — in Go (`runtime/ipc/protocol.go`) and in
C++ (`apps/desktop/src/core/ipc_protocol.{hpp,cpp}`).

**Drift does not fail loudly.** The usual symptom is "the service said it
succeeded but nothing happened in the UI". Neither compiler can catch it,
so a script compares the two definitions directly
(`tools/scripts/check-ipc-contract.ps1`).

Design points:

- The handshake checks the **wire protocol version** and the **payload
  schema version**; they evolve independently.
- A version mismatch **refuses service** rather than parsing best-effort —
  best-effort turns "a field is missing" into a hard-to-locate runtime error.
- Errors carry a `messageKey`; the UI localises, it does not parse
  server-side prose.
- Events carry a sequence number and a drop count, and **local and
  server-side drops are counted separately**: a single number cannot tell
  you whether the UI is seeing all events.
- The desktop reader thread **never calls UI code**; events are queued and
  polled by the UI thread. Calling wxWidgets across threads causes random
  crashes that only appear under load.

---

## 8. Credential handling

Principle: **credentials never appear in source, logs, fixtures or exports.**

| Mechanism | Location | Purpose |
|---|---|---|
| `secrets.Store` | `shared/secrets` | Platform secure storage (DPAPI / Secret Service / Keystore) |
| `secrets.Scrubber` | same | Removes credentials from core logs and error text |
| `json.Redact` | desktop | Recursively replaces sensitive fields before output |
| `RedactUrl` | desktop | A subscription link is the credential; only parameter names survive |
| Handle file mode 0600 | `runtime/ipc` | The handle file contains the local access token |

When a platform lacks secure storage, `secrets` returns an explicit error
and callers **must not** fall back to plaintext.

---

## 9. Localisation

All three clients **share `locales/*.json` at the repository root**. Two
sets of translation files inevitably drift, and the result is the same
error reading differently on different platforms.

Conventions (see [i18n-convention.md](../../../design/architecture/i18n-convention.md)):

- A message key is `<package>.<topic>`; field paths and credential keys use
  different prefixes so an automated check can tell the three apart.
- A missing key returns **the key itself** rather than an empty string: the
  UI shows `panel.unauthorized`, which is immediately visible. An empty
  string becomes an invisible gap.
- Out-of-range placeholders are **left in place**, for the same reason.

`tools/scripts/check-i18n-keys.ps1` scans Go, C++ and Kotlin sources.

---

## 10. Building and verification

**There is no toolchain on the development machine** (no Go, C++, JDK or
Android SDK), so all compilation and testing happens in GitHub Actions.

| Workflow | Trigger | Purpose |
|---|---|---|
| `ci.yml` | push / PR / manual | Static pre-checks, formatting, build and test (Linux/Windows), desktop core and GUI, contracts and i18n |
| `verify-targets.yml` | manual | Cross-compile for every target architecture |
| `build-*.yml` | manual | Produce packages (seven entries) |

For what can be checked locally, and what has actually been run, see
[verification.md](verification.md).

---

## 11. Related documents

| Document | Contents |
|---|---|
| [design/DESIGN.en.md](../../../design/DESIGN.en.md) | Design baseline (authoritative requirements) |
| [design/adr/](../../../design/adr/) | Architecture decision records |
| [design/plan/STATUS.md](../../../design/plan/STATUS.md) | Stage and completion status |
| [design/architecture/repository-layout.md](../../../design/architecture/repository-layout.md) | Layout and version management |
| [deploy/packaging/README.md](../../../src/versions/0.1.0/deploy/packaging/README.md) | Packaging contract |
| [upload-and-build.md](upload-and-build.md) | How to build |
