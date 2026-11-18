package main

import "testing"

// 测试进程的标准输入不是终端（go test 默认非 TTY），stdinIsTerminal 必须返回 false。
// 这条覆盖了「ModeCharDevice 把 /dev/null 等字符设备误判为可交互」的回归。
func TestStdinIsTerminalFalseInNonTTY(t *testing.T) {
	if stdinIsTerminal() {
		t.Fatal("非终端的标准输入不应被判为终端")
	}
}
