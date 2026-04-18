package gleann

import (
	"testing"
)

// ── MetadataFilterEngine ───────────────────────

func TestFilterEngineNoFilters(t *testing.T) {
	e := NewMetadataFilterEngine(nil)
	if !e.Match(map[string]any{"foo": "bar"}) {
		t.Fatal("no filters should match everything")
	}
}

func TestFilterEngineEqualMatch(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "lang", Operator: OpEqual, Value: "go"},
	})
	if !e.Match(map[string]any{"lang": "go"}) {
		t.Fatal("should match")
	}
	if e.Match(map[string]any{"lang": "python"}) {
		t.Fatal("should not match")
	}
}

func TestFilterEngineNotEqual(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "lang", Operator: OpNotEqual, Value: "go"},
	})
	if e.Match(map[string]any{"lang": "go"}) {
		t.Fatal("should not match")
	}
	if !e.Match(map[string]any{"lang": "python"}) {
		t.Fatal("should match")
	}
}

func TestFilterEngineGreaterThan(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "score", Operator: OpGreaterThan, Value: 0.5},
	})
	if !e.Match(map[string]any{"score": 0.7}) {
		t.Fatal("should match")
	}
	if e.Match(map[string]any{"score": 0.3}) {
		t.Fatal("should not match")
	}
	if e.Match(map[string]any{"score": 0.5}) {
		t.Fatal("equal should not match gt")
	}
}

func TestFilterEngineGreaterEqual(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "score", Operator: OpGreaterEqual, Value: 0.5},
	})
	if !e.Match(map[string]any{"score": 0.5}) {
		t.Fatal("should match equal")
	}
	if !e.Match(map[string]any{"score": 0.8}) {
		t.Fatal("should match greater")
	}
}

func TestFilterEngineLessThan(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "score", Operator: OpLessThan, Value: 0.5},
	})
	if !e.Match(map[string]any{"score": 0.3}) {
		t.Fatal("should match")
	}
	if e.Match(map[string]any{"score": 0.7}) {
		t.Fatal("should not match")
	}
}

func TestFilterEngineLessEqual(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "score", Operator: OpLessEqual, Value: 0.5},
	})
	if !e.Match(map[string]any{"score": 0.5}) {
		t.Fatal("should match equal")
	}
	if !e.Match(map[string]any{"score": 0.2}) {
		t.Fatal("should match less")
	}
}

func TestFilterEngineIn(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "ext", Operator: OpIn, Value: []any{".go", ".py", ".rs"}},
	})
	if !e.Match(map[string]any{"ext": ".go"}) {
		t.Fatal("should match")
	}
	if e.Match(map[string]any{"ext": ".js"}) {
		t.Fatal("should not match")
	}
}

func TestFilterEngineInStringSlice(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "ext", Operator: OpIn, Value: []string{".go", ".py"}},
	})
	if !e.Match(map[string]any{"ext": ".go"}) {
		t.Fatal("should match")
	}
}

func TestFilterEngineNotIn(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "ext", Operator: OpNotIn, Value: []any{".go", ".py"}},
	})
	if e.Match(map[string]any{"ext": ".go"}) {
		t.Fatal("should not match")
	}
	if !e.Match(map[string]any{"ext": ".rs"}) {
		t.Fatal("should match")
	}
}

func TestFilterEngineContains(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "path", Operator: OpContains, Value: "internal"},
	})
	if !e.Match(map[string]any{"path": "src/internal/foo.go"}) {
		t.Fatal("should match")
	}
	if e.Match(map[string]any{"path": "src/external/foo.go"}) {
		t.Fatal("should not match")
	}
}

func TestFilterEngineStartsWith(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "path", Operator: OpStartsWith, Value: "src/"},
	})
	if !e.Match(map[string]any{"path": "src/main.go"}) {
		t.Fatal("should match")
	}
	if e.Match(map[string]any{"path": "pkg/main.go"}) {
		t.Fatal("should not match")
	}
}

func TestFilterEngineEndsWith(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "path", Operator: OpEndsWith, Value: ".go"},
	})
	if !e.Match(map[string]any{"path": "main.go"}) {
		t.Fatal("should match")
	}
	if e.Match(map[string]any{"path": "main.py"}) {
		t.Fatal("should not match")
	}
}

func TestFilterEngineExists(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "source", Operator: OpExists, Value: true},
	})
	if !e.Match(map[string]any{"source": "file.go"}) {
		t.Fatal("should match when field exists")
	}
	if e.Match(map[string]any{"other": "val"}) {
		t.Fatal("should not match when field missing")
	}
}

func TestFilterEngineExistsFalse(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "source", Operator: OpExists, Value: false},
	})
	if !e.Match(map[string]any{"other": "val"}) {
		t.Fatal("should match when field missing")
	}
	if e.Match(map[string]any{"source": "file.go"}) {
		t.Fatal("should not match when field exists")
	}
}

