//go:build windows

package main

import "syscall"

// stdinIsTerminal 判断标准输入是否真的是控制台。
// 用 GetConsoleMode 判定（成功即终端）；重定向时失败。
func stdinIsTerminal() bool {
	var mode uint32
	err := syscall.GetConsoleMode(syscall.Stdin, &mode)
	return err == nil
}
