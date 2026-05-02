package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReconcilePassageManager_NoOrphans(t *testing.T) {
	tmp := t.TempDir()
	docsRoot := filepath.Join(tmp, "docs")
	os.MkdirAll(docsRoot, 0755)

	// Create source files.
	os.WriteFile(filepath.Join(docsRoot, "file1.go"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(docsRoot, "file2.go"), []byte("content"), 0644)

	// Set up passages DB.
	basePath := filepath.Join(tmp, "test_index")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	pm.Add([]Item{
		{Text: "chunk from file1", Metadata: map[string]any{"source": "file1.go"}},
		{Text: "chunk from file2", Metadata: map[string]any{"source": "file2.go"}},
	})

	orphans, total := ReconcilePassageManager(pm, docsRoot)

	if total != 2 {
		t.Errorf("expected 2 total passages, got %d", total)
	}
	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans (all files exist), got %v", orphans)
	}
}

func TestReconcilePassageManager_WithOrphans(t *testing.T) {
	tmp := t.TempDir()
	docsRoot := filepath.Join(tmp, "docs")
	os.MkdirAll(docsRoot, 0755)

	// Only file1 exists; file2 was deleted.
	os.WriteFile(filepath.Join(docsRoot, "file1.go"), []byte("content"), 0644)

	basePath := filepath.Join(tmp, "test_index")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	pm.Add([]Item{
		{Text: "chunk from file1", Metadata: map[string]any{"source": "file1.go"}},
		{Text: "chunk from file2", Metadata: map[string]any{"source": "file2.go"}},
		{Text: "another from file2", Metadata: map[string]any{"source": "file2.go"}},
	})

	orphans, total := ReconcilePassageManager(pm, docsRoot)

	if total != 3 {
		t.Errorf("expected 3 total passages, got %d", total)
	}
	if len(orphans) != 1 {
		t.Errorf("expected 1 orphan source, got %d: %v", len(orphans), orphans)
	}
	if len(orphans) == 1 && orphans[0] != "file2.go" {
		t.Errorf("expected orphan 'file2.go', got %q", orphans[0])
	}
}

func TestReconcilePassageManager_AllOrphans(t *testing.T) {
	tmp := t.TempDir()
	docsRoot := filepath.Join(tmp, "docs")
	os.MkdirAll(docsRoot, 0755)
	// No source files exist.

	basePath := filepath.Join(tmp, "test_index")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	pm.Add([]Item{
		{Text: "orphan1", Metadata: map[string]any{"source": "gone1.go"}},
		{Text: "orphan2", Metadata: map[string]any{"source": "gone2.go"}},
	})

	orphans, total := ReconcilePassageManager(pm, docsRoot)

	if total != 2 {
		t.Errorf("expected 2 total, got %d", total)
	}
	if len(orphans) != 2 {
		t.Errorf("expected 2 orphan sources, got %d", len(orphans))
	}
}

func TestReconcilePassageManager_NoSourceMetadata(t *testing.T) {
	tmp := t.TempDir()
	docsRoot := filepath.Join(tmp, "docs")
	os.MkdirAll(docsRoot, 0755)

	basePath := filepath.Join(tmp, "test_index")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	// Passages without source metadata.
	pm.Add([]Item{
		{Text: "no source", Metadata: map[string]any{}},
		{Text: "nil meta", Metadata: nil},
	})

	orphans, total := ReconcilePassageManager(pm, docsRoot)

	if total != 2 {
		t.Errorf("expected 2 total, got %d", total)
	}
	if len(orphans) != 0 {
		t.Errorf("passages without source should not be orphaned, got %v", orphans)
	}
}

func TestReconcilePassageManager_SubdirectoryPaths(t *testing.T) {
	tmp := t.TempDir()
	docsRoot := filepath.Join(tmp, "docs")
	os.MkdirAll(filepath.Join(docsRoot, "pkg", "util"), 0755)
	os.WriteFile(filepath.Join(docsRoot, "pkg", "util", "helper.go"), []byte("code"), 0644)

	basePath := filepath.Join(tmp, "test_index")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	pm.Add([]Item{
		{Text: "chunk", Metadata: map[string]any{"source": "pkg/util/helper.go"}},
		{Text: "orphan", Metadata: map[string]any{"source": "pkg/util/deleted.go"}},
	})

	orphans, total := ReconcilePassageManager(pm, docsRoot)

	if total != 2 {
		t.Errorf("expected 2 total, got %d", total)
	}
	if len(orphans) != 1 {
		t.Errorf("expected 1 orphan, got %d", len(orphans))
	}
}

func TestReconcilePassageManager_EmptyDB(t *testing.T) {
	tmp := t.TempDir()
	docsRoot := filepath.Join(tmp, "docs")
	os.MkdirAll(docsRoot, 0755)

	basePath := filepath.Join(tmp, "test_index")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	orphans, total := ReconcilePassageManager(pm, docsRoot)

	if total != 0 {
		t.Errorf("expected 0 total, got %d", total)
	}
	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans, got %d", len(orphans))
	}
}

func TestReconcilePassageManager_DuplicateSources(t *testing.T) {
	tmp := t.TempDir()
	docsRoot := filepath.Join(tmp, "docs")
	os.MkdirAll(docsRoot, 0755)
	// file1 doesn't exist → orphan checked only once.

	basePath := filepath.Join(tmp, "test_index")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	pm.Add([]Item{
		{Text: "chunk1 from file1", Metadata: map[string]any{"source": "file1.go"}},
		{Text: "chunk2 from file1", Metadata: map[string]any{"source": "file1.go"}},
		{Text: "chunk3 from file1", Metadata: map[string]any{"source": "file1.go"}},
	})

	orphans, total := ReconcilePassageManager(pm, docsRoot)

	if total != 3 {
		t.Errorf("expected 3 total, got %d", total)
	}
	if len(orphans) != 1 {
		t.Errorf("duplicate source should appear once in orphans, got %d", len(orphans))
	}
}

func TestReconcileResult_Defaults(t *testing.T) {
	r := &ReconcileResult{}
	if r.ScannedPassages != 0 || r.RemovedPassages != 0 || r.RemovedVectors != 0 {
		t.Error("zero values should be default")
	}
	if r.OrphanSources != nil {
		t.Error("nil orphan sources should be default")
	}
}
