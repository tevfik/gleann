package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "gleann_server_test_*")
	if err != nil {
		panic(err)
	}

	origRemote := os.Getenv("GLEANN_REMOTE_ADDR")
	origMemDir := os.Getenv("GLEANN_MEMORY_DIR")
	origHome := os.Getenv("HOME")

	_ = os.Setenv("GLEANN_REMOTE_ADDR", "off")
	_ = os.Setenv("GLEANN_MEMORY_DIR", filepath.Join(tmpDir, "memory"))
	_ = os.Setenv("HOME", tmpDir)

	memory.ResetRemoteForTesting()

	code := m.Run()
	// A handle left open under tmpDir (typically a block-memory store opened
	// by a handler under test) makes the RemoveAll below fail on Windows.
	if open := openFilesUnderDir(tmpDir); len(open) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: %d file handle(s) still open at exit: %v\n", len(open), open)
		code = 1
	}

	if origRemote != "" {
		_ = os.Setenv("GLEANN_REMOTE_ADDR", origRemote)
	} else {
		_ = os.Unsetenv("GLEANN_REMOTE_ADDR")
	}
	if origMemDir != "" {
		_ = os.Setenv("GLEANN_MEMORY_DIR", origMemDir)
	} else {
		_ = os.Unsetenv("GLEANN_MEMORY_DIR")
	}
	if origHome != "" {
		_ = os.Setenv("HOME", origHome)
	}
	memory.ResetRemoteForTesting()

	// os.Exit skips deferred calls, so remove the temp dir explicitly.
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}
