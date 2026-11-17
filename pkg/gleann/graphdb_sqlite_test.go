package gleann

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// The read side of RFC 003 step 7 (lane B): the format-2 code graph is served
// through the existing gleann.GraphDB interface, so every existing consumer
// (searcher graph context, def resolver, MCP and REST graph tools) keeps
// working unchanged. These tests run in the lite build (no tags): they open a
// format-2 store, populate it with FileDeltas and document rows, and check the
// adapter against the same semantics the Kùzu backend exposed.

func openGraphStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func mustApplyDelta(t *testing.T, st *store.Store, d graphstore.FileDelta) {
	t.Helper()
	if err := graphstore.New(st).ApplyFileDelta(context.Background(), d); err != nil {
		t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
	}
}

func gSym(fqn, name, file string, line int64) graphstore.Symbol {
	return graphstore.Symbol{
		FQN: fqn, Name: name, Kind: "function", File: file,
		StartLine: line, EndLine: line + 5, StartCol: 1, ByteOffset: line * 40,
	}
}

// seedGraphFixture mirrors the setupGraph of the former Kùzu tests plus
// a test caller: file1 has A→B→C, file2 has D→B and a test caller TestB→B.
// Declarations are applied first so cross-file edges resolve (ApplyFileDelta
// drops edges to undeclared internal targets, AGENTS.md section 2.3).
func seedGraphFixture(t *testing.T, st *store.Store) {
	t.Helper()
	f1 := graphstore.FileDelta{
		File: "pkg/file1.go",
		Symbols: []graphstore.Symbol{
			gSym("mod/pkg.A", "A", "pkg/file1.go", 10),
			gSym("mod/pkg.B", "B", "pkg/file1.go", 20),
			gSym("mod/pkg.C", "C", "pkg/file1.go", 30),
		},
		Edges: []graphstore.Edge{
			{Src: "mod/pkg.A", Dst: "mod/pkg.B"},
			{Src: "mod/pkg.B", Dst: "mod/pkg.C"},
		},
	}
	f2 := graphstore.FileDelta{
		File: "pkg/file2.go",
		Symbols: []graphstore.Symbol{
			gSym("mod/pkg.D", "D", "pkg/file2.go", 10),
			testSym("mod/pkg.TestB", "pkg/file2.go", 20),
		},
		Edges: []graphstore.Edge{
			{Src: "mod/pkg.D", Dst: "mod/pkg.B"},
			{Src: "mod/pkg.TestB", Dst: "mod/pkg.B"},
		},
	}
	mustApplyDelta(t, st, graphstore.FileDelta{File: f1.File, Symbols: f1.Symbols})
	mustApplyDelta(t, st, graphstore.FileDelta{File: f2.File, Symbols: f2.Symbols})
	mustApplyDelta(t, st, f1)
	mustApplyDelta(t, st, f2)
}

func testSym(fqn, file string, line int64) graphstore.Symbol {
	s := gSym(fqn, "TestB", file, line)
	s.IsTest = true
	return s
}

