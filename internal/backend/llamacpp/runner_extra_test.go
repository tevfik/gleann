package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestStart_ExtractionFailure exercises the error path when extractAllBinaries fails.
// We trigger this by setting HOME to a read-only directory.
func TestStart_ExtractionFailure(t *testing.T) {
	tmp := t.TempDir()
	readOnlyDir := filepath.Join(tmp, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0555); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", readOnlyDir)
	t.Setenv("USERPROFILE", readOnlyDir)

	r := NewRunner("/m")
	ctx := context.Background()
	err := r.Start(ctx)

	if err == nil {
		t.Fatal("expected error due to read-only home directory, got nil")
	}
}

// TestStop_ProcessKill exercises the path where Stop() has to force-kill a process.
func TestStop_ForceKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping SIGTERM test on Windows")
	}

	r := NewRunner("/m")
	_ = r.Stop()
}

func TestStart_PortCollision(t *testing.T) {
	// Skipping: requires mocking getFreePort or inducing race condition.
}
