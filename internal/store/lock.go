package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lock is an exclusive, OS-level lock on an index's lock file,
// <IndexDir>/<name>.lock (RFC 003 section 3.6). It serialises builds and
// syncs of one index across processes; searches and memory-style small
// writes do not take it. The lock belongs to the open file handle, so the
// operating system releases it when the holding process exits or is killed:
// no heartbeat row or stale-lock recovery is needed.
//
// The lock file lives beside the index directory, not inside it: the rebuild
// path renames the whole <name>/ directory aside, and on Windows a directory
// containing an open file cannot be renamed.
type Lock struct {
	f *os.File
}

// AcquireLock takes the exclusive lock at path, creating the lock file when
// missing. It fails fast with a readable message when another process holds
// the lock; it never waits.
func AcquireLock(path string) (*Lock, error) {
	// A first build takes the lock before the index directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating directory for lock file %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		// The lock file is <IndexDir>/<name>.lock, so its base name names the
		// index. Builds, syncs and migrations all take this same lock.
		name := strings.TrimSuffix(filepath.Base(path), ".lock")
		return nil, fmt.Errorf("index %q is locked: another gleann process is building or syncing it; "+
			"wait for it to finish or stop it (lock %s is held by another process): %w", name, path, err)
	}
	return &Lock{f: f}, nil
}

// ErrLockTimeout marks an AcquireLockWait that gave up while another process
// still held the lock.
var ErrLockTimeout = errors.New("lock held by another process")

// AcquireLockWait is AcquireLock that polls for up to wait before giving up
// with an error wrapping ErrLockTimeout. It is for short critical sections
// (a metadata read-modify-write), never for the index build lock.
func AcquireLockWait(path string, wait time.Duration) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating directory for lock file %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	for {
		lerr := lockFile(f)
		if lerr == nil {
			return &Lock{f: f}, nil
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w: waited %s for %s: %v", ErrLockTimeout, wait, path, lerr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Release unlocks and closes the lock file. The file itself is left in
// place; RemoveIndex deletes it together with the index's other companion
// files. The OS also releases the lock if the process dies without Release.
func (l *Lock) Release() error {
	return errors.Join(unlockFile(l.f), l.f.Close())
}
