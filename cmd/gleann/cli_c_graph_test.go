package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/pkg/gleann"
)

// CLI tests of `gleann graph ...` on the format-2 code graph of the analysis
// fixture (internal/graph/testdata/analysis_fixture), driven through main().

const (
	cSvcLoad = "example.com/fixture/service.Service.Load"
	cSvcSave = "example.com/fixture/service.Service.Save"
	cMain    = "example.com/fixture.main"
)

// cGraphEnv is a CLI environment plus the fixture graph index.
type cGraphEnv struct {
	*cliEnv
	gdir string // index dir holding the fixture graph
}

func newCGraphEnv(t *testing.T) *cGraphEnv {
	t.Helper()
	e := newCLIEnv(t)
	g := analysisgolden.BuildIndexFormat2(t)
	return &cGraphEnv{cliEnv: e, gdir: g.IndexDir}
}

// graph runs `gleann graph <args...> --index fixture --index-dir <gdir>`.
func (e *cGraphEnv) graph(args ...string) cliResult {
	e.t.Helper()
	full := append([]string{"graph"}, args...)
	full = append(full, "--index", analysisgolden.IndexName, "--index-dir", e.gdir)
	return e.run(full...)
}

func TestCLICGraphUsageAndArgumentErrors(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("graph")
	wantExit(t, r, 1)
	mustContain(t, "graph without sub", r.Out, "gleann graph — Code graph analysis", "graph deps")

	for _, sub := range []string{"help", "--help"} {
		r = e.run("graph", sub)
		wantExit(t, r, 0)
		mustContain(t, "graph "+sub, r.Out, "Usage:", "graph toc")
	}

	// A symbol subcommand without a symbol prints the usage and fails.
	r = e.run("graph", "deps")
	wantExit(t, r, 1)
	mustContain(t, "graph deps without symbol", r.Out, "Usage:")

	// A symbol subcommand without --index.
	r = e.run("graph", "deps", "main")
	wantExit(t, r, 1)
	mustContain(t, "graph deps without --index", r.Err, "--index flag required for graph queries")

	// An index without a graph (and one that does not exist).
	r = e.run("graph", "callers", "main", "--index", "nope")
	wantExit(t, r, 1)
	mustContain(t, "graph on a missing index", r.Err, "error opening graph db for index nope")

	// Each analysis subcommand needs --index.
	for _, sub := range []string{"viz", "report", "communities", "export", "wiki", "map", "risk", "toc"} {
		r = e.run("graph", sub)
		wantExit(t, r, 1)
		mustContain(t, "graph "+sub+" without --index", r.Err, "--index flag required")
	}

	// The analysis commands on an index without a code graph.
	for _, sub := range []string{"viz", "communities", "map", "risk", "wiki"} {
		r = e.run("graph", sub, "--index", "nope")
		wantExit(t, r, 1)
		mustContain(t, "graph "+sub+" on a missing index", r.Err, "error opening graph db")
	}
	r = e.run("graph", "toc", "--index", "nope")
	wantExit(t, r, 1)
	mustContain(t, "graph toc on a missing index", r.Err, "error opening graph db")

	// Unknown flags are rejected per subcommand.
	r = e.run("graph", "deps", "x", "--index", "i", "--bogus-flag")
	if r.Code == 0 {
		t.Errorf("graph deps --bogus-flag must fail, got: %q", r.combined())
	}
}

func TestCLICGraphUnknownSymbolSubcommand(t *testing.T) {
	e := newCGraphEnv(t)
	r := e.graph("frobnicate", "main")
	wantExit(t, r, 1)
	mustContain(t, "unknown graph subcommand", r.Err, "unknown graph command: frobnicate")
	mustContain(t, "unknown graph subcommand usage", r.Out, "Usage:")
}

func TestCLICGraphDepsCallers(t *testing.T) {
	e := newCGraphEnv(t)

	r := e.graph("deps", cSvcSave)
	wantExit(t, r, 0)
	mustContain(t, "graph deps", r.Out, "Dependencies for "+cSvcSave, "example.com/fixture/service.validate")

	r = e.graph("callers", cSvcLoad)
	wantExit(t, r, 0)
	mustContain(t, "graph callers", r.Out, "Callers of "+cSvcLoad, "example.com/fixture.run",
		"example.com/fixture/service.Service.Report")

	// A symbol with no edges still prints an empty list.
	r = e.graph("deps", "example.com/fixture/does.not.Exist")
	wantExit(t, r, 0)
	mustContain(t, "graph deps of unknown symbol", r.Out, "(0):")
}

