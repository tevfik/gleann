package gleann

import "testing"

func TestFilterOperatorConstants(t *testing.T) {
	tests := []struct {
		op   FilterOperator
		want string
	}{
		{OpEqual, "eq"},
		{OpNotEqual, "ne"},
		{OpGreaterThan, "gt"},
		{OpGreaterEqual, "gte"},
		{OpLessThan, "lt"},
		{OpLessEqual, "lte"},
		{OpIn, "in"},
		{OpNotIn, "nin"},
		{OpContains, "contains"},
		{OpStartsWith, "startswith"},
		{OpEndsWith, "endswith"},
		{OpExists, "exists"},
	}
	for _, tt := range tests {
		if string(tt.op) != tt.want {
			t.Errorf("%v = %q, want %q", tt.op, string(tt.op), tt.want)
		}
	}
}

func TestEvaluateFilterAllOperators(t *testing.T) {
	tests := []struct {
		name     string
		filter   MetadataFilter
		metadata map[string]any
		want     bool
	}{
		// Numeric comparisons
		{"gt int", MetadataFilter{"v", OpGreaterThan, 5}, map[string]any{"v": 6}, true},
		{"gt int edge", MetadataFilter{"v", OpGreaterThan, 5}, map[string]any{"v": 5}, false},
		{"gte exact", MetadataFilter{"v", OpGreaterEqual, 5}, map[string]any{"v": 5}, true},
		{"lt", MetadataFilter{"v", OpLessThan, 5}, map[string]any{"v": 4}, true},
		{"lt edge", MetadataFilter{"v", OpLessThan, 5}, map[string]any{"v": 5}, false},
		{"lte exact", MetadataFilter{"v", OpLessEqual, 5}, map[string]any{"v": 5}, true},

		// String comparisons
		{"contains yes", MetadataFilter{"s", OpContains, "world"}, map[string]any{"s": "hello world"}, true},
		{"contains no", MetadataFilter{"s", OpContains, "xyz"}, map[string]any{"s": "hello world"}, false},
		{"startswith yes", MetadataFilter{"s", OpStartsWith, "hello"}, map[string]any{"s": "hello world"}, true},
		{"startswith no", MetadataFilter{"s", OpStartsWith, "world"}, map[string]any{"s": "hello world"}, false},
		{"endswith yes", MetadataFilter{"s", OpEndsWith, "world"}, map[string]any{"s": "hello world"}, true},
		{"endswith no", MetadataFilter{"s", OpEndsWith, "hello"}, map[string]any{"s": "hello world"}, false},

		// In / NotIn
		{"in string slice", MetadataFilter{"s", OpIn, []string{"a", "b", "c"}}, map[string]any{"s": "b"}, true},
		{"in string slice miss", MetadataFilter{"s", OpIn, []string{"a", "b", "c"}}, map[string]any{"s": "d"}, false},
		{"not in", MetadataFilter{"s", OpNotIn, []any{"a", "b"}}, map[string]any{"s": "c"}, true},
		{"not in miss", MetadataFilter{"s", OpNotIn, []any{"a", "b"}}, map[string]any{"s": "a"}, false},

		// Exists
		{"exists true present", MetadataFilter{"k", OpExists, true}, map[string]any{"k": "v"}, true},
		{"exists true missing", MetadataFilter{"k", OpExists, true}, map[string]any{}, false},
		{"exists false present", MetadataFilter{"k", OpExists, false}, map[string]any{"k": "v"}, false},
		{"exists false missing", MetadataFilter{"k", OpExists, false}, map[string]any{}, true},

		// Unknown operator
		{"unknown op", MetadataFilter{"k", "bogus", "v"}, map[string]any{"k": "v"}, false},

		// ne
		{"ne match", MetadataFilter{"k", OpNotEqual, "a"}, map[string]any{"k": "b"}, true},
		{"ne no match", MetadataFilter{"k", OpNotEqual, "a"}, map[string]any{"k": "a"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateFilter(tt.filter, tt.metadata)
			if got != tt.want {
				t.Errorf("evaluateFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want float64
	}{
		{"int", 42, 42.0},
		{"int64", int64(100), 100.0},
		{"float64", 3.14, 3.14},
		{"float32", float32(2.5), 2.5},
		{"string numeric", "42.5", 42.5},
		{"string non-numeric", "abc", 0},
		{"bool", true, 0}, // unsupported type
		{"nil", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toFloat64(tt.val)
			if got != tt.want {
				t.Errorf("toFloat64(%v) = %f, want %f", tt.val, got, tt.want)
			}
		})
	}
}

func TestCompareIn(t *testing.T) {
	tests := []struct {
		name  string
		value any
		list  any
		want  bool
	}{
		{"any slice match", "b", []any{"a", "b", "c"}, true},
		{"any slice no match", "d", []any{"a", "b", "c"}, false},
		{"string slice match", "b", []string{"a", "b", "c"}, true},
		{"string slice no match", "d", []string{"a", "b", "c"}, false},
		{"empty any slice", "a", []any{}, false},
		{"empty string slice", "a", []string{}, false},
		{"unsupported type", "a", "not a slice", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareIn(tt.value, tt.list)
			if got != tt.want {
				t.Errorf("compareIn(%v, %v) = %v, want %v", tt.value, tt.list, got, tt.want)
			}
		})
	}
}

func TestCompareNumeric(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want int
	}{
		{"less", 1, 2, -1},
		{"equal", 5, 5, 0},
		{"greater", 10, 5, 1},
		{"float", 1.5, 2.5, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareNumeric(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareNumeric(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFilterEngineORLogic(t *testing.T) {
	engine := &MetadataFilterEngine{
		Filters: []MetadataFilter{
			{Field: "lang", Operator: OpEqual, Value: "go"},
			{Field: "lang", Operator: OpEqual, Value: "rust"},
		},
		Logic: "or",
	}

	// Should match if any filter matches
	if !engine.Match(map[string]any{"lang": "go"}) {
		t.Error("OR: should match go")
	}
	if !engine.Match(map[string]any{"lang": "rust"}) {
		t.Error("OR: should match rust")
	}
	if engine.Match(map[string]any{"lang": "python"}) {
		t.Error("OR: should not match python")
	}
}

func TestFilterEngineMixedAND(t *testing.T) {
	engine := &MetadataFilterEngine{
		Filters: []MetadataFilter{
			{Field: "lang", Operator: OpEqual, Value: "go"},
			{Field: "score", Operator: OpGreaterThan, Value: 0.5},
		},
		Logic: "and",
	}

	if !engine.Match(map[string]any{"lang": "go", "score": 0.8}) {
		t.Error("AND: should match both conditions")
	}
	if engine.Match(map[string]any{"lang": "go", "score": 0.3}) {
		t.Error("AND: should not match if score too low")
	}
	if engine.Match(map[string]any{"lang": "python", "score": 0.8}) {
		t.Error("AND: should not match if lang wrong")
	}
}

func TestFilterResultsPreservesOrder(t *testing.T) {
	results := []SearchResult{
		{ID: 1, Score: 0.9, Metadata: map[string]any{"type": "code"}},
		{ID: 2, Score: 0.8, Metadata: map[string]any{"type": "doc"}},
		{ID: 3, Score: 0.7, Metadata: map[string]any{"type": "code"}},
		{ID: 4, Score: 0.6, Metadata: map[string]any{"type": "config"}},
	}

	engine := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "type", Operator: OpEqual, Value: "code"},
	})

	filtered := engine.FilterResults(results)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 results, got %d", len(filtered))
	}
	if filtered[0].ID != 1 || filtered[1].ID != 3 {
		t.Error("filtered results should preserve original order")
	}
}
