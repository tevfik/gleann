//go:build windows

package memory

import (
	"os"
	"syscall"
)

// fileAttributes returns the Windows attribute word of an Lstat result.
func fileAttributes(fi os.FileInfo) uint32 {
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && d != nil {
		return d.FileAttributes
	}
	return 0
}
