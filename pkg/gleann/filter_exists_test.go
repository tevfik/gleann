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
