package multimodal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The package runs with a private HOME (see TestMain): the description cache
// fallback under it must not be the user's real one.
func TestPackageRunsWithPrivateHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Clean(home), filepath.Clean(os.TempDir())) {
		t.Fatalf("HOME = %q, want a directory under %q", home, os.TempDir())
	}
}
