package indexing

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/indexer"
	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// execDB runs DDL against the index database through a side connection, the
// way a half-finished manual repair or a foreign tool would damage it.
func execDB(t *testing.T, dbPath string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
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

func abortOn(when, table string) string {
	return `CREATE TRIGGER fault_` + when + `_` + table + ` BEFORE ` + when + ` ON ` + table +
		` BEGIN SELECT RAISE(ABORT, 'injected fault'); END`
}

// faultIndex is a built format-2 graph of goFixture with an open graphstore
// over it, plus the paths a test needs to damage it and to call the helpers.
type faultIndex struct {
	docs, dbPath, indexDir string
	g                      *graphstore.SQLite
}

func newFaultIndex(t *testing.T) *faultIndex {
	t.Helper()
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	dbPath := filepath.Join(env.cfg.IndexDir, "g", "g.db")
	st, err := store.Open(dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	resolved, err := filepath.EvalSymlinks(docs)
	if err != nil {
		resolved = docs
	}
	return &faultIndex{docs: resolved, dbPath: dbPath, indexDir: env.cfg.IndexDir, g: graphstore.New(st)}
}

func (f *faultIndex) gopts(changed ...string) GraphOptions {
	return GraphOptions{Name: "g", DocsDir: f.docs, IndexDir: f.indexDir, ChangedFiles: changed}
}

func sampleDoc(source string) *PluginDoc {
	return &PluginDoc{SourcePath: source, Result: &gleann.PluginResult{
		Nodes: []gleann.PluginNode{
			{Type: "Document", Data: map[string]any{"title": "Notes", "vpath": "docs/" + source}},
			{Type: "Section", Data: map[string]any{"id": "sec:" + source, "heading": "Intro", "level": 1.0}},
		},
	}}
}

func mustContain(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

// An index directory that cannot be created fails the graph build up front.
func TestBuildGraphIndexSQLiteUnopenableDatabase(t *testing.T) {
	root := t.TempDir()
	indexDir := filepath.Join(root, "idx")
	writeFile(t, indexDir, "a file where the index directory should be")
	err := buildGraphIndexSQLite(GraphOptions{Name: "g", DocsDir: goFixture(t), IndexDir: indexDir}, newPrinter(nil, nil))
	mustContain(t, err, "could not open index database for graph")
}

// An unreadable graph while deciding between incremental and full build is
// reported, not mistaken for "no graph yet".
func TestBuildGraphIndexSQLiteHasDataFailure(t *testing.T) {
	f := newFaultIndex(t)
	execDB(t, f.dbPath, `ALTER TABLE doc_nodes RENAME TO doc_nodes_gone`, `DELETE FROM symbols`)
	err := buildGraphIndexSQLite(f.gopts(filepath.Join(f.docs, "a.go")), newPrinter(nil, nil))
	mustContain(t, err, "checking graph data")
}

// Each stage of an incremental graph update reports its own failure, and the
// stages are independent: all of them are joined in the returned error.
func TestBuildGraphIndexSQLiteIncrementalStageFailures(t *testing.T) {
	f := newFaultIndex(t)
	a := filepath.Join(f.docs, "a.go")
	if err := f.g.ApplyDocDelta(context.Background(), pluginDocToDelta(sampleDoc("old.md"))); err != nil {
		t.Fatal(err)
	}
	execDB(t, f.dbPath, abortOn("INSERT", "symbols"), abortOn("DELETE", "doc_nodes"), abortOn("INSERT", "doc_nodes"))

	// a.go changed (code stage fails), old.md's source is gone (retraction
	// fails) and a new document cannot be written.
	gone := filepath.Join(f.docs, "old.md")
	opts := f.gopts(a, gone)
	opts.PluginDocs = []*PluginDoc{sampleDoc("new.md")}
	writeFile(t, a, "package fx\n\nfunc Helper(x int) int { return x + 5 }\n")
	err := buildGraphIndexSQLite(opts, newPrinter(nil, nil))
	for _, want := range []string{"incremental graph indexing failed", "removing document graph rows of old.md", "writing document graph of new.md"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

// A full build reports a failed code-graph write and a failed document-graph
// write separately.
func TestBuildGraphIndexSQLiteFullWriteFailures(t *testing.T) {
	f := newFaultIndex(t)
	execDB(t, f.dbPath, abortOn("INSERT", "symbols"))
	err := buildGraphIndexSQLite(f.gopts(), newPrinter(nil, nil))
	mustContain(t, err, "graph indexing failed")
	mustContain(t, err, "writing code graph")

	f = newFaultIndex(t)
	execDB(t, f.dbPath, abortOn("INSERT", "doc_nodes"))
	opts := f.gopts()
	opts.PluginDocs = []*PluginDoc{sampleDoc("n.md")}
	err = buildGraphIndexSQLite(opts, newPrinter(nil, nil))
	mustContain(t, err, "writing document graph")
}

// A source tree that cannot be walked fails the extraction before anything is
// written.
func TestFullGraphSQLiteMissingRoot(t *testing.T) {
	f := newFaultIndex(t)
	err := fullGraphSQLite(context.Background(), f.g, "example.com/fx", filepath.Join(t.TempDir(), "missing"), false, newPrinter(nil, nil))
	mustContain(t, err, "walking source tree")
}

// A Go module whose packages cannot be loaded is still indexed, with the
// syntactic resolver, and the build says so: that graph lacks the calls made
// through a receiver, a variable or an interface.
func TestFullGraphSQLiteWarnsWhenExactResolutionFails(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	f := newFaultIndex(t)
	t.Setenv("GLEANN_GRAPH_EXACT", "1")
	root := t.TempDir()
	// No Go release satisfies this go line and downloads are off.
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/fx\n\ngo 1.999.0\n")
	writeFile(t, filepath.Join(root, "a.go"), "package fx\n\nfunc A() { B() }\n\nfunc B() {}\n")
	var out, warn bytes.Buffer
	if err := fullGraphSQLite(context.Background(), f.g, "example.com/fx", root, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("the build must not fail: %v", err)
	}
	if !strings.Contains(warn.String(), "exact Go call resolution is unavailable") {
		t.Errorf("no warning about the syntactic fallback; stderr: %q", warn.String())
	}

	// A Go tree with no module at all also warns: no go/types load can run,
	// so every call through a receiver, variable or interface is missing
	// (3.3-nested). The warning goes to stderr, not the JSON/MCP stdout.
	plain := t.TempDir()
	writeFile(t, filepath.Join(plain, "a.go"), "package fx\n\nfunc A() {}\n")
	warn.Reset()
	out.Reset()
	if err := fullGraphSQLite(context.Background(), f.g, "", plain, false, newPrinter(&out, &warn)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "no Go module") {
		t.Errorf("no warning for a Go tree without go.mod; stderr: %q", warn.String())
	}
	if strings.Contains(out.String(), "no Go module") {
		t.Errorf("the warning leaked to stdout: %q", out.String())
	}
}

// A source file that cannot be read aborts the full build with the file named.
func TestFullGraphSQLiteUnreadableFile(t *testing.T) {
	f := newFaultIndex(t)
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not make a file unreadable on Windows")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked.go")
	writeFile(t, locked, "package x\n")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	if _, err := os.ReadFile(locked); err == nil {
		t.Skip("running with privileges that ignore file permissions")
	}
	err := fullGraphSQLite(context.Background(), f.g, "example.com/fx", root, false, newPrinter(nil, nil))
	mustContain(t, err, "locked.go")
}

// Retracting a deleted source's rows can fail; a dirty file that vanished is
// retracted in the second pass, and failures there are reported too.
func TestIncrementalGraphRemoveFailures(t *testing.T) {
	f := newFaultIndex(t)
	ctx := context.Background()
	a := filepath.Join(f.docs, "a.go")
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	execDB(t, f.dbPath, abortOn("DELETE", "symbols"))
	err := incrementalGraphSQLite(ctx, f.g, []string{a}, "example.com/fx", f.docs, newPrinter(nil, nil))
	mustContain(t, err, "removing graph rows of a.go")

	// Second pass: a dirty file that no longer exists.
	if err := f.g.MarkFileDirty(ctx, "a.go"); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err = incrementalGraphSQLite(ctx, f.g, nil, "example.com/fx", f.docs, newPrinter(&out, nil))
	mustContain(t, err, "removing graph rows of a.go")
	if !strings.Contains(out.String(), "Re-applying 1 dirty graph file") {
		t.Errorf("dirty pass not announced: %q", out.String())
	}
}

// A manifest whose dirty flag cannot be read is reported instead of silently
// skipping the convergence pass.
func TestIncrementalGraphDirtyListingFailure(t *testing.T) {
	f := newFaultIndex(t)
	execDB(t, f.dbPath, `ALTER TABLE files RENAME COLUMN graph_dirty TO zz`)
	err := incrementalGraphSQLite(context.Background(), f.g, nil, "example.com/fx", f.docs, newPrinter(nil, nil))
	mustContain(t, err, "listing dirty graph files")

	err = convergeDirtyFormat2Graph(f.indexDir, "g", f.docs, f.gopts(), newPrinter(nil, nil))
	mustContain(t, err, "listing dirty graph files")
}

// Lookups the delta needs (declared targets, package members) fail the file's
// write; the file is marked dirty best-effort and the error names it.
func TestApplyFileGraphLookupFailures(t *testing.T) {
	f := newFaultIndex(t)
	ctx := context.Background()
	a := filepath.Join(f.docs, "a.go")
	src, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}

	execDB(t, f.dbPath, `ALTER TABLE symbols RENAME COLUMN fqn TO zz`)
	err = applyFileGraph(ctx, f.g, "example.com/fx", f.docs, a, string(src), nil, nil)
	mustContain(t, err, "resolving graph targets for a.go")
}

func TestApplyFileGraphPackageMembersFailure(t *testing.T) {
	f := newFaultIndex(t)
	py := filepath.Join(f.docs, "tool.py")
	writeFile(t, py, "def run():\n    return 1\n")
	execDB(t, f.dbPath, `ALTER TABLE symbols RENAME COLUMN name TO zz`)
	err := applyFileGraph(context.Background(), f.g, "example.com/fx", f.docs, py, "def run():\n    return 1\n", nil, nil)
	mustContain(t, err, "resolving member calls for tool.py")
}

// A declaration that is new to the graph turns an unchanged file's external
// call into an internal one: that file is marked dirty so the dirty pass
// re-extracts it.
func TestApplyFileGraphMarksCallersOfNewDeclarationsDirty(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/fx\n\ngo 1.25\n")
	writeFile(t, filepath.Join(docs, "caller.go"), "package fx\n\nfunc Caller() int { return Later() }\n")
	buildFormat2(t, env, docs)
	st, err := store.Open(filepath.Join(env.cfg.IndexDir, "g", "g.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	g := graphstore.New(st)

	later := filepath.Join(docs, "later.go")
	src := "package fx\n\nfunc Later() int { return 1 }\n"
	writeFile(t, later, src)
	if err := applyFileGraph(context.Background(), g, "example.com/fx", docs, later, src, nil, nil); err != nil {
		t.Fatal(err)
	}
	dirty, err := g.DirtyFiles()
	if err != nil || len(dirty) != 1 || dirty[0] != "caller.go" {
		t.Fatalf("dirty files = %v, %v; want [caller.go]", dirty, err)
	}
}

// The document graph reports failed writes and retractions.
func TestPluginDocsWriteFailures(t *testing.T) {
	f := newFaultIndex(t)
	ctx := context.Background()
	if err := applyPluginDocs(ctx, f.g, []*PluginDoc{sampleDoc("x.md")}, false, f.docs); err != nil {
		t.Fatal(err)
	}
	execDB(t, f.dbPath, abortOn("INSERT", "doc_nodes"), abortOn("DELETE", "doc_nodes"))

	err := applyPluginDocs(ctx, f.g, []*PluginDoc{sampleDoc("y.md")}, true, f.docs)
	mustContain(t, err, "writing document graph")
	err = applyPluginDocs(ctx, f.g, []*PluginDoc{sampleDoc("y.md")}, false, f.docs)
	mustContain(t, err, "writing document graph of y.md")
	err = removeDeletedDocs(ctx, f.g, []string{filepath.Join(f.docs, "x.md")}, f.docs)
	mustContain(t, err, "removing document graph rows of x.md")
}

// A dirty-converge pass over an index whose database file is unusable reports
// the open failure.
func TestConvergeDirtyUnopenableDatabase(t *testing.T) {
	f := newFaultIndex(t)
	// Not a SQLite file any more, but still named like an index database.
	if err := os.WriteFile(f.dbPath, []byte(strings.Repeat("garbage ", 600)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"-wal", "-shm"} {
		_ = os.Remove(f.dbPath + ext)
	}
	err := convergeDirtyFormat2Graph(f.indexDir, "g", f.docs, f.gopts(), newPrinter(nil, nil))
	mustContain(t, err, "dirty graph check")
}

// A clean graph needs no convergence; a non-format-2 index is left alone.
func TestConvergeDirtyNoop(t *testing.T) {
	f := newFaultIndex(t)
	if err := convergeDirtyFormat2Graph(f.indexDir, "g", f.docs, f.gopts(), newPrinter(nil, nil)); err != nil {
		t.Errorf("clean graph: %v", err)
	}
	if err := convergeDirtyFormat2Graph(t.TempDir(), "absent", f.docs, f.gopts(), newPrinter(nil, nil)); err != nil {
		t.Errorf("absent index: %v", err)
	}
}

// fileGraphToDelta keeps one row per declaration, drops edges from sources the
// file does not own and defaults an IMPLEMENTS confidence to "extracted".
func TestFileGraphToDeltaRules(t *testing.T) {
	sym := func(fqn, name string) gtypes.SymbolNode {
		return gtypes.SymbolNode{FQN: fqn, Name: name, Kind: "function", File: "x.go", Line: 3}
	}
	fg := indexer.FileGraph{
		File: gtypes.FileNode{Path: "x.go", Lang: "go"},
		Symbols: []gtypes.SymbolNode{
			sym("p.A", "A"), sym("p.A", "A"), // declared twice
			sym("p.B", "B"),
			{FQN: "ext.Stub", Name: "Stub"}, // callee stub, no file: never a row
			sym("p.Other", "Other"),         // owned elsewhere
		},
		Calls:      []gtypes.EdgeCalls{{CallerFQN: "p.A", CalleeFQN: "ext.Stub"}, {CallerFQN: "p.Ghost", CalleeFQN: "p.A"}},
		Implements: []gtypes.EdgeImplements{{ImplFQN: "p.A", IfaceFQN: "p.B"}, {ImplFQN: "p.B", IfaceFQN: "ext.Iface", Confidence: "exact"}},
		References: []gtypes.EdgeReferences{{RefererFQN: "p.Other", RefereeFQN: "p.A"}},
	}
	d := fileGraphToDelta(fg, "", "h", func(fqn string) bool { return fqn != "p.Other" }, func(fqn string) bool { return strings.HasPrefix(fqn, "p.") }, nil)
	if len(d.Symbols) != 2 {
		t.Fatalf("symbols = %+v, want A and B once each", d.Symbols)
	}
	conf := map[string]string{}
	for _, e := range d.Edges {
		conf[e.Src+">"+e.Dst+":"+e.Kind] = e.Confidence
		if e.Src == "p.Ghost" || e.Src == "p.Other" {
			t.Errorf("edge from a source the file does not own: %+v", e)
		}
	}
	if conf["p.A>p.B:IMPLEMENTS"] != "extracted" || conf["p.B>ext.Iface:IMPLEMENTS"] != "exact" {
		t.Errorf("implements confidence: %v", conf)
	}
	for _, e := range d.Edges {
		if e.Dst == "ext.Stub" && !e.DstExternal {
			t.Errorf("stub target must be external: %+v", e)
		}
	}
}

func TestFullGraphSQLiteWarnsNonGoWithoutTreeSitter(t *testing.T) {
	f := newFaultIndex(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\nfunc Main() {}\n")
	writeFile(t, filepath.Join(root, "util.py"), "def helper(): pass\n")
	var out, warn bytes.Buffer
	if err := fullGraphSQLite(context.Background(), f.g, "", root, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite: %v", err)
	}
	if chunking.TreeSitterAvailable() {
		if strings.Contains(warn.String(), "without tree-sitter") {
			t.Errorf("treesitter build must not warn, got: %q", warn.String())
		}
	} else {
		want := "note: 1 non-Go code file extracted without tree-sitter (use a build with tree-sitter: the 'full' release artifact)"
		if !strings.Contains(warn.String(), want) {
			t.Errorf("expected non-Go warning, got:\n%s", warn.String())
		}
	}

	// Go-only repository must not warn in any build.
	goOnly := t.TempDir()
	writeFile(t, filepath.Join(goOnly, "main.go"), "package main\nfunc Main() {}\n")
	warn.Reset()
	if err := fullGraphSQLite(context.Background(), f.g, "", goOnly, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite: %v", err)
	}
	if strings.Contains(warn.String(), "without tree-sitter") {
		t.Errorf("Go-only repo must not warn, got: %q", warn.String())
	}
}

func TestGoOnlyRebuildClearsKey(t *testing.T) {
	f := newFaultIndex(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\nfunc Main() {}\n")
	writeFile(t, filepath.Join(root, "util.py"), "def helper(): pass\n")
	var out, warn bytes.Buffer
	if err := fullGraphSQLite(context.Background(), f.g, "", root, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite: %v", err)
	}

	// Verify key was set
	var val string
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaGraphTreeSitter).Scan(&val); err != nil {
		t.Fatalf("key %s must exist after build with non-Go files: %v", store.MetaGraphTreeSitter, err)
	}

	// Now rebuild with Go-only repo
	goOnly := t.TempDir()
	writeFile(t, filepath.Join(goOnly, "main.go"), "package main\nfunc Main() {}\n")
	warn.Reset()
	if err := fullGraphSQLite(context.Background(), f.g, "", goOnly, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite on goOnly: %v", err)
	}

	// Key must be cleared!
	var valAfter string
	err = db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaGraphTreeSitter).Scan(&valAfter)
	if err == nil {
		t.Fatalf("BUG: Go-only rebuild left the stale key %s=%q; a later sync that adds non-Go files inherits it", store.MetaGraphTreeSitter, valAfter)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unexpected query error: %v", err)
	}
}

func TestIncrementalGraphSQLiteWarnsNonGoWithoutTreeSitter(t *testing.T) {
	f := newFaultIndex(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\nfunc Main() {}\n")
	var out, warn bytes.Buffer
	if err := fullGraphSQLite(context.Background(), f.g, "", root, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite: %v", err)
	}

	pyFile := filepath.Join(root, "util.py")
	writeFile(t, pyFile, "def helper(): pass\n")
	warn.Reset()
	if err := incrementalGraphSQLite(context.Background(), f.g, []string{pyFile}, "", root, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("incrementalGraphSQLite: %v", err)
	}

	if chunking.TreeSitterAvailable() {
		if strings.Contains(warn.String(), "without tree-sitter") {
			t.Errorf("treesitter build must not warn in sync, got: %q", warn.String())
		}
	} else {
		want := "note: 1 non-Go code file extracted without tree-sitter (use a build with tree-sitter: the 'full' release artifact)"
		if !strings.Contains(warn.String(), want) {
			t.Fatalf("BUG: incremental sync missing non-Go warning, got:\n%s", warn.String())
		}
	}
}

func TestSyncStaleKeyTransitions(t *testing.T) {
	f := newFaultIndex(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\nfunc Main() {}\n")
	pyFile := filepath.Join(root, "util.py")
	writeFile(t, pyFile, "def helper(): pass\n")
	var out, warn bytes.Buffer
	if err := fullGraphSQLite(context.Background(), f.g, "", root, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite: %v", err)
	}

	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Sequence A: treesitter build (key=1) -> sync with lite binary
	// Simulate key=1 from treesitter build
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, '1') ON CONFLICT(key) DO UPDATE SET value = '1'`, store.MetaGraphTreeSitter); err != nil {
		t.Fatal(err)
	}

	if !chunking.TreeSitterAvailable() {
		// Lite binary edits util.py and syncs
		writeFile(t, pyFile, "def helper():\n    return 42\n")
		warn.Reset()
		if err := incrementalGraphSQLite(context.Background(), f.g, []string{pyFile}, "", root, newPrinter(&out, &warn)); err != nil {
			t.Fatalf("incrementalGraphSQLite: %v", err)
		}
		var val string
		if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaGraphTreeSitter).Scan(&val); err != nil {
			t.Fatal(err)
		}
		if val != "0" {
			t.Fatalf("BUG: after treesitter build + lite sync, key is %q; want \"0\" (blind spot hidden)", val)
		}
	} else {
		// Treesitter binary: simulate lite build first
		if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, '0') ON CONFLICT(key) DO UPDATE SET value = '0'`, store.MetaGraphTreeSitter); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('graph_nongo_lite:util.py', '1') ON CONFLICT(key) DO UPDATE SET value = '1'`); err != nil {
			t.Fatal(err)
		}
		// Now sync util.py with treesitter
		writeFile(t, pyFile, "def helper():\n    return 42\n")
		warn.Reset()
		if err := incrementalGraphSQLite(context.Background(), f.g, []string{pyFile}, "", root, newPrinter(&out, &warn)); err != nil {
			t.Fatalf("incrementalGraphSQLite: %v", err)
		}
		var val string
		if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaGraphTreeSitter).Scan(&val); err != nil {
			t.Fatal(err)
		}
		if val != "1" {
			t.Fatalf("BUG: after lite build + treesitter sync of all non-Go files, key is %q; want \"1\"", val)
		}
	}

	// Sequence C: non-Go file deleted -> incremental sync clears the key
	if err := os.Remove(pyFile); err != nil {
		t.Fatal(err)
	}
	warn.Reset()
	if err := incrementalGraphSQLite(context.Background(), f.g, []string{pyFile}, "", root, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("incrementalGraphSQLite on deleted non-Go file: %v", err)
	}
	var valCleared string
	err = db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaGraphTreeSitter).Scan(&valCleared)
	if err == nil {
		t.Fatalf("BUG: after non-Go file deletion, key %s=%q survived; want cleared", store.MetaGraphTreeSitter, valCleared)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unexpected query error: %v", err)
	}
}

func TestSyncMarkerWriteFault(t *testing.T) {
	f := newFaultIndex(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\nfunc Main() {}\n")
	var out, warn bytes.Buffer
	if err := fullGraphSQLite(context.Background(), f.g, "", root, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite: %v", err)
	}

	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	pyFile := filepath.Join(root, "util.py")
	writeFile(t, pyFile, "def helper():\n    return 42\n")

	if !chunking.TreeSitterAvailable() {
		// Pure Go (lite) binary:
		// Simulate initial key='1' from a prior treesitter build.
		if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, '1') ON CONFLICT(key) DO UPDATE SET value = '1'`, store.MetaGraphTreeSitter); err != nil {
			t.Fatal(err)
		}
		// Install trigger aborting marker writes
		if _, err := db.Exec(`CREATE TRIGGER fault_marker BEFORE INSERT ON meta WHEN NEW.key LIKE 'graph_nongo_lite:%' BEGIN SELECT RAISE(ABORT, 'injected fault'); END;`); err != nil {
			t.Fatal(err)
		}

		warn.Reset()
		err := incrementalGraphSQLite(context.Background(), f.g, []string{pyFile}, "", root, newPrinter(&out, &warn))
		if err == nil {
			t.Fatalf("BUG: incrementalGraphSQLite returned nil error when marker write failed")
		}

		// Ensure delta rolled back: util.py symbols must NOT exist in the graph
		var symCount int
		if err := db.QueryRow(`SELECT count(*) FROM symbols WHERE file = 'util.py'`).Scan(&symCount); err != nil {
			t.Fatal(err)
		}
		if symCount != 0 {
			t.Fatalf("BUG: util.py symbols committed despite marker write failure: symCount=%d", symCount)
		}
	} else {
		// Tree-sitter build:
		// Simulate initial key='0' and existing marker
		if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, '0') ON CONFLICT(key) DO UPDATE SET value = '0'`, store.MetaGraphTreeSitter); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('graph_nongo_lite:util.py', '1') ON CONFLICT(key) DO UPDATE SET value = '1'`); err != nil {
			t.Fatal(err)
		}
		// Install trigger aborting marker deletion
		if _, err := db.Exec(`CREATE TRIGGER fault_marker BEFORE DELETE ON meta WHEN OLD.key LIKE 'graph_nongo_lite:%' BEGIN SELECT RAISE(ABORT, 'injected fault'); END;`); err != nil {
			t.Fatal(err)
		}

		warn.Reset()
		err := incrementalGraphSQLite(context.Background(), f.g, []string{pyFile}, "", root, newPrinter(&out, &warn))
		if err == nil {
			t.Fatalf("BUG: incrementalGraphSQLite returned nil error when marker delete failed")
		}
	}
}

func TestDirtyPassDeletionClearsMarker(t *testing.T) {
	f := newFaultIndex(t)
	root := t.TempDir()
	aGo := filepath.Join(root, "a.go")
	bPy := filepath.Join(root, "b.py")
	cPy := filepath.Join(root, "c.py")
	writeFile(t, aGo, "package main\nfunc Main() {}\n")
	writeFile(t, bPy, "def b_func(): pass\n")
	writeFile(t, cPy, "def c_func(): pass\n")

	var out, warn bytes.Buffer
	if err := fullGraphSQLite(context.Background(), f.g, "", root, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite: %v", err)
	}

	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Ensure b.py marker is set in meta
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('graph_nongo_lite:b.py', '1') ON CONFLICT(key) DO UPDATE SET value = '1'`); err != nil {
		t.Fatal(err)
	}

	// Mark b.py dirty and delete it from disk
	if err := f.g.MarkFileDirty(context.Background(), "b.py"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bPy); err != nil {
		t.Fatal(err)
	}

	// Sync ONLY a.go (b.py is deleted from disk and only handled in the dirty pass)
	warn.Reset()
	if err := incrementalGraphSQLite(context.Background(), f.g, []string{aGo}, "", root, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("incrementalGraphSQLite: %v", err)
	}

	// Verify marker for b.py is NOT leaked
	var markerCount int
	if err := db.QueryRow(`SELECT count(*) FROM meta WHERE key = 'graph_nongo_lite:b.py'`).Scan(&markerCount); err != nil {
		t.Fatal(err)
	}
	if markerCount != 0 {
		t.Fatalf("BUG: dirty-pass deletion leaked marker graph_nongo_lite:b.py")
	}

	// Also delete c.py via dirty pass to verify transition when 0 non-Go files remain
	if err := f.g.MarkFileDirty(context.Background(), "c.py"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cPy); err != nil {
		t.Fatal(err)
	}
	warn.Reset()
	if err := incrementalGraphSQLite(context.Background(), f.g, []string{aGo}, "", root, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("incrementalGraphSQLite: %v", err)
	}

	// With no non-Go files left, graph_treesitter must be cleared completely
	var val string
	err = db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaGraphTreeSitter).Scan(&val)
	if err == nil {
		t.Fatalf("BUG: after all non-Go files deleted via dirty pass, key graph_treesitter=%q survived; want cleared", val)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unexpected query error: %v", err)
	}
}

// TestSyncGlobalTreeSitterMetaWriteFault pins the atomicity of the global
// graph_treesitter key: it must be recomputed inside the same SQLite
// transaction as each file delta, so a failing key write rolls that file's
// delta and lite marker back instead of committing a lying "1" key beside
// committed lite-extracted rows (v3 review Finding 2).
func TestSyncGlobalTreeSitterMetaWriteFault(t *testing.T) {
	f := newFaultIndex(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\nfunc Main() {}\n")
	var out, warn bytes.Buffer
	if err := fullGraphSQLite(context.Background(), f.g, "", root, false, newPrinter(&out, &warn)); err != nil {
		t.Fatalf("fullGraphSQLite: %v", err)
	}

	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// A prior tree-sitter build left key="1"; this Go-only graph has no
	// non-Go files yet.
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, '1') ON CONFLICT(key) DO UPDATE SET value = '1'`, store.MetaGraphTreeSitter); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TRIGGER fault_ts_key_insert BEFORE INSERT ON meta WHEN NEW.key = 'graph_treesitter' BEGIN SELECT RAISE(ABORT, 'injected key fault'); END`,
		`CREATE TRIGGER fault_ts_key_update BEFORE UPDATE ON meta WHEN NEW.key = 'graph_treesitter' BEGIN SELECT RAISE(ABORT, 'injected key fault'); END`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	pyFile := filepath.Join(root, "util.py")
	writeFile(t, pyFile, "def helper():\n    return 42\n")

	warn.Reset()
	err = incrementalGraphSQLite(context.Background(), f.g, []string{pyFile}, "", root, newPrinter(&out, &warn))
	if err == nil {
		t.Fatalf("BUG: incrementalGraphSQLite returned nil when the graph_treesitter write failed")
	}
	if !strings.Contains(err.Error(), "injected key fault") {
		t.Fatalf("want the injected key fault in the sync error, got: %v", err)
	}

	var symCount int
	if err := db.QueryRow(`SELECT count(*) FROM symbols WHERE file = 'util.py'`).Scan(&symCount); err != nil {
		t.Fatal(err)
	}
	if symCount != 0 {
		t.Fatalf("BUG: util.py symbols committed although the graph_treesitter write failed: symCount=%d", symCount)
	}
	var markerCount int
	if err := db.QueryRow(`SELECT count(*) FROM meta WHERE key = 'graph_nongo_lite:util.py'`).Scan(&markerCount); err != nil {
		t.Fatal(err)
	}
	if markerCount != 0 {
		t.Fatalf("BUG: graph_nongo_lite:util.py marker committed although the graph_treesitter write failed: markerCount=%d", markerCount)
	}
	// The graph is Go-only again after the rollback, so the final metadata
	// reconciliation clears the stale key instead of leaving a "1" beside
	// lite-extracted rows.
	var key string
	err = db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaGraphTreeSitter).Scan(&key)
	if err == nil {
		t.Fatalf("graph_treesitter = %q survived the rollback of the only non-Go file; want cleared", key)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unexpected query error: %v", err)
	}
}
