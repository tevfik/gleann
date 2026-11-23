package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/modules/chunking"
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
	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("foreign hook was deleted by uninstall: %v", err)
	}
	mustContain(t, "foreign hook preserved", string(b), "echo mine")
	mustNotContain(t, "gleann hook removed", string(b), "gleann: auto-rebuild graph index")

	r = e.run("graph", "hook", "frob")
	wantExit(t, r, 1)
	mustContain(t, "hook unknown action", r.Err, "Usage: gleann graph hook")
}

func TestCLICGraphHookMarkersAndSync(t *testing.T) {
	e := newCLIEnv(t)
	if err := os.MkdirAll(filepath.Join(".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(".git", "hooks", "post-commit")

	// Fresh install: no pre-existing hook file.
	r := e.run("graph", "hook", "install")
	wantExit(t, r, 0)
	mustContain(t, "install", r.Out, "Git post-commit hook installed")

	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("hook not written: %v", err)
	}
	content := string(b)
	mustContain(t, "hook markers and sync", content,
		"# BEGIN GLEANN HOOK",
		"# END GLEANN HOOK",
		"index sync",
	)
	mustNotContain(t, "no full build in hook", content, "index build")

	r = e.run("graph", "hook", "status")
	wantExit(t, r, 0)
	mustContain(t, "status installed", r.Out, "hook is installed")

	// Uninstall on a fresh install removes the file completely.
	r = e.run("graph", "hook", "uninstall")
	wantExit(t, r, 0)
	mustContain(t, "uninstall", r.Out, "hook removed")
	if _, err := os.Stat(hook); !os.IsNotExist(err) {
		t.Errorf("expected hook file to be deleted on fresh install uninstall, got %v", err)
	}
}

func TestCLICGraphHookLegacyUninstall(t *testing.T) {
	e := newCLIEnv(t)
	if err := os.MkdirAll(filepath.Join(".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(".git", "hooks", "post-commit")

	legacyHook := `#!/bin/sh
echo "user foreign pre-commit"

#!/bin/sh
# gleann: auto-rebuild graph index after commit
# Installed by: gleann graph hook install
# Remove with:  gleann graph hook uninstall

GLEANN_BIN=$(command -v gleann || echo "")
if [ -z "$GLEANN_BIN" ]; then
    echo "gleann: binary not found, skipping graph rebuild"
    exit 0
fi

CHANGED=$(git diff-tree --no-commit-id --name-only -r HEAD 2>/dev/null)
if [ -z "$CHANGED" ]; then
    exit 0
fi

CODE_CHANGED=$(echo "$CHANGED" | grep -E '\.(go|py)$' || true)
if [ -z "$CODE_CHANGED" ]; then
    exit 0
fi

echo "gleann: rebuilding graph index..."
INDEX="${GLEANN_GRAPH_INDEX:-}"
if [ -n "$INDEX" ]; then
    $GLEANN_BIN index build "$INDEX" --docs . --graph 2>/dev/null &
fi
`
	if err := os.WriteFile(hook, []byte(legacyHook), 0o755); err != nil {
		t.Fatal(err)
	}

	// Status recognises legacy hook.
	r := e.run("graph", "hook", "status")
	wantExit(t, r, 0)
	mustContain(t, "legacy status", r.Out, "hook is installed")

	// Uninstall removes legacy section and keeps user content.
	r = e.run("graph", "hook", "uninstall")
	wantExit(t, r, 0)
	mustContain(t, "legacy uninstall", r.Out, "hook removed")

	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("legacy foreign hook deleted by uninstall: %v", err)
	}
	mustContain(t, "user content kept", string(b), "echo \"user foreign pre-commit\"")
	mustNotContain(t, "legacy gleann removed", string(b), "gleann: auto-rebuild graph index")
}

func TestCLICGraphHookWorktreeAndSubdir(t *testing.T) {
	e := newCLIEnv(t)

	// Initialize real git repo
	if out, err := exec.Command("git", "init").CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v (%s)", err, string(out))
	}
	_ = exec.Command("git", "config", "user.email", "test@test.local").Run()
	_ = exec.Command("git", "config", "user.name", "test").Run()

	sub := filepath.Join(e.Home, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Running from a subdirectory must succeed via git rev-parse --git-path hooks
	t.Chdir(sub)
	r := e.run("graph", "hook", "status")
	wantExit(t, r, 0)
	mustContain(t, "subdir status", r.Out, "is not installed")

	r = e.run("graph", "hook", "install")
	wantExit(t, r, 0)
	mustContain(t, "subdir install", r.Out, "Git post-commit hook installed")

	// Verify hook was installed in the repo's hooks directory
	hooksDirCmd := exec.Command("git", "rev-parse", "--git-path", "hooks")
	out, err := hooksDirCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse failed: %v", err)
	}
	hookPath := filepath.Join(strings.TrimSpace(string(out)), "post-commit")
	if _, err := os.Stat(hookPath); err != nil {
		t.Fatalf("hook not created at %s: %v", hookPath, err)
	}

	// Worktree test: commit so worktree can be created
	t.Chdir(filepath.Dir(e.Home)) // root of repo
	if err := os.WriteFile("dummy.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "add", "dummy.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v (%s)", err, string(out))
	}
	if out, err := exec.Command("git", "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v (%s)", err, string(out))
	}

	wtDir := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "worktree", "add", wtDir).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v (%s)", err, string(out))
	}

	t.Chdir(wtDir)
	r = e.run("graph", "hook", "status")
	wantExit(t, r, 0)
	mustContain(t, "worktree status", r.Out, "hook is installed")
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

	// graph build reads the source documents and rebuilds the document
	// graph: the fixture's README.md and docs/guide.md stay listed (it used
	// to wipe them by replacing doc_nodes with an empty list).
	r = e.run("graph", "toc", "--index", "proj")
	wantExit(t, r, 0)
	mustContain(t, "graph toc after graph build", r.Out, `Indexed Documents in "proj" (2 total)`, "README.md", "docs/guide.md")
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

