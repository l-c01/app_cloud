# AI implementation and maintenance rules / AI 开发维护规则

This is a planning baseline, not an implemented repository. 本包尚无软件实现或构建成功证据。

1. Read README, DESIGN.zh-CN.md or DESIGN.en.md, project-spec.json, current state/handoff and relevant ADRs before editing. 正式仓库出现后先读源码所在目录的规范。
2. Preserve requirement IDs and the required architecture matrix. Do not delete a failing target or weaken acceptance to make CI green. 不得为通过 CI 偷删平台或功能。
3. Default production API/web URLs stay empty. Independent subscription mode must work without panel login. 默认 URL 留空，第三方订阅独立可用。
4. Do not guess panel paths, auth headers, DTOs or core features. Pin sources and cite code locations in api-map/capability records. 不得把假定 API 写成已验证。
5. One shared profile store serves both UI modes. Preserve advanced configuration during mode transitions. 两模式共用配置，切换不破坏专业参数。
6. Do not conflate UI mode, traffic entry, routing policy and Android app scope. 四维独立。
7. Keep core configuration generation out of UI. Use independent versioned compilers, native checks and explicit loss reports. 不得静默丢字段。
8. At most one active connection transaction and one selected core. Use cancellation/generation checks and journaled owned-network recovery. 单连接事务，恢复只处理自身拥有设置。
9. Secrets never enter source, logs, fixtures or ordinary exports. Treat subscription URLs as secrets. 凭据与订阅 token 必须脱敏。
10. Dependency/core/toolchain updates require a bounded task, impact report and compatibility checks. No latest pins. 升级必须记录影响。
11. API/schema changes require compatibility decisions, migrations, rollback and relevant tests. Breaking IPC requires coordinated UI/service updates. 不得只改一端。
12. Compile applications only in the planned GitHub workflows unless the user changes this constraint. Local document/schema/static checks are allowed. 遵守用户不要求本地源码构建的范围。
13. Build workflows are manual, artifact-only by default. Publishing/signing keys require explicit user configuration; never fabricate successful signing. 默认不发布，不生成随机正式签名密钥。
14. Each change updates task/change/state records and both language manuals where affected. 未运行测试标 not_run；架构编译不等于运行成功。
15. Work only on the assigned bounded task. Do not introduce new cores, platforms or major UI frameworks without ADR. 本包不要求使用并行子代理。
16. If evidence is missing, mark it unresolved, state the next check and preserve existing working behavior. 没有证据就明确待验证。

Completion response must report what changed, requirement IDs, validation performed, unrun checks, limitations and next task. Handoff must preserve remaining blockers and exact source SHA when known.
