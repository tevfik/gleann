package graphstore

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

var updatePoisonGolden = flag.Bool("update-poison", false, "rewrite testdata/poisoned_reads.golden")

// poisonColumn replaces a table by a view over it in which one column is
// computed by expr, so every statement that reads that column sees what a
// damaged database would hand back: an error mid-scan (expr overflows) or a
// NULL where the code expects a value. The view is read-only, which is
// enough: the poisoned statements are all reads, including the reads a write
// makes before it changes anything.
func poisonColumn(t *testing.T, s *store.Store, table, col, kind string) {
	t.Helper()
	expr := map[string]string{"error": `abs(-9223372036854775808)`, "null": `NULL`}[kind]
	w := s.WriterDB()
	must := func(q string) {
		t.Helper()
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Keep triggers and views that name the table as they are; the writer is
	// a single connection, so the pragma holds for the statements below.
	must(`PRAGMA legacy_alter_table = ON`)
	must(`ALTER TABLE ` + table + ` RENAME TO ` + table + `_real`)
	rows, err := w.Query(`SELECT name FROM pragma_table_info('` + table + `_real')`)
	if err != nil {
		t.Fatal(err)
	}
	var cols []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == col {
			n = expr + ` AS ` + n
		}
		cols = append(cols, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	must(`CREATE VIEW ` + table + ` AS SELECT ` + strings.Join(cols, ", ") + ` FROM ` + table + `_real`)
}

// Each reader names the failing step when a column it reads comes back
// broken, and keeps working when the broken column is one it never reads.
// The golden file lists, per reader, the poisoned columns it fails on: it
// records which columns each query depends on, so a reader that starts
// ignoring a failure (or reading a new column) shows up as a diff. Rewrite it
// with `go test ./internal/graphstore -run PoisonedColumns -update-poison`.
func TestReadersAndWritesOverPoisonedColumns(t *testing.T) {
	ctx := context.Background()
	columns := map[string][]string{
		"symbols":      {"id", "fqn", "name", "kind", "file", "start_line", "end_line", "start_col", "byte_offset", "is_test", "weight", "doc"},
		"files":        {"path", "graph_hash", "graph_dirty"},
		"graph_labels": {"id", "label"},
		"graph_nodes":  {"id", "fqn"},
		"graph_edges":  {"src", "dst", "kind", "dst_external", "confidence"},
		"meta":         {"key", "value"},
		"doc_nodes":    {"id", "type", "parent", "name", "level", "rpath", "summary", "text", "start_char"},
		"doc_explains": {"chunk_id", "fqn"},
	}
	reads := map[string]func(g *SQLite) error{
		"Symbols":     func(g *SQLite) error { _, err := g.Symbols(""); return err },
		"SymbolsFTS":  func(g *SQLite) error { _, err := g.Symbols("One"); return err },
		"SymbolsLike": func(g *SQLite) error { _, err := g.Symbols("On"); return err },
		"ResolveCaller": func(g *SQLite) error {
			_, err := g.ResolveSymbols("a.One", DirectionCallers)
			return err
		},
		"ResolveCallee": func(g *SQLite) error {
			_, err := g.ResolveSymbols("b.Caller", DirectionCallees)
			return err
		},
		"ResolveSuffix": func(g *SQLite) error {
			_, err := g.ResolveSymbols("One", DirectionCallers)
			return err
		},
		"Callers":    func(g *SQLite) error { _, err := g.Callers("a.One", 2); return err },
		"Callees":    func(g *SQLite) error { _, err := g.Callees("b.Caller", 2); return err },
		"Impact":     func(g *SQLite) error { _, err := g.Impact("a.One", 2); return err },
		"Path":       func(g *SQLite) error { _, err := g.Path("b.Caller", "a.One"); return err },
		"PathToExt":  func(g *SQLite) error { _, err := g.Path("b.Caller", "fmt.Println"); return err },
		"Stats":      func(g *SQLite) error { _, err := g.Stats(); return err },
		"InDegree":   func(g *SQLite) error { _, err := g.InDegreeTop(5); return err },
		"InDegreeEx": func(g *SQLite) error { _, err := g.InDegreeTopExcluding(5, "macro"); return err },
		"Dirty":      func(g *SQLite) error { _, err := g.DirtyFiles(); return err },
		"SymsInFile": func(g *SQLite) error { _, err := g.SymbolsInFile("a.go"); return err },
		"Neighbors":  func(g *SQLite) error { _, err := g.Neighbors("a.One", 1); return err },
		"AllSymbols": func(g *SQLite) error { _, err := g.AllSymbols(); return err },
		"AllEdges":   func(g *SQLite) error { _, err := g.AllEdges(); return err },
		"Declared":   func(g *SQLite) error { _, err := g.DeclaredFQNs([]string{"a.One"}); return err },
		"FilesExt": func(g *SQLite) error {
			_, err := g.FilesWithExternalCalls([]string{"fmt.Println"})
			return err
		},
		"HasData":   func(g *SQLite) error { _, err := g.HasData(); return err },
		"CodeFiles": func(g *SQLite) error { _, err := g.CodeFileCount(); return err },
		"Members":   func(g *SQLite) error { _, err := g.PackageMembers("a"); return err },
		"DocContext": func(g *SQLite) error {
			_, err := g.DocumentContext("docs/guide.md")
			return err
		},
		"DocTOC":     func(g *SQLite) error { _, err := g.DocumentTOC("docs/guide.md"); return err },
		"DocList":    func(g *SQLite) error { _, err := g.ListDocuments(); return err },
		"DocFull":    func(g *SQLite) error { _, err := g.FullDocument("docs/guide.md"); return err },
		"DocSymbols": func(g *SQLite) error { _, err := g.DocumentSymbols("docs/guide.md"); return err },
	}
	// A write reads before it changes anything (the file's counts, its
	// inbound edges, its old symbols); those reads see the poison too.
	// Each write runs on a store of its own because it commits.
	writes := map[string]func(g *SQLite) error{
		"ApplyFileDelta a.go": func(g *SQLite) error {
			return g.ApplyFileDelta(ctx, FileDelta{File: "a.go", GraphHash: "x", Symbols: []Symbol{mkSym("a.One", "a.go", 2)}})
		},
		"RemoveFile a.go": func(g *SQLite) error { return g.RemoveFile(ctx, "a.go") },
		"RemoveFile b.go": func(g *SQLite) error { return g.RemoveFile(ctx, "b.go") },
	}
	newGraph := func(t *testing.T) (*SQLite, *store.Store) {
		g, s := openTestGraph(t)
		seedFaultGraph(t, g)
		mustApply(t, g, FileDelta{File: "b.go", GraphHash: "b-v1", Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
			Edges: []Edge{{Src: "b.Caller", Dst: "a.One"}, {Src: "b.Caller", Dst: "a.Two"}, {Src: "b.Caller", Dst: "fmt.Println", DstExternal: true}}})
		return g, s
	}

	var mu sync.Mutex
	failures := map[string][]string{} // reader -> poisons it failed on
	record := func(name, poison string) {
		mu.Lock()
		defer mu.Unlock()
		failures[name] = append(failures[name], poison)
	}
	t.Run("sweep", func(t *testing.T) {
		var tables []string
		for tb := range columns {
			tables = append(tables, tb)
		}
		sort.Strings(tables)
		for _, tb := range tables {
			for _, col := range columns[tb] {
				for _, kind := range []string{"error", "null"} {
					poison := tb + "." + col + "/" + kind
					t.Run(poison, func(t *testing.T) {
						t.Parallel()
						g, s := newGraph(t)
						poisonColumn(t, s, tb, col, kind)
						for name, read := range reads {
							if err := read(g); err != nil {
								record(name, poison)
							}
						}
						for name, write := range writes {
							g, s := newGraph(t)
							poisonColumn(t, s, tb, col, kind)
							if err := write(g); err != nil {
								record(name, poison)
							}
						}
					})
				}
			}
		}
	})

	var names []string
	for n := range failures {
		names = append(names, n)
	}
	for n := range reads {
		if _, ok := failures[n]; !ok {
			names = append(names, n)
		}
	}
	for n := range writes {
		if _, ok := failures[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		p := failures[n]
		sort.Strings(p)
		fmt.Fprintf(&b, "%s: %s\n", n, strings.Join(p, " "))
	}
	golden := filepath.Join("testdata", "poisoned_reads.golden")
	if *updatePoisonGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != b.String() {
		t.Fatalf("the columns the readers fail on changed; rerun with -update-poison and review the diff\n--- got\n%s", b.String())
	}
}
