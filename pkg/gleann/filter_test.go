package gleann

import (
	"testing"
)

func TestMetadataFilterMatch(t *testing.T) {
	tests := []struct {
		name     string
		filter   MetadataFilter
		metadata map[string]any
		want     bool
	}{
		{"eq match", MetadataFilter{"lang", "eq", "go"}, map[string]any{"lang": "go"}, true},
		{"eq no match", MetadataFilter{"lang", "eq", "python"}, map[string]any{"lang": "go"}, false},
		{"ne match", MetadataFilter{"lang", "ne", "python"}, map[string]any{"lang": "go"}, true},
		{"gt match", MetadataFilter{"score", "gt", 0.5}, map[string]any{"score": 0.8}, true},
		{"gt no match", MetadataFilter{"score", "gt", 0.9}, map[string]any{"score": 0.8}, false},
		{"gte match", MetadataFilter{"score", "gte", 0.8}, map[string]any{"score": 0.8}, true},
		{"lt match", MetadataFilter{"score", "lt", 0.9}, map[string]any{"score": 0.8}, true},
		{"lte match", MetadataFilter{"score", "lte", 0.8}, map[string]any{"score": 0.8}, true},
		{"contains match", MetadataFilter{"path", "contains", "src"}, map[string]any{"path": "/home/src/main.go"}, true},
		{"startswith match", MetadataFilter{"name", "startswith", "test"}, map[string]any{"name": "test_utils.py"}, true},
		{"endswith match", MetadataFilter{"name", "endswith", ".go"}, map[string]any{"name": "main.go"}, true},
		{"exists match", MetadataFilter{"lang", "exists", true}, map[string]any{"lang": "go"}, true},
		{"exists no match", MetadataFilter{"lang", "exists", true}, map[string]any{}, false},
		{"missing field", MetadataFilter{"missing", "eq", "x"}, map[string]any{}, false},
		{"in match", MetadataFilter{"lang", "in", []any{"go", "rust"}}, map[string]any{"lang": "go"}, true},
		{"in no match", MetadataFilter{"lang", "in", []any{"python", "java"}}, map[string]any{"lang": "go"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewMetadataFilterEngine([]MetadataFilter{tt.filter})
			got := engine.Match(tt.metadata)
			if got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetadataFilterResults(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Text: "go code", Score: 0.9, Metadata: map[string]any{"lang": "go"}},
		{ID: 2, Text: "python code", Score: 0.8, Metadata: map[string]any{"lang": "python"}},
		{ID: 3, Text: "rust code", Score: 0.7, Metadata: map[string]any{"lang": "rust"}},
	}

	engine := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "lang", Operator: "eq", Value: "go"},
	})

	filtered := engine.FilterResults(results)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 result, got %d", len(filtered))
	}
	if filtered[0].ID != 1 {
		t.Errorf("expected ID 1, got %d", filtered[0].ID)
	}
}

func TestMetadataFilterOR(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Text: "go", Metadata: map[string]any{"lang": "go"}},
		{ID: 2, Text: "py", Metadata: map[string]any{"lang": "python"}},
		{ID: 3, Text: "rs", Metadata: map[string]any{"lang": "rust"}},
	}

	engine := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "lang", Operator: "eq", Value: "go"},
		{Field: "lang", Operator: "eq", Value: "rust"},
	})
	engine.Logic = "or"

	filtered := engine.FilterResults(results)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 results (go + rust), got %d", len(filtered))
	}
}

func TestMetadataFilterEmpty(t *testing.T) {
	engine := NewMetadataFilterEngine(nil)

	results := []SearchResult{
		{ID: 1, Text: "test", Score: 1.0, Metadata: map[string]any{}},
	}

	filtered := engine.FilterResults(results)
	if len(filtered) != 1 {
		t.Errorf("expected all results with no filters, got %d", len(filtered))
	}

	if !engine.Match(map[string]any{}) {
		t.Error("expected Match() true with no filters")
	}
}
