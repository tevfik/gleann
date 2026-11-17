package graphstore

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// graphSnapshot is everything a graph write may touch: symbols with their
// anchors, edges, the graph columns of the manifest, the external nodes, the
// document hierarchy and the row counters. A failed write must leave it
// exactly as it was (AGENTS.md section 2.3: "if graph update fails, the
// manifest must not advance").
type graphSnapshot struct {
	Symbols  []string
	Edges    []string
	Files    []string
	Nodes    []string
	Docs     []string
	Explains []string
	Counters []string
}

func snapshot(t *testing.T, g *SQLite) graphSnapshot {
	t.Helper()
	q := func(query string) []string {
		rows, err := g.reader.Query(query)
		if err != nil {
			t.Fatalf("snapshot %q: %v", query, err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("snapshot %q: %v", query, err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("snapshot %q: %v", query, err)
		}
		sort.Strings(out)
		return out
	}
	return graphSnapshot{
		Symbols:  q(`SELECT id || ' ' || fqn || ' ' || file || ':' || start_line FROM symbols`),
		Edges:    q(`SELECT src || ' ' || dst || ' ' || kind || ' ' || dst_external || ' ' || confidence FROM graph_edges`),
		Files:    q(`SELECT path || ' ' || coalesce(graph_hash, '-') || ' ' || graph_dirty FROM files`),
		Nodes:    q(`SELECT id || ' ' || fqn FROM graph_nodes`),
		Docs:     q(`SELECT id || ' ' || coalesce(rpath, '-') FROM doc_nodes`),
		Explains: q(`SELECT chunk_id || ' ' || fqn FROM doc_explains`),
		Counters: q(`SELECT key || '=' || value FROM meta WHERE key LIKE 'graph_%'`),
	}
}

// seedFaultGraph: a.go declares a.One and a.Two and calls fmt.Println
// (external); b.go's b.Caller calls a.One (an inbound edge of a.go); a
// document explains a.One.
func seedFaultGraph(t *testing.T, g *SQLite) {
	t.Helper()
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v1",
		Symbols: []Symbol{mkSym("a.One", "a.go", 1), mkSym("a.Two", "a.go", 10)},
		Edges:   []Edge{{Src: "a.Two", Dst: "fmt.Println", DstExternal: true}},
	})
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "b-v1",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
		Edges:   []Edge{{Src: "b.Caller", Dst: "a.One"}},
	})
	d := docDelta("docs/guide.md")
	d.Explains = []DocExplains{{ChunkID: "c1:docs/guide.md", FQN: "a.One"}}
	if err := g.ApplyDocDelta(context.Background(), d); err != nil {
		t.Fatalf("seeding document: %v", err)
	}
}

