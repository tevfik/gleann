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

	return unsafe.Slice((*byte)(unsafe.Pointer(addr)), size), nil
}

func munmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	return windows.UnmapViewOfFile(uintptr(unsafe.Pointer(&data[0])))
}
