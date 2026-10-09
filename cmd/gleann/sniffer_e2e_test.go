package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestE2E_ContentSnifferIndexingAndSearch(t *testing.T) {
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	indexDir := filepath.Join(tmpDir, "indexes")
	_ = os.MkdirAll(docsDir, 0755)
	_ = os.MkdirAll(indexDir, 0755)

	// 1. Extensionless Bash script
	deployFile := filepath.Join(docsDir, "deploy_runner")
	deployContent := "#!/bin/bash\n# Production deployment orchestrator\nexport CLUSTER_LEADER=\"omega-node-42\"\necho \"Deploying leader $CLUSTER_LEADER\"\n"
	_ = os.WriteFile(deployFile, []byte(deployContent), 0755)

	// 2. Extensionless Dockerfile
	dockerFile := filepath.Join(docsDir, "Dockerfile")
	dockerContent := "FROM golang:1.24-alpine\nENV PROD_RELEASE_TARGET=\"production-v99\"\nWORKDIR /workspace\n"
	_ = os.WriteFile(dockerFile, []byte(dockerContent), 0644)

	// 3. Binary masquerading as .txt
	badBinFile := filepath.Join(docsDir, "rogue_binary.txt")
	badContent := "Some ascii header\x00\x00\x00\x00EXPLOIT_PAYLOAD_CORRUPT\x01\x02\x03"
	_ = os.WriteFile(badBinFile, []byte(badContent), 0644)

	// 4. Minified JS bundle
	minJsFile := filepath.Join(docsDir, "vendor.min.js")
	minContent := "function a(){var MINIFIED_TOKEN_SECRET=1;return MINIFIED_TOKEN_SECRET;}"
	_ = os.WriteFile(minJsFile, []byte(minContent), 0644)

	indexName := "e2e_sniffer_idx"

	// Mock embedder
	embedder := &testDefEmbedder{dim: 32}
	cfg := gleann.Config{
		IndexDir:       indexDir,
		Backend:        "diskann",
		EmbeddingModel: "test-model",
		ChunkConfig: gleann.ChunkConfig{
			ChunkSize:    256,
			ChunkOverlap: 32,
		},
	}

	builder, err := gleann.NewBuilder(cfg, embedder)
	if err != nil {
		t.Fatalf("failed to create builder: %v", err)
	}

	// Collect eligible files using our sniffer
	files, err := collectEligibleFiles(docsDir, nil, nil, nil, IndexModeCode, false)
	if err != nil {
		t.Fatalf("collectEligibleFiles failed: %v", err)
	}

	var items []gleann.Item
	for _, f := range files {
		data, rErr := os.ReadFile(f.path)
		if rErr != nil {
			continue
		}
		relPath, _ := filepath.Rel(docsDir, f.path)
		items = append(items, gleann.Item{
			Text: string(data),
			Metadata: map[string]any{
				"file":   relPath,
				"source": relPath,
			},
		})
	}

	if len(items) == 0 {
		t.Fatalf("expected indexed items, got 0")
	}

	// Verify items only contain Dockerfile and deploy_runner
	sources := make(map[string]bool)
	for _, it := range items {
		src := fmt.Sprintf("%v", it.Metadata["source"])
		sources[src] = true
	}

	if !sources["deploy_runner"] {
		t.Errorf("expected deploy_runner to be indexed, got sources: %v", sources)
	}
	if !sources["Dockerfile"] {
		t.Errorf("expected Dockerfile to be indexed, got sources: %v", sources)
	}
	if sources["rogue_binary.txt"] {
		t.Errorf("rogue_binary.txt should NOT be indexed!")
	}
	if sources["vendor.min.js"] {
		t.Errorf("vendor.min.js should NOT be indexed!")
	}

	// Build index
	ctx := context.Background()
	if err := builder.Build(ctx, indexName, items); err != nil {
		t.Fatalf("index build failed: %v", err)
	}

	// Load searcher and query index
	searcher := gleann.NewSearcher(cfg, embedder)
	if err := searcher.Load(ctx, indexName); err != nil {
		t.Fatalf("failed to load index: %v", err)
	}

	// Search for CLUSTER_LEADER
	results, err := searcher.Search(ctx, "CLUSTER_LEADER", gleann.WithTopK(5))
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results for CLUSTER_LEADER")
	}

	foundDeploy := false
	for _, r := range results {
		if r.Metadata["source"] == "deploy_runner" {
			foundDeploy = true
			break
		}
	}
	if !foundDeploy {
		t.Errorf("expected search result to originate from deploy_runner, got: %v", results)
	}
}
