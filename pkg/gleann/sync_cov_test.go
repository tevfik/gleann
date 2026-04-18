package gleann

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── FileSynchronizer ─────────────────────────────────────────────────────────

func TestLoadState_NewIndex(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSynchronizer(dir)
	state, err := fs.LoadState("testindex")
	if err != nil {
		t.Fatal(err)
	}
	if state.IndexName != "testindex" {
		t.Fatalf("expected testindex, got %q", state.IndexName)
	}
	if len(state.Files) != 0 {
		t.Fatal("expected empty files")
	}
}

func TestSaveAndLoadStateCov(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSynchronizer(dir)

	state := &SyncState{
		IndexName: "myindex",
		Files: map[string]*FileState{
			"file1.go": {Path: "file1.go", Hash: "abc123", Size: 100},
		},
		LastSync:   time.Now(),
		NextID:     5,
		TotalFiles: 1,
	}

	if err := fs.SaveState(state); err != nil {
		t.Fatal(err)
	}

	loaded, err := fs.LoadState("myindex")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NextID != 5 {
		t.Fatalf("expected NextID=5, got %d", loaded.NextID)
	}
	if _, ok := loaded.Files["file1.go"]; !ok {
		t.Fatal("expected file1.go in state")
	}
}

func TestLoadState_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSynchronizer(dir)
	// Write corrupt state file.
	stPath := filepath.Join(dir, "corrupt.sync.json")
	os.WriteFile(stPath, []byte("not json"), 0o644)
	_, err := fs.LoadState("corrupt")
	if err == nil {
		t.Fatal("expected error for corrupt state")
	}
}

func TestLoadState_NilFiles(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSynchronizer(dir)
	// Write state with null files.
	stPath := filepath.Join(dir, "nullfiles.sync.json")
	os.WriteFile(stPath, []byte(`{"files":null,"index_name":"nullfiles"}`), 0o644)
	state, err := fs.LoadState("nullfiles")
	if err != nil {
		t.Fatal(err)
	}
	if state.Files == nil {
		t.Fatal("expected non-nil files map")
	}
}

func TestDetectChanges_NewFilesCov(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main"), 0o644)

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		IndexName: "test",
		Files:     make(map[string]*FileState),
	}

	result, err := fs.DetectChanges(state, dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 {
		t.Fatalf("expected 1 added file, got %d", len(result.Added))
	}
}

func TestDetectChanges_ModifiedFile(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "mod.go")
	os.WriteFile(fpath, []byte("package old"), 0o644)

	info, _ := os.Stat(fpath)
	oldHash, _ := hashFile(fpath)

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		IndexName: "test",
		Files: map[string]*FileState{
			"mod.go": {
				Path:    "mod.go",
				Hash:    oldHash,
				Size:    info.Size(),
				ModTime: info.ModTime().Add(-1 * time.Second), // Force mod time diff.
			},
		},
	}

	// Modify the file.
	os.WriteFile(fpath, []byte("package new_content"), 0o644)

	result, err := fs.DetectChanges(state, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Modified) != 1 {
		t.Fatalf("expected 1 modified file, got %d", len(result.Modified))
	}
}

func TestDetectChanges_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		IndexName: "test",
		Files: map[string]*FileState{
			"deleted.go": {Path: "deleted.go", Hash: "old"},
		},
	}

	result, err := fs.DetectChanges(state, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deleted) != 1 {
		t.Fatalf("expected 1 deleted file, got %d", len(result.Deleted))
	}
}

func TestDetectChanges_SkipHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	hiddenDir := filepath.Join(dir, ".hidden")
	os.MkdirAll(hiddenDir, 0o755)
	os.WriteFile(filepath.Join(hiddenDir, "secret.go"), []byte("package hidden"), 0o644)
	os.WriteFile(filepath.Join(dir, "visible.go"), []byte("package main"), 0o644)

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{IndexName: "test", Files: make(map[string]*FileState)}

	result, err := fs.DetectChanges(state, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 {
		t.Fatalf("expected 1 added (hidden skipped), got %d", len(result.Added))
	}
}

func TestDetectChanges_ExtensionFilter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# README"), 0o644)

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{IndexName: "test", Files: make(map[string]*FileState)}

	result, err := fs.DetectChanges(state, dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 1 {
		t.Fatalf("expected 1 file (.go only), got %d", len(result.Added))
	}
}

