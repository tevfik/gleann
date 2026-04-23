//go:build !windows

package service

import (
	"os"
	"syscall"
)

func platformSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// isProcessAlive checks if a process with the given PID exists.
// On Unix, FindProcess always succeeds; Signal(0) checks existence.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
