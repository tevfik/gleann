//go:build windows

package hnsw

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func mmapFile(fd uintptr, size int64) ([]byte, error) {
	if size == 0 {
		return nil, nil // Cannot map 0 bytes on Windows
	}

	hMap, err := windows.CreateFileMapping(windows.Handle(fd), nil, windows.PAGE_READONLY, 0, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateFileMapping failed: %w", err)
	}
	defer windows.CloseHandle(hMap)

	addr, err := windows.MapViewOfFile(hMap, windows.FILE_MAP_READ, 0, 0, uintptr(size))
	if err != nil {
		return nil, fmt.Errorf("MapViewOfFile failed: %w", err)
	}

	// MapViewOfFile returns the view address as a uintptr. Reading it back
	// through a pointer to the variable is the conversion go vet accepts
	// (unsafe.Pointer(addr) is reported as a possible misuse); the view is
	// not Go memory, so the garbage collector never moves it.
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	return unsafe.Slice((*byte)(ptr), size), nil
}

func munmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	return windows.UnmapViewOfFile(uintptr(unsafe.Pointer(&data[0])))
}
