package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// TestMigrateGraphIDs opens a database with the text-keyed edges table and
// checks the graph_ids step converts it in place: every edge survives with the
// same endpoints, kind, owner file, external flag and confidence (read back
// through the edges view), symbols keep their ids as node ids, the external
// target gets a node, and the old table and its index are gone.
func TestMigrateGraphIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	mockOldGraphDB(t, path)

	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open old graph db: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if v, err := s.Meta(ctx, "migration:graph_ids"); err != nil || v != "1" {
		t.Fatalf("migration:graph_ids = %q, %v; want 1", v, err)
	}
	var typ string
	if err := s.reader.QueryRowContext(ctx, `SELECT type FROM sqlite_master WHERE name = 'edges'`).Scan(&typ); err != nil || typ != "view" {
		t.Fatalf("edges is %q, %v; want a view", typ, err)
	}
	var n int
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'edges_dst'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("edges_dst still present (n=%d err=%v)", n, err)
	}

	rows, err := s.reader.QueryContext(ctx, `SELECT src, dst, kind, file, dst_external, confidence FROM edges ORDER BY src, dst, kind`)
	if err != nil {
		t.Fatalf("reading edges view: %v", err)
	}
	var got [][6]any
	for rows.Next() {
		var src, dst, kind, file, conf string
		var ext int
		if err := rows.Scan(&src, &dst, &kind, &file, &ext, &conf); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, [6]any{src, dst, kind, file, ext, conf})
	}
	rows.Close()
	want := [][6]any{
		{"pkg.Alpha", "ext.Lib", "CALLS", "a.go", 1, "extracted"},
		{"pkg.Alpha", "pkg.Beta", "CALLS", "a.go", 0, "extracted"},
		{"pkg.Beta", "pkg.Alpha", "IMPLEMENTS", "b.go", 0, "extracted"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges after migration =\n%v\nwant\n%v", got, want)
	}

	// Only the external target is a graph_nodes row, with an id no symbol has.
	var fqn string
	if err := s.reader.QueryRowContext(ctx, `SELECT group_concat(fqn) FROM graph_nodes`).Scan(&fqn); err != nil || fqn != "ext.Lib" {
		t.Fatalf("graph_nodes = %q, %v; want only ext.Lib", fqn, err)
	}
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM graph_nodes n JOIN symbols s ON s.id = n.id`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("graph_nodes ids shared with symbols = %d, %v; want 0", n, err)
	}
	// The fixed kind ids the graphstore queries use.
	for label, id := range map[string]int{"CALLS": GraphLabelCalls, "IMPLEMENTS": GraphLabelImplements, "REFERENCES": GraphLabelReferences} {
		var got int
		if err := s.reader.QueryRowContext(ctx, `SELECT id FROM graph_labels WHERE label = ?`, label).Scan(&got); err != nil || got != id {
			t.Fatalf("label %s id = %d, %v; want %d", label, got, err, id)
		}
	}
	if v, err := s.Meta(ctx, MetaGraphEdgesPrefix+"CALLS"); err != nil || v != "2" {
		t.Fatalf("CALLS counter = %q, %v; want 2", v, err)
	}
}
