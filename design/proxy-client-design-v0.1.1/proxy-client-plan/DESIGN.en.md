# Dual-core Xboard Client: Workflow and Acceptance Baseline

Version 0.1.1 · 2026-10-14 · Design proposal, not implemented.

## 1. Outcome and delivery boundary

Build a lightweight client for Xboard user operations, independent third-party subscriptions and manually configured nodes. Windows and Android may use separate native interfaces. Linux remains a required desktop target. Xray and sing-box are selectable through separate adapters.

This delivery is a planning package. The implementation delivery must contain application source, locked dependencies, manually triggered GitHub build workflows, packaging/uninstall integration, detailed development and user documentation in Chinese and English, and durable AI context/change records. Nothing in this package establishes successful application builds or runtime compatibility.

Optional Docker means a headless HTTP/SOCKS client service, optionally with container-local TUN. It does not package the desktop/mobile GUI or replace the Xboard server.

## 2. Stable requirements

| ID | Requirement | Acceptance |
|---|---|---|
| R01 | Simple/professional UI and remember-mode option | Restart and transition tests; preserve advanced settings |
| R02 | Empty default API/brand URLs; editable test settings | No default outbound calls; independent profiles remain usable |
| R03 | Xboard login/account/usage/plan/announcements/tickets | Pinned server contract and test-account verification |
| R04 | Selectable Xray/sing-box with explicit capabilities | Both connect; unsupported parameters have field errors |
| R05 | Windows system proxy/TUN/PAC | OS state, traffic path and restoration evidence |
| R06 | Android VPN and all/include/exclude app scope | Real app traffic, transitions and permission revocation |
| R07 | Third-party subscriptions, URIs, native configs and manual nodes | Import/connect without Xboard login |
| R08 | Editable PAC domains and Geo category imports | Conversion report and PAC execution tests |
| R09 | Advanced structured and native configuration | Preview/check/apply/rollback |
| R10 | System-language default and extensible i18n | zh-CN/en, placeholders, pluralization and long text |
| R11 | Required desktop/mobile architectures | Separate build/start/network evidence for each target |
| R12 | Manual GitHub presets sharing build logic | Expanded target matrix matches artifacts |
| R13 | Optional Docker amd64/386/arm64 | Image architecture and network smoke tests |
| R14 | Bilingual docs and AI maintenance process | Coverage and complete handoff/change records |
| R15 | Secret isolation and transactional network ownership | Redaction, permissions and crash restoration |
| R16 | Measured lightweight implementation | Baselines and frozen resource budgets |

Never embed panel administrator credentials, server-node keys or production defaults. Treat subscription tokens as secrets. Panel outages or expired sessions must not disable independent third-party profiles.

## 3. Independent settings and UI

Keep four dimensions distinct: UI mode (`simple/professional`), traffic entry (`system_proxy/tun/pac/local_proxy`), routing (`rule/global/direct`), Android application scope (`all/include/exclude`). All applications entering the VPN can still use rule-based routing. Desktop entry modes are initially mutually exclusive; transitions restore the old mode before taking ownership of the new one.

First launch defaults to simple. With `rememberUiMode=true`, reuse the selected mode. Disabling remembrance retains the current session mode but starts the next session in simple. Both interfaces share one repository. Returning to simple must preserve advanced settings and indicate when advanced overrides remain active; reset requires a backup.

Simple UI uses Connection, Account, Tickets and Settings. Connection exposes connect/disconnect, committed runtime state, core/node selection and essential entry settings. Account provides login/logout, authoritative quota/usage/expiry/plan and announcements. Tickets support list/detail/create/reply/close only where the pinned backend provides them. Settings expose API testing, language, UI preference and platform options. Empty API configuration offers both setup and independent profiles. Registration/password recovery/purchase can initially open a separately configured panel web URL; never infer it from the API URL or put credentials in URLs.

Professional UI adds Profiles/Subscriptions, Nodes/Groups, Routing, DNS, Connections, Logs, Rule Resources and Diagnostics. Expose transport/TLS, DNS, listener ports, routing order, selectors/testing, TUN MTU/IPv6/exclusions, PAC domains and native configuration. Every field is capability/version/platform aware. Missing connection-list or hot-switch APIs are reported as unavailable rather than fabricated from logs.

Native profiles belong to one core. A core switch cannot silently convert them; copy-and-convert must produce a loss report. Preserve unsupported import fields in quarantine, not active verified configuration. Do not execute subscription scripts or remote code.

## 4. Candidate architecture

