package llamacpp

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExtractBinary(t *testing.T) {
	// This test assumes download_binaries.sh filled bin/<os>-<arch>/.
	binName := "llama-server-" + runtime.GOOS + "-" + runtime.GOARCH

	// The embedded binary is Linux-only; skip on other platforms.
	if runtime.GOOS != "linux" {
		t.Skipf("Skipping: embedded binary %s is Linux-only", binName)
	}

	// Ensure the file is embedded
	_, err := embeddedBinaries.ReadFile(embeddedDir + "/" + binName)
	if err != nil {
		t.Skipf("Skipping test: embedded binary %s not found. Did you run download_binaries.sh?", binName)
	}

	// extractAllBinaries extracts to ~/.gleann/bin — redirect HOME to a temp dir.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	// extractAllBinaries extracts the main binary and any co-located .so libraries.
	// It returns the path to the main executable.
	extractedPath, err := extractAllBinaries(binName)
	if err != nil {
		t.Fatalf("Failed to extract binary: %v", err)
	}

	// Verify file exists and is executable
	info, err := os.Stat(extractedPath)
	if err != nil {
		t.Fatalf("Extracted file not found at %s: %v", extractedPath, err)
	}

	if info.Size() == 0 {
		t.Errorf("Extracted file %s is empty", extractedPath)
	}

	// Check if executable bit is set (0111) - skip on Windows
	if runtime.GOOS != "windows" {
		if info.Mode()&0111 == 0 {
			t.Errorf("Extracted file %s is not executable, mode: %v", extractedPath, info.Mode())
		}
	}

	t.Logf("Successfully extracted binary to %s (size: %d bytes)", extractedPath, info.Size())
}

// An older gleann left its llama-server and libraries in ~/.gleann/bin.
// Extraction must replace changed files and drop libraries the new build no
// longer ships, otherwise the old llama-server is used forever.
func TestExtractReplacesOldBinaries(t *testing.T) {
	binName := "llama-server-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS != "linux" {
		t.Skip("embedded binary is Linux-only")
	}
	want, err := embeddedBinaries.ReadFile(embeddedDir + "/" + binName)
	if err != nil {
		t.Skip("embedded binary not found; run download_binaries.sh")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".gleann", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, binName)
	if err := os.WriteFile(old, []byte("old llama-server"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "libllava_shared.so")
	if err := os.WriteFile(stale, []byte("old lib"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := extractAllBinaries(binName); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("old llama-server was not replaced (%d bytes, want %d)", len(got), len(want))
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale library %s was not removed (err=%v)", stale, err)
	}

	// A second run with identical content must not rewrite the file.
	before, _ := os.Stat(old)
	if _, err := extractAllBinaries(binName); err != nil {
		t.Fatalf("second extract: %v", err)
	}
	after, _ := os.Stat(old)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("unchanged binary was rewritten")
	}
}
