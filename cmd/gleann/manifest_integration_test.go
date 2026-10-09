package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestIntegration_Manifest_LifecycleAndDiff(t *testing.T) {
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	indexDir := filepath.Join(tmpDir, "indexes")
	_ = os.MkdirAll(docsDir, 0755)
	_ = os.MkdirAll(indexDir, 0755)

	f1 := filepath.Join(docsDir, "main.go")
	f2 := filepath.Join(docsDir, "utils.go")
	_ = os.WriteFile(f1, []byte("package main\nfunc Main() {}\n"), 0644)
	_ = os.WriteFile(f2, []byte("package main\nfunc Util() {}\n"), 0644)

	files := []string{f1, f2}
	indexName := "manifest_test_idx"
	manifestPath := filepath.Join(indexDir, indexName, indexName+".manifest.json")

	// 1. Build and save manifest
	m, err := gleann.BuildManifest(indexName, docsDir, files)
	if err != nil {
		t.Fatalf("BuildManifest failed: %v", err)
	}
	if err := gleann.SaveManifest(manifestPath, m); err != nil {
		t.Fatalf("SaveManifest failed: %v", err)
	}

	// 2. Load manifest from disk
	loaded, err := gleann.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}
	if loaded.RootHash != m.RootHash {
		t.Errorf("loaded root hash = %s, want %s", loaded.RootHash, m.RootHash)
	}

	// 3. Fast no-op diff
	diff, _, err := gleann.QuickDiffDirectory(loaded, docsDir, files)
	if err != nil {
		t.Fatalf("QuickDiffDirectory failed: %v", err)
	}
	if !diff.IsEmpty() {
		t.Errorf("expected empty diff on untouched directory, got %+v", diff)
	}

	// 4. Modify one file
	_ = os.WriteFile(f2, []byte("package main\nfunc UtilModified() {}\n"), 0644)
	diffMod, newM, err := gleann.QuickDiffDirectory(loaded, docsDir, files)
	if err != nil {
		t.Fatalf("QuickDiffDirectory after mod failed: %v", err)
	}
	if diffMod.IsEmpty() {
		t.Fatalf("expected non-empty diff after modification")
	}
	if len(diffMod.Modified) != 1 || diffMod.Modified[0] != "utils.go" {
		t.Errorf("expected modified [utils.go], got %v", diffMod.Modified)
	}
	if newM.RootHash == loaded.RootHash {
		t.Errorf("expected root hash to change after file update")
	}
}
