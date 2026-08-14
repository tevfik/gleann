package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2ECLI_IndexAndSearch tests the basic CLI workflow of building an index and searching it.
// This is a native Go E2E test designed to gradually replace the bash-based run.sh script.
func TestE2ECLI_IndexAndSearch(t *testing.T) {
	// Skip if running in short mode
	if testing.Short() {
		t.Skip("Skipping E2E tests in short mode")
	}

	// Locate the gleann binary (or build it if not found)
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to determine repo root: %v", err)
	}

	// Check for gleann-full first, fallback to gleann
	binaryPath := filepath.Join(repoRoot, "build", "gleann-full")
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		binaryPath = filepath.Join(repoRoot, "build", "gleann")
		if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
			t.Skipf("Gleann binary not found at %s. Please run 'make build' first.", binaryPath)
		}
	}

	// Set up isolated config directory
	tempDir := t.TempDir()
	os.Setenv("GLEANN_CONFIG_DIR", tempDir)
	os.Setenv("HOME", tempDir) // for backward compat

	// Provide a mock config for the embedding provider so it doesn't need Ollama
	configJSON := `{"completed": true, "provider": "mock", "embedding_provider": "mock", "embedding_model": "mock", "llm_provider": "mock", "llm_model": "mock"}`
	if err := os.MkdirAll(filepath.Join(tempDir, ".gleann"), 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".gleann", "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("Failed to write mock config: %v", err)
	}

	indexName := "e2e-test-index"
	fixturesDir := filepath.Join(repoRoot, "tests", "e2e", "fixtures", "docs")

	t.Run("IndexBuild", func(t *testing.T) {
		cmd := exec.Command(binaryPath, "index", "build", indexName, "--docs", fixturesDir)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("Index build failed: %v\nOutput: %s", err, out.String())
		}

		output := out.String()
		if !strings.Contains(output, "Vector Index") {
			t.Errorf("Expected 'Vector Index' in output, got: %s", output)
		}
	})

	t.Run("IndexList", func(t *testing.T) {
		cmd := exec.Command(binaryPath, "index", "list")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("Index list failed: %v\nOutput: %s", err, out.String())
		}

		output := out.String()
		if !strings.Contains(output, indexName) {
			t.Errorf("Expected index list to contain %q, got: %s", indexName, output)
		}
	})

	t.Run("Search", func(t *testing.T) {
		// Searching for "permafrost" which is in fixtures
		cmd := exec.Command(binaryPath, "search", indexName, "permafrost")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("Search failed: %v\nOutput: %s", err, out.String())
		}

		output := out.String()
		if !strings.Contains(output, "permafrost") {
			t.Errorf("Expected search results to contain 'permafrost', got: %s", output)
		}
	})
}