func TestUpdateFileStateCov(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("package test"), 0o644)

	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		IndexName: "idx",
		Files:     make(map[string]*FileState),
		NextID:    0,
	}

	err := fs.UpdateFileState(state, dir, "test.go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if state.NextID != 3 {
		t.Fatalf("expected NextID=3, got %d", state.NextID)
	}
	entry := state.Files["test.go"]
	if entry == nil {
		t.Fatal("expected file state entry")
	}
	if len(entry.Passages) != 3 {
		t.Fatalf("expected 3 passages, got %d", len(entry.Passages))
	}
}

func TestUpdateFileState_FileNotExist(t *testing.T) {
	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{IndexName: "idx", Files: make(map[string]*FileState)}
	err := fs.UpdateFileState(state, "/nonexistent", "test.go", 1)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestRemoveFile(t *testing.T) {
	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		IndexName: "idx",
		Files: map[string]*FileState{
			"old.go": {Path: "old.go", Passages: []int64{1, 2, 3}},
		},
		TotalFiles: 1,
	}
	ids := fs.RemoveFile(state, "old.go")
	if len(ids) != 3 {
		t.Fatalf("expected 3 IDs, got %d", len(ids))
	}
	if state.TotalFiles != 0 {
		t.Fatalf("expected TotalFiles=0, got %d", state.TotalFiles)
	}
}

func TestRemoveFile_NotExist(t *testing.T) {
	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{IndexName: "idx", Files: make(map[string]*FileState)}
	ids := fs.RemoveFile(state, "nope.go")
	if ids != nil {
		t.Fatalf("expected nil, got %v", ids)
	}
}

func TestGetPassageIDs(t *testing.T) {
	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{
		Files: map[string]*FileState{
			"test.go": {Passages: []int64{10, 20}},
		},
	}
	ids := fs.GetPassageIDs(state, "test.go")
	if len(ids) != 2 {
		t.Fatalf("expected 2, got %d", len(ids))
	}
}

func TestGetPassageIDs_NotExist(t *testing.T) {
	fs := NewFileSynchronizer(t.TempDir())
	state := &SyncState{Files: make(map[string]*FileState)}
	ids := fs.GetPassageIDs(state, "nope.go")
	if ids != nil {
		t.Fatalf("expected nil, got %v", ids)
	}
}

func TestSyncResultHasChanges(t *testing.T) {
	r := &SyncResult{}
	if r.HasChanges() {
		t.Fatal("expected no changes")
	}
	r.Added = []string{"a.go"}
	if !r.HasChanges() {
		t.Fatal("expected changes")
	}
}

func TestSyncResultTotalChanged(t *testing.T) {
	r := &SyncResult{
		Added:    []string{"a.go"},
		Modified: []string{"b.go"},
		Deleted:  []string{"c.go", "d.go"},
	}
	if r.TotalChanged() != 4 {
		t.Fatalf("expected 4, got %d", r.TotalChanged())
	}
}

// ── hashFile ─────────────────────────────────────────────────────────────────

func TestHashFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(f, []byte("hello"), 0o644)
	hash, err := hashFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
}

