//go:build darwin

package main

import "syscall"

// ioctlReadTermios 是 darwin 上读取终端属性的 ioctl 请求号（TIOCGETA）。
const ioctlReadTermios = syscall.TIOCGETA
