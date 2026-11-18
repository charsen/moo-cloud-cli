//go:build linux

package main

import "syscall"

// ioctlReadTermios 是 linux 上读取终端属性的 ioctl 请求号（TCGETS）。
const ioctlReadTermios = syscall.TCGETS
