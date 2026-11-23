package benchmark_test

import (
	"context"
	"path/filepath"
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

func TestBenchmarkRunnerUnavailableStrategy(t *testing.T) {
	tasks := []benchmark.Task{
		{ID: "t1", Query: "search query", GoldFiles: []string{"a.go"}},
	}
	runner := benchmark.NewRunner(tasks)
	runner.RegisterStrategy("BM25", func(ctx context.Context, query string, topK int) ([]string, int, error) {
		return []string{"a.go"}, 100, nil
	})
	runner.RegisterUnavailable("Ripgrep", "ripgrep not installed")

	ctx := context.Background()
	results, err := runner.Run(ctx, 5)
	if err != nil {
		t.Fatalf("runner.Run error: %v", err)
	}

	rg, ok := results["Ripgrep"]
	if !ok {
		t.Fatal("missing Ripgrep in results")
	}
	if rg.Unavailable != "ripgrep not installed" {
		t.Errorf("expected unavailable note, got %q", rg.Unavailable)
	}

	report := benchmark.RenderMarkdownReport(results, len(tasks))
	if !strings.Contains(report, "| **Ripgrep** | *(ripgrep not installed)* | — |") {
		t.Errorf("report does not contain unavailable row for Ripgrep:\n%s", report)
	}
	if !strings.Contains(report, "- Ripgrep baseline: ripgrep not installed.") {
		t.Errorf("report does not explain Ripgrep baseline status:\n%s", report)
	}
	// Report must not print zeros for Ripgrep
	if strings.Contains(report, "| **Ripgrep** | 0.0% |") {
		t.Errorf("report should not print zeros for unavailable strategy:\n%s", report)
	}
}

func TestBenchmarkRunnerUnwritablePerTaskFileErrors(t *testing.T) {
	tasks := []benchmark.Task{
		{ID: "t1", Query: "search query", GoldFiles: []string{"a.go"}},
	}
	runner := benchmark.NewRunner(tasks)
	runner.RegisterStrategy("BM25", func(ctx context.Context, query string, topK int) ([]string, int, error) {
		return []string{"a.go"}, 100, nil
	})
	unwritablePath := filepath.Join(t.TempDir(), "no-such-dir", "sub", "pertask.jsonl")
	runner.PerTaskFile = unwritablePath

	ctx := context.Background()
	_, err := runner.Run(ctx, 1)
	if err == nil {
		t.Fatalf("expected error when PerTaskFile is unwritable, got nil")
	}
	if !strings.Contains(err.Error(), "creating per-task file") {
		t.Errorf("expected error to mention 'creating per-task file', got: %v", err)
	}
}
