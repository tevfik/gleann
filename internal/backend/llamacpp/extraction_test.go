package llamacpp

import (
	"os"
	"testing"
)

func TestExtractBinary(t *testing.T) {
	// This test assumes llama-server-linux-amd64 exists in bin/ due to the download script.
	binName := "llama-server-linux-amd64"

	// Ensure the file is embedded
	_, err := embeddedBinaries.ReadFile("bin/" + binName)
	if err != nil {
		t.Skipf("Skipping test: embedded binary %s not found. Did you run download_binaries.sh?", binName)
	}

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

	// Check if executable bit is set (0111)
	if info.Mode()&0111 == 0 {
		t.Errorf("Extracted file %s is not executable, mode: %v", extractedPath, info.Mode())
	}

	t.Logf("Successfully extracted binary to %s (size: %d bytes)", extractedPath, info.Size())
}
