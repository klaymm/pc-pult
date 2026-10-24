//go:build !linux

package main

import "errors"

func stopService(cfgPath string) error { return nil }

func startService(cfgPath, logPath string) error {
	return errors.New("запуск службы поддерживается только на роутере")
}
