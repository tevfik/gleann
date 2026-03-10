package gleann

import (
	"context"
	"testing"
)

func TestSearchMultipleNoIndexes(t *testing.T) {
	dir := t.TempDir()
	config := DefaultConfig()
	config.IndexDir = dir

	ctx := context.Background()
	results, err := SearchMultiple(ctx, config, nil, nil, "test query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty dir, got %d", len(results))
	}
}

func TestSearchMultipleExplicitNamesNotFound(t *testing.T) {
	dir := t.TempDir()
	config := DefaultConfig()
	config.IndexDir = dir

	ctx := context.Background()
	_, err := SearchMultiple(ctx, config, nil, []string{"nonexistent"}, "test query")
	if err == nil {
		t.Fatal("expected error for nonexistent index, got nil")
	}
}

func TestSearchMultipleMergeOrder(t *testing.T) {
	// Test that MultiSearchResult preserves Index field.
	r := MultiSearchResult{
		SearchResult: SearchResult{ID: 1, Text: "hello", Score: 0.9},
		Index:        "idx-a",
	}
	if r.Index != "idx-a" {
		t.Errorf("expected Index idx-a, got %s", r.Index)
	}
	if r.Score != 0.9 {
		t.Errorf("expected Score 0.9, got %f", r.Score)
	}
}
