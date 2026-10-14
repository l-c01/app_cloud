# Source register / 来源与核实边界

Reviewed 2026-10-14. URLs reference moving upstream pages; G0 must pin exact commits and retain relevant license/code evidence. 本轮为官方资料核对，并非五个项目的完整源码审计。技术路线、目标预算与模块设计为本包提案。

| ID | Primary source | Verified observation / 核实结果 |
|---|---|---|
| S1 | https://github.com/cedar2025/Xboard | Reference repository and MIT marker visible. Specific route/controller retrieval failed; endpoint paths/auth payloads remain unverified. 仓库已核对，具体 API 未确认。 |
| S2 | https://github.com/clash-verge-rev/clash-verge-rev | Modern desktop UI reference using Tauri/mihomo. This plan independently designs dual-core adapters. 用户若特指原 Clash Verge，应在 G0 固定原仓库快照；rev 不被默认为原项目同一源码。 |
| S3 | https://github.com/XTLS/Xray-core/blob/main/proxy/tun/README.md | Current upstream documents TUN, platform integration and routing/DNS constraints. Actual pinned ABI capability needs probes. |
| S4 | https://developer.android.com/develop/connectivity/vpn | Official VpnService, protected sockets, foreground lifecycle, one active VPN and mutually exclusive allow/disallow app lists. |
| S5 | https://sing-box.sagernet.org/configuration/route/geosite/ | Legacy geosite removed in 1.12.0; use rule sets for newer adapters. |
| S5a | https://sing-box.sagernet.org/configuration/rule-set/source-format/ | Official rule-set source format and compile route. |
| S6 | https://wxwidgets.org/about/ | Native cross-platform UI direction; not evidence all requested architectures already build. |
| S7 | https://doc.qt.io/qt-6/supported-platforms.html | Current supported list establishes modern x64/ARM targets, not requested Windows/Linux x86. Source build/community feasibility must be evaluated separately. |
| S8 | https://github.com/Kr328/ClashForAndroid | Retrieval failed in this review. Do not infer removal/license/maintenance state or substitute arbitrary forks. Need verified snapshot. |
| S9 | https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow | Official manual workflow mechanism. Actual app workflows are future implementation. |
| S10 | https://github.com/SagerNet/sing-box | Repository states GPLv3-or-later with naming/association condition. Audit exact locked license and derivative scope before reuse. |
| S11 | https://github.com/XTLS/Xray-core/blob/main/LICENSE | License source available for fixed-version audit; this package does not give a legal compatibility conclusion. |

## G0 source audit checklist / 正式源码审计清单

- Record URL, branch, tag, commit SHA, retrieval date, local file checksum and license for every reused source/library/resource.
- Record file/module references for login/user/traffic/subscription/ticket routes, middleware and response resources. Preserve redacted fixtures from the pinned source/test server.
- Distinguish UI/architecture inspiration from copied code, linked libraries and bundled executables. Retain required notices and source obligations; select project license only after this inventory.
- Check all core protocol/TUN/FD/socket/management APIs against pinned source and build tags. No blanket claim of shared feature parity.
- Check Go toolchain compatibility across imported cores, native bindings, Windows architecture dependencies and Linux i386 build environments.
- Confirm the original Clash Verge / Clash for Android source identity with the user's intended reference. Document fallback choices instead of silently substituting forks.
- Record test environment OS/ABI, driver/library versions and build runner feasibility. Published upstream binaries alone are not proof of source-build feasibility.

## Evidence limits / 证据限制

This package verifies a small set of upstream facts and defines an implementation plan. No server session was used, no full repository was cloned/audited, no application build was run, and no live proxy/VPN flow was tested. All API paths, frozen versions, exact OS baselines and runtime claims must be completed in G0/G1 and later gates.