func TestCLICGraphNonGoNotice(t *testing.T) {
	e := newCGraphEnv(t)

	// Explain on a non-Go symbol (Python)
	r := e.graph("explain", "example.com/fixture/py.clean")
	wantExit(t, r, 0)
	// Callers on a non-Go symbol (Python)
	rCallers := e.graph("callers", "example.com/fixture/py.clean")
	wantExit(t, rCallers, 0)
	// Deps on a non-Go symbol (Python)
	rDeps := e.graph("deps", "example.com/fixture/py.clean")
	wantExit(t, rDeps, 0)

	wantNotice := "note: non-Go code was extracted without tree-sitter (use a build with tree-sitter: the 'full' release artifact)"
	if chunking.TreeSitterAvailable() {
		if strings.Contains(r.Out, "without tree-sitter") {
			t.Errorf("treesitter build must not include notice in explain, got:\n%s", r.Out)
		}
		if strings.Contains(rCallers.Out, "without tree-sitter") {
			t.Errorf("treesitter build must not include notice in callers, got:\n%s", rCallers.Out)
		}
		if strings.Contains(rDeps.Out, "without tree-sitter") {
			t.Errorf("treesitter build must not include notice in deps, got:\n%s", rDeps.Out)
		}
	} else {
		if !strings.Contains(r.Out, wantNotice) {
			t.Errorf("explain on non-Go symbol missing notice in:\n%s", r.Out)
		}
		if !strings.Contains(rCallers.Out, wantNotice) {
			t.Errorf("callers on non-Go symbol missing notice in:\n%s", rCallers.Out)
		}
		if !strings.Contains(rDeps.Out, wantNotice) {
			t.Errorf("deps on non-Go symbol missing notice in:\n%s", rDeps.Out)
		}
	}

	// Go symbol must not include the notice.
	rGo := e.graph("explain", cSvcLoad)
	wantExit(t, rGo, 0)
	if strings.Contains(rGo.Out, "without tree-sitter") {
		t.Errorf("Go symbol must not include notice, got:\n%s", rGo.Out)
	}
}

