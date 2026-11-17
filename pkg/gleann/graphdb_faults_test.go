package gleann

import (
	"strings"
	"testing"
)

// Every GraphDB method reports a store that has gone away instead of returning
// an empty answer a caller would take for "no such symbol".
func TestGraphDBReportsAClosedStore(t *testing.T) {
	st := openGraphStore(t)
	seedGraphFixture(t, st)
	seedDocFixture(t, st)
	db := newSQLiteGraphDB(st)
	if _, err := db.Callees("mod/pkg.A"); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	st.Close()

	calls := map[string]func() error{
		"Callees":         func() error { _, err := db.Callees("mod/pkg.A"); return err },
		"Callers":         func() error { _, err := db.Callers("mod/pkg.B"); return err },
		"SymbolsInFile":   func() error { _, err := db.SymbolsInFile("pkg/file1.go"); return err },
		"DocumentSymbols": func() error { _, err := db.DocumentSymbols("docs/arch.md"); return err },
		"DocumentContext": func() error { _, err := db.DocumentContext("docs/arch.md"); return err },
		"DocumentTOC":     func() error { _, err := db.DocumentTOC("docs/arch.md"); return err },
		"ListDocuments":   func() error { _, err := db.ListDocuments(); return err },
		"FullDocument":    func() error { _, err := db.FullDocument("docs/arch.md"); return err },
		"Impact":          func() error { _, err := db.Impact("mod/pkg.B", 3); return err },
		"Neighbors":       func() error { _, err := db.Neighbors("mod/pkg.B", 2); return err },
		"ShortestPath":    func() error { _, err := db.ShortestPath("mod/pkg.A", "mod/pkg.C"); return err },
		"SymbolSearch":    func() error { _, err := db.SymbolSearch("pkg"); return err },
		"Stats":           func() error { _, err := db.Stats(); return err },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s on a closed store returned no error", name)
		}
	}
}

// A document that is not in the graph is a not-found error; a
// heading that has sub-headings nests them in the table of contents.
func TestGraphDBDocumentsMissingAndNested(t *testing.T) {
	st := openGraphStore(t)
	seedDocFixture(t, st)
	if _, err := st.WriterDB().Exec(`INSERT INTO doc_nodes (id, type, parent, name, level) VALUES ('h1a', 'heading', 'h1', 'Goals', 2)`); err != nil {
		t.Fatal(err)
	}
	db := newSQLiteGraphDB(st)

	if c, err := db.DocumentContext("no/such.md"); err == nil || c != nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("DocumentContext of an unknown document = %+v, %v; want a not-found error", c, err)
	}
	if toc, err := db.DocumentTOC("no/such.md"); err == nil || toc != nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("DocumentTOC of an unknown document = %+v, %v; want a not-found error", toc, err)
	}
	toc, err := db.DocumentTOC("docs/arch.md")
	if err != nil || toc == nil {
		t.Fatalf("DocumentTOC: %+v, %v", toc, err)
	}
	var intro *DocumentHeadingItem
	for i := range toc.Headings {
		if toc.Headings[i].Title == "Introduction" {
			intro = &toc.Headings[i]
		}
	}
	if intro == nil || len(intro.Children) != 1 || intro.Children[0].Title != "Goals" {
		t.Fatalf("headings = %+v, want Goals nested under Introduction", toc.Headings)
	}
}

// Impact normalises a depth outside 1..10 to the default of 5.
func TestGraphDBImpactNormalisesTheDepth(t *testing.T) {
	st := openGraphStore(t)
	seedGraphFixture(t, st)
	db := newSQLiteGraphDB(st)
	for _, depth := range []int{0, -3, 11} {
		res, err := db.Impact("mod/pkg.C", depth)
		if err != nil {
			t.Fatal(err)
		}
		if res.Depth != 5 {
			t.Errorf("Impact depth %d reported %d, want 5", depth, res.Depth)
		}
	}
}

// The graph openers report a graph database they cannot read, and the
// no-graph error names the build command.
func TestOpenGraphReportsUnreadableGraphTables(t *testing.T) {
	dir, base, _ := fxBuilt(t)
	if db, err := OpenGraphDB(dir, "idx"); err != nil || db != nil {
		t.Fatalf("an index without graph rows: %v, %v; want nil, nil", db, err)
	}
	if err := NoCodeGraphError(dir, "idx"); err == nil || !strings.Contains(err.Error(), "gleann index build idx --docs <dir> --graph") {
		t.Fatalf("NoCodeGraphError = %v", err)
	}
	fxExec(t, base, `DROP TABLE symbols`)
	if db, err := OpenGraphDB(dir, "idx"); err == nil || !strings.Contains(err.Error(), "checking graph data") {
		t.Errorf("OpenGraphDB = %v, %v; want the graph-data error", db, err)
	}
	if g, err := OpenGraphStore(dir, "idx"); err == nil || !strings.Contains(err.Error(), "checking graph data") {
		t.Errorf("OpenGraphStore = %v, %v; want the graph-data error", g, err)
	}
	if HasCodeGraph(dir, "idx") {
		t.Error("an unreadable graph is not a code graph")
	}
}
