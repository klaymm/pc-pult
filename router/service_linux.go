//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const servicePath = "/opt/sbin:/opt/bin:/opt/usr/sbin:/opt/usr/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// Processes

func serviceProcesses(cfgPath string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue
		}
		if matchesService(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"), cfgPath) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func stopService(cfgPath string) error {
	for _, pid := range serviceProcesses(cfgPath) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if len(serviceProcesses(cfgPath)) == 0 {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, pid := range serviceProcesses(cfgPath) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}

func startService(cfgPath, logPath string) error {
	if err := stopService(cfgPath); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	started := time.Now().Add(-time.Second)
	cmd := exec.Command(exe, "-supervise", "-config", cfgPath)
	cmd.Env = append(os.Environ(), "PATH="+servicePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st, err := os.Stat(logPath); err == nil && st.Size() > 0 && st.ModTime().After(started) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("служба не начала писать журнал " + logPath)
}
