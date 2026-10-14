# 上传源码与手动构建操作步骤

面向：**你（项目所有者）**。目标是把工作区源码推到 GitHub，并用手动工作流完成验证与构建。

当前状态（截至 2026-10-14）：
- 源码已在本地 git 仓库中，含 1 个里程碑 tag：`v0.1.0-alpha.1`
- **尚未在 GitHub 上存在仓库**
- **代码一行都未编译过**（开发机没有 Go / CMake / JDK），首次 CI 就是第一次真正的编译验证

---

## 0. 前置：Git 不在 PATH

本机 Git 位于：

```
C:\Program Files\Git\cmd\git.exe
```

**没有加入 PATH**，所以直接敲 `git` 会提示找不到命令。以下命令统一用全路径调用。
如果想省事，先把这一行加进 PowerShell 会话：

```powershell
$env:Path += ";C:\Program Files\Git\cmd"
```

Git 提交身份已在本地仓库配置好（`xboard-client` / `xboard-client@localhost`），
如需改成你自己的署名：

```powershell
& "C:\Program Files\Git\cmd\git.exe" -C D:\workai\xbaord config user.name "你的名字"
& "C:\Program Files\Git\cmd\git.exe" -C D:\workai\xbaord config user.email "你的邮箱"
```

---

## 1. 在 GitHub 上创建空仓库

1. 打开 https://github.com/new
2. 填写仓库名（例如 `xboard-client`）
3. **可见性按你的需要选**；若选私有，Actions 额度受你的套餐限制
4. **不要**勾选 "Add a README file"、**.gitignore**、**license** ——
   本地已有这些文件，勾选会造成推送冲突
5. 创建后记下仓库地址，形如 `https://github.com/<用户名>/<仓库名>.git`

> 仓库地址填哪里？填进 `design/client-config.json` 的 `project.repositoryUrl`，
> 供后续构建读取身份信息。它与 git remote 是两回事，两边都要填。

---

## 2. 把源码推上去

### 方式 A：直接用本地 git 推送（推荐）

工作区 `D:\workai\xbaord` **本身就是一个 git 仓库**，直接加远端推上去即可：

```powershell
$git = "C:\Program Files\Git\cmd\git.exe"
cd D:\workai\xbaord

# 1) 确认当前状态干净
& $git status --short --branch

# 2) 关联远端（把地址换成你自己的）
& $git remote add origin https://github.com/<用户名>/<仓库名>.git

# 3) 推送主分支
& $git push -u origin main

# 4) 推送里程碑 tag
& $git push origin --tags
```

推送会要求登录。GitHub 已不支持密码推送，需要**个人访问令牌（PAT）**：
Settings → Developer settings → Personal access tokens → 生成一个带 `repo` 权限的令牌，
在提示输入密码时粘贴该令牌。

若之前已加过 origin，改用：

```powershell
& $git remote set-url origin https://github.com/<用户名>/<仓库名>.git
```

### 方式 B：网页上传（不用命令行）

适合不想配令牌的情况：

1. 从 `output/exports/` 取源码压缩包（如 `xboard-client-source-0.1.0-alpha.1-<日期>.zip`）
2. 在 GitHub 仓库页面点 **Add file → Upload files**
3. 解压后把**内容**（不是外层文件夹）拖进去
4. 填提交信息后提交

> 网页上传**不会**带上 git tag，也无法一次提交上千个文件；
> 文件多时强烈建议用方式 A。

### 不会上传的东西（这是故意的）

`.gitignore` 已排除以下内容，推送后仓库里**没有**它们：

| 排除项 | 原因 | 影响 |
|---|---|---|
| `src/references/**` | 5 份第三方源码快照（4309 个文件） | 换机器后需重新获取；来源记录在 `design/audit/source-register.md` |
| `output/builds/`、`output/exports/` | 可再生成的产物 | 无 |
| 各类构建产物与秘密 | 体积与安全 | 无 |

`output/reports/` **会**上传（它是证据）。

---

## 3. 首次 CI 验证（最重要的一步）

推送后 **CI 会自动运行**（`.github/workflows/ci.yml` 监听 push）。
也可以手动跑：仓库页 → **Actions** → 左侧 **CI checks** → **Run workflow**。

它会并行跑 **4 个作业**：

| 作业 | 检查内容 | 失败含义 |
|---|---|---|
| `Go formatting` | `gofmt -l` 是否为空 | 有文件未按 gofmt 格式化 |
| `Go build / vet / test (linux)` | 编译、`go mod tidy`、`go vet`、单测、`-race` | 编译错误 / 测试失败 / go.mod 不一致 |
| `Go build / test (windows)` | 同上（Windows 平台） | Windows 专属代码编译或测试失败 |
| `Design JSON, config schema and i18n` | 设计 JSON、配置 Schema、文案键一致性 | 契约或翻译不一致 |

**为什么要拆成 4 个**：格式问题最容易修，如果它让流水线在第一步就停，
真正的编译错误会被掩盖。并行之后一次就能看到全部问题。

### 首次运行最可能出现的三种失败，以及怎么修

#### 失败 1：`Go formatting` —— 预期之内，一条命令修好

开发机没有 Go，无法在本地执行 `gofmt`，所以手工对齐可能不完整。
处理方式二选一：

- **推荐**：下载该作业产出的 **`gofmt-fixes` artifact**（一个补丁文件），然后

  ```powershell
  $git = "C:\Program Files\Git\cmd\git.exe"
  cd D:\workai\xbaord
  & $git apply gofmt.patch
  & $git commit -am "style: apply gofmt"
  & $git push
  ```

- 或者你本地装了 Go：在 `src/versions/0.1.0/src` 下执行 `gofmt -w .` 后提交。

