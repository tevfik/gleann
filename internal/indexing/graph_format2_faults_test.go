package indexing

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/indexer"
	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
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
	err := fullGraphSQLite(context.Background(), f.g, "example.com/fx", filepath.Join(t.TempDir(), "missing"), false)
	mustContain(t, err, "walking source tree")
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
	err := fullGraphSQLite(context.Background(), f.g, "example.com/fx", root, false)
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
	err = applyFileGraph(ctx, f.g, "example.com/fx", f.docs, a, string(src), nil)
	mustContain(t, err, "resolving graph targets for a.go")
}

func TestApplyFileGraphPackageMembersFailure(t *testing.T) {
	f := newFaultIndex(t)
	py := filepath.Join(f.docs, "tool.py")
	writeFile(t, py, "def run():\n    return 1\n")
	execDB(t, f.dbPath, `ALTER TABLE symbols RENAME COLUMN name TO zz`)
	err := applyFileGraph(context.Background(), f.g, "example.com/fx", f.docs, py, "def run():\n    return 1\n", nil)
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
	if err := applyFileGraph(context.Background(), g, "example.com/fx", docs, later, src, nil); err != nil {
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
