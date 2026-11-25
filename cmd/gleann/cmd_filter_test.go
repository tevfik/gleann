package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// TestParseCLIFilter exercises the "field op value" grammar, the unary
// "exists" form (no value), JSON value decoding and the validation of every
// operator.
func TestParseCLIFilter(t *testing.T) {
	cases := []struct {
		name   string
		expr   string
		field  string
		op     gleann.FilterOperator
		hasVal bool
		value  any // compared by JSON when set
	}{
		{name: "string value", expr: "lang eq go", field: "lang", op: gleann.OpEqual, hasVal: true, value: "go"},
		{name: "number decodes as number", expr: "loc gt 100", field: "loc", op: gleann.OpGreaterThan, hasVal: true, value: float64(100)},
		{name: "bool decodes as bool", expr: "archived eq false", field: "archived", op: gleann.OpEqual, hasVal: true, value: false},
		{name: "quoted string decodes", expr: `name eq "hello world"`, field: "name", op: gleann.OpEqual, hasVal: true, value: "hello world"},
		{name: "array decodes", expr: "tags in [\"go\", \"api\"]", field: "tags", op: gleann.OpIn, hasVal: true, value: []any{"go", "api"}},
		{name: "exists unary, no value", expr: "lang exists", field: "lang", op: gleann.OpExists, hasVal: false},
		{name: "operator case-insensitive", expr: "kind EQ code", field: "kind", op: gleann.OpEqual, hasVal: true, value: "code"},
		{name: "startswith", expr: "source startswith internal/", field: "source", op: gleann.OpStartsWith, hasVal: true, value: "internal/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parseCLIFilter(tc.expr)
			if err != nil {
				t.Fatalf("parseCLIFilter(%q): unexpected error: %v", tc.expr, err)
			}
			if f.Field != tc.field {
				t.Errorf("field = %q, want %q", f.Field, tc.field)
			}
			if f.Operator != tc.op {
				t.Errorf("operator = %q, want %q", f.Operator, tc.op)
			}
			if !tc.hasVal {
				if f.Value != nil {
					t.Errorf("expected no value, got %#v", f.Value)
				}
				return
			}
			got, _ := json.Marshal(f.Value)
			want, _ := json.Marshal(tc.value)
			if string(got) != string(want) {
				t.Errorf("value = %s, want %s", got, want)
			}
		})
	}
}

// TestParseCLIFilterRejectsBad confirms that an unknown operator, a missing
// operator, and an empty filter are all rejected with an error.
func TestParseCLIFilterRejectsBad(t *testing.T) {
	bad := []string{
		"lang is go",      // unknown operator
		"lang",            // missing operator
		"  ",              // empty
		" eq go",          // missing field -> Fields gives ["eq","go"] -> field "eq" valid? operator "go" unknown -> rejected
		"loc between 1 5", // not an operator
	}
	for _, expr := range bad {
		if f, err := parseCLIFilter(expr); err == nil {
			t.Errorf("parseCLIFilter(%q) accepted, parsed as %+v", expr, f)
		}
	}
}

// TestParseCLIFiltersRepeatable checks the repeatable --filter flag on the
// command line, in both "--filter value" and "--filter=value" forms, and that
// it stops at "--".
func TestParseCLIFiltersRepeatable(t *testing.T) {
	args := []string{
		"idx", "query text",
		"--filter", "lang eq go",
		"--filter=kind eq code",
		"--filter", "loc gt 50",
		"--", "--filter", "this is text",
	}
	fs, err := parseCLIFilters(args)
	if err != nil {
		t.Fatalf("parseCLIFilters: %v", err)
	}
	if len(fs) != 3 {
		t.Fatalf("got %d filters, want 3: %+v", len(fs), fs)
	}
	if fs[0].Field != "lang" || fs[0].Value != "go" {
		t.Errorf("filter[0] = %+v", fs[0])
	}
	if fs[1].Field != "kind" || fs[1].Value != "code" {
		t.Errorf("filter[1] = %+v", fs[1])
	}
	if fs[2].Field != "loc" || fs[2].Value != float64(50) {
		t.Errorf("filter[2] = %+v", fs[2])
	}

	if none, err := parseCLIFilters([]string{"idx", "query"}); err != nil || none != nil {
		t.Errorf("no --filter should give (nil, nil), got (%#v, %v)", none, err)
	}
}

// TestParseCLIFiltersInvalidPropagates confirms a bad --filter surfaces as an
// error (the command turns this into exit 2).
func TestParseCLIFiltersInvalidPropagates(t *testing.T) {
	_, err := parseCLIFilters([]string{"idx", "q", "--filter", "lang whoops go"})
	if err == nil {
		t.Fatal("expected an error for an unknown operator")
	}
	if !strings.Contains(err.Error(), "whoops") {
		t.Errorf("error should name the bad operator, got: %v", err)
	}
}

// TestSearchFilterFlagExitsTwo checks that cmdSearch turns an invalid
// --filter into exit code 2 (usage error) before touching any index.
func TestSearchFilterFlagExitsTwo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	for _, k := range []string{"GLEANN_HOME", "GLEANN_INDEX_DIR", "GLEANN_MODEL", "OLLAMA_HOST"} {
		t.Setenv(k, "")
	}
	code := runExit(t, func() {
		cmdSearch([]string{"definitely-no-such-index", "some query", "--filter", "lang whoops go"})
	})
	if code != 2 {
		t.Fatalf("invalid --filter: exit code = %d, want 2", code)
	}
}

// TestSearchFilterFlagAcceptsValid checks that a valid --filter reaches the
// searcher path (and fails later only because the index does not exist, which
// is exit 1, never the usage-error 2 from a filter problem).
func TestSearchFilterFlagAcceptsValid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	for _, k := range []string{"GLEANN_HOME", "GLEANN_INDEX_DIR", "GLEANN_MODEL", "OLLAMA_HOST"} {
		t.Setenv(k, "")
	}
	code := runExit(t, func() {
		cmdSearch([]string{"definitely-no-such-index", "some query", "--filter", "lang eq go", "--filter", "kind exists"})
	})
	if code == 2 {
		t.Fatalf("valid --filter should not exit with the usage-error status 2")
	}
}

// TestFilterFlagsListedInHelp confirms --filter is documented in both
// `search --help` and `ask --help`, so the flag is discoverable and the
// help-coverage tests stay green.
func TestFilterFlagsListedInHelp(t *testing.T) {
	sh := captureHelp(t, func() { printSearchUsage() })
	if !strings.Contains(sh, "--filter") {
		t.Error("search --help must document --filter")
	}
	ah := captureHelp(t, func() { printCmdAskUsage() })
	if !strings.Contains(ah, "--filter") {
		t.Error("ask --help must document --filter")
	}
}
