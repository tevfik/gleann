//go:build unix || darwin || linux || freebsd || openbsd || netbsd || dragonfly

package hnsw

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// releaseFileAfterLoad is false on Unix: the index file stays mapped (and
// open) until Close. Renaming a new generation over it is safe there; the
// mapping keeps the old inode alive. See mmap_windows.go for Windows.
const releaseFileAfterLoad = false

// mmapFile maps the first size bytes of f read-only.
func mmapFile(f *os.File, size int64) ([]byte, error) {
	data, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
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