#### 失败 2：编译错误或测试失败

日志里会给出**文件名与行号**。把那段日志发我，我来修。
这是预期内的风险：约 1.1 万行 Go 代码从未编译过。

#### 失败 3：`Design JSON, config schema and i18n`

先看是哪一步。i18n 检查会明确指出「哪个键缺在哪个语言里」或「占位符数量不一致」。

---

## 4. 多目标交叉编译验证（现在就能成功的那个）

仓库页 → **Actions** → **Verify target architectures** → **Run workflow**。

它把共享 Go module 交叉编译到**全部目标平台与架构**：

```
windows/amd64   windows/386    windows/arm64
linux/amd64     linux/386      linux/arm64
android/arm64   android/arm(v7) android/386
```

- 交叉编译结果给出「每个目标都能编译」的硬结论
- 勾选 `tests` 时额外跑一次原生架构的单元测试
- **它不产出安装包**，只产出编译结论

这是目前唯一能给出真实通过/失败结论的手动构建入口。

---

## 5. 安装包构建入口（现在会失败，属正常）

仓库页 → **Actions** 里可以看到 7 个手动入口：

| 入口 | 作用 |
|---|---|
| `Build all` | 全部必需目标（可选含 Docker） |
| `Build Windows` | Windows x64 / x86 / ARM64 |
| `Build Android` | Android v8a / v7a / x86 |
| `Build Linux` | Linux x64 / x86 / ARM64 |
| `Build Docker` | 三架构镜像（可选目标） |
| `Build by architecture` | 按架构筛选，平台开关默认 Windows + Linux |
| `Build common` | 常用组合（Win x64/x86 + Linux x64/x86 + Android v8a/v7a） |

**现在触发它们会在 preflight 明确失败**，提示类似：

```
desktop source is not implemented yet: .../src/apps/desktop/CMakeLists.txt is missing
```

这是刻意的：宁可明确失败，也不产出一个假的「构建成功」。
它们的构建步骤尚未接线，等桌面 GUI 与 Android 源码写好后才会真正产出安装包。

**另外两条硬规则**（已实现，可放心）：

- 核心版本未在 `versions.lock.json` 冻结时，**release 构建会被拒绝**
  （占位锁文件不得进入发布构建）
- Android release **缺少签名密钥时会失败**，绝不临时生成随机 release key

---

## 6. 签名密钥（只在做正式 release 时需要）

Android 正式签名：仓库 → **Settings → Secrets and variables → Actions → New repository secret**，添加：

| Secret 名 | 内容 |
|---|---|
| `ANDROID_KEYSTORE_BASE64` | keystore 文件的 base64（`[Convert]::ToBase64String([IO.File]::ReadAllBytes("app.keystore"))`） |
| `ANDROID_KEYSTORE_PASSWORD` | keystore 密码 |
| `ANDROID_KEY_ALIAS` | 密钥别名 |

Windows 代码签名：可选。没有签名仍能产出测试安装包，但报告里会标注签名状态。

> 这些凭据**不要**发到对话里，也不要提交到仓库。

---

## 7. 填写你自己的项目信息

编辑 `design/client-config.json`：

```json
{
  "project": {
    "repositoryUrl": "https://github.com/<用户名>/<仓库名>",
    "license": ""
  },
  "branding": {
    "applicationName": "default",
    "displayNames": { "zh-CN": "", "en": "" },
    "publisher": "",
    "applicationId": "",
    "androidPackageId": "",
    ...
  }
}
```

| 字段 | 说明 |
|---|---|
| `applicationName` | 当前为 `default`，改成你的软件名 |
| `displayNames` | 两种语言的显示名；留空则回退到 `applicationName` |
| `applicationId` | 桌面应用标识，反向域名格式（如 `com.example.client`） |
| `androidPackageId` | Android 包名，反向域名格式；**正式发布后不可随意更改** |
| `project.repositoryUrl` | 你的仓库地址 |
| `project.license` | 许可证标识；**留空前不要引入 GPL 代码**（见 ADR-0002） |

改完后 CI 的 `Design JSON, config schema and i18n` 作业会校验它是否符合 Schema。

提交前想本地自查：

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force
cd D:\workai\xbaord
& .\tools\scripts\validate-client-config.ps1
& .\tools\scripts\check-i18n-keys.ps1
```

---

## 8. 排错速查

| 现象 | 原因 | 处理 |
|---|---|---|
| `git` 不是可识别的命令 | Git 不在 PATH | 用全路径 `C:\Program Files\Git\cmd\git.exe` |
| 推送要求输入密码但被拒 | GitHub 不接受密码 | 用 Personal Access Token |
| `failed to push some refs` | 远端有本地没有的提交（常用于勾选了 README 的仓库） | `& $git pull --rebase origin main` 后重推 |
| 网页上传后 Actions 没反应 | workflow 文件不在默认分支，或路径过滤未命中 | 确认 `.github/workflows/ci.yml` 已在 main 上 |
| CI 全部作业都失败 | 可能是 checkout 或 setup-go 问题 | 看日志最上面几行，通常在 setup 步骤就报错 |
| 想重跑某个作业 | —— | 该次运行页面右上 **Re-run jobs** |

---

## 9. 建议的执行顺序

1. 建空仓库
2. 方式 A 推送源码与 tag
3. 等 `CI checks` 自动跑完 → 按第 3 节处理失败
4. 手动跑一次 `Verify target architectures`，确认全部目标架构可编译
5. 按第 7 节填好 `client-config.json`（软件名、包名、仓库地址）
6. 把 CI 结果发我 → 我继续把桌面 GUI 与 Android 写出来
7. GUI 完成后，7 个构建入口才会真正产出安装包
