package gleann

import (
	"context"
	"testing"
)

func TestFAISSConfig_NeedsTrainAndIsIVF(t *testing.T) {
	// NeedsTrain tests
	trainCases := []struct {
		idxType  string
		expected bool
	}{
		{"hnsw_pq", true},
		{"hnsw_sq8", true},
		{"ivf_flat", true},
		{"ivf_pq", true},
		{"ivf_sq8", true},
		{"flat", false},
		{"hnsw", false},
	}
	for _, tc := range trainCases {
		cfg := FAISSConfig{IndexType: tc.idxType}
		if got := cfg.NeedsTrain(); got != tc.expected {
			t.Errorf("FAISSConfig{%q}.NeedsTrain() = %v, want %v", tc.idxType, got, tc.expected)
		}
	}

	// IsIVF tests
	ivfCases := []struct {
		idxType  string
		expected bool
	}{
		{"ivf_flat", true},
		{"ivf_pq", true},
		{"ivf_sq8", true},
		{"flat", false},
		{"hnsw_pq", false},
	}
	for _, tc := range ivfCases {
		cfg := FAISSConfig{IndexType: tc.idxType}
		if got := cfg.IsIVF(); got != tc.expected {
			t.Errorf("FAISSConfig{%q}.IsIVF() = %v, want %v", tc.idxType, got, tc.expected)
		}
	}
}

func TestSearchOptions_And_SearcherAccessors(t *testing.T) {
	s := &LeannSearcher{
		warnings: []string{"warn1", "warn2"},
	}

	// Warnings
	w := s.Warnings()
	if len(w) != 2 || w[0] != "warn1" {
		t.Fatalf("expected warnings, got %v", w)
	}

	// SetMeta
	meta := IndexMeta{EmbeddingModel: "custom-embed"}
	s.SetMeta(meta)
	if s.Meta().EmbeddingModel != "custom-embed" {
		t.Fatalf("SetMeta failed")
	}

	// Backend & GraphDB & PassageManager
	if s.Backend() != nil {
		t.Fatalf("expected nil backend")
	}
	if s.GraphDB() != nil {
		t.Fatalf("expected nil GraphDB")
	}
	if s.PassageManager() != nil {
		t.Fatalf("expected nil PassageManager")
	}

	// SearchOptions test
	cfg := SearchConfig{}
	WithTopK(20)(&cfg)
	if cfg.TopK != 20 {
		t.Errorf("WithTopK failed: %d", cfg.TopK)
	}

	WithHybridAlpha(0.7)(&cfg)
	if cfg.HybridAlpha != 0.7 {
		t.Errorf("WithHybridAlpha failed: %f", cfg.HybridAlpha)
	}

	WithMinScore(0.5)(&cfg)
	if cfg.MinScore != 0.5 {
		t.Errorf("WithMinScore failed: %f", cfg.MinScore)
	}

	WithReranker(true)(&cfg)
	if !cfg.UseReranker {
		t.Errorf("WithReranker failed")
	}

	WithCustomReranker(nil)(&cfg)
	if !cfg.UseReranker {
		t.Errorf("WithCustomReranker failed")
	}

	WithGraphContext(true)(&cfg)
	if !cfg.UseGraphContext {
		t.Errorf("WithGraphContext failed")
	}

	WithRerankPool(35)(&cfg)
	if cfg.RerankPool != 35 {
		t.Errorf("WithRerankPool failed: %d", cfg.RerankPool)
	}

	WithOnePerFile(true)(&cfg)
	if !cfg.OnePerFile {
		t.Errorf("WithOnePerFile failed")
	}

	WithMetadataFilters([]MetadataFilter{{Field: "tag", Value: "go"}}, "OR")(&cfg)
	if len(cfg.MetadataFilters) != 1 || cfg.FilterLogic != "OR" {
		t.Errorf("WithMetadataFilters failed")
	}
}

func TestReconcile_InvalidName(t *testing.T) {
	b := &LeannBuilder{}
	_, err := b.Reconcile(context.Background(), "../invalid-name", ".")
	if err == nil {
		t.Fatal("expected error on invalid index name")
	}
}
