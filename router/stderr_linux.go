package main

import (
	"os"
	"syscall"
)

func redirectStderr(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	_ = syscall.Dup3(int(f.Fd()), 2, 0)
}