// TestGraphWriteFailuresRollBack injects a failure at each step of the graph
// write paths (a trigger aborting one statement, or a missing counter) and
// checks that the write reports a wrapped error naming the step and that
// nothing it did before the failure is visible: graph rows, manifest graph
// columns, external nodes, documents and counters are unchanged.
func TestGraphWriteFailuresRollBack(t *testing.T) {
	ctx := context.Background()
	trigger := func(name, when string) string {
		return `CREATE TRIGGER ` + name + ` ` + when + ` BEGIN SELECT RAISE(ABORT, 'injected ` + name + `'); END`
	}
	tests := []struct {
		name   string
		inject string // SQL run on the writer before the operation
		op     func(g *SQLite) error
		want   string // substring of the error
	}{
		{
			name:   "inbound edge delete",
			inject: trigger("t1", `BEFORE DELETE ON graph_edges`),
			op: func(g *SQLite) error {
				return g.ApplyFileDelta(ctx, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.One", "a.go", 2)}})
			},
			want: "deleting inbound edges",
		},
		{
			name:   "own edge delete",
			inject: trigger("t1", `BEFORE DELETE ON graph_edges`),
			op: func(g *SQLite) error {
				return g.ApplyFileDelta(ctx, FileDelta{File: "b.go", Symbols: []Symbol{mkSym("b.Caller", "b.go", 2)}})
			},
			want: "deleting edges",
		},
		{
			name:   "symbol delete",
			inject: trigger("t1", `BEFORE DELETE ON symbols`),
			op: func(g *SQLite) error {
				return g.RemoveFile(ctx, "b.go")
			},
			want: "deleting symbols",
		},
		{
			name:   "symbol insert",
			inject: trigger("t1", `BEFORE INSERT ON symbols`),
			op: func(g *SQLite) error {
				return g.ApplyFileDelta(ctx, FileDelta{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)}})
			},
			want: `inserting symbol "c.New"`,
		},
		{
			name:   "edge insert",
			inject: trigger("t1", `BEFORE INSERT ON graph_edges`),
			op: func(g *SQLite) error {
				return g.ApplyFileDelta(ctx, FileDelta{
					File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)},
					Edges: []Edge{{Src: "c.New", Dst: "a.Two"}},
				})
			},
			want: `inserting edge "c.New" -> "a.Two"`,
		},
		{
			name:   "inbound edge restore",
			inject: trigger("t1", `BEFORE INSERT ON graph_edges`),
			op: func(g *SQLite) error {
				// a.One survives, so b.Caller -> a.One is re-inserted.
				return g.ApplyFileDelta(ctx, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.One", "a.go", 5)}})
			},
			want: "restoring inbound edge",
		},
		{
			name:   "new label",
			inject: trigger("t1", `BEFORE INSERT ON graph_labels`),
			op: func(g *SQLite) error {
				return g.ApplyFileDelta(ctx, FileDelta{
					File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)},
					Edges: []Edge{{Src: "c.New", Dst: "a.Two", Confidence: "guessed"}},
				})
			},
			want: `interning label "guessed"`,
		},
		{
			name:   "external node insert",
			inject: trigger("t1", `BEFORE INSERT ON graph_nodes`),
			op: func(g *SQLite) error {
				return g.ApplyFileDelta(ctx, FileDelta{
					File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)},
					Edges: []Edge{{Src: "c.New", Dst: "os.Exit", DstExternal: true}},
				})
			},
			want: `interning "os.Exit"`,
		},
		{
			name:   "external node moved into symbols",
			inject: trigger("t1", `BEFORE DELETE ON graph_nodes`),
			op: func(g *SQLite) error {
				// Declaring fmt.Println moves its graph_nodes row into symbols.
				return g.ApplyFileDelta(ctx, FileDelta{File: "fmt/print.go", Symbols: []Symbol{mkSym("fmt.Println", "fmt/print.go", 1)}})
			},
			want: `moving node "fmt.Println" into symbols`,
		},
		{
			name:   "unreferenced external node cleanup",
			inject: trigger("t1", `BEFORE DELETE ON graph_nodes`),
			op: func(g *SQLite) error {
				return g.RemoveFile(ctx, "a.go")
			},
			want: "cleaning up node",
		},
		{
			name:   "referrer marked dirty",
			inject: trigger("t1", `BEFORE UPDATE OF graph_dirty ON files WHEN NEW.graph_dirty = 1`),
			op: func(g *SQLite) error {
				// a.One disappears, so b.go must become dirty.
				return g.ApplyFileDelta(ctx, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.Two", "a.go", 10)}})
			},
			want: `marking "b.go" graph_dirty`,
		},
		{
			name:   "files row of a new file",
			inject: trigger("t1", `BEFORE INSERT ON files`),
			op: func(g *SQLite) error {
				return g.ApplyFileDelta(ctx, FileDelta{File: "c.go", GraphHash: "c1", Symbols: []Symbol{mkSym("c.New", "c.go", 1)}})
			},
			want: "writing files row",
		},
		{
			name:   "graph_hash cleared on remove",
			inject: trigger("t1", `BEFORE UPDATE OF graph_hash ON files`),
			op: func(g *SQLite) error {
				return g.RemoveFile(ctx, "b.go")
			},
			want: "clearing graph_hash",
		},
		{
			name:   "counter update",
			inject: trigger("t1", `BEFORE UPDATE ON meta`),
			op: func(g *SQLite) error {
				return g.ApplyFileDelta(ctx, FileDelta{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)}})
			},
			want: "updating graph counter " + store.MetaGraphSymbols,
		},
		{
			name:   "counter missing",
			inject: `DELETE FROM meta WHERE key = '` + store.MetaGraphSymbols + `'`,
			op: func(g *SQLite) error {
				return g.RemoveFile(ctx, "b.go")
			},
			want: "schema not migrated",
		},
		{
			name:   "bulk retraction",
			inject: trigger("t1", `BEFORE DELETE ON graph_edges`),
			op: func(g *SQLite) error {
				return g.ReplaceAll(ctx, []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)}}})
			},
			want: "DELETE FROM graph_edges",
		},
		{
			name:   "bulk symbol insert",
			inject: trigger("t1", `BEFORE INSERT ON symbols`),
			op: func(g *SQLite) error {
				return g.ReplaceAll(ctx, []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)}}})
			},
			want: "injected t1",
		},
		{
			name:   "bulk counters",
			inject: trigger("t1", `BEFORE UPDATE ON meta`),
			op: func(g *SQLite) error {
				return g.ReplaceAll(ctx, []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)}}})
			},
			want: "setting graph counter",
		},
		{
			name:   "bulk external node insert",
			inject: trigger("t1", `BEFORE INSERT ON graph_nodes`),
			op: func(g *SQLite) error {
				return g.ReplaceAll(ctx, []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)},
					Edges: []Edge{{Src: "c.New", Dst: "os.Exit", DstExternal: true}}}})
			},
			want: "inserting external nodes",
		},
		{
			name:   "bulk edge insert",
			inject: trigger("t1", `BEFORE INSERT ON graph_edges`),
			op: func(g *SQLite) error {
				return g.ReplaceAll(ctx, []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1), mkSym("c.Old", "c.go", 2)},
					Edges: []Edge{{Src: "c.New", Dst: "c.Old"}}}})
			},
			want: "inserting edges",
		},
		{
			name:   "bulk files row",
			inject: trigger("t1", `BEFORE INSERT ON files`),
			op: func(g *SQLite) error {
				return g.ReplaceAll(ctx, []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1)}}})
			},
			want: `writing files row "c.go"`,
		},
		{
			name:   "bulk new label",
			inject: trigger("t1", `BEFORE INSERT ON graph_labels`),
			op: func(g *SQLite) error {
				return g.ReplaceAll(ctx, []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.New", "c.go", 1), mkSym("c.Old", "c.go", 2)},
					Edges: []Edge{{Src: "c.New", Dst: "c.Old", Confidence: "guessed"}}}})
			},
			want: `interning label "guessed"`,
		},
		{
			name:   "document node insert",
			inject: trigger("t1", `BEFORE INSERT ON doc_nodes`),
			op: func(g *SQLite) error {
				return g.ApplyDocDelta(ctx, docDelta("docs/guide.md"))
			},
			want: "inserting document node",
		},
		{
			name:   "document explains insert",
			inject: trigger("t1", `BEFORE INSERT ON doc_explains`),
			op: func(g *SQLite) error {
				return g.ApplyDocDelta(ctx, docDelta("docs/guide.md"))
			},
			want: "inserting document explains",
		},
		{
			name:   "document explains delete",
			inject: trigger("t1", `BEFORE DELETE ON doc_explains`),
			op: func(g *SQLite) error {
				return g.RemoveDoc(ctx, "docs/guide.md")
			},
			want: "deleting document explains",
		},
		{
			name:   "document node delete",
			inject: trigger("t1", `BEFORE DELETE ON doc_nodes`),
			op: func(g *SQLite) error {
				return g.RemoveDoc(ctx, "docs/guide.md")
			},
			want: "deleting document nodes",
		},
		{
			name:   "document rebuild explains delete",
			inject: trigger("t1", `BEFORE DELETE ON doc_explains`),
			op: func(g *SQLite) error {
				return g.ReplaceDocs(ctx, nil)
			},
			want: "deleting explains",
		},
		{
			name:   "document rebuild node delete",
			inject: trigger("t1", `BEFORE DELETE ON doc_nodes`),
			op: func(g *SQLite) error {
				return g.ReplaceDocs(ctx, nil)
			},
			want: "deleting nodes",
		},
		{
			name:   "document rebuild insert",
			inject: trigger("t1", `BEFORE INSERT ON doc_nodes`),
			op: func(g *SQLite) error {
				return g.ReplaceDocs(ctx, []DocDelta{docDelta("docs/new.md")})
			},
			want: "replacing document graph",
		},
		{
			name:   "mark dirty outside a delta",
			inject: trigger("t1", `BEFORE UPDATE OF graph_dirty ON files`),
			op: func(g *SQLite) error {
				return g.MarkFileDirty(ctx, "a.go")
			},
			want: `marking "a.go" graph_dirty`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, s := openTestGraph(t)
			seedFaultGraph(t, g)
			if _, err := s.WriterDB().ExecContext(ctx, tc.inject); err != nil {
				t.Fatalf("injecting fault: %v", err)
			}
			before := snapshot(t, g)
			err := tc.op(g)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
			if after := snapshot(t, g); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed write changed the graph:\nbefore %+v\nafter  %+v", before, after)
			}
		})
	}
}

