//go:build windows

package gleann

import (
	"syscall"
	"unsafe"
)

// diskSpace returns the size of the volume holding path and the space its
// user can still write (GetDiskFreeSpaceExW).
func diskSpace(path string) (total, free uint64) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	var freeToCaller, totalBytes, totalFree uint64
	r, _, _ := proc.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, 0
	}
	return totalBytes, freeToCaller
}