func TestHashFile_NotExist(t *testing.T) {
	_, err := hashFile("/nonexistent/file")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── matchExtensions ──────────────────────────────────────────────────────────

func TestMatchExtensions_Empty(t *testing.T) {
	if !matchExtensions("test.go", nil) {
		t.Fatal("empty extensions should match all")
	}
}

func TestMatchExtensions_Match(t *testing.T) {
	if !matchExtensions("test.go", []string{".go", ".py"}) {
		t.Fatal("should match .go")
	}
}

func TestMatchExtensions_NoMatch(t *testing.T) {
	if matchExtensions("test.go", []string{".py", ".rs"}) {
		t.Fatal("should not match")
	}
}

func TestMatchExtensions_WithoutDot(t *testing.T) {
	if !matchExtensions("test.go", []string{"go"}) {
		t.Fatal("should match without leading dot")
	}
}

// ── PassageManager ───────────────────────────────────────────────────────────

func TestPassageManager_AddAndGet(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	_, err := pm.Add([]Item{
		{Text: "first passage", Metadata: map[string]interface{}{"source": "test.go"}},
		{Text: "second passage"},
	})
	if err != nil {
		t.Fatal(err)
	}

	p, err := pm.Get(0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != "first passage" {
		t.Fatalf("expected 'first passage', got %q", p.Text)
	}
}

func TestPassageManager_GetBatch(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	pm.Add([]Item{
		{Text: "one"},
		{Text: "two"},
		{Text: "three"},
	})

	passages, err := pm.GetBatch([]int64{0, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(passages) != 2 {
		t.Fatalf("expected 2, got %d", len(passages))
	}
}

func TestPassageManager_GetNotExist(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	_, err := pm.Get(999)
	if err == nil {
		t.Fatal("expected error for nonexistent passage")
	}
}

func TestPassageManager_All(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	pm.Add([]Item{
		{Text: "one"},
		{Text: "two"},
	})

	all := pm.All()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
}

func TestPassageManager_Count(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	pm.Add([]Item{{Text: "one"}})
	n := pm.Count()
	if n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}
}

func TestPassageManager_DeleteAll(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	pm.Add([]Item{{Text: "one"}, {Text: "two"}})
	err := pm.Delete()
	if err != nil {
		t.Fatal(err)
	}

	n := pm.Count()
	if n != 0 {
		t.Fatalf("expected 0 after delete, got %d", n)
	}
}

// ── doc_native: extractHTML ──────────────────────────────────────────────────

func TestHtmlToMarkdownCov(t *testing.T) {
	html := `<html><body><h1>Title</h1><p>Hello world</p><h2>Section</h2><p>Details</p></body></html>`
	result := htmlToMarkdown(html, "test.html")
	if result == "" {
		t.Fatal("expected non-empty markdown")
	}
}

// ── types: AttributesToJSON ──────────────────────────────────────────────────

func TestAttributesToJSON_NilMap(t *testing.T) {
	result, err := AttributesToJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "{}" {
		t.Fatalf("expected '{}', got %q", result)
	}
}

func TestAttributesToJSON_WithData(t *testing.T) {
	attrs := map[string]interface{}{"key": "value", "num": 42}
	result, err := AttributesToJSON(attrs)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["key"] != "value" {
		t.Fatalf("expected 'value', got %v", parsed["key"])
	}
}

// ── ListIndexes ──────────────────────────────────────────────────────────────

func TestListIndexes_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	indexes, err := ListIndexes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 0 {
		t.Fatalf("expected 0 indexes, got %d", len(indexes))
	}
}

func TestListIndexes_NonexistentDir(t *testing.T) {
	indexes, err := ListIndexes("/nonexistent/dir")
	if err != nil {
		t.Fatal(err) // ListIndexes may return empty on ENOENT
	}
	if len(indexes) != 0 {
		t.Fatalf("expected 0 indexes, got %d", len(indexes))
	}
}

func TestListIndexes_WithMetaFiles(t *testing.T) {
	dir := t.TempDir()
	idx1 := filepath.Join(dir, "project")
	os.MkdirAll(idx1, 0o755)
	meta := IndexMeta{Name: "project", Backend: "hnsw", EmbeddingModel: "nomic", NumPassages: 100}
	data, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(idx1, "project.meta.json"), data, 0o644)

	indexes, err := ListIndexes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(indexes))
	}
	if indexes[0].Name != "project" {
		t.Fatalf("expected 'project', got %q", indexes[0].Name)
	}
}

// ── NullSearcher ─────────────────────────────────────────────────────────────

func TestNullSearcher(t *testing.T) {
	ns := NullSearcher{}
	results, err := ns.Search(nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatal("expected no results")
	}
	if err := ns.Close(); err != nil {
		t.Fatal(err)
	}
}

// ── cosineSimilarity ─────────────────────────────────────────────────────────

func TestCosineSimilarity_Identical(t *testing.T) {
	v := []float32{1.0, 0.0, 0.0}
	score := cosineSimilarity(v, v)
	if score < 0.99 {
		t.Fatalf("expected ~1.0, got %f", score)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1.0, 0.0}
	b := []float32{0.0, 1.0}
	score := cosineSimilarity(a, b)
	if score > 0.01 {
		t.Fatalf("expected ~0.0, got %f", score)
	}
}

func TestCosineSimilarity_Empty(t *testing.T) {
	score := cosineSimilarity(nil, nil)
	if score != 0 {
		t.Fatalf("expected 0, got %f", score)
	}
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	a := []float32{1.0, 0.0}
	b := []float32{1.0, 0.0, 0.0}
	score := cosineSimilarity(a, b)
	// Should handle gracefully.
	_ = score
}
