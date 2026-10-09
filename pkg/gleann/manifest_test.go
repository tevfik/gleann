package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifest_BuildAndDeterministicRootHash(t *testing.T) {
	tmpDir := t.TempDir()

	f1 := filepath.Join(tmpDir, "file1.go")
	f2 := filepath.Join(tmpDir, "file2.txt")
	_ = os.WriteFile(f1, []byte("package main\nfunc A() {}\n"), 0644)
	_ = os.WriteFile(f2, []byte("Hello world notes\n"), 0644)

	files := []string{f1, f2}

	m1, err := BuildManifest("test-idx", tmpDir, files)
	if err != nil {
		t.Fatalf("BuildManifest failed: %v", err)
	}

	if len(m1.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m1.Entries))
	}
	if m1.RootHash == "" {
		t.Fatalf("expected non-empty RootHash")
	}

	// Rebuild on same files should produce exact same RootHash
	m2, err := BuildManifest("test-idx", tmpDir, files)
	if err != nil {
		t.Fatalf("BuildManifest 2 failed: %v", err)
	}

	if m1.RootHash != m2.RootHash {
		t.Errorf("expected deterministic RootHash, got %s vs %s", m1.RootHash, m2.RootHash)
	}
}

func TestManifest_DiffAddedModifiedDeleted(t *testing.T) {
	tmpDir := t.TempDir()

	f1 := filepath.Join(tmpDir, "a.go")
	f2 := filepath.Join(tmpDir, "b.go")
	f3 := filepath.Join(tmpDir, "c.go")
	_ = os.WriteFile(f1, []byte("package a\n"), 0644)
	_ = os.WriteFile(f2, []byte("package b\n"), 0644)

	mOriginal, err := BuildManifest("test-idx", tmpDir, []string{f1, f2})
	if err != nil {
		t.Fatalf("BuildManifest failed: %v", err)
	}

	// Case 1: Identical files -> diff is empty
	diffIdentical := mOriginal.Diff(mOriginal)
	if !diffIdentical.IsEmpty() {
		t.Errorf("expected empty diff on identical manifest, got %+v", diffIdentical)
	}

	// Case 2: Modify f2, delete f1, add f3
	_ = os.WriteFile(f2, []byte("package b modified content\n"), 0644)
	_ = os.WriteFile(f3, []byte("package c new file\n"), 0644)

	mNew, err := BuildManifest("test-idx", tmpDir, []string{f2, f3})
	if err != nil {
		t.Fatalf("BuildManifest new failed: %v", err)
	}

	diff := mOriginal.Diff(mNew)
	if diff.IsEmpty() {
		t.Fatalf("expected non-empty diff")
	}

	// a.go was in mOriginal but not in mNew -> Deleted
	if len(diff.Deleted) != 1 || diff.Deleted[0] != "a.go" {
		t.Errorf("expected Deleted [a.go], got %v", diff.Deleted)
	}

	// b.go was modified -> Modified
	if len(diff.Modified) != 1 || diff.Modified[0] != "b.go" {
		t.Errorf("expected Modified [b.go], got %v", diff.Modified)
	}

	// c.go was added -> Added
	if len(diff.Added) != 1 || diff.Added[0] != "c.go" {
		t.Errorf("expected Added [c.go], got %v", diff.Added)
	}
}

func TestManifest_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "main.go")
	_ = os.WriteFile(f1, []byte("package main\n"), 0644)

	m, err := BuildManifest("test-idx", tmpDir, []string{f1})
	if err != nil {
		t.Fatalf("BuildManifest failed: %v", err)
	}

	manifestPath := filepath.Join(tmpDir, "test-idx.manifest.json")
	if err := SaveManifest(manifestPath, m); err != nil {
		t.Fatalf("SaveManifest failed: %v", err)
	}

	loaded, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}

	if loaded.RootHash != m.RootHash {
		t.Errorf("loaded RootHash = %s, want %s", loaded.RootHash, m.RootHash)
	}
	if len(loaded.Entries) != len(m.Entries) {
		t.Errorf("loaded entries count = %d, want %d", len(loaded.Entries), len(m.Entries))
	}
}

func TestManifest_QuickDiffDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "alpha.go")
	_ = os.WriteFile(f1, []byte("package alpha\n"), 0644)

	m1, err := BuildManifest("test-idx", tmpDir, []string{f1})
	if err != nil {
		t.Fatalf("BuildManifest failed: %v", err)
	}

	// Quick diff without changes
	diff1, newM, err := QuickDiffDirectory(m1, tmpDir, []string{f1})
	if err != nil {
		t.Fatalf("QuickDiffDirectory failed: %v", err)
	}
	if !diff1.IsEmpty() {
		t.Errorf("expected empty diff, got %+v", diff1)
	}
	if newM.RootHash != m1.RootHash {
		t.Errorf("expected same root hash, got %s vs %s", newM.RootHash, m1.RootHash)
	}

	// Modify alpha.go
	_ = os.WriteFile(f1, []byte("package alpha updated\n"), 0644)
	diff2, newM2, err := QuickDiffDirectory(m1, tmpDir, []string{f1})
	if err != nil {
		t.Fatalf("QuickDiffDirectory 2 failed: %v", err)
	}
	if diff2.IsEmpty() {
		t.Fatalf("expected non-empty diff after file modification")
	}
	if len(diff2.Modified) != 1 || diff2.Modified[0] != "alpha.go" {
		t.Errorf("expected modified [alpha.go], got %v", diff2.Modified)
	}
	if newM2.RootHash == m1.RootHash {
		t.Errorf("expected changed root hash after modification")
	}
}
