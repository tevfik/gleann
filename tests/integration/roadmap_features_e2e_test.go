package integration

import (
	"context"
	"path/filepath"
	"testing"

	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

// TestE2E_MultimodalIndexingAndSearch tests the indexing and retrieval of
// multimodal items (e.g. image descriptions and audio transcripts)
// ensuring the single-binary crossplatform pipeline functions seamlessly.
func TestE2E_MultimodalIndexingAndSearch(t *testing.T) {
	dir := t.TempDir()
	indexName := "e2e-multimodal"

	// Simulate items produced by the multimodal processor (vision & audio sidecars)
	items := []gleann.Item{
		{
			Text: "High-level system architecture diagram showing KuzuDB graph database connected to Vector Index and Ollama Vision pipeline.",
			Metadata: map[string]any{
				"source":     "diagrams/arch.png",
				"media_type": "image",
				"model":      "qwen2.5-vl",
			},
		},
		{
			Text: "Engineering standup audio recording discussing daemon background service with systemd and launchd integration.",
			Metadata: map[string]any{
				"source":     "recordings/standup.wav",
				"media_type": "audio",
				"model":      "whisper",
			},
		},
	}

	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.EmbeddingModel = "mock"
	config.HNSWConfig.UseMmap = false
	config.HNSWConfig.PruneEmbeddings = false

	embedder := &mockEmbeddingComputer{dim: 32}
	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	if err := builder.Build(context.Background(), indexName, items); err != nil {
		t.Fatalf("builder.Build: %v", err)
	}

	searcher := gleann.NewSearcher(config, embedder)
	defer searcher.Close()

	if err := searcher.Load(context.Background(), indexName); err != nil {
		t.Fatalf("searcher.Load: %v", err)
	}

	// 1. Search using exact text of diagram item
	results, err := searcher.Search(context.Background(), items[0].Text, gleann.WithTopK(1))
	if err != nil {
		t.Fatalf("Search diagram: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for diagram query")
	}
	if results[0].Metadata["source"] != "diagrams/arch.png" {
		t.Errorf("expected source diagrams/arch.png, got %v", results[0].Metadata["source"])
	}
	if results[0].Metadata["media_type"] != "image" {
		t.Errorf("expected media_type image, got %v", results[0].Metadata["media_type"])
	}

	// 2. Search using exact text of audio transcript item
	audioResults, err := searcher.Search(context.Background(), items[1].Text, gleann.WithTopK(1))
	if err != nil {
		t.Fatalf("Search audio: %v", err)
	}
	if len(audioResults) == 0 {
		t.Fatal("expected search results for audio query")
	}
	if audioResults[0].Metadata["source"] != "recordings/standup.wav" {
		t.Errorf("expected source recordings/standup.wav, got %v", audioResults[0].Metadata["source"])
	}
	if audioResults[0].Metadata["media_type"] != "audio" {
		t.Errorf("expected media_type audio, got %v", audioResults[0].Metadata["media_type"])
	}
}

// TestE2E_IncrementalWatch_RebuildPipeline tests the live file change & incremental update
// pipeline that powers `gleann index watch`. It confirms that modifying one file updates its
// chunks in the vector index while leaving untouched files and their embeddings preserved.
func TestE2E_IncrementalWatch_RebuildPipeline(t *testing.T) {
	dir := t.TempDir()
	indexName := "e2e-watch-incremental"

	// Initial files
	items := []gleann.Item{
		{
			Text: "Initial content of alpha document.",
			Metadata: map[string]any{
				"source": "alpha.txt",
			},
		},
		{
			Text: "Initial content of beta document that remains untouched.",
			Metadata: map[string]any{
				"source": "beta.txt",
			},
		},
	}

	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.EmbeddingModel = "mock"
	config.HNSWConfig.UseMmap = false
	config.HNSWConfig.PruneEmbeddings = false

	embedder := &mockEmbeddingComputer{dim: 32}
	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	ctx := context.Background()
	if err := builder.Build(ctx, indexName, items); err != nil {
		t.Fatalf("builder.Build: %v", err)
	}

	// Verify initial index has 2 passages
	basePath := filepath.Join(dir, indexName, indexName)
	pm := gleann.NewPassageManager(basePath)
	if err := pm.Load(); err != nil {
		t.Fatalf("pm.Load: %v", err)
	}
	if pm.Count() != 2 {
		t.Fatalf("expected 2 passages initially, got %d", pm.Count())
	}
	pm.Close()

	// Simulate incremental watcher rebuild: alpha.txt was modified
	newAlphaItems := []gleann.Item{
		{
			Text: "Updated content of alpha document with advanced GraphRAG capabilities.",
			Metadata: map[string]any{
				"source": "alpha.txt",
			},
		},
	}

	builder2, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder2: %v", err)
	}

	// UpdateIndex: remove old "alpha.txt" passages and add new ones
	if err := builder2.UpdateIndex(ctx, indexName, newAlphaItems, []string{"alpha.txt"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}

	// Check updated passage count
	pm2 := gleann.NewPassageManager(basePath)
	if err := pm2.Load(); err != nil {
		t.Fatalf("pm2.Load: %v", err)
	}
	count2 := pm2.Count()
	pm2.Close()
	if count2 != 2 {
		t.Errorf("expected 2 passages after incremental update, got %d", count2)
	}

	// Verify searching returns the updated content
	searcher := gleann.NewSearcher(config, embedder)
	defer searcher.Close()

	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("searcher.Load: %v", err)
	}

	results, err := searcher.Search(ctx, newAlphaItems[0].Text, gleann.WithTopK(1))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for updated content")
	}
	if results[0].Metadata["source"] != "alpha.txt" {
		t.Errorf("expected source alpha.txt, got %v", results[0].Metadata["source"])
	}

	// Beta should still be searchable
	betaResults, err := searcher.Search(ctx, items[1].Text, gleann.WithTopK(1))
	if err != nil {
		t.Fatalf("search beta: %v", err)
	}
	if len(betaResults) == 0 {
		t.Fatal("expected search results for beta document")
	}
	if betaResults[0].Metadata["source"] != "beta.txt" {
		t.Errorf("expected source beta.txt, got %v", betaResults[0].Metadata["source"])
	}
}
