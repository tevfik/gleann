//go:build !unix && !windows

package store

import (
	"errors"
	"os"
)

// Platforms without flock or LockFileEx (none of the six supported targets)
// get a clean error instead of a silent no-op lock (AGENTS.md stub rule).
func lockFile(f *os.File) error {
	return errors.New("index locking is not supported on this platform")
}

func unlockFile(f *os.File) error {
	return errors.New("index locking is not supported on this platform")
}
