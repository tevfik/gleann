package graphstore

import "testing"

// A row holding a value of the wrong type (SQLite's flexible typing lets a
// hand-edit or a foreign tool write one) must make the readers that scan it
// fail with an error instead of returning a zero value or a half-filled path.
func TestReadersReportCorruptValues(t *testing.T) {
	reads := map[string]func(g *SQLite) error{
		"Symbols":     func(g *SQLite) error { _, err := g.Symbols(""); return err },
		"Callers":     func(g *SQLite) error { _, err := g.Callers("a.One", 2); return err },
		"Callees":     func(g *SQLite) error { _, err := g.Callees("b.Caller", 2); return err },
		"Impact":      func(g *SQLite) error { _, err := g.Impact("a.One", 2); return err },
		"Path":        func(g *SQLite) error { _, err := g.Path("b.Caller", "a.One"); return err },
		"AllEdges":    func(g *SQLite) error { _, err := g.AllEdges(); return err },
		"DocSymbols":  func(g *SQLite) error { _, err := g.DocumentSymbols("docs/guide.md"); return err },
		"InDegreeTop": func(g *SQLite) error { _, err := g.InDegreeTop(5); return err },
	}
	cases := []struct {
		damage string
		failed []string // the reads that must report it
		clean  []string // reads that never scan the damaged column and must keep working
	}{
		{`UPDATE symbols SET is_test = 'x'`, []string{"Symbols", "Callers", "Callees", "Impact", "DocSymbols"}, []string{"AllEdges", "InDegreeTop"}},
		{`UPDATE symbols SET start_line = 'x'`, []string{"Symbols", "Callers", "Callees", "DocSymbols"}, []string{"Impact", "AllEdges"}},
		{`UPDATE symbols SET weight = 'x'`, []string{"Symbols", "DocSymbols"}, []string{"Callers", "Impact", "AllEdges"}},
		{`UPDATE graph_edges SET dst_external = 'x'`, []string{"Path", "AllEdges"}, []string{"Symbols", "Impact"}},
		{`UPDATE graph_edges SET src = 'x'`, []string{"Path"}, []string{"Symbols"}},
		{`UPDATE graph_edges SET kind = 'x'`, []string{"Path"}, []string{"Symbols"}},
	}
	for _, tc := range cases {
		t.Run(tc.damage, func(t *testing.T) {
			g, s := openTestGraph(t)
			seedFaultGraph(t, g)
			mustApply(t, g, FileDelta{File: "b.go", Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
				Edges: []Edge{{Src: "b.Caller", Dst: "a.One"}, {Src: "b.Caller", Dst: "a.Two"}}})
			if _, err := s.WriterDB().Exec(tc.damage); err != nil {
				t.Fatal(err)
			}
			for _, name := range tc.failed {
				if err := reads[name](g); err == nil {
					t.Errorf("%s read over %q without an error", name, tc.damage)
				}
			}
			for _, name := range tc.clean {
				if err := reads[name](g); err != nil {
					t.Errorf("%s does not read the damaged column but failed: %v", name, err)
				}
			}
		})
	}
}
