//go:build !unix && !windows

package memory

import (
	"errors"
	"os"
)

// Platforms without flock or LockFileEx (none of the six supported targets)
// get a clean error instead of a silent no-op lock (AGENTS.md stub rule).
func lockFileShared(f *os.File) error {
	return errors.New("memory locking is not supported on this platform")
}

func lockFileExclusive(f *os.File) error {
	return errors.New("memory locking is not supported on this platform")
}

func unlockFile(f *os.File) error {
	return errors.New("memory locking is not supported on this platform")
}
