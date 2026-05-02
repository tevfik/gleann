package embedding

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashText(t *testing.T) {
	h1 := HashText("hello world")
	h2 := HashText("hello world")
	h3 := HashText("different text")

	if h1 != h2 {
		t.Errorf("same input should produce same hash, got %s vs %s", h1, h2)
	}
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}
	if len(h1) != 64 { // SHA-256 hex = 64 chars
		t.Errorf("hash should be 64 hex chars, got %d", len(h1))
	}
}

func TestChunkMemoStore_HasAndRecord(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")
	s := NewChunkMemoStoreFromPath(path)

	if s.Has("abc123", "bge-m3") {
		t.Error("empty store should not have any fingerprints")
	}

	s.Record(ChunkFingerprint{
		TextHash:   "abc123",
		Model:      "bge-m3",
		Source:     "file.go",
		ChunkIndex: 0,
	})

	if !s.Has("abc123", "bge-m3") {
		t.Error("should find recorded fingerprint")
	}
	if s.Has("abc123", "different-model") {
		t.Error("should not match different model")
	}
	if s.Has("different-hash", "bge-m3") {
		t.Error("should not match different hash")
	}
}

func TestChunkMemoStore_FilterUncached(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")
	s := NewChunkMemoStoreFromPath(path)

	texts := []string{"chunk A", "chunk B", "chunk C", "chunk D"}
	model := "bge-m3"

	// Nothing cached yet — all should be uncached.
	uncached := s.FilterUncached(texts, model)
	if len(uncached) != 4 {
		t.Errorf("expected 4 uncached, got %d", len(uncached))
	}

	// Record chunks 0 and 2.
	s.Record(ChunkFingerprint{TextHash: HashText("chunk A"), Model: model, Source: "a.go", ChunkIndex: 0})
	s.Record(ChunkFingerprint{TextHash: HashText("chunk C"), Model: model, Source: "a.go", ChunkIndex: 2})

	uncached = s.FilterUncached(texts, model)
	if len(uncached) != 2 {
		t.Errorf("expected 2 uncached, got %d", len(uncached))
	}
	if uncached[0] != 1 || uncached[1] != 3 {
		t.Errorf("expected indices [1,3], got %v", uncached)
	}
}

func TestChunkMemoStore_RecordBatch(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")
	s := NewChunkMemoStoreFromPath(path)

	texts := []string{"alpha", "beta", "gamma"}
	s.RecordBatch(texts, "bge-m3", "src.go")

	if s.Len() != 3 {
		t.Errorf("expected 3 fingerprints, got %d", s.Len())
	}

	// All should be cached now.
	uncached := s.FilterUncached(texts, "bge-m3")
	if len(uncached) != 0 {
		t.Errorf("expected 0 uncached after RecordBatch, got %d", len(uncached))
	}

	// Different model = all uncached.
	uncached = s.FilterUncached(texts, "other-model")
	if len(uncached) != 3 {
		t.Errorf("expected 3 uncached for different model, got %d", len(uncached))
	}
}

func TestChunkMemoStore_RemoveBySource(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")
	s := NewChunkMemoStoreFromPath(path)

	s.RecordBatch([]string{"a", "b"}, "bge-m3", "file1.go")
	s.RecordBatch([]string{"c", "d"}, "bge-m3", "file2.go")

	if s.Len() != 4 {
		t.Fatalf("expected 4, got %d", s.Len())
	}

	removed := s.RemoveBySource([]string{"file1.go"})
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if s.Len() != 2 {
		t.Errorf("expected 2 remaining, got %d", s.Len())
	}
}

func TestChunkMemoStore_RemoveByModel(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")
	s := NewChunkMemoStoreFromPath(path)

	s.RecordBatch([]string{"x", "y"}, "model-a", "src.go")
	s.RecordBatch([]string{"z"}, "model-b", "src.go")

	if s.Len() != 3 {
		t.Fatalf("expected 3, got %d", s.Len())
	}

	removed := s.RemoveByModel("model-a")
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if s.Len() != 1 {
		t.Errorf("expected 1 remaining, got %d", s.Len())
	}
}

func TestChunkMemoStore_SaveAndReload(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")

	// Create and populate.
	s1 := NewChunkMemoStoreFromPath(path)
	s1.RecordBatch([]string{"hello", "world"}, "bge-m3", "test.go")
	if err := s1.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Reload from disk.
	s2 := NewChunkMemoStoreFromPath(path)
	if s2.Len() != 2 {
		t.Errorf("reloaded store should have 2 entries, got %d", s2.Len())
	}
	if !s2.Has(HashText("hello"), "bge-m3") {
		t.Error("reloaded store should have 'hello' fingerprint")
	}
}

func TestChunkMemoStore_SaveCreatesDirectory(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "subdir", "deep", "test.chunk_memo.json")

	s := NewChunkMemoStoreFromPath(path)
	s.Record(ChunkFingerprint{TextHash: "abc", Model: "m", Source: "s"})
	if err := s.Save(); err != nil {
		t.Fatalf("save should create directories: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("file should exist after save")
	}
}

func TestChunkMemoStore_Stats(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")
	s := NewChunkMemoStoreFromPath(path)

	s.RecordBatch([]string{"a", "b"}, "model-x", "file1.go")
	s.RecordBatch([]string{"c"}, "model-y", "file2.go")

	stats := s.Stats()
	if stats.TotalFingerprints != 3 {
		t.Errorf("expected 3 total, got %d", stats.TotalFingerprints)
	}
	if stats.ByModel["model-x"] != 2 {
		t.Errorf("model-x should have 2, got %d", stats.ByModel["model-x"])
	}
	if stats.ByModel["model-y"] != 1 {
		t.Errorf("model-y should have 1, got %d", stats.ByModel["model-y"])
	}
	if stats.BySource["file1.go"] != 2 {
		t.Errorf("file1.go should have 2, got %d", stats.BySource["file1.go"])
	}
}

func TestChunkMemoStore_EmptyFilterUncached(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")
	s := NewChunkMemoStoreFromPath(path)

	uncached := s.FilterUncached(nil, "bge-m3")
	if len(uncached) != 0 {
		t.Errorf("nil texts should return empty slice, got %d", len(uncached))
	}

	uncached = s.FilterUncached([]string{}, "bge-m3")
	if len(uncached) != 0 {
		t.Errorf("empty texts should return empty slice, got %d", len(uncached))
	}
}

func TestChunkMemoStore_ConcurrentAccess(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.chunk_memo.json")
	s := NewChunkMemoStoreFromPath(path)

	// Concurrent writes should not panic.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			s.RecordBatch([]string{"text"}, "model", "src.go")
			_ = s.FilterUncached([]string{"text"}, "model")
			_ = s.Has(HashText("text"), "model")
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestNewChunkMemoStore_IndexDirConstructor(t *testing.T) {
	tmp := t.TempDir()
	indexDir := filepath.Join(tmp, "indexes", "myidx")
	os.MkdirAll(indexDir, 0755)

	s := NewChunkMemoStore(filepath.Join(tmp, "indexes"), "myidx")
	s.RecordBatch([]string{"test"}, "model", "src.go")
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file was created at expected path.
	expectedPath := filepath.Join(tmp, "indexes", "myidx", "myidx.chunk_memo.json")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("expected memo file at %s", expectedPath)
	}

	// Reload and verify.
	s2 := NewChunkMemoStore(filepath.Join(tmp, "indexes"), "myidx")
	if s2.Len() != 1 {
		t.Errorf("expected 1 entry after reload, got %d", s2.Len())
	}
}
