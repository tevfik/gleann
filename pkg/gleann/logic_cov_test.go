package gleann

import (
	"encoding/json"
	"testing"
	"time"
)

// ── BM25Adapter ───────────────────────────────────────────────

func TestBM25Adapter_NewAndScore(t *testing.T) {
	a := NewBM25Adapter()
	passages := []Passage{
		{ID: 1, Text: "The quick brown fox jumps over the lazy dog"},
		{ID: 2, Text: "A completely unrelated sentence about programming"},
	}
	scores := a.Score("fox dog", passages)
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	if scores[0] <= scores[1] {
		t.Fatal("passage 1 should score higher for 'fox dog'")
	}
}

func TestBM25Adapter_WithParams(t *testing.T) {
	a := NewBM25AdapterWithParams(1.5, 0.75)
	if a == nil {
		t.Fatal("nil adapter")
	}
}

func TestBM25Adapter_AddDocuments(t *testing.T) {
	a := NewBM25Adapter()
	a.AddDocuments([]Passage{
		{ID: 1, Text: "hello world"},
		{ID: 2, Text: "goodbye world"},
	})
	scores := a.Score("hello", []Passage{
		{ID: 1, Text: "hello world"},
		{ID: 2, Text: "goodbye world"},
	})
	if scores[0] <= 0 {
		t.Fatal("expected positive score for matching document")
	}
}

// ── Registry ──────────────────────────────────────────────────

type mockBackendFactoryCov struct{}

func (m mockBackendFactoryCov) Name() string                              { return "test-backend-cov" }
func (m mockBackendFactoryCov) NewBuilder(config Config) BackendBuilder   { return nil }
func (m mockBackendFactoryCov) NewSearcher(config Config) BackendSearcher { return nil }

func TestRegisterAndGetBackendCov(t *testing.T) {
	RegisterBackend(mockBackendFactoryCov{})
	f, err := GetBackend("test-backend-cov")
	if err != nil {
		t.Fatal(err)
	}
	if f.Name() != "test-backend-cov" {
		t.Fatalf("expected test-backend-cov, got %s", f.Name())
	}
}

func TestGetBackend_NotFoundCov(t *testing.T) {
	_, err := GetBackend("nonexistent-backend-xyz")
	if err == nil {
		t.Fatal("expected error for missing backend")
	}
}

func TestListBackends_Cov(t *testing.T) {
	names := ListBackends()
	if len(names) == 0 {
		t.Fatal("expected at least one backend (from init)")
	}
}

// ── Types: IndexMeta MarshalJSON ──────────────────────────────

func TestIndexMeta_MarshalJSON(t *testing.T) {
	meta := IndexMeta{
		Name:           "test",
		Backend:        "hnsw",
		EmbeddingModel: "bge-m3",
		Dimensions:     1024,
		NumPassages:    100,
		CreatedAt:      time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2024, 1, 16, 12, 0, 0, 0, time.UTC),
		Version:        "1.0",
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}

	// Check RFC3339 format
	ca, ok := m["created_at"].(string)
	if !ok || ca != "2024-01-15T10:00:00Z" {
		t.Fatalf("unexpected created_at: %v", ca)
	}
}

// ── Types: DefaultConfig / DefaultChunkConfig ─────────────────

func TestDefaultConfig_Cov(t *testing.T) {
	c := DefaultConfig()
	if c.IndexDir != "." {
		t.Fatalf("expected '.', got %q", c.IndexDir)
	}
	if c.EmbeddingModel != "bge-m3" {
		t.Fatalf("expected bge-m3, got %q", c.EmbeddingModel)
	}
	if c.SearchConfig.TopK != 10 {
		t.Fatalf("expected TopK=10, got %d", c.SearchConfig.TopK)
	}
}

func TestDefaultChunkConfig_Cov(t *testing.T) {
	c := DefaultChunkConfig()
	if c.ChunkSize != 512 {
		t.Fatalf("expected 512, got %d", c.ChunkSize)
	}
	if c.ChunkOverlap != 50 {
		t.Fatalf("expected 50, got %d", c.ChunkOverlap)
	}
}

// ── Types: JSONToAttributes ───────────────────────────────────

func TestJSONToAttributes_Empty(t *testing.T) {
	m, err := JSONToAttributes("")
	if err != nil || m != nil {
		t.Fatal("empty string should return nil, nil")
	}
}

func TestJSONToAttributes_EmptyObj(t *testing.T) {
	m, err := JSONToAttributes("{}")
	if err != nil || m != nil {
		t.Fatal("{} should return nil, nil")
	}
}

func TestJSONToAttributes_Valid(t *testing.T) {
	m, err := JSONToAttributes(`{"key":"value","num":42}`)
	if err != nil {
		t.Fatal(err)
	}
	if m["key"] != "value" {
		t.Fatalf("expected 'value', got %v", m["key"])
	}
}

