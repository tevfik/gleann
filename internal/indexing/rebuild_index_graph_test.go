package indexing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// RebuildIndex (watch mode) over a database that cannot be opened recreates
// it and, like Build and Sync, rebuilds the graph that was lost with it.
func TestRebuildIndexOverDamagedDatabaseRebuildsTheGraph(t *testing.T) {
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
	if _, err := RebuildIndex(context.Background(), "g", docs, env.options(nil)); err != nil {
		t.Fatalf("RebuildIndex over a damaged database: %v", err)
	}
	db := openGraphDB(t, env)
	if n := countRows(t, db, mainHelperEdge); n != 1 {
		t.Fatalf("graph after RebuildIndex recreated the database = %d edges, want it rebuilt (1)", n)
	}
}
