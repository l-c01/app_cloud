//go:build !windows

package winproxy

// 非 Windows 上的通知实现。
//
// 它什么都不做，而且**这是正确的**：本包的注册表路径是
// HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings，
// 在非 Windows 上根本不会被走到（Runner 会是 UnavailableRunner，
// 写注册表会先失败）。因此这里返回 nil 不是「假装成功」，
// 而是「在这条路径不可达的前提下无事可做」。
//
// 需要的理由纯粹是编译：system_proxy.go 是跨平台的，
// Linux CI 作业会编译它，而 wininet 只能在 Windows 上加载。
type platformNotifier struct{}

// Notify 在非 Windows 上不做任何事。
func (platformNotifier) Notify() error { return nil }
