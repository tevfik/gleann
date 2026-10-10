package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

func runCommandWithOutput(f func()) (string, string) {
	origExit := exitFunc
	defer func() { exitFunc = origExit }()
	exitFunc = func(code int) {
		panic(fmt.Sprintf("exit_%d", code))
	}

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				// caught exit
			}
		}()
		f()
	}()

	_ = wOut.Close()
	_ = wErr.Close()

	var bufOut, bufErr bytes.Buffer
	_, _ = io.Copy(&bufOut, rOut)
	_, _ = io.Copy(&bufErr, rErr)

	return bufOut.String(), bufErr.String()
}

func TestE2E_Manifest_SyncFastPath(t *testing.T) {
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	indexDir := filepath.Join(tmpDir, "indexes")
	_ = os.MkdirAll(docsDir, 0755)
	_ = os.MkdirAll(indexDir, 0755)

	f1 := filepath.Join(docsDir, "service.go")
	_ = os.WriteFile(f1, []byte("package service\n// Service implementation\nfunc RunService() string { return \"running\" }\n"), 0644)

	indexName := "manifest_e2e_idx"
	manifestPath := filepath.Join(indexDir, indexName, indexName+".manifest.json")

	// Set test environment config
	t.Setenv("GLEANN_INDEX_DIR", indexDir)

	// Step 1: Build the index using cmdBuild with --instant (offline/lexical)
	outBuild, _ := runCommandWithOutput(func() {
		cmdBuild([]string{
			indexName,
			"--docs", docsDir,
			"--index-dir", indexDir,
			"--mode", "code",
			"--instant",
			"--no-report",
			"--no-agents",
		})
	})

	if !strings.Contains(outBuild, "Instant lexical index") && !strings.Contains(outBuild, "Vector Index") {
		t.Fatalf("expected build success, got: %s", outBuild)
	}

	// Verify manifest was automatically generated and saved by cmdBuild
	m1, err := gleann.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("manifest was not generated at %s: %v", manifestPath, err)
	}
	if m1.RootHash == "" {
		t.Fatalf("expected non-empty RootHash in generated manifest")
	}
	if len(m1.Entries) != 1 {
		t.Fatalf("expected 1 entry in manifest, got %d", len(m1.Entries))
	}

	// Step 2: Run cmdSync immediately on untouched directory (Manifest Merkle fast path)
	outSync1, _ := runCommandWithOutput(func() {
		cmdSync([]string{
			indexName,
			"--docs", docsDir,
			"--index-dir", indexDir,
			"--mode", "code",
		})
	})

	if !strings.Contains(outSync1, "already up to date") {
		t.Errorf("expected 'already up to date' in sync output, got: %s", outSync1)
	}
	if !strings.Contains(outSync1, "Merkle root") {
		t.Errorf("expected Merkle root verification in sync output, got: %s", outSync1)
	}

	// Step 3: Modify file and run cmdSync
	_ = os.WriteFile(f1, []byte("package service\n// Service implementation v2\nfunc RunService() string { return \"running-v2\" }\n"), 0644)

	outSync2, _ := runCommandWithOutput(func() {
		cmdSync([]string{
			indexName,
			"--docs", docsDir,
			"--index-dir", indexDir,
			"--mode", "code",
		})
	})

	if !strings.Contains(outSync2, "Sync complete") && !strings.Contains(outSync2, "Vector index updated") && !strings.Contains(outSync2, "Lexical index updated") {
		t.Errorf("expected sync complete after file modification, got: %s", outSync2)
	}

	// Verify manifest root hash was updated
	m2, err := gleann.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("failed to reload manifest: %v", err)
	}
	if m2.RootHash == m1.RootHash {
		t.Errorf("expected new RootHash after file edit, got identical: %s", m2.RootHash)
	}

	// Step 4: Re-run sync -> should be up to date again
	outSync3, _ := runCommandWithOutput(func() {
		cmdSync([]string{
			indexName,
			"--docs", docsDir,
			"--index-dir", indexDir,
			"--mode", "code",
		})
	})

	if !strings.Contains(outSync3, "already up to date") {
		t.Errorf("expected 'already up to date' after sync, got: %s", outSync3)
	}
}
