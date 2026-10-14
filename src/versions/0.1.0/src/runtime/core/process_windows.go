//go:build windows

package core

import "os/exec"

// configureProcess 在 Windows 上不设置进程组。
func configureProcess(cmd *exec.Cmd) {}

// terminate 在 Windows 上终止核心进程。
//
// 重要限制（必须反映到设计与用户文档中）：
//   Windows 没有与 SIGTERM 等价的、可由普通进程发送的优雅终止信号，
//   因此这里只能直接结束进程。这意味着**不能依赖核心自身完成网络清理**——
//   系统代理、路由、DNS 的恢复必须由客户端自己负责，并由持久化恢复日志保障
//   （见设计基线 §7.2）。
//
// 未来若需要更温和的终止，可考虑向核心的管理接口发送停止指令，
// 但那属于能力探测范围，且不是所有版本都提供。
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
