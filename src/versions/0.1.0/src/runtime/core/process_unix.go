//go:build !windows

package core

import (
	"os/exec"
	"syscall"
)

// configureProcess 让子进程成为独立进程组组长。
//
// 目的：核心可能派生助手进程。把它们放进同一进程组后，
// 我们可以按组发送信号，避免核心退出后仍有孤儿进程占用本地端口。
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate 请求进程优雅退出（SIGTERM）。
//
// sing-box 响应 SIGTERM 并自行清理（关闭监听端口、恢复资源）。
// 若进程组不可用，则退回对单进程发送信号。
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		// 负 PID 表示整个进程组。
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}