func TestJSONToAttributes_Invalid(t *testing.T) {
	_, err := JSONToAttributes("{invalid")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ── parseReActStep ────────────────────────────────────────────

func TestParseReActStep_Full(t *testing.T) {
	input := "Thought: I need to search\nAction: search\nAction Input: golang testing"
	step := parseReActStep(input)
	if step.Thought != "I need to search" {
		t.Fatalf("unexpected thought: %q", step.Thought)
	}
	if step.Action != "search" {
		t.Fatalf("unexpected action: %q", step.Action)
	}
	if step.ActionInput != "golang testing" {
		t.Fatalf("unexpected input: %q", step.ActionInput)
	}
}

func TestParseReActStep_Partial(t *testing.T) {
	step := parseReActStep("Thought: just thinking")
	if step.Thought != "just thinking" {
		t.Fatalf("unexpected thought: %q", step.Thought)
	}
	if step.Action != "" {
		t.Fatal("expected empty action")
	}
}

func TestParseReActStep_Empty(t *testing.T) {
	step := parseReActStep("")
	if step.Thought != "" || step.Action != "" || step.ActionInput != "" {
		t.Fatal("expected empty step")
	}
}

// ── doc_native: renderMarkdownTable, padRow ───────────────────

func TestRenderMarkdownTable_Empty_Cov(t *testing.T) {
	if renderMarkdownTable(nil) != "" {
		t.Fatal("nil should return empty")
	}
}

func TestRenderMarkdownTable_EmptyCols(t *testing.T) {
	if renderMarkdownTable([][]string{{}}) != "" {
		t.Fatal("empty cols should return empty")
	}
}

func TestRenderMarkdownTable_Normal(t *testing.T) {
	rows := [][]string{
		{"Name", "Age"},
		{"Alice", "30"},
		{"Bob", "25"},
	}
	result := renderMarkdownTable(rows)
	if result == "" {
		t.Fatal("expected non-empty table")
	}
	// Should have header, separator, and 2 data rows
	if len(result) < 20 {
		t.Fatal("table too short")
	}
}

func TestRenderMarkdownTable_UnevenRows(t *testing.T) {
	rows := [][]string{
		{"A", "B", "C"},
		{"1"},
		{"x", "y"},
	}
	result := renderMarkdownTable(rows)
	if result == "" {
		t.Fatal("expected non-empty table")
	}
}

func TestPadRow_Wider(t *testing.T) {
	row := padRow([]string{"a"}, 3)
	if len(row) != 3 || row[0] != "a" || row[1] != "" || row[2] != "" {
		t.Fatalf("unexpected: %v", row)
	}
}

// ── doc_extractor: smallItoa ──────────────────────────────────

func TestSmallItoa_Cov(t *testing.T) {
	tests := []struct {
		in  int
		out string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
		{9, "9"},
	}
	for _, tt := range tests {
		if got := smallItoa(tt.in); got != tt.out {
			t.Errorf("smallItoa(%d) = %q, want %q", tt.in, got, tt.out)
		}
	}
}

// ── doc_native: CanHandle, SupportedExtensions ─────────────────

func TestNativeExtractor_CanHandle_Cov(t *testing.T) {
	ne := NewNativeExtractor()
	for _, ext := range []string{".md", ".txt", ".csv", ".html", ".docx", ".xlsx"} {
		if !ne.CanHandle(ext) {
			t.Fatalf("expected CanHandle(%q) = true", ext)
		}
	}
	if ne.CanHandle(".xyz") {
		t.Fatal("should not handle .xyz")
	}
}

func TestNativeExtractor_SupportedExtensions_Cov(t *testing.T) {
	ne := NewNativeExtractor()
	exts := ne.SupportedExtensions()
	if len(exts) == 0 {
		t.Fatal("expected some extensions")
	}
}

// ── ExtractSummary ────────────────────────────────────────────

func TestExtractSummary_Short(t *testing.T) {
	// Text too short for sentences
	result := ExtractSummary("hello")
	if result != "" {
		t.Fatalf("expected empty, got %q", result)
	}
}

func TestExtractSummary_OnlyCodeBlocks(t *testing.T) {
	result := ExtractSummary("```go\nfmt.Println('hello')\n```")
	if result != "" {
		t.Fatalf("expected empty after code removal, got %q", result)
	}
}

// ── searcher options ──────────────────────────────────────────

func TestSearchOptions_Cov(t *testing.T) {
	cfg := &SearchConfig{}

	WithTopK(20)(cfg)
	if cfg.TopK != 20 {
		t.Fatalf("expected 20, got %d", cfg.TopK)
	}

	WithHybridAlpha(0.5)(cfg)
	if cfg.HybridAlpha != 0.5 {
		t.Fatalf("expected 0.5, got %f", cfg.HybridAlpha)
	}

	WithMinScore(0.3)(cfg)
	if cfg.MinScore != 0.3 {
		t.Fatalf("expected 0.3, got %f", cfg.MinScore)
	}

	WithReranker(true)(cfg)
	if !cfg.UseReranker {
		t.Fatal("expected reranker enabled")
	}

	WithGraphContext(true)(cfg)
	if !cfg.UseGraphContext {
		t.Fatal("expected graph context enabled")
	}
}
