//go:build windows

package store

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes a non-blocking exclusive byte-range lock via LockFileEx.
// Like flock on unix, the lock is tied to the file handle and Windows
// releases it when the process exits or is killed.
func lockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &ol)
}

func unlockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
