//go:build unix

package store

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes a non-blocking exclusive flock. The lock is tied to the
// open file description, so the kernel releases it when the process exits.
func lockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