// TestRetractSourceTxDocumentFailure: when the document half of a source
// retraction fails, the caller's transaction is left to roll back and the
// code half is not committed either.
func TestRetractSourceTxDocumentFailure(t *testing.T) {
	ctx := context.Background()
	g, s := openTestGraph(t)
	seedFaultGraph(t, g)
	before := snapshot(t, g)
	if _, err := s.WriterDB().ExecContext(ctx, `CREATE TRIGGER t1 BEFORE DELETE ON doc_nodes BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = g.RetractSourceTx(ctx, tx, "docs/guide.md")
	if err == nil || !strings.Contains(err.Error(), "removing document graph rows") {
		tx.Rollback()
		t.Fatalf("RetractSourceTx error = %v, want the document retraction failure", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, g); !reflect.DeepEqual(before, after) {
		t.Fatalf("rolled-back retraction changed the graph:\nbefore %+v\nafter  %+v", before, after)
	}

	// The code half failing stops before the document half.
	if _, err := s.WriterDB().ExecContext(ctx, `DROP TRIGGER t1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriterDB().ExecContext(ctx, `CREATE TRIGGER t2 BEFORE DELETE ON symbols BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err = s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := g.RetractSourceTx(ctx, tx, "a.go"); err == nil || !strings.Contains(err.Error(), "deleting symbols") {
		t.Fatalf("RetractSourceTx error = %v, want the symbol delete failure", err)
	}
}

// TestDocDeltaValidation: a node without an ID or a relation missing either
// end refuses the whole delta.
func TestDocDeltaValidation(t *testing.T) {
	ctx := context.Background()
	g, _ := openTestGraph(t)
	seedFaultGraph(t, g)
	before := snapshot(t, g)

	noID := docDelta("docs/guide.md")
	noID.Nodes = append(noID.Nodes, DocNode{Type: "chunk"})
	if err := g.ApplyDocDelta(ctx, noID); err == nil || !strings.Contains(err.Error(), "empty ID") {
		t.Fatalf("node without ID: error = %v", err)
	}
	noFQN := docDelta("docs/guide.md")
	noFQN.Explains = append(noFQN.Explains, DocExplains{ChunkID: "c1:docs/guide.md"})
	if err := g.ApplyDocDelta(ctx, noFQN); err == nil || !strings.Contains(err.Error(), "empty chunk ID or FQN") {
		t.Fatalf("relation without FQN: error = %v", err)
	}
	if err := g.ReplaceDocs(ctx, []DocDelta{noID}); err == nil || !strings.Contains(err.Error(), "empty ID") {
		t.Fatalf("ReplaceDocs with a node without ID: error = %v", err)
	}
	if after := snapshot(t, g); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused document deltas changed the graph:\nbefore %+v\nafter  %+v", before, after)
	}
}

// TestFileDeltaValidation covers the delta shapes ApplyFileDelta refuses
// before writing anything.
func TestFileDeltaValidation(t *testing.T) {
	ctx := context.Background()
	g, _ := openTestGraph(t)
	seedFaultGraph(t, g)
	before := snapshot(t, g)
	tests := []struct {
		name string
		d    FileDelta
		want string
	}{
		{"empty path", FileDelta{}, "empty file path"},
		{"empty FQN", FileDelta{File: "c.go", Symbols: []Symbol{{Name: "x"}}}, "empty FQN"},
		{"declared twice", FileDelta{File: "c.go", Symbols: []Symbol{mkSym("c.X", "c.go", 1), mkSym("c.X", "c.go", 2)}}, "declared twice"},
		{"anchored elsewhere", FileDelta{File: "c.go", Symbols: []Symbol{mkSym("c.X", "d.go", 1)}}, `anchored in "d.go"`},
		{"declared in another file", FileDelta{File: "c.go", Symbols: []Symbol{mkSym("a.One", "c.go", 1)}}, `already declared in "a.go"`},
	}
	for _, tc := range tests {
		if err := g.ApplyFileDelta(ctx, tc.d); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
	if after := snapshot(t, g); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused deltas changed the graph:\nbefore %+v\nafter  %+v", before, after)
	}
}

// TestWritesWithCanceledContext: a write whose context is already canceled
// fails at BEGIN and changes nothing.
func TestWritesWithCanceledContext(t *testing.T) {
	g, _ := openTestGraph(t)
	seedFaultGraph(t, g)
	before := snapshot(t, g)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writes := map[string]func() error{
		"ApplyFileDelta": func() error {
			return g.ApplyFileDelta(ctx, FileDelta{File: "c.go", Symbols: []Symbol{mkSym("c.X", "c.go", 1)}})
		},
		"RemoveFile":    func() error { return g.RemoveFile(ctx, "a.go") },
		"ReplaceAll":    func() error { return g.ReplaceAll(ctx, nil) },
		"ReplaceDocs":   func() error { return g.ReplaceDocs(ctx, nil) },
		"ApplyDocDelta": func() error { return g.ApplyDocDelta(ctx, docDelta("docs/x.md")) },
		"RemoveDoc":     func() error { return g.RemoveDoc(ctx, "docs/guide.md") },
		"MarkFileDirty": func() error { return g.MarkFileDirty(ctx, "a.go") },
	}
	for name, w := range writes {
		if err := w(); err == nil || !strings.Contains(err.Error(), "beginning transaction") {
			t.Errorf("%s with a canceled context: error = %v, want a begin failure", name, err)
		}
	}
	if after := snapshot(t, g); !reflect.DeepEqual(before, after) {
		t.Fatalf("canceled writes changed the graph:\nbefore %+v\nafter  %+v", before, after)
	}
}

// TestReadsOnClosedStore: every read reports a wrapped error, never a panic
// or an empty success, once the store's pools are closed (a server shutting
// down under a query).
func TestReadsOnClosedStore(t *testing.T) {
	g, s := openTestGraph(t)
	seedFaultGraph(t, g)
	// Warm the statement cache so the cached-statement path is the one
	// that fails.
	if _, err := g.Callers("a.One", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reads := map[string]func() error{
		"Symbols(empty)":   func() error { _, err := g.Symbols(""); return err },
		"Symbols(short)":   func() error { _, err := g.Symbols("ab"); return err },
		"Symbols(trigram)": func() error { _, err := g.Symbols("One"); return err },
		"ResolveSymbols":   func() error { _, err := g.ResolveSymbols("a.One", DirectionCallers); return err },
		"Callers":          func() error { _, err := g.Callers("a.One", 2); return err },
		"Callees":          func() error { _, err := g.Callees("b.Caller", 2); return err },
		"Impact":           func() error { _, err := g.Impact("a.One", 2); return err },
		"Path":             func() error { _, err := g.Path("b.Caller", "a.One"); return err },
		"Stats":            func() error { _, err := g.Stats(); return err },
		"InDegreeTop":      func() error { _, err := g.InDegreeTop(5); return err },
		"InDegreeTopExcl":  func() error { _, err := g.InDegreeTopExcluding(5, "macro"); return err },
		"DirtyFiles":       func() error { _, err := g.DirtyFiles(); return err },
		"SymbolsInFile":    func() error { _, err := g.SymbolsInFile("a.go"); return err },
		"Neighbors":        func() error { _, err := g.Neighbors("a.One", 1); return err },
		"AllSymbols":       func() error { _, err := g.AllSymbols(); return err },
		"AllEdges":         func() error { _, err := g.AllEdges(); return err },
		"DeclaredFQNs":     func() error { _, err := g.DeclaredFQNs([]string{"a.One"}); return err },
		"HasData":          func() error { _, err := g.HasData(); return err },
		"CodeFileCount":    func() error { _, err := g.CodeFileCount(); return err },
		"PackageMembers":   func() error { _, err := g.PackageMembers("a"); return err },
		"FilesWithExt":     func() error { _, err := g.FilesWithExternalCalls([]string{"fmt.Println"}); return err },
		"DocumentContext":  func() error { _, err := g.DocumentContext("docs/guide.md"); return err },
		"DocumentTOC":      func() error { _, err := g.DocumentTOC("docs/guide.md"); return err },
		"ListDocuments":    func() error { _, err := g.ListDocuments(); return err },
		"FullDocument":     func() error { _, err := g.FullDocument("docs/guide.md"); return err },
		"DocumentSymbols":  func() error { _, err := g.DocumentSymbols("docs/guide.md"); return err },
	}
	for name, r := range reads {
		if err := r(); err == nil {
			t.Errorf("%s on a closed store: nil error", name)
		}
	}
}

// TestCorruptRowsAreErrors: a row whose column no longer has the declared
// type (a damaged or hand-edited database; SQLite's type affinity lets the
// write through) makes the readers fail with an error instead of returning a
// zero-valued symbol or a half-filled result.
func TestCorruptRowsAreErrors(t *testing.T) {
	ctx := context.Background()
	g, s := openTestGraph(t)
	seedFaultGraph(t, g)
	mustApply(t, g, FileDelta{File: "b.go", Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
		Edges: []Edge{{Src: "b.Caller", Dst: "a.One"}, {Src: "b.Caller", Dst: "a.Two"}}})
	if _, err := s.WriterDB().ExecContext(ctx, `UPDATE symbols SET start_line = 'not a line' WHERE fqn = 'a.One'`); err != nil {
		t.Fatal(err)
	}
	reads := map[string]func() error{
		"Symbols":         func() error { _, err := g.Symbols(""); return err },
		"SymbolsInFile":   func() error { _, err := g.SymbolsInFile("a.go"); return err },
		"AllSymbols":      func() error { _, err := g.AllSymbols(); return err },
		"PackageMembers":  func() error { _, err := g.PackageMembers(""); return err },
		"Callees":         func() error { _, err := g.Callees("b.Caller", 1); return err },
		"DocumentSymbols": func() error { _, err := g.DocumentSymbols("docs/guide.md"); return err },
	}
	// PackageMembers selects only "Type::member" names: make a.One one.
	if _, err := s.WriterDB().ExecContext(ctx, `UPDATE symbols SET name = 'T::One' WHERE fqn = 'a.One'`); err != nil {
		t.Fatal(err)
	}
	for name, r := range reads {
		if err := r(); err == nil {
			t.Errorf("%s over a corrupt row: nil error", name)
		}
	}

	// A counter that is not a number is reported, not read as zero.
	if _, err := s.WriterDB().ExecContext(ctx, `UPDATE meta SET value = 'many' WHERE key = ?`, store.MetaGraphSymbols); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Stats(); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("counter %s", store.MetaGraphSymbols)) {
		t.Fatalf("Stats with a non-numeric counter: error = %v", err)
	}
	// A missing counter means an unmigrated schema.
	if _, err := s.WriterDB().ExecContext(ctx, `DELETE FROM meta WHERE key = ?`, store.MetaGraphSymbols); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Stats(); err == nil || !strings.Contains(err.Error(), "schema not migrated") {
		t.Fatalf("Stats with a missing counter: error = %v", err)
	}
}
