package diskann

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskANN_MmapSearchAndUnmap(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(42))
	n := 40
	dims := 16

	embeddings := make([][]float32, n)
	for i := 0; i < n; i++ {
		embeddings[i] = make([]float32, dims)
		for d := 0; d < dims; d++ {
			embeddings[i][d] = rng.Float32()
		}
	}

	cfg := DiskANNConfig{
		R:              16,
		L:              30,
		Alpha:          1.2,
		PQDim:          4,
		PQCentroids:    16,
		SearchL:        30,
		DistanceMetric: DistanceL2,
		UseMmap:        true,
	}

	idx, err := BuildDiskIndex(embeddings, cfg)
	if err != nil {
		t.Fatalf("BuildDiskIndex: %v", err)
	}

	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.index")

	var buf bytes.Buffer
	if _, err := idx.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if err := os.WriteFile(indexPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	searcher := &Searcher{
		config: Config{
			DiskANNConfig: cfg,
		},
	}

	if err := searcher.LoadFromFile(ctx, indexPath); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if searcher.idx == nil {
		t.Fatal("expected searcher.idx != nil")
	}
	if searcher.idx.Embeddings != nil {
		t.Errorf("expected searcher.idx.Embeddings == nil in mmap mode, got len=%d", len(searcher.idx.Embeddings))
	}
	if searcher.idx.mmapData == nil {
		t.Fatal("expected searcher.idx.mmapData != nil")
	}

	// Verify GetVector from mmap matches original embedding
	v0 := searcher.idx.GetVector(0)
	if len(v0) != dims {
		t.Fatalf("expected len(v0)==%d, got %d", dims, len(v0))
	}
	for d := 0; d < dims; d++ {
		if v0[d] != embeddings[0][d] {
			t.Errorf("dim %d mismatch: got %v, want %v", d, v0[d], embeddings[0][d])
		}
	}

	// Search
	query := embeddings[0]
	ids, dists, err := searcher.Search(ctx, query, 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("expected non-empty search results")
	}
	if ids[0] != 0 {
		t.Errorf("top-1 result should be 0 (exact match), got %d", ids[0])
	}
	if dists[0] > 1e-4 {
		t.Errorf("top-1 distance should be ~0, got %f", dists[0])
	}

	// Close must unmap and release file handle so file can be renamed or deleted
	if err := searcher.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify unmapping by renaming the file (fails on Windows if still mapped/open)
	renamedPath := filepath.Join(tmpDir, "test.index.renamed")
	if err := os.Rename(indexPath, renamedPath); err != nil {
		t.Fatalf("os.Rename failed after Close (mapping or handle still open): %v", err)
	}
	if err := os.Remove(renamedPath); err != nil {
		t.Fatalf("os.Remove failed: %v", err)
	}

	// Double close should be a no-op
	if err := searcher.Close(); err != nil {
		t.Errorf("double Close failed: %v", err)
	}
}

func TestDiskANN_MmapInvalidFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Truncated file
	truncPath := filepath.Join(tmpDir, "trunc.index")
	if err := os.WriteFile(truncPath, []byte("short"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := OpenDiskIndexMmap(truncPath)
	if err == nil {
		t.Fatal("expected error on truncated file")
	}

	// Non-existent file
	_, err = OpenDiskIndexMmap(filepath.Join(tmpDir, "nonexistent"))
	if err == nil {
		t.Fatal("expected error on nonexistent file")
	}
}
