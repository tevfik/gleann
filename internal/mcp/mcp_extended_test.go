package mcp

import (
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestParseFiltersExtended(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]interface{}
		wantLen   int
		wantLogic string
	}{
		{
			"empty args",
			map[string]interface{}{},
			0, "and",
		},
		{
			"no filters key",
			map[string]interface{}{"query": "test"},
			0, "and",
		},
		{
			"single filter",
			map[string]interface{}{
				"filters": []interface{}{
					map[string]interface{}{
						"field":    "lang",
						"operator": "eq",
						"value":    "go",
					},
				},
			},
			1, "and",
		},
		{
			"multiple filters",
			map[string]interface{}{
				"filters": []interface{}{
					map[string]interface{}{"field": "lang", "operator": "eq", "value": "go"},
					map[string]interface{}{"field": "score", "operator": "gt", "value": 0.5},
				},
			},
			2, "and",
		},
		{
			"or logic",
			map[string]interface{}{
				"filter_logic": "or",
				"filters": []interface{}{
					map[string]interface{}{"field": "lang", "operator": "eq", "value": "go"},
				},
			},
			1, "or",
		},
		{
			"invalid logic falls back to and",
			map[string]interface{}{
				"filter_logic": "invalid",
				"filters": []interface{}{
					map[string]interface{}{"field": "a", "operator": "eq", "value": "b"},
				},
			},
			1, "and",
		},
		{
			"malformed filter entry",
			map[string]interface{}{
				"filters": []interface{}{
					"not a map",
					map[string]interface{}{"field": "a", "operator": "eq", "value": "b"},
				},
			},
			1, "and",
		},
		{
			"incomplete filter (missing value)",
			map[string]interface{}{
				"filters": []interface{}{
					map[string]interface{}{"field": "a", "operator": "eq"},
				},
			},
			0, "and",
		},
		{
			"filters not a slice",
			map[string]interface{}{
				"filters": "not a slice",
			},
			0, "and",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters, logic := parseFilters(tt.args)
			if len(filters) != tt.wantLen {
				t.Errorf("len(filters) = %d, want %d", len(filters), tt.wantLen)
			}
			if logic != tt.wantLogic {
				t.Errorf("logic = %q, want %q", logic, tt.wantLogic)
			}
		})
	}
}

func TestParseFiltersOperators(t *testing.T) {
	args := map[string]interface{}{
		"filters": []interface{}{
			map[string]interface{}{"field": "type", "operator": "contains", "value": "code"},
		},
	}

	filters, _ := parseFilters(args)
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(filters))
	}
	if filters[0].Operator != gleann.FilterOperator("contains") {
		t.Errorf("operator = %q, want 'contains'", filters[0].Operator)
	}
	if filters[0].Field != "type" {
		t.Errorf("field = %q, want 'type'", filters[0].Field)
	}
}

func TestNewServerConfig(t *testing.T) {
	cfg := Config{
		IndexDir: "/tmp/test",
	}
	s := NewServer(cfg)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.config.IndexDir != "/tmp/test" {
		t.Errorf("IndexDir = %q, want /tmp/test", s.config.IndexDir)
	}
}

func TestServerGetSearcherError(t *testing.T) {
	cfg := Config{
		IndexDir: "/nonexistent/path",
	}
	s := NewServer(cfg)

	// Should error for nonexistent index
	_, err := s.getSearcher("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent index")
	}
}
