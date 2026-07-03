package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstall_UnsupportedPlatform(t *testing.T) {
	// We cannot easily change runtime.GOOS, but we can test the logic 
	// if it were an unsupported platform. Since GOOS is a constant,
	// this is mostly for coverage on other platforms or future-proofing.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("skipping supported platform")
	}

	err := Install("", "")
	if err == nil {
		t.Error("expected error on unsupported platform")
	}
}

func TestUninstall_UnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("skipping supported platform")
	}

	err := Uninstall()
	if err == nil {
		t.Error("expected error on unsupported platform")
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Simulate running service via PID file.
	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)
	pidPath := filepath.Join(gleannD, "server.pid")
	pidData := []byte(`{"pid":` + itoa(os.Getpid()) + `,"addr":":8080","started":"2024-01-01T00:00:00Z"}`)
	os.WriteFile(pidPath, pidData, 0o644)

	err := Start("", "")
	if err == nil {
		t.Error("expected error when starting already running service")
	}
	if !contains(err.Error(), "already running") {
		t.Errorf("expected 'already running' in error, got %s", err.Error())
	}
}

func TestStart_DirCreationFailed(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a file where the .gleann directory should be to force MkdirAll failure.
	badPath := filepath.Join(tmpDir, ".gleann")
	os.WriteFile(badPath, []byte("not a dir"), 0o644)

	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	err := Start("", "")
	if err == nil {
		t.Error("expected error when creating gleann directory fails")
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
