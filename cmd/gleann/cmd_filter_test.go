package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		{name: "eq value stays a string", expr: "archived eq false", field: "archived", op: gleann.OpEqual, hasVal: true, value: "false"},
		{name: "quoted string decodes", expr: `name eq "hello world"`, field: "name", op: gleann.OpEqual, hasVal: true, value: "hello world"},
		{name: "array decodes", expr: "tags in [\"go\", \"api\"]", field: "tags", op: gleann.OpIn, hasVal: true, value: []any{"go", "api"}},
		{name: "exists unary means true", expr: "lang exists", field: "lang", op: gleann.OpExists, hasVal: true, value: true},
		{name: "exists false", expr: "lang exists false", field: "lang", op: gleann.OpExists, hasVal: true, value: false},
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
		"lang eq",         // missing value
		"lang eq   ",      // missing value
		"kind EQ code",    // operators are case-sensitive like REST/MCP
		"loc gt many",     // gt needs a number
		"tags in go",      // in needs a JSON array
		"a exists maybe",  // exists takes true/false
		`t eq "unterminated`,
		"loc lte NaN",  // not a finite number
		"loc gt Inf",   // not a finite number
		"loc gt -Inf",  // not a finite number
		"tags in null", // needs a non-null array
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

// TestParseCLIFilterExistsEvaluates runs the parsed filter through the engine:
// 'exists' keeps passages WITH the field, 'exists false' those without.
func TestParseCLIFilterExistsEvaluates(t *testing.T) {
	with := map[string]any{"author": "x"}
	without := map[string]any{"other": "x"}
	for expr, want := range map[string][2]bool{
		"author exists":       {true, false},
		"author exists true":  {true, false},
		"author exists false": {false, true},
	} {
		f, err := parseCLIFilter(expr)
		if err != nil {
			t.Fatalf("%q: %v", expr, err)
		}
		e := gleann.NewMetadataFilterEngine([]gleann.MetadataFilter{f})
		if got := e.Match(with); got != want[0] {
			t.Errorf("%q on passage with field: got %v want %v", expr, got, want[0])
		}
		if got := e.Match(without); got != want[1] {
			t.Errorf("%q on passage without field: got %v want %v", expr, got, want[1])
		}
	}
}

// TestParseCLIFilterLossless pins whitespace and numeric-looking strings.
func TestParseCLIFilterLossless(t *testing.T) {
	f, err := parseCLIFilter(`title eq "a  b"`)
	if err != nil || f.Value != "a  b" {
		t.Fatalf("quoted: %#v %v", f.Value, err)
	}
	f, err = parseCLIFilter("title eq a  b")
	if err != nil || f.Value != "a  b" {
		t.Fatalf("verbatim rest: %#v %v", f.Value, err)
	}
	f, err = parseCLIFilter("version eq 1.10")
	if err != nil || f.Value != "1.10" {
		t.Fatalf("1.10 must stay a string: %#v %v", f.Value, err)
	}
	e := gleann.NewMetadataFilterEngine([]gleann.MetadataFilter{f})
	if !e.Match(map[string]any{"version": "1.10"}) {
		t.Error("version eq 1.10 must match \"1.10\"")
	}
}

// TestParseCLIFiltersEmptyAndDangling rejects --filter= and a trailing --filter.
func TestParseCLIFiltersEmptyAndDangling(t *testing.T) {
	for _, args := range [][]string{{"i", "q", "--filter="}, {"i", "q", "--filter"}} {
		if _, err := parseCLIFilters(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if fs, err := parseCLIFilters([]string{"i", "q", "--", "--filter="}); err != nil || fs != nil {
		t.Errorf("after -- is text: %v %v", fs, err)
	}
}

// TestSearchFilterValidatedBeforeBackendInit: a bad --filter exits 2 even when
// the config points at an unreachable embedder, before any init.
func TestSearchFilterEmptyExitsTwo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	code := runExit(t, func() {
		cmdSearch([]string{"no-such-index", "q", "--filter="})
	})
	if code != 2 {
		t.Fatalf("empty --filter=: exit %d, want 2", code)
	}
}

// TestMemoryReflectLimitJSONNeedList: --limit/--json without --list exit 2 and
// never open the memory manager or run a cycle.
func TestMemoryReflectLimitJSONNeedList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(home, "mem"))
	for _, args := range [][]string{{"--limit", "5"}, {"--json"}, {"--limit", "5", "--json"}, {"--limit=5"}, {"--limit=0"}, {"--json=no"}} {
		code := runExit(t, func() { cmdMemoryReflect(args) })
		if code != 2 {
			t.Errorf("reflect %v: exit %d, want 2", args, code)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "mem")); err == nil {
		t.Error("memory dir created: reflect proceeded past flag check")
	}
}

// Unquoted values lose trailing whitespace; quoted ones keep it.
func TestParseCLIFilterTrimsTrailingSpace(t *testing.T) {
	f, err := parseCLIFilter("lang eq go \t")
	if err != nil || f.Value != "go" {
		t.Fatalf("unquoted: %#v %v", f.Value, err)
	}
	f, err = parseCLIFilter(`lang eq "go "`)
	if err != nil || f.Value != "go " {
		t.Fatalf("quoted: %#v %v", f.Value, err)
	}
}
