//go:build darwin || linux

package main

import (
	"syscall"
	"unsafe"
)

// stdinIsTerminal 判断标准输入是否真的是终端。
// 不能用 ModeCharDevice：null 设备、zero 设备等也是字符设备，会被误判为可交互，
// 导致重定向输入或 CI 环境里误弹确认。这里用 ioctl 读取终端属性判定（失败即非终端）。
func stdinIsTerminal() bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(0), ioctlReadTermios, uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}
