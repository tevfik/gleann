package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/modules/chunking"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── Feature #1: Chunk Memoization Integration ──

func TestChunkMemo_IncrementalRebuild(t *testing.T) {
	dir := t.TempDir()

	// Create a memo store.
	memo := embedding.NewChunkMemoStore(dir, "test-idx")

	texts := []string{
		"func main() { fmt.Println(\"hello\") }",
		"func helper() { return }",
		"var globalConfig = Config{}",
	}
	model := "bge-m3"

	// Initially, all should be uncached.
	uncached := memo.FilterUncached(texts, model)
	if len(uncached) != 3 {
		t.Fatalf("expected 3 uncached on first run, got %d", len(uncached))
	}

	// Record all as embedded.
	memo.RecordBatch(texts, model, "main.go")
	if err := memo.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Simulate incremental rebuild: only the changed text needs re-embedding.
	updatedTexts := []string{
		"func main() { fmt.Println(\"hello\") }", // unchanged
		"func helper() { return nil }",           // CHANGED
		"var globalConfig = Config{}",            // unchanged
	}

	uncached = memo.FilterUncached(updatedTexts, model)
	if len(uncached) != 1 {
		t.Fatalf("expected 1 uncached (only changed text), got %d", len(uncached))
	}
	if uncached[0] != 1 {
		t.Errorf("expected index 1 (helper) to be uncached, got %d", uncached[0])
	}

	// Verify savings: 2/3 chunks saved = 66% reduction.
	savedPct := float64(len(texts)-len(uncached)) / float64(len(texts)) * 100
	if savedPct < 60 {
		t.Errorf("expected >60%% savings, got %.0f%%", savedPct)
	}
}

func TestChunkMemo_SourceDeletion(t *testing.T) {
	dir := t.TempDir()
	memo := embedding.NewChunkMemoStore(dir, "test-idx")

	// Record chunks from two files.
	memo.RecordBatch([]string{"a", "b"}, "model", "file1.go")
	memo.RecordBatch([]string{"c", "d"}, "model", "file2.go")

	// "Delete" file1.go — remove its fingerprints.
	removed := memo.RemoveBySource([]string{"file1.go"})
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}

	// file2.go chunks should still be cached.
	uncached := memo.FilterUncached([]string{"c", "d"}, "model")
	if len(uncached) != 0 {
		t.Errorf("file2.go chunks should still be cached, got %d uncached", len(uncached))
	}
}

// ── Feature #5: Provenance Integration ──

func TestProvenance_SentenceSplitterEndToEnd(t *testing.T) {
	fullText := "Line one of the document. Line two continues here. Line three ends the file."
	splitter := chunking.NewSentenceSplitter(40, 10)

	chunks := splitter.ChunkWithProvenance(fullText, map[string]any{
		"source": "readme.md",
	})

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	for i, c := range chunks {
		// Every chunk should have provenance metadata.
		if c.Metadata["source"] != "readme.md" {
			t.Errorf("chunk %d: source not set", i)
		}
		startByte, ok := c.Metadata["start_byte"].(int)
		if !ok {
			t.Errorf("chunk %d: start_byte not int", i)
			continue
		}
		endByte, ok := c.Metadata["end_byte"].(int)
		if !ok {
			t.Errorf("chunk %d: end_byte not int", i)
			continue
		}

		// Byte offsets should be within bounds.
		if startByte < 0 || endByte > len(fullText) || startByte >= endByte {
			t.Errorf("chunk %d: invalid byte range [%d, %d) for text len %d", i, startByte, endByte, len(fullText))
		}
	}
}

func TestProvenance_CodeChunkerEndToEnd(t *testing.T) {
	code := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}

