//go:build darwin || linux

package main

import (
	"os"
	"syscall"
)

func acquireCLIUpdateLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	// 锁跟随文件描述符，进程退出即释放。保留锁文件，避免删除后出现两个 inode 各自持锁。
	return func() { _ = file.Close() }, nil
}
