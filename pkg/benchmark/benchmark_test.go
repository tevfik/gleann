package benchmark_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/benchmark"
)

func TestBenchmarkRunner(t *testing.T) {
	tasks := []benchmark.Task{
		{
			ID:          "task-1",
			Description: "Search for chunking implementation",
			Query:       "markdown chunker split text",
			GoldFiles:   []string{"modules/chunking/markdown.go", "modules/chunking/chunker.go"},
		},
		{
			ID:          "task-2",
			Description: "Search for vector distance",
			Query:       "L2 distance calculation",
			GoldFiles:   []string{"modules/diskann/distance.go"},
		},
	}

	runner := benchmark.NewRunner(tasks)

	// Mock BM25 searcher
	runner.RegisterStrategy("BM25", func(ctx context.Context, query string, topK int) ([]string, int, error) {
		if strings.Contains(query, "chunker") {
			return []string{"modules/chunking/chunker.go", "other/file.go"}, 500, nil
		}
		return []string{"random.go"}, 500, nil
	})

	// Mock Vector searcher
	runner.RegisterStrategy("Vector (DiskANN+PQ)", func(ctx context.Context, query string, topK int) ([]string, int, error) {
		if strings.Contains(query, "chunker") {
			return []string{"modules/chunking/markdown.go", "modules/chunking/chunker.go"}, 350, nil
		}
		return []string{"modules/diskann/distance.go"}, 350, nil
	})

	// Mock GraphRAG searcher
	runner.RegisterStrategy("GraphRAG", func(ctx context.Context, query string, topK int) ([]string, int, error) {
		if strings.Contains(query, "chunker") {
			return []string{"modules/chunking/markdown.go", "modules/chunking/chunker.go"}, 250, nil
		}
		return []string{"modules/diskann/distance.go"}, 250, nil
	})

	ctx := context.Background()
	results, err := runner.Run(ctx, 5)
	if err != nil {
		t.Fatalf("runner.Run error: %v", err)
	}

	bm25, ok := results["BM25"]
	if !ok {
		t.Fatal("missing BM25 results")
	}

	vec, ok := results["Vector (DiskANN+PQ)"]
	if !ok {
		t.Fatal("missing Vector results")
	}

	if vec.AvgRecall5 <= bm25.AvgRecall5 {
		t.Errorf("expected Vector recall (%.1f%%) > BM25 recall (%.1f%%)", vec.AvgRecall5, bm25.AvgRecall5)
	}

	report := benchmark.RenderMarkdownReport(results, len(tasks))
	if !strings.Contains(report, "SWE-Bench") || !strings.Contains(report, "DiskANN+PQ") {
		t.Errorf("unexpected report output:\n%s", report)
	}

	jsonOut, err := benchmark.RenderJSON(results)
	if err != nil {
		t.Fatalf("RenderJSON error: %v", err)
	}
	if !strings.Contains(jsonOut, "avg_recall_5") {
		t.Errorf("unexpected json output:\n%s", jsonOut)
	}
}
