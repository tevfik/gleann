//go:build treesitter

package indexing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// analysisFixtureDir is the RFC 004 A0 fixture project (Go, Python, TypeScript,
// Rust) shared with the analysis goldens.
func analysisFixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "graph", "testdata", "analysis_fixture")
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

type goldenCallee struct {
	FQN string `json:"fqn"`
}

// callsGolden is one entry of testdata/analysis_golden/calls_nongo.json.
type callsGolden struct {
	Kind    string         `json:"kind"`
	File    string         `json:"file"`
	Line    int64          `json:"line"`
	Callees []goldenCallee `json:"callees"`
}

// nonGoEdge is a CALLS edge reduced to what Kùzu and the SQLite graph agree on:
// the file and the top-level declaration of the caller (Kùzu charges a call made
// inside a method to its class, the SQLite graph to the method) and the bare
// name of the callee (Kùzu stores a phantom symbol "pkg.add" for a call to a
// method, the SQLite graph binds it to "pkg.Registry::add" or keeps an
// external edge). See internal/graph/testdata/analysis_golden/README.md.
type nonGoEdge struct{ File, Caller, Callee string }

func lastAfterSep(s, sep string) string {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[i+len(sep):]
	}
	return s
}

func nonGoCalleeBase(fqn string) string {
	if strings.Contains(fqn, "::") {
		return lastAfterSep(fqn, "::")
	}
	return lastAfterSep(fqn, ".")
}

func TestFormat2NonGoCallsMatchKuzuGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(analysisFixtureDir(t), "..", "analysis_golden", "calls_nongo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]callsGolden
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}

	// Expected edges. Symbols named "main" are skipped as sources: Kùzu's
	// base-name union gives the main of one language the callees of the
	// mains of the others (golden README, "Observed behaviours").
	//
	// Kùzu also stored a phantom symbol for every callee declared nowhere
	// (len, strip, map, keys ...). The SQLite graph keeps no such target
	// (issue #12), so only callees whose bare name is a declaration of the
	// fixture are compared.
	declaredBase := map[string]bool{}
	for fqn, g := range golden {
		if g.File != "" {
			declaredBase[nonGoCalleeBase(fqn)] = true
		}
	}
	want := map[nonGoEdge]bool{}
	for fqn, g := range golden {
		if g.File == "" {
			continue // phantom symbol: no declaration, no edges of its own
		}
		name := lastAfterSep(fqn, ".")
		if name == "main" {
			continue
		}
		caller := strings.SplitN(name, "::", 2)[0]
		for _, c := range g.Callees {
			if !declaredBase[nonGoCalleeBase(c.FQN)] {
				continue // Kùzu phantom: a builtin or library call
			}
			want[nonGoEdge{g.File, caller, nonGoCalleeBase(c.FQN)}] = true
		}
	}
	if len(want) == 0 {
		t.Fatal("no golden edges compared")
	}

	env := newEnv(t)
	buildFormat2(t, env, analysisFixtureDir(t))
	db := openGraphDB(t, env)

	rows, err := db.Query(`SELECT s.file, s.name, e.dst FROM edges e JOIN symbols s ON s.fqn = e.src
WHERE e.kind = 'CALLS' AND (s.file LIKE '%.py' OR s.file LIKE '%.ts' OR s.file LIKE '%.rs')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[nonGoEdge]bool{}
	for rows.Next() {
		var file, name, dst string
		if err := rows.Scan(&file, &name, &dst); err != nil {
			t.Fatal(err)
		}
		if name == "main" {
			continue
		}
		got[nonGoEdge{file, strings.SplitN(name, "::", 2)[0], nonGoCalleeBase(dst)}] = true
	}
	for e := range want {
		if !got[e] {
			t.Errorf("Kùzu edge missing from the SQLite graph: %+v", e)
		}
	}
	for e := range got {
		if !want[e] {
			t.Errorf("SQLite edge Kùzu did not have: %+v", e)
		}
	}

	// Anchored declarations: same kind, file and start line (the script
	// symbol exists only for top-level calls and is named differently).
	for fqn, g := range golden {
		if g.File == "" || g.Kind == "script" {
			continue
		}
		var kind, file string
		var line int64
		err := db.QueryRow(`SELECT kind, file, start_line FROM symbols WHERE fqn = ?`, fqn).Scan(&kind, &file, &line)
		if err != nil {
			t.Errorf("%s: declaration missing: %v", fqn, err)
			continue
		}
		if kind != g.Kind || file != g.File || line != g.Line {
			t.Errorf("%s: got %s %s:%d, Kùzu %s %s:%d", fqn, kind, file, line, g.Kind, g.File, g.Line)
		}
	}

	// The module-level call of app.py keeps main's caller, anchored.
	var script string
	var line, endLine, col, off int64
	if err := db.QueryRow(`SELECT fqn, start_line, end_line, start_col, byte_offset FROM symbols WHERE kind = 'script' AND file = 'py/app.py'`).Scan(&script, &line, &endLine, &col, &off); err != nil {
		t.Fatalf("script symbol of py/app.py: %v", err)
	}
	if line != 1 || col != 1 || off != 0 || endLine < 2 {
		t.Errorf("script anchor = line %d-%d col %d offset %d, want 1-N, col 1, offset 0", line, endLine, col, off)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND src = ? AND dst LIKE '%py.main'`, script); n != 1 {
		t.Errorf("%s -> py.main edges = %d, want 1", script, n)
	}
	assertNoOrphans(t, db)
}
