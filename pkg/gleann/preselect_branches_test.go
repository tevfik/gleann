package gleann

import (
	"context"
	"testing"
)

func TestCompileFilterSQLRejections(t *testing.T) {
	eq := func(field string, v any) MetadataFilter {
		return MetadataFilter{Field: field, Operator: OpEqual, Value: v}
	}
	cases := []struct {
		name    string
		filters []MetadataFilter
	}{
		{"no filters", nil},
		{"empty field", []MetadataFilter{eq("", "x")}},
		{"quote in field name", []MetadataFilter{eq(`a"b`, "x")}},
		{"backslash in field name", []MetadataFilter{eq(`a\b`, "x")}},
		{"control char in field name", []MetadataFilter{eq("a\x01b", "x")}},
		{"numeric operator", []MetadataFilter{{Field: "lang", Operator: OpGreaterThan, Value: 3}}},
		{"one bad branch spoils the set", []MetadataFilter{eq("lang", "go"), eq("chunk_index", 1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if where, args, ok := compileFilterSQL(c.filters, "and"); ok {
				t.Errorf("compiled %q %v, want a refusal", where, args)
			}
		})
	}

	// Logic picks the joiner between branches.
	and, _, ok := compileFilterSQL([]MetadataFilter{eq("lang", "go"), eq("kind", "fn")}, "and")
	or, _, ok2 := compileFilterSQL([]MetadataFilter{eq("lang", "go"), eq("kind", "fn")}, "or")
	if !ok || !ok2 || and == or {
		t.Fatalf("and=%q or=%q", and, or)
	}
}

func TestPreselectFilterIDs(t *testing.T) {
	ctx := context.Background()
	s := lexicalSearcher(t)
	engine := func(logic string, f ...MetadataFilter) *MetadataFilterEngine {
		e := NewMetadataFilterEngine(f)
		e.Logic = logic
		return e
	}
	src := func(v string) MetadataFilter { return MetadataFilter{Field: "source", Operator: OpEqual, Value: v} }

	t.Run("matches by source", func(t *testing.T) {
		got, ok := s.preselectFilterIDs(ctx, engine("and", src("bread.md")))
		if !ok || len(got) != 1 {
			t.Fatalf("got %v ok=%v, want the single bread.md passage", got, ok)
		}
	})
	t.Run("or widens the set", func(t *testing.T) {
		got, ok := s.preselectFilterIDs(ctx, engine("or", src("bread.md"), src("tart.md")))
		if !ok || len(got) != 2 {
			t.Fatalf("got %v ok=%v", got, ok)
		}
	})
	t.Run("filter the SQL compiler cannot express", func(t *testing.T) {
		if got, ok := s.preselectFilterIDs(ctx, engine("and", MetadataFilter{Field: "chunk_index", Operator: OpEqual, Value: 0})); ok || got != nil {
			t.Errorf("got %v ok=%v, want the loop's result to stand", got, ok)
		}
	})
	t.Run("not the SQLite store", func(t *testing.T) {
		other := &LeannSearcher{passages: passageList{}}
		if got, ok := other.preselectFilterIDs(ctx, engine("and", src("x"))); ok || got != nil {
			t.Errorf("got %v ok=%v", got, ok)
		}
	})
	t.Run("store failure degrades instead of failing", func(t *testing.T) {
		broken := lexicalSearcher(t)
		if err := broken.passages.Close(); err != nil {
			t.Fatal(err)
		}
		if got, ok := broken.preselectFilterIDs(ctx, engine("and", src("bread.md"))); ok || got != nil {
			t.Errorf("got %v ok=%v, want a quiet fallback", got, ok)
		}
	})
}