func TestCLICGraphExplain(t *testing.T) {
	e := newCGraphEnv(t)

	// A memory mentioning the symbol is linked into the explanation.
	r := e.run("memory", "remember", "Service.Load returns the empty string for a missing record by design", "--tag", "design")
	wantExit(t, r, 0)

	r = e.graph("explain", cSvcLoad)
	wantExit(t, r, 0)
	mustContain(t, "graph explain", r.Out,
		"Explain: "+cSvcLoad, "Kind: method", "Called by (", "example.com/fixture.run",
		"Impact (blast radius, depth=3)", "Affected files:", "service/service.go",
		"Linked Memory & Architectural Context", "returns the empty string")

	// A substring finds the symbol through SymbolSearch.
	r = e.graph("explain", "validate")
	wantExit(t, r, 0)
	mustContain(t, "graph explain by pattern", r.Out, "Explain: example.com/fixture/service.validate", "Calls (")

	r = e.graph("explain", "NoSuchSymbolAnywhere")
	wantExit(t, r, 1)
	mustContain(t, "graph explain unknown", r.Err, `Symbol "NoSuchSymbolAnywhere" not found`)
}

func TestCLICGraphPathAndQuery(t *testing.T) {
	e := newCGraphEnv(t)

	r := e.graph("path", cMain)
	wantExit(t, r, 1)
	mustContain(t, "graph path with one symbol", r.Err, "graph path requires two symbols")

	r = e.graph("path", cMain, "example.com/fixture/service.validate")
	wantExit(t, r, 0)
	mustContain(t, "graph path", r.Out, "Shortest path: "+cMain+" → example.com/fixture/service.validate", "1. ")

	r = e.graph("path", cMain, "example.com/nowhere.Nothing")
	if r.Code == 0 {
		mustContain(t, "graph path without a route", r.Out, "(0 hops)")
	} else {
		mustContain(t, "graph path without a route", r.Err, "error:")
	}

	r = e.graph("query", "Service.Load", "--depth", "1")
	wantExit(t, r, 0)
	mustContain(t, "graph query", r.Out, `Symbols matching "Service.Load"`, "[method] "+cSvcLoad,
		"Neighborhood of", "depth=1")

	r = e.graph("query", "zzzNoMatchzzz")
	wantExit(t, r, 0)
	mustContain(t, "graph query no match", r.Out, `No symbols matching "zzzNoMatchzzz" found.`)
}

