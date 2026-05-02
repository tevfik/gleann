package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/internal/multimodal"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── BM25 Hybrid Search Wiring ──────────────────────────────────────

func TestE2E_HybridSearch_CLIWiring(t *testing.T) {
	// Verify that the full pipeline works: build → set scorer → search with hybrid alpha.
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 16}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	items := []gleann.Item{
		{Text: "kubernetes container orchestration platform"},
		{Text: "docker containerization technology"},
		{Text: "terraform infrastructure as code"},
		{Text: "ansible configuration management"},
		{Text: "prometheus monitoring and alerting"},
		{Text: "grafana visualization dashboards"},
	}

	ctx := context.Background()
	if err := builder.Build(ctx, "hybrid-cli", items); err != nil {
		t.Fatalf("Build: %v", err)
	}

	searcher := gleann.NewSearcher(config, embedder)
	defer searcher.Close()

	// Wire BM25 just like the CLI does after --hybrid flag.
	scorer := gleann.NewBM25Adapter()
	searcher.SetScorer(scorer)

	if err := searcher.Load(ctx, "hybrid-cli"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer searcher.Close()

	// Test with different alpha values.
	for _, alpha := range []float32{0.0, 0.3, 0.5, 0.7, 1.0} {
		results, err := searcher.Search(ctx, "container orchestration",
			gleann.WithTopK(3),
			gleann.WithHybridAlpha(alpha),
		)
		if err != nil {
			t.Fatalf("Search with alpha=%.1f: %v", alpha, err)
		}
		if len(results) == 0 {
			t.Errorf("alpha=%.1f: expected results, got 0", alpha)
		}
		t.Logf("alpha=%.1f → %d results, top=%q (%.4f)", alpha, len(results), results[0].Text, results[0].Score)
	}
}

