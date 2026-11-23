package indexing

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// A failed retraction of a non-code file's legacy rows must be retried by the
// next sync (review Q6): the passage step already advanced the file's hash,
// so without a dirty flag the next sync reports "already up to date" and the
// stale rows persist forever.
func TestSyncRetriesFailedNonCodeRetraction(t *testing.T) {
	env := newEnv(t)
	docs := syncExactFixture(t)
	writeFile(t, filepath.Join(docs, "notes.md"), "# Notes\n\nfirst\n")
	buildFormat2(t, env, docs)
	dbPath := filepath.Join(env.cfg.IndexDir, "g", "g.db")

	// Rows an older gleann wrote for the document.
	st, err := store.Open(dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = graphstore.New(st).ApplyFileDelta(context.Background(), graphstore.FileDelta{
		File:    "notes.md",
		Symbols: []graphstore.Symbol{{FQN: syncExactPkg + ".block_1", Name: "block_1", Kind: "block", File: "notes.md", StartLine: 1, EndLine: 3}},
	})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}

	// The retraction fails (a damaged database aborts the delete).
	execDB(t, dbPath, abortOn("DELETE", "symbols"))
	writeFile(t, filepath.Join(docs, "notes.md"), "# Notes\n\nsecond\n")
	var out bytes.Buffer
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "g"}); err == nil {
		t.Fatalf("the sync succeeded despite the injected retraction fault:\n%s", out.String())
	}

	// Repair the database. notes.md's hash already advanced, so only a dirty
	// flag can bring the retraction back.
	execDB(t, dbPath, `DROP TRIGGER fault_DELETE_symbols`)
	syncGraph(t, env)
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE file = 'notes.md'`); n != 0 {
		t.Errorf("%d stale notes.md symbol(s) persist after the failed retraction; the next sync did not retry it", n)
	}
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d file(s) left dirty after the retried retraction", n)
	}
	assertNoOrphans(t, db)
}