func helper() int {
	return 42
}
`
	chunker := chunking.NewCodeChunker(80, 20)
	chunks := chunker.ChunkWithProvenance(code, map[string]any{
		"source":   "main.go",
		"language": "go",
	})

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	for i, c := range chunks {
		startLine, ok := c.Metadata["start_line"].(int)
		if !ok {
			t.Errorf("chunk %d: missing start_line", i)
		}
		if startLine < 1 {
			t.Errorf("chunk %d: line numbers should be 1-based, got %d", i, startLine)
		}
		if c.Metadata["language"] != "go" {
			t.Errorf("chunk %d: language metadata not preserved", i)
		}
	}
}

// ── Feature #6: Reconciliation Integration ──

func TestReconciliation_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	docsRoot := filepath.Join(dir, "docs")
	os.MkdirAll(docsRoot, 0755)

	// Create source files.
	os.WriteFile(filepath.Join(docsRoot, "keep.go"), []byte("package keep"), 0644)
	os.WriteFile(filepath.Join(docsRoot, "delete.go"), []byte("package delete"), 0644)

	// Build an index with passages from both files.
	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.EmbeddingModel = "mock"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 32}
	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("new builder: %v", err)
	}

	items := []gleann.Item{
		{Text: "code from keep.go", Metadata: map[string]any{"source": "keep.go"}},
		{Text: "code from delete.go", Metadata: map[string]any{"source": "delete.go"}},
		{Text: "more from delete.go", Metadata: map[string]any{"source": "delete.go"}},
	}

	if err := builder.Build(context.Background(), "test-recon", items); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Simulate file deletion.
	os.Remove(filepath.Join(docsRoot, "delete.go"))

	// Run reconciliation check.
	basePath := filepath.Join(dir, "test-recon", "test-recon")
	pm := gleann.NewPassageManager(basePath)
	if err := pm.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pm.Close()

	orphans, total := gleann.ReconcilePassageManager(pm, docsRoot)

	if total != 3 {
		t.Errorf("expected 3 total passages, got %d", total)
	}
	if len(orphans) != 1 || orphans[0] != "delete.go" {
		t.Errorf("expected orphan [delete.go], got %v", orphans)
	}
}

// ── Feature #7: Cache Invalidation Integration ──

func TestCacheInvalidation_ModelSwitch(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	os.MkdirAll(cacheDir, 0755)

	// Simulate L2 cache files from old model.
	for i := 0; i < 5; i++ {
		name := embedding.HashText("text"+string(rune('0'+i))) + ".bin"
		os.WriteFile(filepath.Join(cacheDir, name), []byte("fake-vector"), 0644)
	}

	// Create memo store with old model entries.
	memo := embedding.NewChunkMemoStore(dir, "test-idx")
	memo.RecordBatch([]string{"a", "b", "c"}, "old-model", "src.go")
	memo.RecordBatch([]string{"x"}, "new-model", "other.go") // should survive
	memo.Save()

	// Trigger invalidation.
	result, err := embedding.InvalidateOnModelChange("old-model", "new-model", cacheDir, memo)
	if err != nil {
		t.Fatalf("invalidation failed: %v", err)
	}

	if !result.Changed {
		t.Error("model change should be detected")
	}
	if result.InvalidatedL2 != 5 {
		t.Errorf("expected 5 L2 files removed, got %d", result.InvalidatedL2)
	}
	if result.InvalidatedMemo != 3 {
		t.Errorf("expected 3 old memo entries removed, got %d", result.InvalidatedMemo)
	}

	// new-model entries should survive.
	if memo.Len() != 1 {
		t.Errorf("new-model entry should survive, got %d entries", memo.Len())
	}
}

func TestCacheInvalidation_SameModel_NoAction(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	os.MkdirAll(cacheDir, 0755)

	os.WriteFile(filepath.Join(cacheDir, "abc.bin"), []byte("v"), 0644)

	memo := embedding.NewChunkMemoStore(dir, "test-idx")
	memo.RecordBatch([]string{"x"}, "bge-m3", "src.go")

	result, err := embedding.InvalidateOnModelChange("bge-m3", "bge-m3", cacheDir, memo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Changed {
		t.Error("same model should not trigger invalidation")
	}
	if result.InvalidatedL2 != 0 || result.InvalidatedMemo != 0 {
		t.Error("nothing should be invalidated for same model")
	}

	// Cache file and memo entry should both still exist.
	if _, err := os.Stat(filepath.Join(cacheDir, "abc.bin")); os.IsNotExist(err) {
		t.Error("cache file should not be removed")
	}
	if memo.Len() != 1 {
		t.Error("memo entry should not be removed")
	}
}

// ── Cross-Feature Integration ──

func TestMemoAndProvenance_Combined(t *testing.T) {
	// Test that chunk memoization and provenance work together:
	// provenance-enriched chunks should be cacheable by their text hash.
	dir := t.TempDir()
	memo := embedding.NewChunkMemoStore(dir, "combined")

	fullText := "First paragraph of the document. Second paragraph continues."
	splitter := chunking.NewSentenceSplitter(40, 10)
	chunks := splitter.ChunkWithProvenance(fullText, map[string]any{"source": "doc.txt"})

	// Extract texts and record in memo.
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	memo.RecordBatch(texts, "bge-m3", "doc.txt")

	// On rebuild, unchanged chunks should be cached.
	uncached := memo.FilterUncached(texts, "bge-m3")
	if len(uncached) != 0 {
		t.Errorf("all chunks should be cached after record, got %d uncached", len(uncached))
	}

	// Verify provenance metadata is still there.
	for i, c := range chunks {
		if _, ok := c.Metadata["start_byte"]; !ok {
			t.Errorf("chunk %d: provenance data missing after memoization", i)
		}
	}
}
