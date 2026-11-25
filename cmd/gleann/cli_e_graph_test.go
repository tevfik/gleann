package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/internal/graph/community"
)

// More CLI tests of `gleann graph`: a damaged code graph database, an empty
// graph, the git hook and wiki file-system failures, and the rendering
// helpers.

// eExecGraphDB runs statements against the code graph database of the
// analysis fixture index.
func eExecGraphDB(t *testing.T, indexDir string, stmts ...string) {
	t.Helper()
	name := analysisgolden.IndexName
	db, err := sql.Open("sqlite", filepath.Join(indexDir, name, name+".db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// eDropGraphTables drops every table of the fixture index database that
// holds the graph (not meta, symbols, files and passages), so the index
// still opens but every graph read fails.
func eDropGraphTables(t *testing.T, indexDir string) {
	t.Helper()
	name := analysisgolden.IndexName
	db, err := sql.Open("sqlite", filepath.Join(indexDir, name, name+".db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for pass := 0; pass < 4; pass++ {
		rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT IN ('meta', 'symbols', 'files', 'passages')`)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			names = append(names, n)
		}
		rows.Close()
		for _, n := range names {
			_, _ = db.Exec(`DROP TABLE IF EXISTS "` + n + `"`) // shadow tables go with their virtual table
		}
	}
}

func TestCLIEGraphDamagedDatabase(t *testing.T) {
	e := newCGraphEnv(t)
	eDropGraphTables(t, e.gdir)

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"deps", cSvcLoad}, "no such table: graph_edges"},
		{[]string{"callers", cSvcLoad}, "no such table: graph_edges"},
		{[]string{"path", cSvcLoad, cSvcSave}, `finding path from "` + cSvcLoad},
		{[]string{"query", "Load"}, `searching symbols for "Load"`},
		{[]string{"toc"}, "error listing documents"},
		{[]string{"viz"}, "load edges"},
		{[]string{"communities"}, "load edges"},
		{[]string{"export", "--format", "graphml"}, "error loading graph"},
		{[]string{"export", "--format", "cypher"}, "error loading graph"},
		{[]string{"wiki"}, "load edges"},
		{[]string{"map"}, "error loading graph"},
		{[]string{"risk"}, "error loading graph"},
		{[]string{"risk", "--by-file"}, "error loading graph"},
		{[]string{"explain", cSvcLoad}, "not found"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			r := e.graph(c.args...)
			wantExit(t, r, 1)
			mustContain(t, "graph "+strings.Join(c.args, " "), r.Err, c.want)
		})
	}
}

func TestCLIEGraphWithoutSymbols(t *testing.T) {
	e := newCGraphEnv(t)
	eExecGraphDB(t, e.gdir, "DELETE FROM graph_edges", "DELETE FROM symbols")

	// A graph database without symbols is no code graph: every analysis
	// command says how to build one instead of printing an empty result.
	for _, sub := range []string{"map", "risk", "viz", "communities", "wiki", "report"} {
		r := e.graph(sub)
		wantExit(t, r, 1)
		mustContain(t, "graph "+sub, r.Err, `index "fixture" has no code graph yet`, "gleann index build fixture --docs <dir> --graph")
	}
}

func TestCLIEGraphExplainExternalAndPaths(t *testing.T) {
	e := newCGraphEnv(t)
	long := "errors New is the constructor every failing path in this service uses, so a change to it touches every caller"
	wantExit(t, e.run("memory", "remember", long), 0)

	// errors.New is an external symbol: it is not in the symbol index, but
	// its callers are known and the memory about it is linked.
	r := e.graph("explain", "errors.New")
	wantExit(t, r, 0)
	mustContain(t, "external symbol", r.Out, "Explain: errors.New", "Called by (3, 1 production + 2 test):", "← [function] example.com/fixture.main",
		"🧪 [function] example.com/fixture/service.TestReport",
		"Linked Memory & Architectural Context (1):", "errors New is the constructor", "...")
	mustNotContain(t, "external symbol", r.Out, "Kind:", "Impact")

	// A path against the call direction is drawn with the reverse arrow.
	r = e.graph("path", cSvcLoad, cMain)
	wantExit(t, r, 0)
	mustContain(t, "reverse path", r.Out, "2 hops", "← [CALLED_BY]")
	r = e.graph("path", cMain, cSvcLoad)
	wantExit(t, r, 0)
	mustContain(t, "forward path", r.Out, "→ [CALLS]")
	mustNotContain(t, "forward path", r.Out, "CALLED_BY")
}

func TestCLIEGraphHookFileSystemFailures(t *testing.T) {
	e := newCLIEnv(t)

	// .git is a file: the hooks directory cannot be created below it.
	if err := os.WriteFile(".git", []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.run("graph", "hook", "install")
	wantExit(t, r, 1)
	mustContain(t, "no hooks dir", r.Err, "error:")
	if err := os.Remove(".git"); err != nil {
		t.Fatal(err)
	}

	// The hook path is a directory: it cannot be written.
	if err := os.MkdirAll(filepath.Join(".git", "hooks", "post-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	r = e.run("graph", "hook", "install")
	wantExit(t, r, 1)
	mustContain(t, "unwritable hook", r.Err, "error writing hook")
}

func TestCLIEGraphWikiFileSystemFailures(t *testing.T) {
	e := newCGraphEnv(t)

	// The output directory is below a regular file.
	file := filepath.Join(e.Home, "plain-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.graph("wiki", "--output", filepath.Join(file, "wiki"))
	wantExit(t, r, 1)
	mustContain(t, "wiki dir", r.Err, "error creating wiki dir")

	// A community article that cannot be created is skipped, the rest is written.
	out := filepath.Join(e.Home, "wiki")
	wantExit(t, e.graph("wiki", "--output", out), 0)
	articles, err := filepath.Glob(filepath.Join(out, "community-*.md"))
	if err != nil || len(articles) == 0 {
		t.Fatalf("no community articles: %v %v", articles, err)
	}
	for _, a := range articles {
		if err := os.Remove(a); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(a, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r = e.graph("wiki", "--output", out)
	wantExit(t, r, 0)
	mustContain(t, "wiki with blocked articles", r.Out, "Wiki generated:")
}

func TestCLIEGraphExportsSkipMissingNodes(t *testing.T) {
	// An edge may name a node that was never added (the CALLS graph keeps the
	// target of an external call); the exports skip such nodes.
	g := community.NewGraph()
	g.AddNode(community.Node{ID: "pkg.File", Name: "file.go", Kind: "file", File: "file.go"})
	g.AddNode(community.Node{ID: "pkg.Fn", Name: "Fn<T>", Kind: "function", File: "file.go"})
	g.AddEdge("pkg.Fn", "ext.Missing", 1)

	var ml, cy bytes.Buffer
	exportGraphML(&ml, g)
	exportCypher(&cy, g)
	if strings.Contains(ml.String(), `node id="ext.Missing"`) || strings.Contains(cy.String(), `{fqn: "ext.Missing"}) SET`) {
		t.Errorf("node without a declaration exported:\n%s\n%s", ml.String(), cy.String())
	}
	mustContain(t, "graphml", ml.String(), `node id="pkg.Fn"`, "Fn&lt;T&gt;", `source="pkg.Fn" target="ext.Missing"`)
	mustContain(t, "cypher", cy.String(), "MERGE (n:CodeFile {fqn: \"pkg.File\"})", "MERGE (n:Symbol {fqn: \"pkg.Fn\"})", `{fqn: "ext.Missing"}) MERGE`)
}

func TestCLIEGraphRenderHelpers(t *testing.T) {
	ranks := map[string]float64{"a": 0.5, "b": 0.05, "c": 0.0001}
	for _, c := range []struct {
		name  string
		score float64
		ranks map[string]float64
		want  string
	}{
		{"top rank fills the bar", 0.5, ranks, strings.Repeat("█", 10)},
		{"half", 0.25, ranks, strings.Repeat("█", 5)},
		{"tiny scores keep one block", 0.0001, ranks, "█"},
		{"no ranks", 0.3, map[string]float64{}, "▏"},
		{"all zero", 0, map[string]float64{"a": 0}, "▏"},
		{"above the maximum is clamped", 5, ranks, strings.Repeat("█", 10)},
	} {
		if got := renderBar(c.score, c.ranks); got != c.want {
			t.Errorf("%s: renderBar(%v) = %q, want %q", c.name, c.score, got, c.want)
		}
	}
	if got := formatScore(0.123456); got != "0.1235" {
		t.Errorf("formatScore(0.123456) = %q", got)
	}
	if got := formatScore(0.000123456); got != "0.000123" {
		t.Errorf("formatScore(0.000123456) = %q", got)
	}
	for level, icon := range map[string]string{"critical": "🔴", "high": "🟠", "medium": "🟡", "low": "🟢", "": "🟢"} {
		if got := riskIcon(level); got != icon {
			t.Errorf("riskIcon(%q) = %q, want %q", level, got, icon)
		}
	}
}
