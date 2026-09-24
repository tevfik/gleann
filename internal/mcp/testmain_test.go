package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "gleann_mcp_test_*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	origRemote := os.Getenv("GLEANN_REMOTE_ADDR")
	origMemDir := os.Getenv("GLEANN_MEMORY_DIR")
	origHome := os.Getenv("HOME")

	_ = os.Setenv("GLEANN_REMOTE_ADDR", "off")
	_ = os.Setenv("GLEANN_MEMORY_DIR", filepath.Join(tmpDir, "memory"))
	_ = os.Setenv("HOME", tmpDir)

	memory.ResetRemoteForTesting()

	code := m.Run()

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

	os.Exit(code)
}
