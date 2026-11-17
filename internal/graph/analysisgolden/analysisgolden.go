// Package analysisgolden is the test support for the analysis goldens of
// RFC 004 (internal/graph/testdata/analysis_golden): the A0 files frozen from
// the former Kùzu path, which the SQLite graph is still compared against, and
// the sqlite_lite/ files of the SQLite path itself. It builds the format-2
// code graph of the fixture project (BuildIndexFormat2), normalises volatile
// parts of an output and compares or rewrites a golden file. It is imported
// by tests only: the CLI (cmd/gleann), the MCP tools (internal/mcp), the REST
// handlers (internal/server) and the indexing tests.
package analysisgolden

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// IndexName is the name of the index built from the fixture.
const IndexName = "fixture"

// Update rewrites the golden files instead of comparing against them.
var Update = flag.Bool("update", false, "rewrite the analysis golden files")

func thisDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

// FixtureDir returns the real path of the fixture project.
func FixtureDir(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(thisDir(), "..", "testdata", "analysis_fixture")
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("fixture dir: %v", err)
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		t.Fatalf("fixture dir: %v", err)
	}
	return abs
}

// GoldenDir returns the directory holding the golden files.
func GoldenDir() string {
	return filepath.Join(thisDir(), "..", "testdata", "analysis_golden")
}

// Env is an index of the fixture in a temporary index directory.
type Env struct {
	IndexDir string // holds <IndexName>/<IndexName>.db (format 2)
	Fixture  string // absolute fixture path
}

// placeholderPathRe matches a placeholder and the path components after it,
// separated by "/", "\\" or a JSON-escaped "\\\\" (Windows paths).
var placeholderPathRe = regexp.MustCompile(`<(?:FIXTURE|INDEXDIR)>(?:(?:/|\\\\|\\)[A-Za-z0-9._-]+)+`)

// replaceDir replaces dir by placeholder in s, in the forms an output may
// carry it on Windows too (backslashes, forward slashes, JSON-escaped
// backslashes), and writes the path components after it with "/", so one
// golden file serves every platform.
func replaceDir(s, dir, placeholder string) string {
	if dir == "" {
		return s
	}
	for _, form := range []string{strings.ReplaceAll(dir, `\`, `\\`), dir, strings.ReplaceAll(dir, `\`, "/")} {
		s = strings.ReplaceAll(s, form, placeholder)
	}
	return placeholderPathRe.ReplaceAllStringFunc(s, func(m string) string {
		m = strings.ReplaceAll(m, `\\`, "/")
		return strings.ReplaceAll(m, `\`, "/")
	})
}

var (
	floatRe    = regexp.MustCompile(`-?\d+\.\d{5,}`)
	durationRe = regexp.MustCompile(`\((?:\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m))+\)`)
	stampRe    = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)
	queryMsRe  = regexp.MustCompile(`"query_ms": ?\d+`)
)

// Normalize replaces the volatile parts of an output: the fixture and index
// directories, timestamps, durations, query timings and floating-point noise
// (numbers with five or more decimals are rounded to four).
func (e *Env) Normalize(s string) string {
	s = replaceDir(s, e.Fixture, "<FIXTURE>")
	s = replaceDir(s, e.IndexDir, "<INDEXDIR>")
	s = stampRe.ReplaceAllString(s, "<TIMESTAMP>")
	s = durationRe.ReplaceAllString(s, "(<DURATION>)")
	s = queryMsRe.ReplaceAllString(s, `"query_ms": 0`)
	s = floatRe.ReplaceAllStringFunc(s, func(m string) string {
		f, err := strconv.ParseFloat(m, 64)
		if err != nil {
			return m
		}
		out := strconv.FormatFloat(f, 'f', 4, 64)
		if out == "-0.0000" {
			out = "0.0000"
		}
		return out
	})
	return s
}

// CanonicalJSON re-encodes a JSON document with sorted keys and indentation
// after normalising it. Arrays keep their order.
func (e *Env) CanonicalJSON(t testing.TB, raw string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("canonical JSON: %v\n%s", err, raw)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return e.Normalize(string(b)) + "\n"
}

// Check compares got with the golden file name (or rewrites it with -update).
func Check(t testing.TB, name, got string) {
	t.Helper()
	path := filepath.Join(GoldenDir(), name)
	if *Update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (generate with -update): %v", name, err)
	}
	if string(want) == got {
		return
	}
	wl, gl := strings.Split(string(want), "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			t.Errorf("%s differs from the golden at line %d\n golden: %q\n    got: %q\n(%d vs %d lines; rewrite with -update only if the change is intended)",
				name, i+1, w, g, len(wl), len(gl))
			return
		}
	}
}

// Capture runs fn with os.Stdout redirected and returns what it printed.
func Capture(t testing.TB, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	func() {
		defer func() {
			os.Stdout = old
			_ = w.Close()
		}()
		fn()
	}()
	out := <-done
	_ = r.Close()
	return out
}
