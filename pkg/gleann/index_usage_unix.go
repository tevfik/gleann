//go:build linux || darwin

package gleann

import "syscall"

// diskSpace returns the size of the file system holding path and the space its user can still write.
func diskSpace(path string) (total, free uint64) {
	var fs syscall.Statfs_t
	if syscall.Statfs(path, &fs) != nil {
		return 0, 0
	}
	bsize := uint64(fs.Bsize)
	return fs.Blocks * bsize, fs.Bavail * bsize
}