Desktop candidate: C++20/wxWidgets native UI and CMake, shared Go business service, pinned Xray/sing-box sidecar processes. Android candidate: Kotlin/Compose, VpnService and Go/AAR bindings. These are engineering proposals intended to retain x86 and avoid a bundled browser runtime, not proven platform coverage. wxWidgets documents a native cross-platform UI approach [S6]. Every target still needs G1 probes.

Use Clash Verge as a reference for profile/tray/log/connection UX, not as a configuration backend for different cores [S2]. The specified Clash for Android repository was unreadable during this review; require a verifiable source snapshot rather than substituting an arbitrary similarly named fork [S8]. Qt remains an alternative, but its current official support list does not establish Windows/Linux x86 support [S7].

Android should first probe one AAR containing common wrappers and both cores, avoiding multiple independent Go runtimes. Dependency coexistence, JNI ABIs, Go bindings and native libraries must be verified. Conflicts require a compatible locked combination or an ADR evaluating other integration routes, not silently dropping a core.

| Module | Responsibility |
|---|---|
| DesktopUI / AndroidUI | Interaction, translated presentation, local input validation |
| PanelAdapter | Versioned user API authentication, usage and tickets |
| SubscriptionService | Fetch/decode/update/source attribution and reports |
| ProfileRepository | Versioned profiles, overrides, secret references and migrations |
| ConfigCompiler | Independent Xray/sing-box emitters and loss validation |
| RuleResourceService | Geo parsing, canonical rules and PAC subset generation |
| CoreAdapter | Config checks, lifecycle, capabilities and available statistics |
| ConnectionCoordinator | One cancellable connection transaction and committed generation |
| PlatformNetwork | OS proxy/DNS/routes, PAC ownership and Android VPN |
| PrivilegedBroker | Authenticated allowlisted privileged desktop operations |
| SecretStore / Diagnostics | OS-backed secrets and bounded redacted diagnostics |

UI must not mutate routes or generate core configuration. The broker must not execute arbitrary commands or write arbitrary files. The core adapter must not silently take unjournaled ownership of network settings.

Pipeline: imported/user settings → parse report → canonical Profile/Node/Rule/DNS → capability check → compile → native core validation → connection transaction → health verification → committed runtime state. Neither a saved profile, existing process nor open port alone proves connectivity.

Repository layout: `apps/desktop`, `apps/android`, `shared/{domain,panel,subscription,compiler,rules}`, `runtime`, `platform`, `schemas`, `locales`, `build`, `tests`, `docs/{zh-CN,en,adr,changes}`, `.github/workflows`.

## 5. Panel and internal contracts

Default `apiBaseUrl` and `webBaseUrl` are empty; `apiProfile=auto`. Allow timeout, prefix and version-profile configuration. Prefer HTTPS. Explicit development HTTP mode must be visible and must not disable TLS verification globally.

The Xboard repository was reviewed, but specific route/controller files could not be retrieved. Endpoint methods, paths, payloads and authentication headers remain unverified [S1]. Abstract operations are `discoverPanel`, `login`, `getAccount`, `getTraffic`, `getSubscriptionDescriptor`, `listAnnouncements`, ticket operations and `logout`. They are proposed client operations, not claimed backend routes.

G0 must inspect pinned routes, middleware, controllers, resources and authentication responses. Produce `api-map.json` with operation/method/path, DTOs, auth semantics, errors, source locations and redacted fixtures. Do not assume Bearer headers or refresh tokens. Keep panel usage distinct from local traffic counters, preserve server units and billing semantics, and isolate sessions by server. Do not forward authentication on cross-host redirects. Avoid blind retries of ticket mutations; respect rate limits and handle expired sessions without discarding usable independent profiles. Panel calls default to direct networking; optional proxied panel access needs startup dependency-loop checks.

Desktop regular services use ACL-protected named pipes/Unix sockets with versioned JSON RPC; Android uses bound-service/AAR equivalents. The privileged broker has a separate restricted channel. Handshake validates protocol/schema versions. Public management HTTP is disabled by default.

Contract operations: capabilities, profile validation/compilation, idempotent stop, start, runtime state, node switching, entry-mode switching, bounded event watching and owned-network recovery. Requests carry operation IDs and profile revisions; state/events carry generation and schema version. Common errors contain stable code/message key, field path, retryability, operation ID and redacted details. Hot switching is used only when supported; otherwise perform a transactional restart.