func TestCLICGraphTreeSitterNoticeCrossVariant(t *testing.T) {
	e := newCGraphEnv(t)
	dbPath := filepath.Join(e.gdir, analysisgolden.IndexName, analysisgolden.IndexName+".db")

	st, err := store.Open(dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Case 1: graph has graph_treesitter = "1" (treesitter-built graph)
	if err := graphstore.New(st).SetMeta(context.Background(), store.MetaGraphTreeSitter, "1"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	rExp := e.graph("explain", "example.com/fixture/py.clean")
	wantExit(t, rExp, 0)
	if strings.Contains(rExp.Out, "without tree-sitter") {
		t.Errorf("graph with graph_treesitter=1 must not show notice in explain, got:\n%s", rExp.Out)
	}
	rCallers := e.graph("callers", "example.com/fixture/py.clean")
	wantExit(t, rCallers, 0)
	if strings.Contains(rCallers.Out, "without tree-sitter") {
		t.Errorf("graph with graph_treesitter=1 must not show notice in callers, got:\n%s", rCallers.Out)
	}

	// Case 2: graph has graph_treesitter = "0" (lite-built graph)
	st2, err := store.Open(dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphstore.New(st2).SetMeta(context.Background(), store.MetaGraphTreeSitter, "0"); err != nil {
		t.Fatal(err)
	}
	st2.Close()

	if !chunking.TreeSitterAvailable() {
		rExp0 := e.graph("explain", "example.com/fixture/py.clean")
		wantExit(t, rExp0, 0)
		if !strings.Contains(rExp0.Out, "without tree-sitter") {
			t.Errorf("graph with graph_treesitter=0 must show notice in explain under lite, got:\n%s", rExp0.Out)
		}
		rCallers0 := e.graph("callers", "example.com/fixture/py.clean")
		wantExit(t, rCallers0, 0)
		if !strings.Contains(rCallers0.Out, "without tree-sitter") {
			t.Errorf("graph with graph_treesitter=0 must show notice in callers under lite, got:\n%s", rCallers0.Out)
		}
	}
}

func TestCLICallersShortNameFalseNotice(t *testing.T) {
	docsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(docsDir, "go.mod"), []byte("module example.com/m\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "a.py"), []byte("def clean():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docsDir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "svc", "z.go"), []byte("package svc\nfunc Run() { clean() }\nfunc clean() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexDir := t.TempDir()
	idxPath := filepath.Join(indexDir, "zz2")
	if err := os.MkdirAll(idxPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxPath, "zz2.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var outBuf bytes.Buffer
	indexing.BuildGraphIndex(indexing.GraphOptions{
		Name:     "zz2",
		DocsDir:  docsDir,
		IndexDir: indexDir,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
	})

	e := newCLIEnv(t)
	r := e.run("graph", "callers", "clean", "--index", "zz2", "--index-dir", indexDir)
	wantExit(t, r, 0)
	if !strings.Contains(r.Out, "example.com/m/svc.Run") {
		t.Fatalf("expected caller svc.Run, got:\n%s", r.Out)
	}
	if strings.Contains(r.Out, "without tree-sitter") {
		t.Fatalf("BUG: callers of the Go symbol svc.clean carry a non-Go notice (resolution mismatch):\n%s", r.Out)
	}
}

func TestCLIDepsShortNameClean_NoNoticeOnGo(t *testing.T) {
	docsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(docsDir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "go.mod"), []byte("module example.com/m\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "a.py"), []byte("def clean(): pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "svc", "z.go"), []byte("package svc\nfunc clean() { helper() }\nfunc helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexDir := t.TempDir()
	idxPath := filepath.Join(indexDir, "zz3")
	if err := os.MkdirAll(idxPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxPath, "zz3.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var outBuf bytes.Buffer
	indexing.BuildGraphIndex(indexing.GraphOptions{
		Name:     "zz3",
		DocsDir:  docsDir,
		IndexDir: indexDir,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
	})

	e := newCLIEnv(t)
	r := e.run("graph", "deps", "clean", "--index", "zz3", "--index-dir", indexDir)
	wantExit(t, r, 0)
	if !strings.Contains(r.Out, "example.com/m/svc.helper") {
		t.Fatalf("expected callee svc.helper, got:\n%s", r.Out)
	}
	if strings.Contains(r.Out, "without tree-sitter") {
		t.Fatalf("BUG: deps of the Go symbol svc.clean carry a non-Go notice (DirectionCallees mismatch):\n%s", r.Out)
	}
}

// TestCLICGraphPath_NoPathNonGoNotice pins the advisory on the error branch of
// `graph path`: graphstore returns the no-path error (not an empty path) when
// the endpoints are distinct, so the notice must be emitted there, not only in
// the from==to branch (v3 review Finding 3).
func TestCLICGraphPath_NoPathNonGoNotice(t *testing.T) {
	e := newCLIEnv(t)

	docsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(docsDir, "go.mod"), []byte("module example.com/m\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "a.py"), []byte("def clean():\n    pass\n\ndef build():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docsDir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "svc", "z.go"), []byte("package svc\nfunc alpha() {}\nfunc beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexDir := t.TempDir()
	idxPath := filepath.Join(indexDir, "pathlite")
	if err := os.MkdirAll(idxPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxPath, "pathlite.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var outBuf bytes.Buffer
	indexing.BuildGraphIndex(indexing.GraphOptions{
		Name:     "pathlite",
		DocsDir:  docsDir,
		IndexDir: indexDir,
		Stdout:   &outBuf,
		Stderr:   &outBuf,
	})
	if _, err := os.Stat(filepath.Join(idxPath, "pathlite.db")); err != nil {
		t.Fatalf("graph index not built: %v\n%s", err, outBuf.String())
	}

	graph := func(args ...string) cliResult {
		full := append([]string{"graph"}, args...)
		full = append(full, "--index", "pathlite", "--index-dir", indexDir)
		return e.run(full...)
	}

	// Distinct lite non-Go symbols with no route: the error branch must carry
	// the advisory.
	r := graph("path", "example.com/m.clean", "example.com/m.build")
	wantExit(t, r, 1)
	mustContain(t, "distinct no-path Python symbols", r.Err, "no path within 10 hops")
	if chunking.TreeSitterAvailable() {
		if strings.Contains(r.Err, "without tree-sitter") {
			t.Fatalf("treesitter build must not include the notice, got:\n%s", r.Err)
		}
	} else if !strings.Contains(r.Err, indexing.NonGoNoticeMessage) {
		t.Fatalf("BUG: distinct no-path Python symbols carry no non-Go notice on stderr:\n%s", r.Err)
	}

	// Distinct Go symbols with no route: no notice.
	rGo := graph("path", "example.com/m/svc.alpha", "example.com/m/svc.beta")
	wantExit(t, rGo, 1)
	mustContain(t, "distinct no-path Go symbols", rGo.Err, "no path within 10 hops")
	if strings.Contains(rGo.Err, "without tree-sitter") {
		t.Fatalf("BUG: distinct no-path Go symbols carry a non-Go notice:\n%s", rGo.Err)
	}
}
