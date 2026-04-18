package gleann

import (
	"testing"
)

// ── MetadataFilterEngine ──────────────────────────────────────

func TestNewMetadataFilterEngine(t *testing.T) {
	filters := []MetadataFilter{
		{Field: "type", Operator: OpEqual, Value: "doc"},
	}
	e := NewMetadataFilterEngine(filters)
	if e == nil || len(e.Filters) != 1 || e.Logic != "and" {
		t.Fatal("unexpected engine state")
	}
}

func TestMetadataFilterEngine_Match_Empty(t *testing.T) {
	e := &MetadataFilterEngine{}
	if !e.Match(map[string]any{"a": 1}) {
		t.Fatal("empty filters should match everything")
	}
}

func TestMetadataFilterEngine_Match_And(t *testing.T) {
	e := &MetadataFilterEngine{
		Filters: []MetadataFilter{
			{Field: "type", Operator: OpEqual, Value: "doc"},
			{Field: "lang", Operator: OpEqual, Value: "en"},
		},
		Logic: "and",
	}
	if !e.Match(map[string]any{"type": "doc", "lang": "en"}) {
		t.Fatal("should match")
	}
	if e.Match(map[string]any{"type": "doc", "lang": "tr"}) {
		t.Fatal("should not match (lang mismatch)")
	}
}

func TestMetadataFilterEngine_Match_Or(t *testing.T) {
	e := &MetadataFilterEngine{
		Filters: []MetadataFilter{
			{Field: "type", Operator: OpEqual, Value: "doc"},
			{Field: "type", Operator: OpEqual, Value: "code"},
		},
		Logic: "or",
	}
	if !e.Match(map[string]any{"type": "doc"}) {
		t.Fatal("should match first")
	}
	if !e.Match(map[string]any{"type": "code"}) {
		t.Fatal("should match second")
	}
	if e.Match(map[string]any{"type": "image"}) {
		t.Fatal("should not match")
	}
}

func TestFilterResults_Cov(t *testing.T) {
	e := &MetadataFilterEngine{
		Filters: []MetadataFilter{
			{Field: "lang", Operator: OpEqual, Value: "en"},
		},
		Logic: "and",
	}
	results := []SearchResult{
		{Text: "a", Metadata: map[string]any{"lang": "en"}},
		{Text: "b", Metadata: map[string]any{"lang": "tr"}},
		{Text: "c", Metadata: map[string]any{"lang": "en"}},
	}
	filtered := e.FilterResults(results)
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
}

func TestFilterResults_Empty(t *testing.T) {
	e := &MetadataFilterEngine{}
	results := []SearchResult{{Text: "a"}}
	if len(e.FilterResults(results)) != 1 {
		t.Fatal("empty filter should return all results")
	}
}

// ── evaluateFilter: all operators ─────────────────────────────

func TestEvaluateFilter_Equal(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpEqual, Value: "hello"}
	if !evaluateFilter(f, map[string]any{"x": "hello"}) {
		t.Fatal("should match")
	}
	if evaluateFilter(f, map[string]any{"x": "world"}) {
		t.Fatal("should not match")
	}
}

func TestEvaluateFilter_NotEqual(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpNotEqual, Value: "hello"}
	if !evaluateFilter(f, map[string]any{"x": "world"}) {
		t.Fatal("should match")
	}
	if evaluateFilter(f, map[string]any{"x": "hello"}) {
		t.Fatal("should not match")
	}
}

func TestEvaluateFilter_GreaterThan(t *testing.T) {
	f := MetadataFilter{Field: "n", Operator: OpGreaterThan, Value: 5}
	if !evaluateFilter(f, map[string]any{"n": 10}) {
		t.Fatal("10>5 should match")
	}
	if evaluateFilter(f, map[string]any{"n": 3}) {
		t.Fatal("3>5 should not match")
	}
	if evaluateFilter(f, map[string]any{"n": 5}) {
		t.Fatal("5>5 should not match")
	}
}

func TestEvaluateFilter_GreaterEqual(t *testing.T) {
	f := MetadataFilter{Field: "n", Operator: OpGreaterEqual, Value: 5}
	if !evaluateFilter(f, map[string]any{"n": 5}) {
		t.Fatal("5>=5 should match")
	}
}

func TestEvaluateFilter_LessThan(t *testing.T) {
	f := MetadataFilter{Field: "n", Operator: OpLessThan, Value: 5}
	if !evaluateFilter(f, map[string]any{"n": 3}) {
		t.Fatal("3<5 should match")
	}
	if evaluateFilter(f, map[string]any{"n": 5}) {
		t.Fatal("5<5 should not match")
	}
}

