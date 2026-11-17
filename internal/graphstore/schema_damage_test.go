package graphstore

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// damage is a schema breakage a half-finished manual repair or a foreign tool
// could leave behind: a dropped table, a renamed column, a trigger that
// aborts one kind of write on a table, or a row of the wrong type.
type damage struct {
	sql   string
	table string
}

func graphDamages() []damage {
	cols := map[string][]string{
		"symbols":      {"id", "fqn", "name", "kind", "file", "start_line", "end_line", "start_col", "byte_offset", "is_test", "weight", "doc"},
		"files":        {"path", "hash", "graph_hash", "graph_dirty"},
		"graph_labels": {"id", "label"},
		"graph_nodes":  {"id", "fqn"},
		"graph_edges":  {"src", "dst", "kind", "dst_external", "confidence"},
		"meta":         {"key", "value"},
	}
	var tables []string
	for tb := range cols {
		tables = append(tables, tb)
	}
	sort.Strings(tables)
	var out []damage
	for _, tb := range tables {
		out = append(out, damage{`DROP TABLE ` + tb, tb})
		for _, c := range cols[tb] {
			out = append(out, damage{`ALTER TABLE ` + tb + ` RENAME COLUMN ` + c + ` TO zz`, tb})
		}
	}
	out = append(out, damage{`DROP TABLE symbols_fts`, "symbols_fts"})
	for _, tb := range []string{"symbols", "files", "graph_labels", "graph_nodes", "graph_edges"} {
		for _, when := range []string{"INSERT", "DELETE", "UPDATE"} {
			out = append(out, damage{faultTrigger(when, tb), tb})
		}
	}
	out = append(out,
		damage{faultTrigger("UPDATE", "meta"), "meta"},
		// Rows holding a value of the wrong type (SQLite's flexible typing
		// lets the write through).
		damage{`UPDATE graph_edges SET dst_external = 'x'`, "graph_edges"},
		damage{`UPDATE graph_edges SET confidence = 'x'`, "graph_edges"},
	)
	return out
}

func faultTrigger(when, table string) string {
	return `CREATE TRIGGER fault BEFORE ` + when + ` ON ` + table + ` BEGIN SELECT RAISE(ABORT, 'injected fault'); END`
}

// harmless reports whether op never executes the statement the damage breaks,
// so the write rightly succeeds. Everything else must fail.
func harmless(op string, d damage) bool {
	trigger := func(when, table string) bool { return d.sql == faultTrigger(when, table) }
	// No graph write updates these tables, deletes manifest rows, or does
	// more with the label table than read the three seeded labels.
	if trigger("UPDATE", "symbols") || trigger("DELETE", "files") || trigger("UPDATE", "graph_nodes") ||
		trigger("UPDATE", "graph_edges") || trigger("INSERT", "graph_labels") || trigger("DELETE", "graph_labels") ||
		trigger("UPDATE", "graph_labels") {
		return true
	}
	if strings.HasPrefix(d.sql, "UPDATE graph_edges SET") {
		// A wrong-typed edge row is only read back when the inbound edges of
		// a rewritten or removed file are restored.
		return !strings.HasSuffix(op, "a.go")
	}
	switch op {
	case "MarkFileDirty":
		return d.table != "files" // only the manifest row is touched
	case "ApplyFileDelta b.go":
		return trigger("DELETE", "graph_nodes") // b.go has no external target to collect
	case "RemoveFile a.go", "RemoveFile b.go":
		// Removal never inserts and never reads the display columns.
		for _, c := range []string{"name", "kind", "start_line", "end_line", "start_col", "byte_offset", "is_test", "weight", "doc"} {
			if d.sql == `ALTER TABLE symbols RENAME COLUMN `+c+` TO zz` {
				return true
			}
		}
		if d.sql == `ALTER TABLE graph_nodes RENAME COLUMN fqn TO zz` ||
			trigger("INSERT", "symbols") || trigger("INSERT", "graph_nodes") || trigger("INSERT", "graph_edges") {
			return true
		}
		if op == "RemoveFile b.go" {
			return trigger("DELETE", "graph_nodes") || // b.go has no external target to collect
				d.sql == `ALTER TABLE files RENAME COLUMN hash TO zz` || trigger("INSERT", "files")
		}
	}
	return false
}

