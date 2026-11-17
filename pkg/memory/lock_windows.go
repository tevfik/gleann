//go:build windows

package memory

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFileShared takes a non-blocking shared byte-range lock via LockFileEx.
// Like flock on unix, the lock is tied to the file handle and Windows
// releases it when the process exits or is killed.
//
// The lock is on the same byte bbolt locks on Windows (offset 2^64-1, see
// bbolt's bolt_windows.go): byte-range locks only conflict when they
// overlap, so the guard's shared lock must cover that byte for an older
// binary's bbolt.Open to wait on it, as it does with flock on unix.
func lockFileShared(f *os.File) error {
	ol := bboltLockRange()
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &ol)
}

// lockFileExclusive takes a non-blocking exclusive byte-range lock, used for
// the migration lock (RFC 003 section 6.4 Memory step 1).
func lockFileExclusive(f *os.File) error {
	ol := bboltLockRange()
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &ol)
}

func unlockFile(f *os.File) error {
	ol := bboltLockRange()
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}

// bboltLockRange is the one-byte range at offset 2^64-1 that bbolt locks.
func bboltLockRange() windows.Overlapped {
	const m1 = ^uint32(0)
	return windows.Overlapped{Offset: m1, OffsetHigh: m1}
}