func TestCLICGraphAnalysisCommands(t *testing.T) {
	e := newCGraphEnv(t)
	out := t.TempDir()

	// viz: explicit and default output file.
	html := filepath.Join(out, "g.html")
	r := e.graph("viz", "--output", html)
	wantExit(t, r, 0)
	mustContain(t, "graph viz", r.Out, "communities", "Graph visualization saved to "+html)
	if b, err := os.ReadFile(html); err != nil || !strings.Contains(string(b), "<html") {
		t.Fatalf("viz html not written: %v", err)
	}
	r = e.graph("viz")
	wantExit(t, r, 0)
	if _, err := os.Stat("fixture_graph.html"); err != nil {
		t.Errorf("viz default output fixture_graph.html missing: %v", err)
	}
	r = e.graph("viz", "--output", filepath.Join(out, "missing-dir", "g.html"))
	wantExit(t, r, 1)
	mustContain(t, "graph viz unwritable", r.Err, "error creating file")

	// communities.
	r = e.graph("communities")
	wantExit(t, r, 0)
	mustContain(t, "graph communities", r.Out, "Community Detection Results", "Nodes:", "Community 0:")

	// export: graphml/cypher with default names, unsupported and missing format.
	r = e.graph("export", "--format", "graphml")
	wantExit(t, r, 0)
	mustContain(t, "export graphml", r.Out, "GraphML exported to fixture_graph.graphml")
	if b, _ := os.ReadFile("fixture_graph.graphml"); !strings.Contains(string(b), "<graphml") || !strings.Contains(string(b), "CALLS") {
		t.Errorf("graphml content: %.200s", b)
	}
	r = e.graph("export", "--format", "cypher")
	wantExit(t, r, 0)
	mustContain(t, "export cypher", r.Out, "Neo4j Cypher exported to fixture_cypher.txt")
	if b, _ := os.ReadFile("fixture_cypher.txt"); !strings.Contains(string(b), "MERGE (n:Symbol") {
		t.Errorf("cypher content: %.200s", b)
	}
	r = e.graph("export", "--format", "dot")
	wantExit(t, r, 1)
	mustContain(t, "export dot", r.Err, `unsupported format "dot"`)
	r = e.graph("export")
	wantExit(t, r, 1)
	mustContain(t, "export without format", r.Err, "--format flag required")
	for _, f := range []string{"graphml", "cypher"} {
		r = e.graph("export", "--format", f, "--output", filepath.Join(out, "nodir", "x"))
		wantExit(t, r, 1)
		mustContain(t, "export "+f+" unwritable", r.Err, "error:")
	}

	// report: written, then guarded against another index's report.
	report := filepath.Join(out, "R.md")
	r = e.graph("report", "--output", report)
	wantExit(t, r, 0)
	mustContain(t, "graph report", r.Out, "Graph report saved to "+report)
	if err := os.WriteFile(report, []byte("# Graph Report: other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.graph("report", "--output", report)
	wantExit(t, r, 1)
	mustContain(t, "graph report guard", r.Err, `graph report of index "other"`, "--force")
	r = e.graph("report", "--output", report, "--force")
	wantExit(t, r, 0)
	r = e.graph("report", "--output", filepath.Join(out, "nodir", "R.md"))
	wantExit(t, r, 1)
	mustContain(t, "graph report unwritable", r.Err, "error generating graph report")

	// wiki.
	wiki := filepath.Join(out, "wiki")
	r = e.graph("wiki", "--output", wiki)
	wantExit(t, r, 0)
	mustContain(t, "graph wiki", r.Out, "Wiki generated:")
	if entries, _ := os.ReadDir(wiki); len(entries) < 2 {
		t.Errorf("wiki wrote %d files", len(entries))
	}

	// map and risk.
	r = e.graph("map", "--top", "3")
	wantExit(t, r, 0)
	mustContain(t, "graph map", r.Out, "Repo Map — fixture (top 3 symbols", "Total:")
	r = e.graph("risk", "--top", "3")
	wantExit(t, r, 0)
	mustContain(t, "graph risk", r.Out, "fixture")
	r = e.graph("risk", "--by-file")
	wantExit(t, r, 0)
	mustContain(t, "graph risk --by-file", r.Out, "service/service.go")
}

func TestCLICGraphHook(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("graph", "hook")
	wantExit(t, r, 1)
	mustContain(t, "hook without action", r.Err, "Usage: gleann graph hook")

	// The working directory (the env root) is not a git repository.
	r = e.run("graph", "hook", "status")
	wantExit(t, r, 1)
	mustContain(t, "hook outside git", r.Err, "not in a git repository")

	if err := os.MkdirAll(".git", 0o755); err != nil {
		t.Fatal(err)
	}
	r = e.run("graph", "hook", "uninstall")
	wantExit(t, r, 0)
	mustContain(t, "uninstall without hook", r.Out, "No gleann hook found")

	r = e.run("graph", "hook", "status")
	wantExit(t, r, 0)
	mustContain(t, "status before install", r.Out, "is not installed")

	// An existing foreign hook is kept and our script appended.
	if err := os.MkdirAll(filepath.Join(".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(".git", "hooks", "post-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r = e.run("graph", "hook", "uninstall")
	wantExit(t, r, 0)
	mustContain(t, "uninstall with foreign hook", r.Out, "No gleann hook found in post-commit")

	r = e.run("graph", "hook", "install")
	wantExit(t, r, 0)
	mustContain(t, "install", r.Out, "Git post-commit hook installed")
	b, _ := os.ReadFile(hook)
	mustContain(t, "installed hook", string(b), "echo mine", "gleann: auto-rebuild graph index")

	r = e.run("graph", "hook", "status")
	wantExit(t, r, 0)
	mustContain(t, "status after install", r.Out, "hook is installed")

	r = e.run("graph", "hook", "uninstall")
	wantExit(t, r, 0)
	mustContain(t, "uninstall", r.Out, "hook removed")
	if _, err := os.Stat(hook); !os.IsNotExist(err) {
		t.Errorf("hook still present after uninstall: %v", err)
	}

	r = e.run("graph", "hook", "frob")
	wantExit(t, r, 1)
	mustContain(t, "hook unknown action", r.Err, "Usage: gleann graph hook")
}

func TestCLICGraphBuildAndTOC(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("proj", "--graph")
	if !gleann.HasCodeGraph(e.IndexDir, "proj") {
		t.Fatal("index build --graph wrote no code graph")
	}

	// graph build: usage errors, then a rebuild from the meta's source dir.
	r := e.run("graph", "build")
	wantExit(t, r, 1)
	mustContain(t, "graph build without name", r.Err, "usage: gleann graph build")
	r = e.run("graph", "build", "unknown-index")
	wantExit(t, r, 1)
	mustContain(t, "graph build unknown index", r.Err, "usage: gleann graph build")

	r = e.run("graph", "build", "proj")
	wantExit(t, r, 0)
	r2 := e.run("graph", "build", "--index", "proj", "--docs", e.Docs)
	wantExit(t, r2, 0)
	if r.combined() == "" {
		t.Error("graph build printed nothing")
	}

	r = e.run("graph", "deps", "auth.ValidateToken", "--index", "proj")
	wantExit(t, r, 0)
	mustContain(t, "deps after graph build", r.Out, "auth.checkSignature")

	// An index without documents lists none.
	r = e.run("graph", "toc", "--index", "proj")
	wantExit(t, r, 0)
	mustContain(t, "graph toc of an index without documents", r.Out, `Indexed Documents in "proj" (0 total)`, "No documents found")
}

// cSeedDocuments writes a document tree (folder, two documents, nested
// headings) into the fixture graph, as the document plugins do.
func cSeedDocuments(t *testing.T, indexDir string) {
	t.Helper()
	g, err := gleann.OpenGraphStore(indexDir, analysisgolden.IndexName)
	if err != nil || g == nil {
		t.Fatalf("OpenGraphStore: %v", err)
	}
	defer g.Close()
	deltas := []graphstore.DocDelta{
		{RPath: "guide/setup.md", Nodes: []graphstore.DocNode{
			{ID: "folder:guide", Type: "folder", Name: "guide"},
			{ID: "doc:guide/setup.md", Type: "document", Parent: "folder:guide", Name: "Setup Guide", RPath: "guide/setup.md",
				Summary: "How to install and configure the fixture service, " + strings.Repeat("with many details ", 8)},
			{ID: "h1", Type: "heading", Parent: "doc:guide/setup.md", Name: "Install", Level: 1, RPath: "guide/setup.md"},
			{ID: "h2", Type: "heading", Parent: "h1", Name: "Requirements", Level: 2, RPath: "guide/setup.md"},
			{ID: "h3", Type: "heading", Parent: "h2", Name: "Go toolchain", Level: 3, RPath: "guide/setup.md"},
			{ID: "h4", Type: "heading", Parent: "doc:guide/setup.md", Name: "Configure", Level: 1, RPath: "guide/setup.md"},
		}},
		{RPath: "NOTES.md", Nodes: []graphstore.DocNode{
			{ID: "doc:NOTES.md", Type: "document", Name: "Notes", RPath: "NOTES.md"},
		}},
	}
	if err := g.ReplaceDocs(context.Background(), deltas); err != nil {
		t.Fatalf("ReplaceDocs: %v", err)
	}
}

func TestCLICGraphTOC(t *testing.T) {
	e := newCGraphEnv(t)
	cSeedDocuments(t, e.gdir)

	r := e.graph("toc")
	wantExit(t, r, 0)
	mustContain(t, "graph toc", r.Out, `Indexed Documents in "fixture" (2 total)`,
		"guide/setup.md (4 headings) [guide]", "How to install", "…", "NOTES.md (0 headings)")

	r = e.graph("toc", "--json")
	wantExit(t, r, 0)
	var docs []gleann.DocumentTOCInfo
	if err := json.Unmarshal([]byte(r.Out), &docs); err != nil || len(docs) != 2 {
		t.Fatalf("graph toc --json = %v (%v)\n%s", docs, err, r.Out)
	}

	r = e.graph("toc", "guide/setup.md")
	wantExit(t, r, 0)
	mustContain(t, "graph toc <doc>", r.Out, "Document: Setup Guide", "Path:     guide/setup.md",
		"Folder:   guide", "Summary:", "Headings: 4", "Hierarchical Outline:",
		"├── 📌 Configure (H1)\n└── 📌 Install (H1)\n    └── 📎 Requirements (H2)\n        └── ▫️ Go toolchain (H3)")

	// The document path may follow the flags; --index's value is skipped.
	r = e.run("graph", "toc", "--index", analysisgolden.IndexName, "--index-dir", e.gdir, "guide/setup.md", "--json")
	wantExit(t, r, 0)
	var toc gleann.DocumentTOCInfo
	if err := json.Unmarshal([]byte(r.Out), &toc); err != nil || toc.Title != "Setup Guide" || len(toc.Headings) != 2 {
		t.Fatalf("graph toc <doc> --json = %+v (%v)", toc, err)
	}

	r = e.graph("toc", "NOTES.md")
	wantExit(t, r, 0)
	mustContain(t, "graph toc of a document without headings", r.Out, "No headings indexed for this document")

	r = e.graph("toc", "no/such/doc.md")
	wantExit(t, r, 1)
	mustContain(t, "graph toc unknown doc", r.Err, "error getting document TOC")
}
