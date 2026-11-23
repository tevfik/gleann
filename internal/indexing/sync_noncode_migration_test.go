package indexing

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// Rows an older gleann wrote for non-code files (block_N symbols of README,
// LICENSE, go.mod ...) must be retracted by the first sync with the new code
// even when those files did not change, and the migration must cost nothing
// on later syncs (reviews Q5, G3).
func TestSyncMigratesLegacyNonCodeSymbols(t *testing.T) {
	env := newEnv(t)
	docs := syncExactFixture(t)
	writeFile(t, filepath.Join(docs, "README.md"), "# Project\n\nIntro text.\n")
	writeFile(t, filepath.Join(docs, "LICENSE"), "Permission is hereby granted.\n")
	writeFile(t, filepath.Join(docs, "docs", "README.md"), "# Docs\n\nMore text.\n")
	buildFormat2(t, env, docs)
	dbPath := filepath.Join(env.cfg.IndexDir, "g", "g.db")

	// Simulate an index written by the old code: block_N rows for non-code
	// files that this sync will not touch.
	st, err := store.Open(dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := graphstore.New(st)
	for i, f := range []string{"README.md", "LICENSE", "go.mod", "docs/README.md"} {
		name := fmt.Sprintf("block_%d", i+1)
		if err := g.ApplyFileDelta(context.Background(), graphstore.FileDelta{
			File:    f,
			Symbols: []graphstore.Symbol{{FQN: syncExactPkg + "." + name, Name: name, Kind: "block", File: f, StartLine: 1, EndLine: 3}},
		}); err != nil {
			st.Close()
			t.Fatal(err)
		}
	}
	st.Close()

	// Change only a Go file, without adding a declaration (an added one would
	// mark the whole directory dirty and retract the root-level rows by
	// accident; the docs/ ones would survive either way).
	writeFile(t, filepath.Join(docs, "app.go"), strings.Replace(sxApp, "func Use(g Getter) int { return g.Get() }", "func Use(g Getter) int { return g.Get() + 1 }", 1))
	out := syncGraph(t, env)
	db := openGraphDB(t, env)
	const legacy = `SELECT count(*) FROM symbols WHERE file IN ('README.md', 'LICENSE', 'go.mod', 'docs/README.md')`
	if n := countRows(t, db, legacy); n != 0 {
		t.Errorf("%d pre-patch non-code symbol(s) survive a sync that does not touch those files (no migration)", n)
	}
	if !strings.Contains(out, "legacy") {
		t.Errorf("the sync does not report the migration: %s", out)
	}
	full := newEnv(t)
	buildFormat2(t, full, docs)
	assertGraphEquals(t, "legacy non-code migration", openGraphDB(t, env), openGraphDB(t, full))
	assertNoOrphans(t, db)

	// The migration is one-time: a later sync must not scan or report again.
	writeFile(t, filepath.Join(docs, "app.go"), sxApp+"\n// Extra is added by the edit.\nfunc Extra() int { return Use(nil) }\n\n// More is added later.\nfunc More() int { return Extra() }\n")
	if out := syncGraph(t, env); strings.Contains(out, "legacy") {
		t.Errorf("the migration ran again on a later sync: %s", out)
	}
	if n := countRows(t, openGraphDB(t, env), legacy); n != 0 {
		rows, err := openGraphDB(t, env).Query(`SELECT fqn, file FROM symbols WHERE file IN ('README.md', 'LICENSE', 'go.mod', 'docs/README.md')`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var fqn, file string
			if err := rows.Scan(&fqn, &file); err != nil {
				t.Fatal(err)
			}
			t.Logf("legacy row: %s in %s", fqn, file)
		}
		t.Errorf("%d non-code symbol(s) after the second sync", n)
	}
}
