package gleann

import (
	"encoding/json"
	"testing"
)

func decodeFilter(t *testing.T, raw string) MetadataFilter {
	t.Helper()
	var f MetadataFilter
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return f
}

// An exists filter without a value means true; an explicit false means false.
func TestExistsOmittedValueMeansTrue(t *testing.T) {
	with := map[string]any{"source": "a/b.go"}
	without := map[string]any{"lang": "go"}
	cases := []struct {
		raw      string
		with, wo bool
	}{
		{`{"field":"source","operator":"exists"}`, true, false},
		{`{"field":"source","operator":"exists","value":null}`, true, false},
		{`{"field":"source","operator":"exists","value":true}`, true, false},
		{`{"field":"source","operator":"exists","value":false}`, false, true},
	}
	for _, c := range cases {
		e := NewMetadataFilterEngine([]MetadataFilter{decodeFilter(t, c.raw)})
		if got := e.Match(with); got != c.with {
			t.Errorf("%s on passage with source: %v, want %v", c.raw, got, c.with)
		}
		if got := e.Match(without); got != c.wo {
			t.Errorf("%s on passage without source: %v, want %v", c.raw, got, c.wo)
		}
	}
}

func TestExistsRejectsNonBoolean(t *testing.T) {
	for _, v := range []any{"yes", 1.0, []any{true}} {
		if err := (MetadataFilter{Field: "k", Operator: OpExists, Value: v}).Validate(); err == nil {
			t.Errorf("exists value %#v accepted", v)
		}
	}
}

// ext and extension are derived: an empty extension means "has none".
func TestExtExistsIgnoresEmptyExtension(t *testing.T) {
	passages := map[string]map[string]any{
		"go":       {"source": "pkg/a.go"},
		"makefile": {"source": "Makefile"},
		"file key": {"file": "docs/README"},
		"empty":    {"ext": ""},
		"stored":   {"ext": ".md"},
		"fallback": {"ext": "", "source": "x/y.txt"},
		"none":     {"lang": "go"},
	}
	wantExt := map[string]bool{"go": true, "makefile": false, "file key": false, "empty": false, "stored": true, "fallback": true, "none": false}
	for _, field := range []string{"ext", "extension"} {
		yes := NewMetadataFilterEngine([]MetadataFilter{{Field: field, Operator: OpExists, Value: true}})
		no := NewMetadataFilterEngine([]MetadataFilter{{Field: field, Operator: OpExists, Value: false}})
		for name, md := range passages {
			if got := yes.Match(md); got != wantExt[name] {
				t.Errorf("%s exists on %s: %v, want %v", field, name, got, wantExt[name])
			}
			if got := no.Match(md); got == wantExt[name] {
				t.Errorf("%s exists false on %s: %v", field, name, got)
			}
		}
	}
}

// The SQL pre-selection reads an omitted exists value like the engine does.
func TestExistsOmittedValueSQLMatchesEngine(t *testing.T) {
	omitted, _, ok1 := compileFilterBranch(MetadataFilter{Field: "lang", Operator: OpExists})
	explicit, _, ok2 := compileFilterBranch(MetadataFilter{Field: "lang", Operator: OpExists, Value: true})
	if !ok1 || !ok2 || omitted != explicit {
		t.Errorf("omitted %q (%v) != explicit true %q (%v)", omitted, ok1, explicit, ok2)
	}
}

// ext has ONE definition: chunking.FileExt of the passage's file/source, the
// value indexing stores. "1.10" has the extension ".10"; a dot in a directory
// name is not an extension; stored and derived values compare lower-cased.
func TestExtDefinitionIsTheStoredOne(t *testing.T) {
	exists := NewMetadataFilterEngine([]MetadataFilter{{Field: "ext", Operator: OpExists}})
	for _, c := range []struct {
		md   map[string]any
		want bool
	}{
		{map[string]any{"source": "1.10"}, true},
		{map[string]any{"source": "1.10", "ext": ".10"}, true},
		{map[string]any{"source": "v1.2/Makefile"}, false},
		{map[string]any{"file": "v1.2/Makefile", "ext": ""}, false},
	} {
		if got := exists.Match(c.md); got != c.want {
			t.Errorf("ext exists on %v = %v, want %v", c.md, got, c.want)
		}
	}
	eq := NewMetadataFilterEngine([]MetadataFilter{{Field: "ext", Operator: OpEqual, Value: ".md"}})
	for _, md := range []map[string]any{{"ext": ".MD"}, {"source": "A/Readme.MD"}} {
		if !eq.Match(md) {
			t.Errorf("ext eq .md missed %v", md)
		}
	}
}

// The SQL pre-selection and the metadata index do not compile ext: both leave
// it to the engine, so no second definition of ext can exist.
func TestExtIsLeftToTheEngineBySQLAndIndex(t *testing.T) {
	for _, field := range []string{"ext", "extension"} {
		for _, f := range []MetadataFilter{
			{Field: field, Operator: OpExists},
			{Field: field, Operator: OpExists, Value: false},
			{Field: field, Operator: OpEqual, Value: ".10"},
			{Field: field, Operator: OpIn, Value: []any{".go", ".10"}},
		} {
			if sql, _, ok := compileFilterBranch(f); ok {
				t.Errorf("SQL compiled %v: %s", f, sql)
			}
			if _, ok := compileFilterIndex(f); ok {
				t.Errorf("filter index compiled %v", f)
			}
		}
	}
}

// Stored ext is lower-cased, so the filter value of ext/extension is too, for
// every operator that compares text; other fields keep their case.
func TestExtFilterValueIsCaseInsensitive(t *testing.T) {
	md := map[string]any{"source": "Docs/README.MD", "lang": "Go"}
	for _, field := range []string{"ext", "extension"} {
		for _, c := range []struct {
			op   FilterOperator
			val  any
			want bool
		}{
			{OpEqual, ".MD", true},
			{OpNotEqual, ".MD", false},
			{OpIn, []any{".GO", ".Md"}, true},
			{OpIn, []string{".GO", ".MD"}, true},
			{OpNotIn, []any{".MD"}, false},
			{OpStartsWith, ".M", true},
			{OpEndsWith, "D", true},
			{OpContains, ".MD", true},
		} {
			got := NewMetadataFilterEngine([]MetadataFilter{{Field: field, Operator: c.op, Value: c.val}}).Match(md)
			if got != c.want {
				t.Errorf("%s %s %v = %v, want %v", field, c.op, c.val, got, c.want)
			}
		}
	}
	if NewMetadataFilterEngine([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}).Match(md) {
		t.Error("lang stays case-sensitive")
	}
}