func TestEvaluateFilter_LessEqual(t *testing.T) {
	f := MetadataFilter{Field: "n", Operator: OpLessEqual, Value: 5}
	if !evaluateFilter(f, map[string]any{"n": 5}) {
		t.Fatal("5<=5 should match")
	}
	if evaluateFilter(f, map[string]any{"n": 6}) {
		t.Fatal("6<=5 should not match")
	}
}

func TestEvaluateFilter_In(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpIn, Value: []any{"a", "b", "c"}}
	if !evaluateFilter(f, map[string]any{"x": "b"}) {
		t.Fatal("should be in list")
	}
	if evaluateFilter(f, map[string]any{"x": "d"}) {
		t.Fatal("should not be in list")
	}
}

func TestEvaluateFilter_InStrings(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpIn, Value: []string{"a", "b"}}
	if !evaluateFilter(f, map[string]any{"x": "a"}) {
		t.Fatal("should match")
	}
}

func TestEvaluateFilter_NotIn(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpNotIn, Value: []any{"a", "b"}}
	if !evaluateFilter(f, map[string]any{"x": "c"}) {
		t.Fatal("c not in [a,b] should match")
	}
	if evaluateFilter(f, map[string]any{"x": "a"}) {
		t.Fatal("a not in [a,b] should not match")
	}
}

func TestEvaluateFilter_Contains(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpContains, Value: "ell"}
	if !evaluateFilter(f, map[string]any{"x": "hello"}) {
		t.Fatal("should contain")
	}
	if evaluateFilter(f, map[string]any{"x": "world"}) {
		t.Fatal("should not contain")
	}
}

func TestEvaluateFilter_StartsWith(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpStartsWith, Value: "hel"}
	if !evaluateFilter(f, map[string]any{"x": "hello"}) {
		t.Fatal("should match")
	}
}

func TestEvaluateFilter_EndsWith(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpEndsWith, Value: "llo"}
	if !evaluateFilter(f, map[string]any{"x": "hello"}) {
		t.Fatal("should match")
	}
}

func TestEvaluateFilter_Exists(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpExists, Value: true}
	if !evaluateFilter(f, map[string]any{"x": 1}) {
		t.Fatal("field exists, want true")
	}
	if evaluateFilter(f, map[string]any{"y": 1}) {
		t.Fatal("field missing, want true → should not match")
	}
}

func TestEvaluateFilter_ExistsFalse(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpExists, Value: false}
	if !evaluateFilter(f, map[string]any{"y": 1}) {
		t.Fatal("field missing, want false → should match")
	}
	if evaluateFilter(f, map[string]any{"x": 1}) {
		t.Fatal("field exists, want false → should not match")
	}
}

func TestEvaluateFilter_MissingField(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpEqual, Value: "a"}
	if evaluateFilter(f, map[string]any{"y": "a"}) {
		t.Fatal("missing field should not match")
	}
}

func TestEvaluateFilter_UnknownOperator(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: "unknown", Value: "a"}
	if evaluateFilter(f, map[string]any{"x": "a"}) {
		t.Fatal("unknown operator should not match")
	}
}

// ── compareNumeric & toFloat64 ────────────────────────────────

func TestCompareNumeric_Cov(t *testing.T) {
	if compareNumeric(10, 5) != 1 {
		t.Fatal("10>5")
	}
	if compareNumeric(5, 10) != -1 {
		t.Fatal("5<10")
	}
	if compareNumeric(5, 5) != 0 {
		t.Fatal("5==5")
	}
}

func TestToFloat64_Cov(t *testing.T) {
	tests := []struct {
		in  any
		out float64
	}{
		{int(42), 42},
		{int64(100), 100},
		{float64(3.14), 3.14},
		{float32(2.5), 2.5},
		{"7.5", 7.5},
		{true, 0}, // unsupported type
	}
	for _, tt := range tests {
		got := toFloat64(tt.in)
		if got != tt.out {
			t.Errorf("toFloat64(%v) = %v, want %v", tt.in, got, tt.out)
		}
	}
}

func TestCompareIn_UnsupportedType(t *testing.T) {
	// compareIn with an unsupported list type
	if compareIn("a", "not-a-slice") {
		t.Fatal("unsupported list type should return false")
	}
}

// ── SearchOption functions ────────────────────────────────────

func TestWithMetadataFilter_Cov(t *testing.T) {
	f := MetadataFilter{Field: "x", Operator: OpEqual, Value: "y"}
	opt := WithMetadataFilter(f)
	cfg := &SearchConfig{}
	opt(cfg)
	if len(cfg.MetadataFilters) != 1 {
		t.Fatal("filter not set")
	}
}

func TestWithFilterLogic_Cov(t *testing.T) {
	opt := WithFilterLogic("or")
	cfg := &SearchConfig{}
	opt(cfg)
	if cfg.FilterLogic != "or" {
		t.Fatal("logic not set")
	}
}