Support URI lists, Base64 lists, Clash-compatible YAML, Xray JSON and sing-box JSON. Initial structured protocols are SS/VMess/VLESS/Trojan with version-validated TLS/Reality/transports; others require explicit extensions or native profiles. Do not promise lossless arbitrary Clash conversion. Stable IDs preserve node choices; display names are insufficient. Subscription refresh validates a candidate snapshot before atomic replacement and preserves manual overrides and last-known-good state. A removed selected node triggers explicit selection or a configured fallback. Bound payload/decompressed sizes, node counts, nesting, redirects and timeouts. Safe YAML only; no scripts or unrestricted file inclusion. Export is redacted by default; secret backups require explicit encrypted export.

## 6. Core and network behavior

Run one selected core. Each compiler calls the pinned core's own config check. Versioned capabilities cover protocols, TUN, statistics, routing and hot reload. Restrict management interfaces to local endpoints with authentication where supported and detect port conflicts.

Current Xray documentation includes TUN support, so an unconditional claim that Xray lacks TUN is obsolete. Native TUN, routing/DNS and Android FD handling require pinned-version tests [S3]. If a required target cannot use native TUN, evaluate a named TUN-to-SOCKS bridge and test UDP/DNS/IPv6 independently; never secretly route through the other core while presenting pure Xray.

Android supplies VPN FD, protected sockets and network callbacks. Test consent refusal/revocation, foreground lifecycle and network transitions. Android provides one active VPN per user/profile; app allow/disallow lists are mutually exclusive. It does not provide equivalent Windows-wide one-click system-proxy/PAC ownership. Local proxies/PAC export are optional for individually supporting apps [S4].

State sequence: stopped, validating, starting_core, preparing_network, connecting, running, stopping, restoring, stopped. Errors enter recovering then failed, or recovery_required if restoration cannot finish. Generation and cancellation prevent stale callbacks from committing. Desktop proxy/PAC takeover follows core readiness. TUN prepares required interfaces and bypass paths before route/DNS commit.

Persist a recovery journal with original/applied values, resource ownership and transaction phase. A privileged watchdog supports crash recovery. Restore only settings still owned by this client; preserve later external edits and report conflicts. Test exit/crash/update/uninstall/reboot. Do not depend on killed core processes to clean up. Define bootstrap DNS, server socket and control paths to avoid loops; make IPv6 behavior explicit. Default failure behavior restores direct connectivity. A future kill switch needs a separate advanced feature and acceptance plan.

Windows scope: current-user proxy settings; WinHTTP is separate and initially unchanged. TUN uses restricted service and architecture-matched dependencies. Linux initially integrates GNOME settings with documented manual alternatives and controlled privileged routing/DNS. Docker does not mutate host proxy settings; TUN is disabled by default and container-local when enabled.

## 7. PAC and Geo resources

Treat `geodomain:gfw` as a user alias for a verified dataset/category, not a literal field accepted by every core. Verify category availability. Xray Geo and sing-box rule sets have different formats. Official sing-box documentation states legacy geosite was removed in 1.12.0; new adapters must use rule sets [S5].

Resource import → hash/format/category checks → canonical rules → Xray rules/sing-box rules/PAC domain subset. Record resource source/version/license/hash, category counts and excluded entries. PAC supports exact/suffix domains; wildcard/keyword/regex semantics require explicit treatment. IP/process/port/compound rules cannot be silently approximated. Default reject lossy conversion; permit only an explicitly acknowledged supported subset. Priority: user direct exceptions, user proxy domains, imported domains, default action. PAC is proxy selection for participating applications, not whole-device packet routing.

Escape inputs, prohibit JS injection, avoid blocking DNS queries by default, bind PAC serving to loopback, use version/hash-aware script URLs and restore OS ownership on shutdown. Benchmark 1k/10k/50k domains and lazy-load large resources.

## 8. i18n, secrets and resource budgets

Language priority: user override, mapped system preference, English fallback. Ship zh-CN/en first. A canonical key/argument/plural source generates desktop and Android resources. Localize dates/numbers/units, avoid string concatenation, keep technical diagnostic keys stable, and do not translate server content automatically. Validate missing keys, arguments, long text, RTL readiness and accessibility labels.

Use SQLite/versioned JSON for ordinary data and secret references. Use Windows DPAPI/Credential Manager, Android Keystore-protected keys and Linux Secret Service. Without a keyring, default to session-only secrets or an explicitly chosen encrypted vault, never plaintext fallback. Atomic backups/migrations carry versions and checksums.

