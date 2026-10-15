//go:build windows

package winproxy

import (
	"fmt"
	"sync"
	"syscall"
)

// 本文件实现 Windows 上的「代理设置已变更」通知。
//
// 依据：wininet.dll 的 InternetSetOptionW。
//
//	INTERNET_OPTION_SETTINGS_CHANGED = 39
//	INTERNET_OPTION_REFRESH          = 37
//
// 两个调用是**互补**的，不是二选一：
//   - SETTINGS_CHANGED 告诉系统「注册表里的设置变了，重新读一遍」；
//   - REFRESH 让当前进程与已加载 wininet 的进程重建连接状态。
//
// 只调其中一个时，常见表现是「新开的程序走代理、已经开着的浏览器不走」，
// 或者反过来——取决于该应用何时读取设置。两个都调才能覆盖这两类。
//
// 为什么放在独立文件：它需要 windows 专属的 syscall，
// 而 system_proxy.go 是跨平台的（Linux CI 作业也会编译它）。
// 混在一起会让非 Windows 的构建失败。

var (
	wininetOnce sync.Once
	wininetDLL  *syscall.LazyDLL
	wininetErr  error
)

// internetSetOption 取自 wininet.dll。
//
// 用 syscall.NewLazyDLL 而不是直接 syscall.MustLoadDLL：
// 前者在找不到导出时才在调用点报错，而把「加载失败」推迟到调用点
// 可以让本包的构造过程在非 Windows 环境（例如 CI 的交叉编译检查）里照常进行。
const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

// platformNotifier 是 Windows 上的通知实现。
type platformNotifier struct{}

// Notify 调用 InternetSetOptionW 两次。
//
// 参数全为 nil 是有意的：这两个选项对当前用户生效，不需要指定句柄。
func (platformNotifier) Notify() error {
	wininetOnce.Do(func() {
		wininetDLL = syscall.NewLazyDLL("wininet.dll")
		if err := wininetDLL.Load(); err != nil {
			wininetErr = fmt.Errorf("winproxy: load wininet.dll: %w", err)
		}
	})
	if wininetErr != nil {
		return wininetErr
	}

	proc := wininetDLL.NewProc("InternetSetOptionW")
	if err := proc.Find(); err != nil {
		return fmt.Errorf("winproxy: wininet.dll has no InternetSetOptionW: %w", err)
	}

	for _, option := range []uintptr{internetOptionSettingsChanged, internetOptionRefresh} {
		// 返回值是 BOOL。按 Windows API 惯例，0 表示失败。
		//
		// 四个参数分别是 hInternet / dwOption / lpBuffer / dwBufferLength，
		// 全为 0（nil）是有意的：这两个选项对当前用户生效，不需要句柄，
		// 也不接受缓冲区。
		ret, _, callErr := proc.Call(0, option, 0, 0)
		if ret == 0 {
			// callErr 在 API 自身失败时可能是 "The operation completed
			// successfully."——这是 syscall 包的老毛病，因此只在它非空且
			// 不是那句废话时才带上它，否则给出一个可读的说明。
			if callErr != nil && callErr.Error() != "The operation completed successfully." {
				return fmt.Errorf("winproxy: InternetSetOptionW(%d) failed: %w", option, callErr)
			}
			return fmt.Errorf("winproxy: InternetSetOptionW(%d) returned FALSE", option)
		}
	}
	return nil
}
