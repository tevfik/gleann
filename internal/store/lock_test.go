package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLockInProcess(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "idx.lock")
	l1, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	// A second open of the same lock file conflicts even in one process:
	// flock/LockFileEx locks belong to the open handle, not the process.
	if _, err := AcquireLock(lockPath); err == nil || !strings.Contains(err.Error(), "held by another process") {
		t.Fatalf("second AcquireLock err = %v, want held-by-another-process", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	l2, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("AcquireLock after Release: %v", err)
	}
	l2.Release()
}