func TestFilterEngineOrLogic(t *testing.T) {
	e := &MetadataFilterEngine{
		Filters: []MetadataFilter{
			{Field: "lang", Operator: OpEqual, Value: "go"},
			{Field: "lang", Operator: OpEqual, Value: "python"},
		},
		Logic: "or",
	}
	if !e.Match(map[string]any{"lang": "go"}) {
		t.Fatal("should match first")
	}
	if !e.Match(map[string]any{"lang": "python"}) {
		t.Fatal("should match second")
	}
	if e.Match(map[string]any{"lang": "rust"}) {
		t.Fatal("should not match either")
	}
}

func TestFilterEngineAndLogic(t *testing.T) {
	e := &MetadataFilterEngine{
		Filters: []MetadataFilter{
			{Field: "lang", Operator: OpEqual, Value: "go"},
			{Field: "score", Operator: OpGreaterThan, Value: 0.5},
		},
		Logic: "and",
	}
	if !e.Match(map[string]any{"lang": "go", "score": 0.8}) {
		t.Fatal("should match both")
	}
	if e.Match(map[string]any{"lang": "go", "score": 0.3}) {
		t.Fatal("should fail on second")
	}
	if e.Match(map[string]any{"lang": "py", "score": 0.8}) {
		t.Fatal("should fail on first")
	}
}

func TestFilterEngineMissingField(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "lang", Operator: OpEqual, Value: "go"},
	})
	if e.Match(map[string]any{}) {
		t.Fatal("missing field should not match")
	}
}

func TestFilterEngineUnknownOperator(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "lang", Operator: "unknown", Value: "go"},
	})
	if e.Match(map[string]any{"lang": "go"}) {
		t.Fatal("unknown operator should not match")
	}
}

func TestFilterResults(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "ext", Operator: OpEqual, Value: ".go"},
	})
	results := []SearchResult{
		{Text: "a", Metadata: map[string]any{"ext": ".go"}},
		{Text: "b", Metadata: map[string]any{"ext": ".py"}},
		{Text: "c", Metadata: map[string]any{"ext": ".go"}},
	}
	filtered := e.FilterResults(results)
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
}

func TestFilterResultsNoFilters(t *testing.T) {
	e := NewMetadataFilterEngine(nil)
	results := []SearchResult{{Text: "a"}, {Text: "b"}}
	filtered := e.FilterResults(results)
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
}

// ── WithMetadataFilter / WithFilterLogic ───────

func TestWithMetadataFilterOption(t *testing.T) {
	cfg := SearchConfig{}
	opt := WithMetadataFilter(MetadataFilter{Field: "f", Operator: OpEqual, Value: "v"})
	opt(&cfg)
	if len(cfg.MetadataFilters) != 1 {
		t.Fatal("should set one filter")
	}
}

func TestWithFilterLogicOption(t *testing.T) {
	cfg := SearchConfig{}
	opt := WithFilterLogic("or")
	opt(&cfg)
	if cfg.FilterLogic != "or" {
		t.Fatalf("expected or, got %s", cfg.FilterLogic)
	}
}

// ── compareNumeric edge cases ──────────────────

func TestToFloat64Types(t *testing.T) {
	tests := []struct {
		in  any
		out float64
	}{
		{42, 42.0},
		{int64(100), 100.0},
		{float64(3.14), 3.14},
		{float32(2.5), 2.5},
		{"1.5", 1.5},
		{struct{}{}, 0},
		{nil, 0},
	}
	for _, tt := range tests {
		if got := toFloat64(tt.in); got != tt.out {
			t.Errorf("toFloat64(%v) = %v, want %v", tt.in, got, tt.out)
		}
	}
}

func TestCompareEqualStrings(t *testing.T) {
	if !compareEqual("abc", "abc") {
		t.Fatal("should match")
	}
	if compareEqual("abc", "def") {
		t.Fatal("should not match")
	}
}

func TestCompareInEmpty(t *testing.T) {
	if compareIn("go", []any{}) {
		t.Fatal("empty list should not match")
	}
	if compareIn("go", 42) {
		t.Fatal("non-list should not match")
	}
}

// ── Numeric comparison ─────────────────────────

func TestFilterEngineIntComparison(t *testing.T) {
	e := NewMetadataFilterEngine([]MetadataFilter{
		{Field: "count", Operator: OpGreaterThan, Value: 10},
	})
	if !e.Match(map[string]any{"count": 20}) {
		t.Fatal("int to int comparison should work")
	}
	if e.Match(map[string]any{"count": 5}) {
		t.Fatal("should not match")
	}
}
