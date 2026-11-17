package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/modules/chunking"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

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
	pm := openPassages(t, basePath)
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
