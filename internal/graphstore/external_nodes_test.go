package graphstore

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestExternalTargetSharingAndStaleClassification: external targets are
// interned once and shared by every file that calls them, garbage-collected
// with their last referrer, and an "external" edge to an FQN that is in fact
// declared binds to the declaration (the extractor's classification can be
// stale) so the caller shows up in the declaration's callers.
func TestExternalTargetSharingAndStaleClassification(t *testing.T) {
	ctx := context.Background()
	g, _ := openTestGraph(t)
	nodeRows := func() int {
		return countQuery(t, g, `SELECT count(*) FROM graph_nodes WHERE fqn = 'fmt.Println'`)
	}

	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.A", "a.go", 1)},
		Edges: []Edge{{Src: "a.A", Dst: "fmt.Println", DstExternal: true}}})
	mustApply(t, g, FileDelta{File: "b.go", Symbols: []Symbol{mkSym("b.B", "b.go", 1)},
		Edges: []Edge{{Src: "b.B", Dst: "fmt.Println", DstExternal: true}}})
	if n := nodeRows(); n != 1 {
		t.Fatalf("graph_nodes rows for fmt.Println = %d, want 1 shared row", n)
	}
	files, err := g.FilesWithExternalCalls([]string{"fmt.Println"})
	if err != nil || !reflect.DeepEqual(files, []string{"a.go", "b.go"}) {
		t.Fatalf("FilesWithExternalCalls = %v, %v", files, err)
	}

	// Removing one referrer keeps the node; removing the last drops it.
	if err := g.RemoveFile(ctx, "a.go"); err != nil {
		t.Fatal(err)
	}
	if n := nodeRows(); n != 1 {
		t.Fatalf("graph_nodes rows after removing one referrer = %d, want 1", n)
	}
	if err := g.RemoveFile(ctx, "b.go"); err != nil {
		t.Fatal(err)
	}
	if n := nodeRows(); n != 0 {
		t.Fatalf("graph_nodes rows after removing every referrer = %d, want 0", n)
	}

	// A stale external classification of a declared symbol.
	mustApply(t, g, FileDelta{File: "c.go", Symbols: []Symbol{mkSym("c.Decl", "c.go", 1)}})
	mustApply(t, g, FileDelta{File: "d.go", Symbols: []Symbol{mkSym("d.User", "d.go", 1)},
		Edges: []Edge{{Src: "d.User", Dst: "c.Decl", DstExternal: true}}})
	if n := countQuery(t, g, `SELECT count(*) FROM graph_nodes`); n != 0 {
		t.Fatalf("a declared FQN was interned as an external node (%d rows)", n)
	}
	callers, err := g.Callers("c.Decl", 1)
	if err != nil || len(callers) != 1 || callers[0].FQN != "d.User" {
		t.Fatalf("Callers(c.Decl) = %+v, %v; want d.User", callers, err)
	}
}

// TestPathFromExternalTarget: the backward CALLS arm has no external filter,
// so a path can start at an external target and reach the code calling it;
// the external node's name comes from graph_nodes.
func TestPathFromExternalTarget(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.A", "a.go", 1), mkSym("a.B", "a.go", 5)},
		Edges: []Edge{{Src: "a.A", Dst: "fmt.Println", DstExternal: true}, {Src: "a.A", Dst: "a.B"}}})

	steps, err := g.Path("fmt.Println", "a.B")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := []Step{
		{From: "fmt.Println", To: "a.A", Relation: "CALLED_BY"},
		{From: "a.A", To: "a.B", Relation: "CALLS"},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Path(fmt.Println, a.B) = %+v, want %+v", steps, want)
	}
	// External targets are never walked forward: nothing reaches them.
	steps, err = g.Path("a.B", "fmt.Println")
	if err == nil || !strings.Contains(err.Error(), "no path") || len(steps) != 0 {
		t.Fatalf("Path(a.B, fmt.Println) = %+v, %v; want the no-path error", steps, err)
	}
}

