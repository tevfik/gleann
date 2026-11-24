package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The package runs with a private HOME (see TestMain): the vault database
// under it (DefaultDBPath) must not be the user's real one.
func TestPackageRunsWithPrivateHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Clean(home), filepath.Clean(os.TempDir())) {
		t.Fatalf("HOME = %q, want a directory under %q", home, os.TempDir())
	}
	if db := DefaultDBPath(); !strings.HasPrefix(filepath.Clean(db), filepath.Clean(home)) {
		t.Fatalf("DefaultDBPath() = %q, want it under HOME %q", db, home)
	}
}

// Directory overrides must not lead out of the private HOME (TestMain clears
// every GLEANN_*_DIR(S) variable).
func TestPackageHasNoDirOverrides(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "GLEANN_") || !(strings.HasSuffix(name, "_DIR") || strings.HasSuffix(name, "_DIRS")) {
			continue
		}
		if val != "" && !withinDir(home, val) {
			t.Errorf("%s = %q points outside the private HOME %q", name, val, home)
		}
	}
}

// withinDir reports whether path is dir or lies below it (a path-separator
// boundary: "/tmp/home2" is not within "/tmp/home").
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