// seedResolutionFixture mirrors internal/graphstore's resolveFixture: symbols
// with Go-style ('.', '/') and Rust-style ('::') FQNs, two symbols sharing the
// base name "Close", and a symbol whose FQN is exactly "StoreA.Close".
func seedResolutionFixture(t *testing.T, st *store.Store) {
	t.Helper()
	rsym := func(fqn, name, file string, line int64) graphstore.Symbol {
		return graphstore.Symbol{FQN: fqn, Name: name, Kind: "method", File: file,
			StartLine: line, EndLine: line + 1, StartCol: 1, ByteOffset: line}
	}
	files := []struct {
		file  string
		syms  []graphstore.Symbol
		edges []graphstore.Edge
	}{
		{"pkg/a/store.go", []graphstore.Symbol{rsym("pkg/a.StoreA.Close", "Close", "pkg/a/store.go", 50)}, nil},
		{"pkg/b/store.go", []graphstore.Symbol{rsym("pkg/b.StoreB.Close", "Close", "pkg/b/store.go", 80)}, nil},
		{"pkg/a/worker.go", []graphstore.Symbol{rsym("pkg/a.WorkerA.Run", "Run", "pkg/a/worker.go", 20)},
			[]graphstore.Edge{{Src: "pkg/a.WorkerA.Run", Dst: "pkg/a.StoreA.Close"}}},
		{"pkg/b/worker.go", []graphstore.Symbol{rsym("pkg/b.WorkerB.Run", "Run", "pkg/b/worker.go", 35)},
			[]graphstore.Edge{{Src: "pkg/b.WorkerB.Run", Dst: "pkg/b.StoreB.Close"}}},
		{"pkg/extra.go", []graphstore.Symbol{
			rsym("StoreA.Close", "Close", "pkg/extra.go", 5),
			rsym("pkg/extra.Extra", "Extra", "pkg/extra.go", 7),
		}, []graphstore.Edge{{Src: "pkg/extra.Extra", Dst: "StoreA.Close"}}},
		{"mod/util/search.go", []graphstore.Symbol{rsym("mod/util.Search", "Search", "mod/util/search.go", 5)}, nil},
		{"mod/pkg/lookup.go", []graphstore.Symbol{rsym("mod/pkg.Lookup", "Lookup", "mod/pkg/lookup.go", 12)},
			[]graphstore.Edge{{Src: "mod/pkg.Lookup", Dst: "mod/util.Search"}}},
		{"crate/mod.rs", []graphstore.Symbol{
			rsym("crate::mod::Thing", "Thing", "crate/mod.rs", 3),
			rsym("crate::mod::Caller", "Caller", "crate/mod.rs", 9),
		}, []graphstore.Edge{{Src: "crate::mod::Caller", Dst: "crate::mod::Thing"}}},
	}
	for _, f := range files {
		mustApplyDelta(t, st, graphstore.FileDelta{File: f.file, Symbols: f.syms})
	}
	for _, f := range files {
		mustApplyDelta(t, st, graphstore.FileDelta{File: f.file, Symbols: f.syms, Edges: f.edges})
	}
}

