//go:build unix || darwin || linux || freebsd || openbsd || netbsd || dragonfly

package hnsw

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func mmapFile(fd uintptr, size int64) ([]byte, error) {
	data, err := unix.Mmap(int(fd), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap error: %w", err)
	}
	return data, nil
}

func munmapFile(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	return unix.Munmap(data)
}
