//go:build unix

package memory

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFileShared takes a non-blocking shared flock. Any number of memory
// store processes hold it at once, but an exclusive open (an old binary's
// bbolt.Open on the guard file) blocks until they close. The lock is tied to
// the open file description, so the kernel releases it when the process exits.
func lockFileShared(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB)
}

// lockFileExclusive takes a non-blocking exclusive flock, used for the
// migration lock (RFC 003 section 6.4 Memory step 1).
func lockFileExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
