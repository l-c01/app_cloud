# ADR-0006：Android 的核心集成方式 —— libbox（gomobile AAR），VpnService 的 fd 交给核心

- 状态：**已决定，实现未开始**
- 日期：2026-10-15
- 相关：ADR-0003（核心分发）、ADR-0005（单内核 sing-box）

---

## 背景

Android 上「VPN 能建立，但不会代理任何流量」是本项目长期挂着的未完成项。
难点不在 Kotlin 侧：`ProxyVpnService.kt` 已经能把 `VpnService` 建立起来、
能拿到 TUN 的 `ParcelFileDescriptor`、能表达应用范围（允许/排除列表）。

难点是一个此前**没有证据、因此不敢决定**的问题：
**核心怎么拿到那个 fd？**

在此之前本文件的替代物是 HANDOFF 里的一句「由 G1 探针决定」，
以及三条并列的路线（AAR 内嵌 / 预构建进程 / tun2socks 桥接）。
三条并列不是决策，是拖延——而猜一个接口会写出「编译通过、运行即崩」的代码。

本次读参考源码，把事实查清了。

## 事实（可复核）

### 1. 参考实现用 gomobile 把核心编成库，fd 通过函数参数进核心

`src/references/ClashMetaForAndroid-main/core/src/main/golang/native/tun.go:67`

```go
//export startTun
func startTun(fd C.int, stack, gateway, portal, dns C.c_string, callback unsafe.Pointer) C.int
```

`//export` + `import "C"` 意味着这是 cgo 导出，由 gomobile 打包成 AAR。
Kotlin 侧把 VpnService 的 fd 传进来。同一文件还导出了 `stopTun`。

### 2. fd 是作为「拨号器/监听器」的选项交给 TUN 实现的，不是写进配置

`ClashMetaForAndroid/core/src/main/golang/native/tun/tun.go:54-65`

```go
options := LC.Tun{
    Enable: true,
    Stack:  tunStack,
    DNSHijack: dnsHijack,
    AutoRoute: false,           // 路由已在 TunService.kt 里设置
    AutoDetectInterface: false, // 由 VpnService::protect 实现
    Inet4Address: prefix4,
    Inet6Address: prefix6,
    MTU: 9000,                  // 与 TunService.kt 的 TUN_MTU 一致
    FileDescriptor: fd,
}
listener, err := sing_tun.New(options, tunnel.Tunnel)
```

关键观察：**`FileDescriptor` 是 `libbox`/mihomo 在配置之外额外提供的字段**，
不是配置文件里的键。也就是说「fd 怎么进去」取决于**用哪个 Go API**，
而不是取决于生成什么 JSON。这一点此前被忽略了。

### 3. sing-box 对应的入口是 `PlatformInterface.OpenInterface`

`src/references/sing-box-testing/adapter/platform.go:12-63`

```go
type PlatformInterface interface {
    Initialize(networkManager NetworkManager) error
    UsePlatformInterface() bool
    OpenInterface(options *tun.Options, platformOptions option.TunPlatformOptions) (tun.Tun, error)
    ProcessPlatformOptions(options option.TunPlatformOptions) error
    UsePlatformAutoDetectInterfaceControl() bool
    AutoDetectInterfaceControl(fd int) error
    ...
}
```

sing-box 把「平台如何创建 TUN、如何保护 socket」抽成了 `PlatformInterface`。
**我们的 Go 侧目前没有实现它**，因此哪怕内核装上了，Android 也不会代理流量——
`UsePlatformInterface()` 返回 false 时，sing-box 会自己去创建 TUN，
而在未 root 的 Android 上那是做不到的。

`option/tun.go` 的 `TunInboundOptions` 里**没有** `file_descriptor` 字段
（已逐个字段核对）——所以「在生成的配置里加一个 fd 字段」既不可能也不必要。

## 决策

**Android 采用 AAR（gomobile）内嵌核心，并由我们自己实现 `PlatformInterface`
把 VpnService 的 fd 交给 sing-box。**

具体形态：

1. Go 侧新增一个 `//export` 入口（对标上文的 `startTun`），
   接收 `fd / mtu / stack / dns / 应用范围 / 配置路径` 等参数；
2. 在其中实现 `adapter.PlatformInterface`，`OpenInterface` 用自己的 fd
   构造 TUN，`AutoDetectInterfaceControl` 回调 Kotlin 侧的
   `VpnService.protect(fd)` —— **不 protect 会让代理自己的出站流量
   再被隧道抓一次，形成回环**，这是 Android 代理最常见的错误；
3. 用 gomobile 把该包编成 AAR，在 CI 里构建（本机无 JDK/NDK，无法本地验证）；
4. Kotlin 侧 `ProxyVpnService` 在 `establish()` 后把 fd 交给 libbox，
   断开时调停止入口。

## 理由

- **不再需要 tun2socks 这类额外转发层**：fd 直接进核心，
  少一层转发就少一份性能损耗与故障面；
- **进程内没有 IPC 开销**，也避免了一个独立核心进程在低内存设备上
  被系统杀掉而导致「连上了但没流量」；
- **与参考实现一致**：ClashMetaForAndroid 是同一问题的成熟解法，
  照它做比自创路线风险低得多；
- **`protect` 是必须的**，而 `PlatformInterface` 正好提供了
  `AutoDetectInterfaceControl(fd)` 这个挂载点。

## 代价与风险（必须如实记录）

- **需要 gomobile 与 Android NDK**，CI 构建时间显著增加；
- AAR 会随核心版本变化，需要与 ADR-0003 的核心分发对齐
  （目前 `versions.lock.json` 仍是占位）；
- **在本机完全无法验证**：没有 JDK、NDK、Android SDK，也没有真机。
  因此本条决策记录的是**方向**，实现与验证必须在有真机与工具链的环境里做；
- 仍然需要真机确认「流量真的走了代理」——
  `VpnService` 建立成功**不等于**代理生效，本项目此前正是在这里出过
  「能建立但不代理」的状态。

## 未采纳的方案与理由

| 方案 | 为什么不选 |
|---|---|
| 预构建二进制 + 独立进程 + fd 继承 | 需要核心支持从 fd 接管，而 sing-box 的 `PlatformInterface` 是**进程内**接口；跨进程还要处理生命周期与低内存回收，复杂度更高 |
| tun2socks 桥接 | 多一层转发、多一份依赖；出站保护（`protect`）仍要自己处理，等于把问题推迟而没有消除 |
| 在生成的配置里写 `file_descriptor` | **该字段不存在**（已核对 `option/tun.go`）。写进去会被核心按未知字段拒绝启动 |

## 实现前的检查清单

1. 用真实设备确认 sing-box 的 `libbox` 暴露了哪些 `//export` 入口
   （本仓库的参考源码里**没有** sing-box 的 Android 客户端实现，
   只有 `clients/android` 的文档目录；因此这一步必须对着真实 SDK 做）；
2. 确认 `PlatformInterface` 的完整方法集与当前版本一致
   （本 ADR 依据的 `adapter/platform.go` 是一个特定快照）；
3. 确认 `VpnService.protect` 的调用时机与 `AutoDetectInterfaceControl` 的对应关系；
4. 真机上验证「流量真的走了代理」，而不是只看 `VpnService` 是否建立。

**在上述 4 项完成之前，Android 的 TUN 必须继续标记为未实现。**