// Every graph write reports a damaged schema from the statement that hits it,
// instead of committing a partial graph.
func TestWritesReportEachDamagedSchemaStep(t *testing.T) {
	ctx := context.Background()
	ops := []struct {
		name string
		run  func(g *SQLite) error
	}{
		{"ApplyFileDelta a.go", func(g *SQLite) error {
			return g.ApplyFileDelta(ctx, FileDelta{File: "a.go", GraphHash: "a-v2",
				Symbols: []Symbol{mkSym("a.One", "a.go", 2), mkSym("a.Three", "a.go", 20)},
				Edges:   []Edge{{Src: "a.Three", Dst: "a.One"}, {Src: "a.Three", Dst: "os.Exit", DstExternal: true}}})
		}},
		{"ApplyFileDelta b.go", func(g *SQLite) error {
			return g.ApplyFileDelta(ctx, FileDelta{File: "b.go", GraphHash: "b-v2",
				Symbols: []Symbol{mkSym("b.Caller", "b.go", 2)},
				Edges:   []Edge{{Src: "b.Caller", Dst: "a.One"}, {Src: "b.Caller", Dst: "ext.New", DstExternal: true}}})
		}},
		{"RemoveFile a.go", func(g *SQLite) error { return g.RemoveFile(ctx, "a.go") }},
		{"RemoveFile b.go", func(g *SQLite) error { return g.RemoveFile(ctx, "b.go") }},
		{"MarkFileDirty", func(g *SQLite) error { return g.MarkFileDirty(ctx, "a.go") }},
		{"ReplaceAll", func(g *SQLite) error {
			return g.ReplaceAll(ctx, []FileDelta{{File: "z.go", GraphHash: "z", Symbols: []Symbol{mkSym("z.Z", "z.go", 1)}, Edges: []Edge{{Src: "z.Z", Dst: "fmt.Println", DstExternal: true}}}})
		}},
	}
	for _, op := range ops {
		for _, d := range graphDamages() {
			t.Run(op.name+"/"+d.sql, func(t *testing.T) {
				t.Parallel()
				g, s := openTestGraph(t)
				seedFaultGraph(t, g)
				if _, err := s.WriterDB().Exec(d.sql); err != nil {
					t.Fatalf("applying the damage: %v", err)
				}
				err := op.run(g)
				switch {
				case harmless(op.name, d):
					if err != nil {
						t.Fatalf("%s does not run the broken statement, got %v", op.name, err)
					}
				case err == nil:
					t.Fatalf("%s succeeded over %q", op.name, d.sql)
				case strings.TrimSpace(err.Error()) == "":
					t.Fatal("empty error")
				}
			})
		}
	}
}

// docDamages break the document tables the document writes use.
var docDamages = []string{
	`DROP TABLE doc_nodes`,
	`DROP TABLE doc_explains`,
	`ALTER TABLE doc_nodes RENAME COLUMN rpath TO rp`,
	`ALTER TABLE doc_nodes RENAME COLUMN summary TO sm`,
	`ALTER TABLE doc_explains RENAME COLUMN fqn TO q`,
	`ALTER TABLE doc_explains RENAME COLUMN chunk_id TO c`,
}

// unaffectedDocs lists the document operation/damage pairs whose statements
// never touch the damaged object.
var unaffectedDocs = map[string]bool{
	"RemoveDoc|ALTER TABLE doc_nodes RENAME COLUMN summary TO sm": true, // removal never reads the summary
	"RemoveDoc|ALTER TABLE doc_explains RENAME COLUMN fqn TO q":   true, // removal deletes explains by chunk
}

func TestDocumentWritesReportEachDamagedSchemaStep(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(g *SQLite) error{
		"ApplyDocDelta": func(g *SQLite) error {
			d := docDelta("docs/new.md")
			d.Explains = []DocExplains{{ChunkID: "c2:docs/new.md", FQN: "a.One"}}
			return g.ApplyDocDelta(ctx, d)
		},
		"RemoveDoc": func(g *SQLite) error { return g.RemoveDoc(ctx, "docs/guide.md") },
	}
	for name, op := range ops {
		for _, damage := range docDamages {
			t.Run(name+"/"+damage, func(t *testing.T) {
				g, s := openTestGraph(t)
				seedFaultGraph(t, g)
				if _, err := s.WriterDB().Exec(damage); err != nil {
					t.Fatalf("applying the damage: %v", err)
				}
				err := op(g)
				switch {
				case unaffectedDocs[name+"|"+damage]:
					if err != nil {
						t.Fatalf("this damage does not touch %s, got %v", name, err)
					}
				case err == nil:
					t.Fatalf("%s succeeded over %q", name, damage)
				}
			})
		}
	}
}