No bundled Chromium; only the chosen core runs; cap default logs at 20 MiB, bound latency-test concurrency and avoid idle polling. ABI-split Android packaging and lazy Geo loading reduce waste. Freeze measured G1 budgets for archive size, idle/connected RSS, startup and connect latency. Provisional optimization goals: desktop UI+business idle RSS ≤120 MiB and compressed dual-core desktop package ≤100 MiB excluding optional Geo resources. These are unmeasured goals, not reported performance. Exceptions require evidence and ADRs, not deleted targets/features.

## 9. Build targets and manual workflows

Normalize amd32 to x86/Go 386; generic arm means arm64. Android v7a and x86 are 32-bit targets. Optional x86_64 does not replace requested x86.

| Family | Required targets | Planned artifacts |
|---|---|---|
| Windows | amd64, 386, arm64 | zip and installer |
| Android | arm64-v8a, armeabi-v7a, x86 | ABI-specific signed APK; optional universal |
| Linux | amd64, 386, arm64 | deb and tar.gz |
| Docker, optional | linux/amd64, linux/386, linux/arm64 | OCI images and optional pushed manifest |

Windows builds use MSVC x64/Win32/ARM64 paths on compatible runners. Android uses Gradle/NDK/Go bindings and per-ABI checks. Linux uses pinned minimum-glibc build environments; i386 requires a distribution that actually supplies the dependencies. Docker requires checking each base-image manifest. Cross compilation or emulated smoke tests alone do not establish all native OS/driver compatibility.

Provisional OS scope is Windows 10/11 and Windows on ARM. G0/G1 freezes exact Debian/Ubuntu/i386 environments and Android minimum SDK, initially evaluating API 26 or later according to core/toolchain requirements. Track planned/build_passed/runtime_passed/blocked/deferred separately; all current targets are planned.

Manual `workflow_dispatch` entry points:

| Workflow | Expansion |
|---|---|
| build-all | All required Windows/Android/Linux targets; Docker opt-in |
| build-windows | All or selected desktop architecture |
| build-android | All or selected ABI; optional universal |
| build-linux | All or selected Linux architecture |
| build-docker | Three platforms, optional manifest/push; merge repeated request |
| build-arch | amd64/386/arm64/all, explicit family filters; extra armv7 selection |
| build-common | Windows/Linux amd64+386 and Android v8a+v7a; Docker off |

For build-arch, Android mapping is amd64→optional x86_64, 386→x86, arm64→v8a, armv7→v7a. Desktop/Linux are the default families; Android/Docker require explicit selection. Unavailable optional targets fail preflight instead of disappearing. All-build does not implicitly add optional Android x86_64. When deferred Docker is explicitly selected, preflight clearly reports it as unimplemented.

Wrappers call reusable desktop/android/docker workflows driven by one target manifest. Uploading source must not automatically build/release installers. Lightweight source checks may be automatic. Default output is Actions artifacts; releases and registry pushes require explicit publish inputs. GitHub documents manual workflow triggering [S9].

Inputs: source ref, preset/architecture, Docker inclusion, build type, artifact-only/publish and release tag. Resolve a single source SHA and lock snapshot for all jobs. Lock core commits, Go/CMake/wxWidgets/compiler/JDK/Gradle/AGP/Kotlin/NDK/SDK, resource hashes, Actions SHAs and image digests. No per-job latest downloads or unresolved placeholders in release builds. Application source builds in GitHub without locally supplied prebuilts.

Pipeline: dependency/architecture preflight, fixed checkout, compile, unit/contract/native config checks, package, machine-type/dependency checks, available runtime smoke, SBOM/checksums/build report, artifacts. Use fail-fast=false to collect failures, but aggregate required failures into overall failure. Never mark skipped runtime checks as passed or use continue-on-error to announce universal success.

Without user Android signing secrets, produce explicitly labeled debug/test APKs only; release signing requires a stable supplied keystore and otherwise fails. Do not generate random replacement release keys. Unsigned Windows test packages are allowed with clear signing status. Publishing permissions belong only to publish jobs; secrets never enter source. Cache keys include lock hash/architecture.

Docker defaults to a non-root headless manager and loopback host bindings, with mounted configuration/secrets. No baked credentials; one chosen core runs. Optional TUN requires minimal capabilities/device access and is limited to the container namespace. Host routing is a separate deployment design.

## 10. Stage gates