// seedDocFixture mirrors the document fixture of the former Kùzu tests:
// folder > document > two headings > two chunks, one chunk explaining a symbol.
func seedDocFixture(t *testing.T, st *store.Store) {
	t.Helper()
	nodes := []graphstore.DocNode{
		{ID: "fold1", Type: "folder", Name: "docs"},
		{ID: "doc1", Type: "document", Parent: "fold1", Name: "arch.md", RPath: "docs/arch.md", Summary: "Architecture documentation summary."},
		{ID: "h1", Type: "heading", Parent: "doc1", Name: "Introduction", Level: 1},
		{ID: "h2", Type: "heading", Parent: "doc1", Name: "Storage Engine", Level: 2},
		{ID: "c1", Type: "chunk", Parent: "doc1", Text: "Introduction text.", StartChar: 0, EndChar: 18},
		{ID: "c2", Type: "chunk", Parent: "doc1", Text: "Storage engine details.", StartChar: 19, EndChar: 42},
	}
	db := st.WriterDB()
	for _, n := range nodes {
		var parent, name, rpath, summary, text any
		if n.Parent != "" {
			parent = n.Parent
		}
		if n.Name != "" {
			name = n.Name
		}
		if n.RPath != "" {
			rpath = n.RPath
		}
		if n.Summary != "" {
			summary = n.Summary
		}
		if n.Text != "" {
			text = n.Text
		}
		if _, err := db.Exec(`INSERT INTO doc_nodes
(id, type, parent, name, level, rpath, hash, summary, text, start_char, end_char)
VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?)`,
			n.ID, n.Type, parent, name, n.Level, rpath, summary, text, n.StartChar, n.EndChar); err != nil {
			t.Fatalf("inserting doc node %s: %v", n.ID, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO doc_explains (chunk_id, fqn) VALUES ('c1', 'mod/pkg.A')`); err != nil {
		t.Fatalf("inserting doc_explains: %v", err)
	}
}

// sortStrings sorts a copy, so order-insensitive comparisons don't mutate the
// result under test.
func sortStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestSQLiteGraphDB_SymbolMethods(t *testing.T) {
	st := openGraphStore(t)
	seedGraphFixture(t, st)
	db := newSQLiteGraphDB(st)

	callees, err := db.Callees("mod/pkg.A")
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if len(callees) != 1 || callees[0].FQN != "mod/pkg.B" || callees[0].Name != "B" ||
		callees[0].File != "pkg/file1.go" || callees[0].Line != 20 {
		t.Fatalf("Callees(A) = %+v, want mod/pkg.B (B, pkg/file1.go:20)", callees)
	}

	callers, err := db.Callers("mod/pkg.B")
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	// Production callers first (is_test ASC), then FQN, then the test caller.
	want := []string{"mod/pkg.A", "mod/pkg.D", "mod/pkg.TestB"}
	var got []string
	for _, c := range callers {
		got = append(got, c.FQN)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Callers(B) = %v, want %v (production before test)", got, want)
	}
	if !callers[2].IsTest {
		t.Fatalf("Callers(B)[2].IsTest = false, want true")
	}

	syms, err := db.SymbolsInFile("pkg/file1.go")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	if len(syms) != 3 {
		t.Fatalf("SymbolsInFile = %d symbols, want 3: %+v", len(syms), syms)
	}

	search, err := db.SymbolSearch("mod/pkg")
	if err != nil {
		t.Fatalf("SymbolSearch: %v", err)
	}
	if len(search) != 5 {
		t.Fatalf("SymbolSearch(mod/pkg) = %d, want 5", len(search))
	}
	none, err := db.SymbolSearch("zzz_nonexistent_zzz")
	if err != nil || len(none) != 0 {
		t.Fatalf("SymbolSearch(nonexistent) = %+v, %v; want empty", none, err)
	}
}

func TestSQLiteGraphDB_ImpactNeighborsPathStats(t *testing.T) {
	st := openGraphStore(t)
	seedGraphFixture(t, st)
	db := newSQLiteGraphDB(st)

	impact, err := db.Impact("mod/pkg.C", 5)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if impact.Symbol != "mod/pkg.C" || impact.Depth != 5 {
		t.Fatalf("Impact = %+v", impact)
	}
	if !reflect.DeepEqual(sortStrings(impact.DirectCallers), []string{"mod/pkg.B"}) {
		t.Fatalf("DirectCallers = %v, want [mod/pkg.B]", impact.DirectCallers)
	}
	if !reflect.DeepEqual(sortStrings(impact.TransitiveCallers), []string{"mod/pkg.A", "mod/pkg.D"}) {
		t.Fatalf("TransitiveCallers = %v, want [mod/pkg.A mod/pkg.D]", impact.TransitiveCallers)
	}
	if !reflect.DeepEqual(sortStrings(impact.TestCallers), []string{"mod/pkg.TestB"}) {
		t.Fatalf("TestCallers = %v, want [mod/pkg.TestB]", impact.TestCallers)
	}
	if !reflect.DeepEqual(impact.AffectedFiles, []string{"pkg/file1.go", "pkg/file2.go"}) {
		t.Fatalf("AffectedFiles = %v, want [pkg/file1.go pkg/file2.go]", impact.AffectedFiles)
	}

	edges, err := db.Neighbors("mod/pkg.B", 1)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(edges) != 4 {
		t.Fatalf("Neighbors(B, 1) = %d edges, want 4: %+v", len(edges), edges)
	}

	path, err := db.ShortestPath("mod/pkg.A", "mod/pkg.C")
	if err != nil {
		t.Fatalf("ShortestPath: %v", err)
	}
	wantPath := []PathStep{
		{From: "mod/pkg.A", To: "mod/pkg.B", Relation: "CALLS"},
		{From: "mod/pkg.B", To: "mod/pkg.C", Relation: "CALLS"},
	}
	if !reflect.DeepEqual(path, wantPath) {
		t.Fatalf("ShortestPath(A, C) = %+v, want %+v", path, wantPath)
	}
	if _, err := db.ShortestPath("mod/pkg.A", "nonexistent.Foo"); err == nil {
		t.Fatal("ShortestPath to nonexistent target succeeded, want error")
	}

	stats, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	wantStats := &GraphStats{Files: 2, Symbols: 5, CallEdges: 4, DeclareEdges: 5, ImplementsEdges: 0}
	if !reflect.DeepEqual(stats, wantStats) {
		t.Fatalf("Stats = %+v, want %+v", stats, wantStats)
	}
}

func TestSQLiteGraphDB_DocumentMethods(t *testing.T) {
	st := openGraphStore(t)
	seedGraphFixture(t, st)
	seedDocFixture(t, st)
	db := newSQLiteGraphDB(st)

	ctx, err := db.DocumentContext("docs/arch.md")
	if err != nil {
		t.Fatalf("DocumentContext: %v", err)
	}
	if ctx.Name != "arch.md" || ctx.FolderName != "docs" || ctx.Summary != "Architecture documentation summary." {
		t.Fatalf("DocumentContext metadata = %+v", ctx)
	}
	if !reflect.DeepEqual(ctx.Headings, []string{"Introduction", "Storage Engine"}) {
		t.Fatalf("headings = %v", ctx.Headings)
	}
	if ctx.Breadcrumb != "docs > arch.md > Introduction > Storage Engine" {
		t.Fatalf("breadcrumb = %q", ctx.Breadcrumb)
	}

	toc, err := db.DocumentTOC("docs/arch.md")
	if err != nil {
		t.Fatalf("DocumentTOC: %v", err)
	}
	if toc.Title != "arch.md" || toc.Folder != "docs" || toc.TotalNodes != 2 {
		t.Fatalf("DocumentTOC metadata = %+v", toc)
	}

	docs, err := db.ListDocuments()
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].Title != "arch.md" || docs[0].TotalNodes != 2 {
		t.Fatalf("ListDocuments = %+v", docs)
	}

	full, err := db.FullDocument("docs/arch.md")
	if err != nil {
		t.Fatalf("FullDocument: %v", err)
	}
	if full != "Introduction text.\n\nStorage engine details." {
		t.Fatalf("FullDocument = %q", full)
	}

	docSyms, err := db.DocumentSymbols("docs/arch.md")
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	if len(docSyms) != 1 || docSyms[0].FQN != "mod/pkg.A" {
		t.Fatalf("DocumentSymbols = %+v, want [mod/pkg.A]", docSyms)
	}

	// Close is a no-op over the shared store; it must not panic or close the
	// store (the cleanup close below still works).
	db.Close()
}

func TestLoadPicksGraphstoreGraphDB(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	seedGraphFixture(t, st)
	st.Close()

	meta := IndexMeta{Name: "idx", Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewSearcher(Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	db := s.GraphDB()
	if db == nil {
		t.Fatal("a format-2 index with graph data must expose a GraphDB in the lite build")
	}
	if _, ok := db.(*sqliteGraphDB); !ok {
		t.Fatalf("GraphDB() = %T, want *sqliteGraphDB", db)
	}
	callers, err := db.Callers("mod/pkg.B")
	if err != nil || len(callers) != 3 {
		t.Fatalf("Callers(B) through Loaded GraphDB = %+v, %v; want 3", callers, err)
	}
}

func TestLoadNoGraphDataYieldsNilGraphDB(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	meta := IndexMeta{Name: "idx", Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewSearcher(Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()
	if s.GraphDB() != nil {
		t.Fatalf("a format-2 index with empty symbols/doc_nodes must not expose a GraphDB, got %T", s.GraphDB())
	}
}

// calleeFQNs returns the FQNs of a Callee slice, sorted for order-insensitive
// comparison.
func calleeFQNs(cs []Callee) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.FQN)
	}
	sort.Strings(out)
	return out
}

// TestSQLiteGraphDB_NameResolution exercises the adapter's three-step lookup
// through Callees/Callers/Impact: short name, Type.Method and path-qualified
// suffix, ambiguous base name (union), and exact FQN preferred over a suffix.
func TestSQLiteGraphDB_NameResolution(t *testing.T) {
	st := openGraphStore(t)
	seedResolutionFixture(t, st)
	db := newSQLiteGraphDB(st)

	// Ambiguous base name for callees: the union of both Worker symbols.
	callees, err := db.Callees("Run")
	if err != nil {
		t.Fatalf("Callees(Run): %v", err)
	}
	if want := []string{"pkg/a.StoreA.Close", "pkg/b.StoreB.Close"}; !reflect.DeepEqual(calleeFQNs(callees), want) {
		t.Fatalf("Callees(Run) = %v, want %v", calleeFQNs(callees), want)
	}

	// Type.Method suffix ('.'): "WorkerA.Run" resolves via ".WorkerA.Run".
	callees, err = db.Callees("WorkerA.Run")
	if err != nil {
		t.Fatalf("Callees(WorkerA.Run): %v", err)
	}
	if want := []string{"pkg/a.StoreA.Close"}; !reflect.DeepEqual(calleeFQNs(callees), want) {
		t.Fatalf("Callees(WorkerA.Run) = %v, want %v", calleeFQNs(callees), want)
	}

	// Exact FQN preferred over suffix: "StoreA.Close" denotes the symbol whose
	// FQN is exactly "StoreA.Close", not the suffix match "pkg/a.StoreA.Close".
	callers, err := db.Callers("StoreA.Close")
	if err != nil {
		t.Fatalf("Callers(StoreA.Close): %v", err)
	}
	if want := []string{"pkg/extra.Extra"}; !reflect.DeepEqual(calleeFQNs(callers), want) {
		t.Fatalf("Callers(StoreA.Close) = %v, want %v", calleeFQNs(callers), want)
	}

	// Ambiguous base name for callers: all three "Close" symbols' callers.
	callers, err = db.Callers("Close")
	if err != nil {
		t.Fatalf("Callers(Close): %v", err)
	}
	if want := []string{"pkg/a.WorkerA.Run", "pkg/b.WorkerB.Run", "pkg/extra.Extra"}; !reflect.DeepEqual(calleeFQNs(callers), want) {
		t.Fatalf("Callers(Close) = %v, want %v", calleeFQNs(callers), want)
	}

	// Path-qualified suffix ('/'): "a.StoreA.Close" resolves via "/a.StoreA.Close".
	callers, err = db.Callers("a.StoreA.Close")
	if err != nil {
		t.Fatalf("Callers(a.StoreA.Close): %v", err)
	}
	if want := []string{"pkg/a.WorkerA.Run"}; !reflect.DeepEqual(calleeFQNs(callers), want) {
		t.Fatalf("Callers(a.StoreA.Close) = %v, want %v", calleeFQNs(callers), want)
	}

	// Impact resolves a short name the same way (through the callee side).
	impact, err := db.Impact("Search", 3)
	if err != nil {
		t.Fatalf("Impact(Search): %v", err)
	}
	if !reflect.DeepEqual(impact.DirectCallers, []string{"mod/pkg.Lookup"}) {
		t.Fatalf("Impact(Search).DirectCallers = %v, want [mod/pkg.Lookup]", impact.DirectCallers)
	}
}

// TestSQLiteGraphDB_DocumentVPath checks that the adapter fills VPath with the
// physical rpath (the value kuzu's vpath would have shown for a document with
// no virtual-path override) and that lookups by that vpath keep working.
func TestSQLiteGraphDB_DocumentVPath(t *testing.T) {
	st := openGraphStore(t)
	seedDocFixture(t, st)
	db := newSQLiteGraphDB(st)

	toc, err := db.DocumentTOC("docs/arch.md")
	if err != nil {
		t.Fatalf("DocumentTOC: %v", err)
	}
	if toc.VPath != "docs/arch.md" {
		t.Fatalf("DocumentTOC.VPath = %q, want docs/arch.md", toc.VPath)
	}

	docs, err := db.ListDocuments()
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].VPath != "docs/arch.md" {
		t.Fatalf("ListDocuments = %+v, want one doc with VPath docs/arch.md", docs)
	}

	ctx, err := db.DocumentContext("docs/arch.md")
	if err != nil {
		t.Fatalf("DocumentContext: %v", err)
	}
	if ctx.VPath != "docs/arch.md" {
		t.Fatalf("DocumentContext.VPath = %q, want docs/arch.md", ctx.VPath)
	}

	// kuzu matched vpath or rpath; a lookup by the reported vpath must resolve.
	if _, err := db.DocumentTOC(docs[0].VPath); err != nil {
		t.Fatalf("DocumentTOC(by vpath): %v", err)
	}
	if _, err := db.FullDocument(toc.VPath); err != nil {
		t.Fatalf("FullDocument(by vpath): %v", err)
	}
}

// buildFormat2GraphIndex creates a format-2 index directory with graph data
// and a meta.json recording store format 2, and returns the IndexDir.
func buildFormat2GraphIndex(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, name, name)
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	seedGraphFixture(t, st)
	st.Close()

	meta := IndexMeta{Name: name, Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadReloadReattachesGraphDB(t *testing.T) {
	dir := buildFormat2GraphIndex(t, "idx")
	s := NewSearcher(Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()
	db1 := s.GraphDB()
	if db1 == nil {
		t.Fatal("a format-2 index with graph data must expose a GraphDB")
	}
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("reload: %v", err)
	}
	db2 := s.GraphDB()
	if db2 == nil {
		t.Fatal("reload must reattach the graphDB")
	}
	if db1.(*sqliteGraphDB) == db2.(*sqliteGraphDB) {
		t.Fatal("reload kept the old graphDB adapter; Load must reset and reattach a fresh one")
	}
	if callers, err := db2.Callers("mod/pkg.B"); err != nil || len(callers) != 3 {
		t.Fatalf("Callers(B) after reload = %+v, %v; want 3", callers, err)
	}
}

func TestLoadAfterCloseReattachesGraphDB(t *testing.T) {
	dir := buildFormat2GraphIndex(t, "idx")
	s := NewSearcher(Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.GraphDB() == nil {
		t.Fatal("a format-2 index with graph data must expose a GraphDB")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if s.GraphDB() != nil {
		t.Fatal("Close must set graphDB to nil")
	}
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load after Close: %v", err)
	}
	defer s.Close()
	if s.GraphDB() == nil {
		t.Fatal("Load after Close must reattach the graphDB")
	}
	if callers, err := s.GraphDB().Callers("mod/pkg.B"); err != nil || len(callers) != 3 {
		t.Fatalf("Callers(B) after Close+reload = %+v, %v; want 3", callers, err)
	}
}

func TestLoadFailedThenRetryReattachesGraphDB(t *testing.T) {
	dir := buildFormat2GraphIndex(t, "idx")
	s := NewSearcher(Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.GraphDB() == nil {
		t.Fatal("a format-2 index with graph data must expose a GraphDB")
	}

	// Corrupt meta.json so the next Load fails before opening the store.
	base := filepath.Join(dir, "idx", "idx")
	if err := os.WriteFile(base+".meta.json", []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(context.Background(), "idx"); err == nil {
		t.Fatal("Load with corrupt meta.json succeeded, want error")
	}
	if s.GraphDB() != nil {
		t.Fatal("a failed Load must not leave the previous graphDB attached")
	}

	// Restore meta.json and retry.
	meta := IndexMeta{Name: "idx", Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("retry Load: %v", err)
	}
	defer s.Close()
	if s.GraphDB() == nil {
		t.Fatal("a retry after a failed Load must reattach the graphDB")
	}
}

// An old <name>_graph Kùzu directory next to an index is never read (RFC 004
// A3): a format-2 index with graph data serves its SQLite graph, and one with
// empty graph tables has no graph at all.
func TestLoadFormat2IgnoresOldKuzuDirectory(t *testing.T) {
	dir := buildFormat2GraphIndex(t, "idx")
	if err := os.MkdirAll(filepath.Join(dir, "idx_graph"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()
	if _, ok := s.GraphDB().(*sqliteGraphDB); !ok {
		t.Fatalf("GraphDB() = %T, want *sqliteGraphDB", s.GraphDB())
	}
}

func TestLoadFormat2EmptySQLiteHasNoGraph(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	if err := os.MkdirAll(filepath.Join(dir, "idx"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close() // empty store: no graph data
	meta := IndexMeta{Name: "idx", Backend: "hnsw", LexicalOnly: true, StoreFormat: 2, Version: "1.0.0"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "idx_graph"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()
	if s.GraphDB() != nil {
		t.Fatalf("GraphDB() = %T for empty graph tables, want nil", s.GraphDB())
	}
	if HasCodeGraph(dir, "idx") {
		t.Fatal("HasCodeGraph: an old idx_graph directory must not count as a code graph")
	}
}

// TestSQLiteGraphDB_ImpactMultiSeedDirectWins: a symbol that is a direct caller
// of any resolved seed must not also appear in TransitiveCallers (kuzu enqueues
// every seed's direct callers before expanding).
func TestSQLiteGraphDB_ImpactMultiSeedDirectWins(t *testing.T) {
	st := openGraphStore(t)
	sym := func(fqn, name, file string, line int64) graphstore.Symbol {
		return graphstore.Symbol{FQN: fqn, Name: name, Kind: "function", File: file,
			StartLine: line, EndLine: line + 1, StartCol: 1, ByteOffset: line}
	}
	seeds := []graphstore.Symbol{
		sym("mod/pkg.SeedA", "Target", "pkg/a.go", 10),
		sym("mod/pkg.SeedB", "Target", "pkg/a.go", 20),
		sym("mod/pkg.C", "C", "pkg/a.go", 30),
		sym("mod/pkg.X", "X", "pkg/a.go", 40),
	}
	// C -> X -> SeedA (C is a transitive caller of SeedA) and C -> SeedB (C is
	// a direct caller of SeedB). Both seeds share the base name "Target".
	mustApplyDelta(t, st, graphstore.FileDelta{File: "pkg/a.go", Symbols: seeds})
	mustApplyDelta(t, st, graphstore.FileDelta{File: "pkg/a.go", Symbols: seeds,
		Edges: []graphstore.Edge{
			{Src: "mod/pkg.C", Dst: "mod/pkg.X"},
			{Src: "mod/pkg.X", Dst: "mod/pkg.SeedA"},
			{Src: "mod/pkg.C", Dst: "mod/pkg.SeedB"},
		}})

	db := newSQLiteGraphDB(st)
	impact, err := db.Impact("Target", 3)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if !reflect.DeepEqual(sortStrings(impact.DirectCallers), []string{"mod/pkg.C", "mod/pkg.X"}) {
		t.Fatalf("DirectCallers = %v, want [C X]", impact.DirectCallers)
	}
	if len(impact.TransitiveCallers) != 0 {
		t.Fatalf("TransitiveCallers = %v, want empty (C is a direct caller of SeedB)", impact.TransitiveCallers)
	}
}

// TestSQLiteGraphDB_SelfRecursion: a direct self edge reports the symbol as its
// own callee/caller and direct Impact caller, while a longer cycle must not
// add the start node back.
func TestSQLiteGraphDB_SelfRecursion(t *testing.T) {
	st := openGraphStore(t)
	sym := func(fqn, name, file string, line int64) graphstore.Symbol {
		return graphstore.Symbol{FQN: fqn, Name: name, Kind: "function", File: file,
			StartLine: line, EndLine: line + 1, StartCol: 1, ByteOffset: line}
	}
	syms := []graphstore.Symbol{
		sym("mod/pkg.Rec", "Rec", "pkg/a.go", 10),
		sym("mod/pkg.A", "A", "pkg/a.go", 20),
		sym("mod/pkg.B", "B", "pkg/a.go", 30),
	}
	mustApplyDelta(t, st, graphstore.FileDelta{File: "pkg/a.go", Symbols: syms})
	mustApplyDelta(t, st, graphstore.FileDelta{File: "pkg/a.go", Symbols: syms,
		Edges: []graphstore.Edge{
			{Src: "mod/pkg.Rec", Dst: "mod/pkg.Rec"},
			{Src: "mod/pkg.A", Dst: "mod/pkg.B"},
			{Src: "mod/pkg.B", Dst: "mod/pkg.A"},
		}})

	db := newSQLiteGraphDB(st)

	callees, err := db.Callees("mod/pkg.Rec")
	if err != nil {
		t.Fatalf("Callees(Rec): %v", err)
	}
	if !reflect.DeepEqual(calleeFQNs(callees), []string{"mod/pkg.Rec"}) {
		t.Fatalf("Callees(Rec) = %v, want [Rec] (direct self edge)", calleeFQNs(callees))
	}
	callers, err := db.Callers("mod/pkg.Rec")
	if err != nil {
		t.Fatalf("Callers(Rec): %v", err)
	}
	if !reflect.DeepEqual(calleeFQNs(callers), []string{"mod/pkg.Rec"}) {
		t.Fatalf("Callers(Rec) = %v, want [Rec] (direct self edge)", calleeFQNs(callers))
	}
	impact, err := db.Impact("mod/pkg.Rec", 5)
	if err != nil {
		t.Fatalf("Impact(Rec): %v", err)
	}
	if !reflect.DeepEqual(impact.DirectCallers, []string{"mod/pkg.Rec"}) {
		t.Fatalf("Impact(Rec).DirectCallers = %v, want [Rec]", impact.DirectCallers)
	}
	if len(impact.TransitiveCallers) != 0 {
		t.Fatalf("Impact(Rec).TransitiveCallers = %v, want empty", impact.TransitiveCallers)
	}

	// A longer cycle (a -> b -> a): the start node is not its own caller.
	callersA, err := db.Callers("mod/pkg.A")
	if err != nil {
		t.Fatalf("Callers(A): %v", err)
	}
	if !reflect.DeepEqual(calleeFQNs(callersA), []string{"mod/pkg.B"}) {
		t.Fatalf("Callers(A) = %v, want [B] (cycle must not report A as its own caller)", calleeFQNs(callersA))
	}
	impactA, err := db.Impact("mod/pkg.A", 5)
	if err != nil {
		t.Fatalf("Impact(A): %v", err)
	}
	if !reflect.DeepEqual(impactA.DirectCallers, []string{"mod/pkg.B"}) {
		t.Fatalf("Impact(A).DirectCallers = %v, want [B]", impactA.DirectCallers)
	}
	if len(impactA.TransitiveCallers) != 0 {
		t.Fatalf("Impact(A).TransitiveCallers = %v, want empty", impactA.TransitiveCallers)
	}
}

// newSQLiteGraphDB wraps an already-open format-2 store's graph side. A nil
// store yields a nil adapter so callers can guard on it uniformly.
func newSQLiteGraphDB(st *store.Store) *sqliteGraphDB {
	if st == nil {
		return nil
	}
	return &sqliteGraphDB{g: graphstore.New(st)}
}
