package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestAcquireLockWaitTimesOutThenSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.meta.lock")
	held, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := AcquireLockWait(path, 100*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("want ErrLockTimeout, got %v", err)
	}
	if time.Since(start) < 90*time.Millisecond {
		t.Fatal("did not wait")
	}
	go func() { time.Sleep(50 * time.Millisecond); _ = held.Release() }()
	l, err := AcquireLockWait(path, 5*time.Second)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = l.Release()
}
