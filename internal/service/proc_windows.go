//go:build windows

package service

import (
	"syscall"
	"unsafe"
)

func platformSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000008, // CREATE_NO_WINDOW
	}
}

// isProcessAlive checks if a process with the given PID exists on Windows.
// Signal(0) doesn't work on Windows, so we use OpenProcess + GetExitCodeProcess.
func isProcessAlive(pid int) bool {
	const (
		PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
		STILL_ACTIVE                      = 259
	)

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	getExitCodeProcess := kernel32.NewProc("GetExitCodeProcess")
	closeHandle := kernel32.NewProc("CloseHandle")

	handle, _, err := openProcess.Call(
		uintptr(PROCESS_QUERY_LIMITED_INFORMATION),
		0,
		uintptr(pid),
	)
	if handle == 0 {
		_ = err // permission denied or process not found
		return false
	}
	defer closeHandle.Call(handle)

	var exitCode uint32
	ret, _, _ := getExitCodeProcess.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	if ret == 0 {
		return false
	}
	return exitCode == STILL_ACTIVE
}