| Gate | Work and output | Exit condition |
|---|---|---|
| G0 | Pinned sources/API/license/OS/toolchain audit | Source register, API map, target routes and decisions |
| G1 | GUI/core/ABI/AAR probes; Windows/Android dual-core network tests | Candidate compiles and connects; blockers have credible routes |
| G2 | Models/secrets/API/parser/i18n/UI shell | Independent import, safe empty API and contract/migration tests |
| G3 | Windows/Android simple operation and business flows | Representative targets connect through both cores and restore network |
| G4 | Windows TUN/PAC, Android app scope, advanced customization | Real functionality, loss reports and non-destructive UI transitions |
| G5 | Linux/all architecture packaging/manual presets; optional Docker | Required target evidence and accurate build summaries |
| G6 | Bilingual manuals/AI context/update/recovery closure | Definition of Done satisfied |

Loop within each gate: objective → contract/design → bounded implementation → automated checks → platform verification → fixes → archived evidence → state update. G1 must probe actual candidate dependencies and bindings, not only a Hello World.

## 11. Verification and convergence

Stable tests T01–T17 cover mode memory; empty API/independent subscriptions; auth/server isolation; usage/tickets; both cores/capabilities; Windows entry modes; Android scopes; crash/revoke/kill/uninstall/reboot recovery; network/sleep transitions; all import formats/update rollback; Geo/PAC injection/loss; preset expansion; machine types/native runtime; i18n/accessibility; atomic migrations/native edits; resource budgets; optional Docker.

Use controlled proxy and destination endpoints to verify each leg and egress, with redacted evidence. A public website opening is insufficient to prove correct core/routing. Non-privileged CI tests cannot replace privileged OS/VPN tests. Block releases for secret exposure, unrecoverable takeover, wrong exits, silent field loss, fabricated business data, unusable required targets or destructive mode changes.

Core v1.0 requires build and runtime verification across all required Windows/Android/Linux targets, dual-core connection tests per platform family, and evidence for architecture-specific driver/binding paths. All mandatory features and manual build presets must work with no open P0/P1 defects. If a required target is build-only, deliver a clearly labeled source-only/pre-release package, not a completed v1.0. Changing mandatory scope requires a requirement/matrix/ADR update. Deferred Docker does not block core v1.0; its completion requires R13/T17 separately.

A clean checkout must build through documented GitHub manual workflows and locked dependencies. Artifacts identify source SHA, versions, architecture, test/signature status, SBOM and SHA256. Updates preserve settings and uninstall restores owned network state.

Final development manuals in zh-CN/en must cover architecture/directories, contracts/DTOs/errors, schemas/migrations, compilers/capabilities/rules, platform permissions/restoration, i18n, toolchains/presets/signing/images, tests/performance/diagnostics and AI maintenance. User manuals must cover architecture selection/install/uninstall, API/login/usage/tickets, UI memory, cores/nodes/subscriptions, platform modes, PAC/Geo, advanced settings, manual builds/artifacts, updates/recovery and limitations. Generate these from implemented UI and verified commands; this plan is not an existing product manual.

## 12. AI maintenance and unresolved work

Read AGENTS, design/spec, current state/handoff, affected contracts/source/lockfiles and relevant ADRs before changes. Define one bounded task with requirement IDs, platforms, files, acceptance and rollback. Implement small changes, run affected checks, update bilingual docs and evidence, and produce a handoff. Never claim unrun tests passed, remove failing targets, upgrade toolchains opportunistically, or change schemas/API without migrations/contracts.

Use Markdown task/ADR/change templates and JSON verification records. State includes source SHA, gate, completed requirements, blockers, unknowns and the next single task. Technical changes require recorded decisions preserving stable IDs.

Unresolved before implementation: exact Xboard fork/commit and authentication/routes; verifiable Clash for Android snapshot; desktop x86 drivers/dependencies; combined Android AAR compatibility; OS/glibc/minSdk; final identity/signing; per-file license audit; Docker timing. Default common preset includes Linux amd64/386. These do not require production credentials now: source audit and probes can proceed first, while real server acceptance waits for a test environment.

## 13. Confirmed configuration and validation arrangement (0.1.1)

The user performs Windows/Android physical-device validation; device versions are not required before source work. Evaluate Windows 10/11 and Android API 26+ provisionally, subject to dependency audit. Runtime acceptance remains unverified until evidence is returned.

client-config.json centralizes public branding, identity, panel URLs, public headers, verified endpoint overrides, web links and client defaults. Its schema and bilingual notes accompany it. The loader is not implemented. Keep credentials/subscription tokens/signing keys in separate secure storage. OS/toolchain/core versions are build contracts, not arbitrary public JSON switches.

The user reported uploaded source, but accessible file/archive searches did not expose those attachments. Source receipt and audit remain pending; pin the supplied snapshots and hashes when accessible.
