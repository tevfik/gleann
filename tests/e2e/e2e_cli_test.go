package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mockConfig is a completed setup that selects the mock embedding and LLM
// providers, so the E2E run needs no Ollama, no network and no model download.
const mockConfig = `{"completed": true, "provider": "mock", "embedding_provider": "mock", "embedding_model": "mock", "llm_provider": "mock", "llm_model": "mock"}`

// isolatedEnv maps every gleann/home lookup to a scratch directory so the test
// can never touch the real ~/.gleann, ~/.config or ~/.local (home-sentinel).
// It is applied to this test process with t.Setenv and handed to every child
// process explicitly, and every other GLEANN_* variable of the caller is
// dropped so a developer's own configuration cannot leak into the run.
func isolatedEnv(root string) map[string]string {
	return map[string]string{
		"HOME":               filepath.Join(root, "home"),
		"USERPROFILE":        filepath.Join(root, "home"),
		"XDG_CONFIG_HOME":    filepath.Join(root, "xdg-config"),
		"XDG_DATA_HOME":      filepath.Join(root, "xdg-data"),
		"GLEANN_CONFIG_DIR":  filepath.Join(root, "gleann-config"),
		"GLEANN_DATA_DIR":    filepath.Join(root, "gleann-data"),
		"GLEANN_MEMORY_DIR":  filepath.Join(root, "gleann-data", "memory"),
		"GLEANN_INDEX_DIR":   filepath.Join(root, "gleann-data", "indices"),
		"GLEANN_REMOTE_ADDR": "off",
	}
}

// childEnv returns the process environment with the isolated variables
// overridden and every other GLEANN_* variable removed.
func childEnv(isolated map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(isolated))
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, pinned := isolated[key]; pinned || strings.HasPrefix(key, "GLEANN_") {
			continue
		}
		out = append(out, kv)
	}
	for key, value := range isolated {
		out = append(out, key+"="+value)
	}
	return out
}

// resolveBinary returns a gleann binary to drive: a prebuilt build/gleann-full
// or build/gleann when one exists, otherwise a fresh `go build ./cmd/gleann`
// in a temporary directory. A failed build is a failure, never a skip: CI must
// actually run this test.
func resolveBinary(t *testing.T, repoRoot string) string {
	t.Helper()

	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	for _, name := range []string{"gleann-full", "gleann"} {
		path := filepath.Join(repoRoot, "build", name+exe)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	bin := filepath.Join(t.TempDir(), "gleann-e2e"+exe)
	build := exec.Command("go", "build", "-o", bin, "./cmd/gleann")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building ./cmd/gleann for the E2E run: %v\n%s", err, out)
	}
	return bin
}

// TestE2ECLI_IndexAndSearch tests the basic CLI workflow of building an index and searching it.
// This is a native Go E2E test designed to gradually replace the bash-based run.sh script.
// It builds the binary itself when build/ does not have one, so `go test ./...` in CI
// exercises the real CLI instead of skipping.
func TestE2ECLI_IndexAndSearch(t *testing.T) {
	// Skip if running in short mode
	if testing.Short() {
		t.Skip("Skipping E2E tests in short mode")
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("Failed to determine repo root: %v", err)
	}
	binaryPath := resolveBinary(t, repoRoot)

	// Isolate every home, XDG and GLEANN lookup in a scratch directory.
	root := t.TempDir()
	isolated := isolatedEnv(root)
	for key, value := range isolated {
		t.Setenv(key, value)
	}

	// Provide a mock config for the embedding provider so it doesn't need Ollama.
	// It is written both to the explicit config dir and to the default
	// $HOME/.gleann location, whichever one the CLI resolves.
	for _, dir := range []string{isolated["GLEANN_CONFIG_DIR"], filepath.Join(isolated["HOME"], ".gleann")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("Failed to create config dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(mockConfig), 0o644); err != nil {
			t.Fatalf("Failed to write mock config: %v", err)
		}
	}

	indexName := "e2e-test-index"
	fixturesDir := filepath.Join(repoRoot, "tests", "e2e", "fixtures", "docs")

	// run executes the CLI with the isolated environment and returns its output.
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(binaryPath, args...)
		cmd.Env = childEnv(isolated)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("gleann %s failed: %v\nOutput: %s", strings.Join(args, " "), err, out.String())
		}
		return out.String()
	}

	t.Run("IndexBuild", func(t *testing.T) {
		output := run(t, "index", "build", indexName, "--docs", fixturesDir)
		if !strings.Contains(output, "Vector Index") {
			t.Errorf("Expected 'Vector Index' in output, got: %s", output)
		}
	})

	t.Run("IndexList", func(t *testing.T) {
		output := run(t, "index", "list")
		if !strings.Contains(output, indexName) {
			t.Errorf("Expected index list to contain %q, got: %s", indexName, output)
		}
	})

	t.Run("Search", func(t *testing.T) {
		// Searching for "permafrost" which is in fixtures
		output := run(t, "search", indexName, "permafrost")
		if !strings.Contains(output, "permafrost") {
			t.Errorf("Expected search results to contain 'permafrost', got: %s", output)
		}
	})

	t.Run("MemoryIsIsolated", func(t *testing.T) {
		// The memory store lives in the scratch directory, never in the real home.
		output := run(t, "memory", "remember", "e2e isolation probe")
		if !strings.Contains(output, "Stored") && !strings.Contains(output, "e2e") {
			t.Errorf("Expected memory remember to report the stored fact, got: %s", output)
		}
		db := filepath.Join(isolated["GLEANN_MEMORY_DIR"])
		entries, err := os.ReadDir(db)
		if err != nil {
			t.Fatalf("memory dir %s was not created: %v", db, err)
		}
		if len(entries) == 0 {
			t.Errorf("memory dir %s is empty after writing a memory", db)
		}
	})
}