// TestPathNamesMissingNode: a node id with no row in either table (a
// damaged database) is an error, not a step with an empty name.
func TestPathNamesMissingNode(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.A", "a.go", 1)}})
	pn := &pathNames{g: g, names: map[int64]string{}}
	if err := pn.load([]int64{1, 999}); err == nil || !strings.Contains(err.Error(), "node 999 has no name") {
		t.Fatalf("load(missing id) error = %v", err)
	}
	if pn.names[1] != "a.A" {
		t.Fatalf("load resolved id 1 to %q, want a.A", pn.names[1])
	}
}

// TestReplaceAllValidation: the bulk rebuild refuses the same delta shapes
// the per-file path does, before touching the database.
func TestReplaceAllValidation(t *testing.T) {
	ctx := context.Background()
	g, _ := openTestGraph(t)
	seedFaultGraph(t, g)
	before := snapshot(t, g)
	tests := []struct {
		name   string
		deltas []FileDelta
		want   string
	}{
		{"empty path", []FileDelta{{}}, "empty file path"},
		{"empty FQN", []FileDelta{{File: "c.go", Symbols: []Symbol{{Name: "x"}}}}, "empty FQN"},
		{"declared twice in a file", []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.X", "c.go", 1), mkSym("c.X", "c.go", 2)}}}, "declared twice"},
		{"declared in two files", []FileDelta{
			{File: "c.go", Symbols: []Symbol{mkSym("c.X", "c.go", 1)}},
			{File: "d.go", Symbols: []Symbol{mkSym("c.X", "d.go", 1)}},
		}, `already declared in "c.go"`},
		{"anchored elsewhere", []FileDelta{{File: "c.go", Symbols: []Symbol{mkSym("c.X", "d.go", 1)}}}, `anchored in "d.go"`},
	}
	for _, tc := range tests {
		if err := g.ReplaceAll(ctx, tc.deltas); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
	if after := snapshot(t, g); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused rebuilds changed the graph:\nbefore %+v\nafter  %+v", before, after)
	}
}

// TestDeltaOnDamagedSchema: when a graph table the write path reads is
// missing (a damaged or half-migrated database), the delta and the removal
// fail with the step that hit it and leave every other table as it was.
func TestDeltaOnDamagedSchema(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		table string
		op    func(g *SQLite) error
		want  string
	}{
		{"graph_labels", func(g *SQLite) error {
			return g.ApplyFileDelta(ctx, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.One", "a.go", 1)}})
		}, "reading inbound edges"},
		{"graph_labels", func(g *SQLite) error { return g.RemoveFile(ctx, "a.go") }, "reading inbound edges"},
		{"graph_nodes", func(g *SQLite) error {
			return g.ApplyFileDelta(ctx, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.One", "a.go", 1)}})
		}, "listing external targets"},
		{"graph_nodes", func(g *SQLite) error { return g.RemoveFile(ctx, "a.go") }, "listing external targets"},
		{"doc_explains", func(g *SQLite) error { return g.ApplyDocDelta(ctx, docDelta("docs/guide.md")) }, "deleting document explains"},
	}
	for _, tc := range tests {
		t.Run(tc.table, func(t *testing.T) {
			g, s := openTestGraph(t)
			seedFaultGraph(t, g)
			before := snapshot(t, g)
			if _, err := s.WriterDB().ExecContext(ctx, `ALTER TABLE `+tc.table+` RENAME TO hidden`); err != nil {
				t.Fatal(err)
			}
			if err := tc.op(g); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if _, err := s.WriterDB().ExecContext(ctx, `ALTER TABLE hidden RENAME TO `+tc.table); err != nil {
				t.Fatal(err)
			}
			if after := snapshot(t, g); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed write changed the graph:\nbefore %+v\nafter  %+v", before, after)
			}
		})
	}
}

func TestBaseName(t *testing.T) {
	for q, want := range map[string]string{
		"pkg.Type.Method": "Method",
		"crate::m::f":     "f",
		"a/b/c":           "c",
		"plain":           "plain",
		"...":             "...", // nothing but separators: q itself
	} {
		if got := baseName(q); got != want {
			t.Errorf("baseName(%q) = %q, want %q", q, got, want)
		}
	}
}
