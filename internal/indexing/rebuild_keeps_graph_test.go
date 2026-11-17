package indexing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const mainHelperEdge = `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND src = 'example.com/fx.main' AND dst = 'example.com/fx.Helper' AND dst_external = 0`

// A lexical-only sync replaces the passages of the whole index. It used to
// delete <name>.db, which also holds the code graph, so only the changed
// files' graph rows came back and the callers of every unchanged file were
// lost.
func TestLexicalOnlySyncKeepsCallersOfUnchangedFiles(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	writeFile(t, filepath.Join(docs, "c.go"), "package fx\n\nfunc Third() int { return 3 }\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g", InstantOnly: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	db := openGraphDB(t, env)
	if n := countRows(t, db, mainHelperEdge); n != 1 {
		t.Fatalf("caller edge of unchanged files after a lexical-only sync = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = 'example.com/fx.Third'`); n != 1 {
		t.Fatalf("symbol of the new file = %d, want 1", n)
	}
	// The passages were replaced too: the new file is searchable.
	if n := countRows(t, db, `SELECT count(*) FROM passages WHERE source = 'c.go'`); n == 0 {
		t.Fatal("the new file has no passages after the sync")
	}
	assertNoOrphans(t, db)
}

// `index build` without --graph over an index that has a graph keeps it.
func TestBuildWithoutGraphKeepsGraph(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	// a.go changes, main.go does not: a.go's rows describe old content.
	writeFile(t, filepath.Join(docs, "a.go"), "package fx\n\n// Helper does the work.\nfunc Helper(x int) int { return x + 2 }\n")
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs}); err != nil {
		t.Fatalf("Build without --graph: %v", err)
	}

	db := openGraphDB(t, env)
	if n := countRows(t, db, mainHelperEdge); n != 1 {
		t.Fatalf("graph edge after `index build` without --graph = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols`); n < 2 {
		t.Fatalf("symbols after the rebuild = %d, want the graph kept", n)
	}
	// The changed file's graph rows are stale until a graph pass: flagged.
	if n := countRows(t, db, `SELECT count(*) FROM files WHERE path = 'a.go' AND graph_dirty = 1`); n != 1 {
		t.Errorf("a.go changed but is not marked graph_dirty (%d)", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM files WHERE path = 'main.go' AND graph_dirty = 0 AND graph_hash IS NOT NULL`); n != 1 {
		t.Errorf("main.go is unchanged and must keep its graph hash (%d)", n)
	}
	// The next sync converges the dirty file without a full graph rebuild.
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	db2 := openGraphDB(t, env)
	if n := countRows(t, db2, `SELECT count(*) FROM files WHERE graph_dirty = 1`); n != 0 {
		t.Errorf("%d file(s) still graph_dirty after the sync", n)
	}
	if n := countRows(t, db2, mainHelperEdge); n != 1 {
		t.Errorf("caller edge after the converging sync = %d, want 1", n)
	}
}

// A rebuild that no longer finds a source file retracts the file's symbols
// and edges together with its passages and manifest row.
func TestRebuildThatRemovesAFileRetractsItsGraphRows(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	if err := os.Remove(filepath.Join(docs, "main.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	db := openGraphDB(t, env)
	for _, q := range []string{
		`SELECT count(*) FROM symbols WHERE file = 'main.go'`,
		`SELECT count(*) FROM edges WHERE file = 'main.go'`,
		`SELECT count(*) FROM passages WHERE source = 'main.go'`,
		`SELECT count(*) FROM files WHERE path = 'main.go'`,
	} {
		if n := countRows(t, db, q); n != 0 {
			t.Errorf("%s = %d after the file was removed, want 0", q, n)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = 'example.com/fx.Helper'`); n != 1 {
		t.Errorf("unchanged file's symbol = %d, want 1", n)
	}
	assertNoOrphans(t, db)
	// The graph counters stay exact.
	var syms string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'graph_count:symbols'`).Scan(&syms); err != nil {
		t.Fatal(err)
	}
	if want := countRows(t, db, `SELECT count(*) FROM symbols`); syms != itoa(want) {
		t.Errorf("graph_count:symbols = %s, want %d", syms, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// A database that cannot be opened is recreated, and then the graph is
// rebuilt from the sources even without --graph.
func TestBuildOverDamagedDatabaseRebuildsTheGraph(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	dbPath := filepath.Join(env.cfg.IndexDir, "g", "g.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
	if err := os.WriteFile(dbPath, []byte("this is not a database, but it is long enough to be read as a header......................................."), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs}); err != nil {
		t.Fatalf("Build over a damaged database: %v", err)
	}
	db := openGraphDB(t, env)
	if n := countRows(t, db, mainHelperEdge); n != 1 {
		t.Fatalf("graph after recreating the database = %d edges, want it rebuilt (1)", n)
	}
}