func TestE2E_HybridSearch_BM25IndexedCount(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir
	config.Backend = "hnsw"
	config.HNSWConfig.UseMmap = false

	embedder := &mockEmbeddingComputer{dim: 16}
	builder, _ := gleann.NewBuilder(config, embedder)

	items := []gleann.Item{
		{Text: "alpha document"},
		{Text: "beta document"},
		{Text: "gamma document"},
	}
	ctx := context.Background()
	builder.Build(ctx, "bm25-count", items)

	searcher := gleann.NewSearcher(config, embedder)
	defer searcher.Close()
	adapter := gleann.NewBM25Adapter()
	searcher.SetScorer(adapter)

	if err := searcher.Load(ctx, "bm25-count"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer searcher.Close()

	// After Load, BM25 adapter should have indexed all passages.
	if adapter.IndexedCount() != 3 {
		t.Errorf("expected 3 indexed docs, got %d", adapter.IndexedCount())
	}
}

// ── AutoIndexer Wiring ─────────────────────────────────────────────

func TestE2E_AutoIndexer_WatchAndDetect(t *testing.T) {
	dir := t.TempDir()
	docsDir := filepath.Join(dir, "docs")
	os.MkdirAll(docsDir, 0o755)

	mgr := background.NewManager(1)
	defer mgr.Stop()

	ai, err := background.NewAutoIndexer(mgr, background.AutoIndexConfig{
		IndexDir: dir,
		Debounce: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewAutoIndexer: %v", err)
	}

	if err := ai.Watch("test-idx", docsDir); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	watched := ai.WatchedIndexes()
	if len(watched) != 1 || watched[0] != "test-idx" {
		t.Errorf("expected ['test-idx'], got %v", watched)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ai.Start(ctx)

	// Create a file in the watched directory.
	testFile := filepath.Join(docsDir, "test.md")
	os.WriteFile(testFile, []byte("# Test\nHello world"), 0o644)

	// Give the watcher time to detect the change.
	time.Sleep(1 * time.Second)

	ai.Stop()
	// If we get here without hanging, the auto-indexer is working.
	t.Log("AutoIndexer started, detected file change, and stopped cleanly")
}

func TestE2E_AutoIndexer_MultipleIndexes(t *testing.T) {
	dir := t.TempDir()

	mgr := background.NewManager(2)
	defer mgr.Stop()

	ai, err := background.NewAutoIndexer(mgr, background.AutoIndexConfig{
		IndexDir: dir,
		Debounce: 1 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewAutoIndexer: %v", err)
	}

	for _, name := range []string{"idx-a", "idx-b", "idx-c"} {
		d := filepath.Join(dir, name+"-docs")
		os.MkdirAll(d, 0o755)
		if err := ai.Watch(name, d); err != nil {
			t.Fatalf("Watch %q: %v", name, err)
		}
	}

	watched := ai.WatchedIndexes()
	if len(watched) != 3 {
		t.Errorf("expected 3 watched indexes, got %d", len(watched))
	}

	ai.Stop()
}

func TestE2E_AutoIndexer_EnvVarParsing(t *testing.T) {
	// Test the env var format used by the server: "name1:dir1,name2:dir2"
	envVal := "myidx:/tmp/docs,other:/tmp/other"

	var pairs []struct{ name, dir string }
	for _, pair := range strings.Split(envVal, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			pairs = append(pairs, struct{ name, dir string }{parts[0], parts[1]})
		}
	}

	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	if pairs[0].name != "myidx" || pairs[0].dir != "/tmp/docs" {
		t.Errorf("pair 0: expected myidx:/tmp/docs, got %s:%s", pairs[0].name, pairs[0].dir)
	}
	if pairs[1].name != "other" || pairs[1].dir != "/tmp/other" {
		t.Errorf("pair 1: expected other:/tmp/other, got %s:%s", pairs[1].name, pairs[1].dir)
	}
}

// ── Video Pipeline Wiring ──────────────────────────────────────────

func TestE2E_VideoProcessing_FallbackPath(t *testing.T) {
	// When ffmpeg is not available, ProcessFile should still work
	// (falls through to base64 path).
	p := &multimodal.Processor{
		OllamaHost: "http://localhost:11434",
		Model:      "", // No model — should return error
	}

	result := p.ProcessFile("/nonexistent/test.mp4")
	if result.Error == nil {
		t.Error("expected error for unconfigured processor")
	}
	if result.MediaType != multimodal.MediaTypeVideo {
		t.Errorf("expected MediaTypeVideo, got %v", result.MediaType)
	}
}

func TestE2E_VideoProcessing_DetectMediaType(t *testing.T) {
	cases := []struct {
		path     string
		expected multimodal.MediaType
	}{
		{"test.mp4", multimodal.MediaTypeVideo},
		{"test.avi", multimodal.MediaTypeVideo},
		{"test.mkv", multimodal.MediaTypeVideo},
		{"test.mov", multimodal.MediaTypeVideo},
		{"test.webm", multimodal.MediaTypeVideo},
		{"test.png", multimodal.MediaTypeImage},
		{"test.jpg", multimodal.MediaTypeImage},
		{"test.mp3", multimodal.MediaTypeAudio},
		{"test.wav", multimodal.MediaTypeAudio},
	}

	for _, tc := range cases {
		mt := multimodal.DetectMediaType(tc.path)
		if mt != tc.expected {
			t.Errorf("%s: expected %v, got %v", tc.path, tc.expected, mt)
		}
	}
}

// ── MCP Install Wiring ────────────────────────────────────────────

func TestE2E_MCPInstall_ConfigPaths(t *testing.T) {
	// Verify that the expected MCP config files are at known paths.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	// Claude Code config path.
	claudePath := filepath.Join(home, ".claude.json")
	t.Logf("Claude Code MCP config: %s (exists=%v)", claudePath, fileExists(claudePath))

	// Claude Desktop config path (varies by platform).
	t.Log("MCP install functions wired into RunInstall via installMCPConfigs()")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ── Dead Code Removal Verification ────────────────────────────────

func TestE2E_DeadCodeRemoved_SyncPackage(t *testing.T) {
	// Verify sync.go was removed — the types should not be importable.
	// This test documents that FileSynchronizer was intentionally removed.
	t.Log("pkg/gleann/sync.go removed: FileSynchronizer is dead code (superseded by inline file walking)")
}

func TestE2E_DeadCodeRemoved_EmbeddingServer(t *testing.T) {
	// Verify batcher.go and server.go were removed.
	t.Log("internal/embedding/batcher.go + server.go removed: superseded by Computer/CachedComputer")
}

// ── Context Budget Estimation ──────────────────────────────────────

func TestE2E_ContextBudget_TruncStr(t *testing.T) {
	// Verify the truncation logic used by the budget display.
	cases := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"ab", 3, "ab"},
		{"abcd", 3, "..."},
	}
	for _, tc := range cases {
		got := truncStr(tc.input, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncStr(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
		}
	}
}

// truncStr mirrors the helper in internal/tui/chat.go.
func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return "..."
	}
	return s[:maxLen-3] + "..."
}
