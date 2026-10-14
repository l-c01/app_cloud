# 多语言（i18n）约定 / Internationalisation convention

状态：已建立（R10）· 相关实现：`shared/i18n`、`locales/`、`tools/scripts/check-i18n-keys.ps1`

---

## 1. 为什么需要这份约定

代码里同时存在两类「英文点号标识符」：

| 类别 | 例子 | 用途 |
|---|---|---|
| **文案键** | `compiler.protocol_unsupported` | 查语言表，得到用户可见文字 |
| **字段路径** | `tls.reality`、`transport.type` | 定位配置里的具体字段 |

两者形态相同，极易混淆。若不约定，自动检查就无法区分，
而人工翻译时也可能把字段路径误加进语言表。

## 2. 命名约定

**文案键 = `<包名>.<主题>`**，全小写、下划线分词。

已登记的包命名空间（新增包时必须在此登记）：

```
compiler  ipc  netmgr  panel  subscription  core  coordinator  secrets  coreupdater  ui
```

**字段路径**使用被指向对象的路径（`tls.reality`、`transport.path`），
**绝不**加进语言表。

检查脚本 `tools/scripts/check-i18n-keys.ps1` 依据这份命名空间清单区分两者。

## 3. 占位符约定

- 用**位置参数**：`{0}`、`{1}`。不用命名参数，避免不同语言里顺序不一致造成歧义。
- 同一个键在各语言里的占位符**个数必须一致**（检查脚本会校验）。
- 占位符越界或非法时**原样保留** `{2}`，让缺陷在界面上可见，而不是静默变成空串。
- 复数用 `key.one` / `key.other` 约定；两者都缺时回退到 `key`。
  这只是最小可用支持——若要覆盖斯拉夫语系的多样复数形式，必须改用 CLDR 复数规则，
  不能靠这套约定硬撑。

## 4. 缺键行为

取不到的键按以下顺序回退：

1. 请求语言的文案；
2. 兜底语言（默认 `en`）的文案；
3. **返回键名本身**。

第 3 条是刻意的：界面会显示 `compiler.protocol_unsupported`，
问题立刻可见；若返回空串，界面会出现难以察觉的空白。

## 5. 语言协商

`Bundle.Negotiate` 的顺序：

1. `requested == "system"` → 使用系统语言；
2. 精确匹配（`zh-CN`）；
3. 主语言匹配（`zh-Hans` → `zh-CN`；`en-US` → `en`）；
4. 兜底语言；
5. 任意已加载语言（保证总能返回一个可用语言）。

**已知限制**：`DetectSystemLanguage` 只读环境变量（`LC_ALL`/`LC_MESSAGES`/`LANG`）。
Windows 不通过环境变量暴露界面语言，因此那里会回退到兜底语言。
要正确支持 Windows，需平台层调用 `GetUserDefaultUILanguage` 并注入结果。

## 6. 自动化检查

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
& .\tools\scripts\check-i18n-keys.ps1
```

检查三件事：

| 检查 | 失败含义 |
|---|---|
| 代码引用的键是否存在于**所有**语言表 | 某语言下会显示键名 |
| 同一键的占位符个数是否跨语言一致 | 某语言下会显示 `{1}` 字面量或丢参数 |
| （信息）语言表里有但代码未引用的键 | 翻译已就绪但代码还没接上 |

## 7. 缺口状态

**已关闭（2026-10-14）。**

检查脚本报告：**62 个文案键全部被代码引用**，两种语言表的键集合与占位符完全一致。

原先存在 35 个「语言表里有、代码未引用」的键，原因是以下包用硬编码中文构造
面向用户的错误。现已全部改造为返回文案键：

| 包 | 改造方式 |
|---|---|
| `shared/panel` | `Error` 增加 `MessageKey`/`MessageArgs`；新增 `newError` 统一构造；删除 `localizedMessage` |
| `shared/subscription` | 同上，新增 `newError` 与 `messageKeyFor` |
| `runtime/core` | `ConfigError` 增加键字段；新增 `NotReadyError`、`ErrProcessDidNotExit` 与 `MessageKeyFor` |
| `runtime/coreupdater` | 新增 `InstallError`、`ErrChecksumMismatch` 与 `MessageKeyFor` |
| `runtime/coordinator` | 新增 `RecoveryError` 与 `MessageKeyFor`（映射 `ErrBusy`/`ErrSuperseded`） |
| `shared/secrets` | 新增 `MessageKeyFor`（哨兵错误本身挂不了键字段） |
| `platform/netmgr` | `Note` 增加 `MessageKey`/`MessageArgs`；新增 `newNote` 统一构造 |

统一模式：

- 错误的**分类**（Kind / 哨兵值）用于程序判断；
- 错误的**文案**由 `MessageKey` + `MessageArgs` 表达，界面用 `i18n.Bundle` 渲染；
- `Message` 字段降级为**内部诊断文本**，只进日志，不进界面。

`MessageKeyFor(err) string` 是这个模式的公共出口：界面拿到任意 error 后调用它取键，
不需要逐个做类型断言；返回空串表示不是该包的可本地化错误。

### 命名冲突的解决

`shared/secrets` 的凭据键原本叫 `panel.session`、`panel.subscribe_url` 等，
与文案键命名空间 `panel.*` 撞车，会被检查脚本误判为缺失的文案键。

已改为 **`cred.` 前缀**（`cred.panel.session` 等）。

规则：**文案键、字段路径、凭据键三类标识符必须能在命名上区分**，
否则自动检查永远需要维护例外清单，而例外清单迟早会失效。
