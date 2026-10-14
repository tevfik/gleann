package tui

import (
	"os"
	"runtime"
	"testing"
)

// The config file stores API keys and must not be world-readable, including
// when it already exists with looser permissions from an older version.
func TestSaveConfigIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(OnboardResult{OllamaAPIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	path := configPath()
	if err := os.Chmod(path, 0o644); err != nil { // simulate a legacy file
		t.Fatal(err)
	}
	if err := SaveConfig(OnboardResult{OllamaAPIKey: "k2"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config mode = %v, want 0600", perm)
	}
}
