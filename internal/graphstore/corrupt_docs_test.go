package graphstore

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// The document readers report a damaged document table (a dropped table, a
// renamed column, a value of the wrong type) from whichever step reads it,
// and keep working when the damage is in a part they never read.
func TestDocumentReadersReportDamagedTables(t *testing.T) {
	const path = "docs/guide.md"
	reads := map[string]func(g *SQLite) error{
		"Context": func(g *SQLite) error { _, err := g.DocumentContext(path); return err },
		"TOC":     func(g *SQLite) error { _, err := g.DocumentTOC(path); return err },
		"List":    func(g *SQLite) error { _, err := g.ListDocuments(); return err },
		"Full":    func(g *SQLite) error { _, err := g.FullDocument(path); return err },
		"Symbols": func(g *SQLite) error { _, err := g.DocumentSymbols(path); return err },
	}
	cases := []struct {
		damage string
		failed string // space separated reads that must fail; every other read must succeed
	}{
		{`DROP TABLE doc_nodes`, "Context TOC List Full Symbols"},
		{`DROP TABLE doc_explains`, "Symbols"},
		{`ALTER TABLE doc_nodes RENAME COLUMN name TO zz`, "Context TOC List Full Symbols"},
		{`ALTER TABLE doc_nodes RENAME COLUMN level TO zz`, "Context TOC Full Symbols"},
		{`ALTER TABLE doc_nodes RENAME COLUMN start_char TO zz`, "Context TOC Full Symbols"},
		{`ALTER TABLE doc_nodes RENAME COLUMN text TO zz`, "Context TOC Full Symbols"},
		{`UPDATE doc_nodes SET level = x'00' WHERE type = 'heading'`, "Context TOC"},
		{`UPDATE doc_nodes SET level = 'x'`, "Context TOC Full Symbols"},
		{`UPDATE doc_nodes SET text = NULL`, "Full"},
	}
	for _, tc := range cases {
		t.Run(tc.damage, func(t *testing.T) {
			g, s := openTestGraph(t)
			seedFaultGraph(t, g)
			if err := g.ApplyDocDelta(context.Background(), docDelta("docs/two.md")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.WriterDB().Exec(tc.damage); err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{}
			for _, n := range strings.Fields(tc.failed) {
				want[n] = true
			}
			var names []string
			for n := range reads {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				err := reads[n](g)
				switch {
				case want[n] && err == nil:
					t.Errorf("%s read over %q without an error", n, tc.damage)
				case !want[n] && err != nil:
					t.Errorf("%s does not read the damaged part but failed: %v", n, err)
				}
			}
		})
	}
}
