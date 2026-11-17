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

// TestE2E_FullLifecycle simulates an indexing lifecycle:
// 1. Build initial index with provenance
// 2. Delete a file → reconcile stale passages
func TestE2E_FullLifecycle(t *testing.T) {
	dir := t.TempDir()
	docsRoot := filepath.Join(dir, "docs")
	os.MkdirAll(docsRoot, 0755)
	indexName := "e2e-lifecycle"

	// ── Step 1: Create source files.
	writeFile(t, filepath.Join(docsRoot, "main.go"), `package main

import "fmt"

func main() {
	fmt.Println("hello world")
}
`)
	writeFile(t, filepath.Join(docsRoot, "helper.go"), `package main

func helper() int {
	return 42
}
`)
	writeFile(t, filepath.Join(docsRoot, "utils.go"), `package main

func add(a, b int) int {
	return a + b
}
`)

	// ── Step 2: Chunk with provenance.
	chunker := chunking.NewCodeChunker(200, 40)
	var allItems []gleann.Item

	for _, name := range []string{"main.go", "helper.go", "utils.go"} {
		content, _ := os.ReadFile(filepath.Join(docsRoot, name))
		chunks := chunker.ChunkWithProvenance(string(content), map[string]any{
			"source":   name,
			"language": "go",
		})
		for _, c := range chunks {
			allItems = append(allItems, gleann.Item{Text: c.Text, Metadata: c.Metadata})
		}
	}

	if len(allItems) == 0 {
		t.Fatal("no chunks produced")
	}
	t.Logf("Step 2: Produced %d chunks with provenance", len(allItems))

	// Verify provenance metadata on all items.
	for i, item := range allItems {
		if _, ok := item.Metadata["start_byte"]; !ok {
			t.Errorf("item %d: missing provenance start_byte", i)
		}
		if _, ok := item.Metadata["source"]; !ok {
			t.Errorf("item %d: missing source", i)
		}
	}

	// ── Step 3: Build index.
	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 32}
	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("new builder: %v", err)
	}

	if err := builder.Build(context.Background(), indexName, allItems); err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Log("Step 3: Index built successfully")

	// ── Step 4: Delete utils.go → reconcile.
	os.Remove(filepath.Join(docsRoot, "utils.go"))

	basePath := filepath.Join(dir, indexName, indexName)
	pm := openPassages(t, basePath)
	if err := pm.Load(); err != nil {
		t.Fatalf("load passages: %v", err)
	}

	orphans, totalPassages := gleann.ReconcilePassageManager(pm, docsRoot)
	pm.Close()

	t.Logf("Step 4: Found %d orphan sources out of %d passages", len(orphans), totalPassages)
	if len(orphans) != 1 || orphans[0] != "utils.go" {
		t.Errorf("expected [utils.go] as orphan, got %v", orphans)
	}

	t.Log("E2E lifecycle complete ✓")
}

// TestE2E_ProvenanceInSearchResults verifies that search results
// carry provenance metadata all the way through.
func TestE2E_ProvenanceInSearchResults(t *testing.T) {
	dir := t.TempDir()
	indexName := "e2e-provenance"

	// Create items with provenance metadata.
	items := []gleann.Item{
		{
			Text: "func main() { fmt.Println(\"hello\") }",
			Metadata: map[string]any{
				"source":     "main.go",
				"start_byte": 0,
				"end_byte":   38,
				"start_line": 1,
				"end_line":   1,
			},
		},
		{
			Text: "func helper() { return 42 }",
			Metadata: map[string]any{
				"source":     "helper.go",
				"start_byte": 0,
				"end_byte":   27,
				"start_line": 1,
				"end_line":   1,
			},
		},
	}

	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 32}
	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if err := builder.Build(context.Background(), indexName, items); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Search and verify provenance survives in results.
	searcher := gleann.NewSearcher(config, embedder)
	defer searcher.Close()
	defer searcher.Close()

	if err := searcher.Load(context.Background(), indexName); err != nil {
		t.Fatalf("load: %v", err)
	}

	results, err := searcher.Search(context.Background(), "main function", gleann.WithTopK(2))
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected search results")
	}

	for i, r := range results {
		if r.Metadata == nil {
			t.Errorf("result %d: nil metadata", i)
			continue
		}
		if _, ok := r.Metadata["source"]; !ok {
			t.Errorf("result %d: missing source in metadata", i)
		}
		if _, ok := r.Metadata["start_byte"]; !ok {
			t.Errorf("result %d: missing start_byte in metadata", i)
		}
		if _, ok := r.Metadata["start_line"]; !ok {
			t.Errorf("result %d: missing start_line in metadata", i)
		}
		t.Logf("result %d: source=%v start_line=%v score=%.4f", i,
			r.Metadata["source"], r.Metadata["start_line"], r.Score)
	}
}

// TestE2E_ReconcileAfterBuild tests full build → delete → reconcile → verify.
func TestE2E_ReconcileAfterBuild(t *testing.T) {
	dir := t.TempDir()
	docsRoot := filepath.Join(dir, "docs")
	os.MkdirAll(docsRoot, 0755)
	indexName := "e2e-reconcile"

	// Create 3 files.
	writeFile(t, filepath.Join(docsRoot, "a.txt"), "Content of file A.")
	writeFile(t, filepath.Join(docsRoot, "b.txt"), "Content of file B.")
	writeFile(t, filepath.Join(docsRoot, "c.txt"), "Content of file C.")

	items := []gleann.Item{
		{Text: "Content of file A.", Metadata: map[string]any{"source": "a.txt"}},
		{Text: "Content of file B.", Metadata: map[string]any{"source": "b.txt"}},
		{Text: "Content of file C.", Metadata: map[string]any{"source": "c.txt"}},
	}

	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 32}
	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("builder: %v", err)
	}

	if err := builder.Build(context.Background(), indexName, items); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Delete files b.txt and c.txt.
	os.Remove(filepath.Join(docsRoot, "b.txt"))
	os.Remove(filepath.Join(docsRoot, "c.txt"))

	// Reconcile.
	result, err := builder.Reconcile(context.Background(), indexName, docsRoot)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if result.ScannedPassages != 3 {
		t.Errorf("expected 3 scanned, got %d", result.ScannedPassages)
	}
	if len(result.OrphanSources) != 2 {
		t.Errorf("expected 2 orphan sources, got %d: %v", len(result.OrphanSources), result.OrphanSources)
	}
	if result.RemovedPassages != 2 {
		t.Errorf("expected 2 removed passages, got %d", result.RemovedPassages)
	}

	// Verify only a.txt passages remain.
	basePath := filepath.Join(dir, indexName, indexName)
	pm := openPassages(t, basePath)
	pm.Load()
	defer pm.Close()

	count := pm.Count()
	if count != 1 {
		t.Errorf("expected 1 passage remaining, got %d", count)
	}

	t.Logf("Reconcile result: scanned=%d, orphans=%v, removed=%d",
		result.ScannedPassages, result.OrphanSources, result.RemovedPassages)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
