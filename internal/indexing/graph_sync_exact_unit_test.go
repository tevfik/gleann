package indexing

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/indexer"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// exactCandidate and the per-directory bookkeeping of syncExact.
func TestSyncExactLoadsEachDirectoryOnce(t *testing.T) {
	docs := syncExactFixture(t)
	writeFile(t, filepath.Join(docs, "app_test.go"), "package sx\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) { Use(nil) }\n")
	resolved, err := filepath.EvalSymlinks(docs)
	if err != nil {
		resolved = docs
	}
	var warn bytes.Buffer
	s := newSyncExact(nil, syncExactPkg, resolved, newPrinter(nil, &warn))
	s.load([]string{"app.go", "app_test.go", "README.md", "store/store.go"})
	if len(s.resolvers) != 1 || !s.loaded["."] || !s.loaded["store"] || s.loaded["cli"] {
		t.Fatalf("after the first load: %d resolver(s), loaded = %v", len(s.resolvers), s.loaded)
	}
	s.load([]string{"other.go", "store/store.go"}) // both directories are covered
	if len(s.resolvers) != 1 {
		t.Errorf("a covered directory was loaded again: %d resolvers", len(s.resolvers))
	}
	s.load([]string{"cli/cli.go"})
	if len(s.resolvers) != 2 {
		t.Errorf("the new directory was not loaded: %d resolvers", len(s.resolvers))
	}
	for _, rel := range []string{"app.go", "app_test.go", "other.go", "store/store.go", "cli/cli.go"} {
		if s.resolverFor(rel) == nil {
			t.Errorf("no resolver for %s", rel)
		}
	}
	if s.resolverFor("missing.go") != nil {
		t.Error("a resolver for a file no load type-checked")
	}

	// A directory holding only _test.go files has no production view: the load
	// is attempted (a test file is a candidate) but yields no resolver, no
	// warning and no retry entry.
	writeFile(t, filepath.Join(resolved, "onlytest", "x_test.go"), "package onlytest\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	s.load([]string{"onlytest/x_test.go"})
	if !s.loaded["onlytest"] {
		t.Error("a test-only directory was not loaded")
	}
	if s.resolverFor("onlytest/x_test.go") != nil {
		t.Error("a test-only package got a resolver")
	}
	if len(s.failedFiles) != 0 {
		t.Errorf("a test-only package was marked failed: %v", s.failedFiles)
	}
	if warn.Len() != 0 {
		t.Errorf("unexpected warning: %s", warn.String())
	}

	var nilSync *syncExact
	nilSync.load([]string{"app.go"})
	if nilSync.resolverFor("app.go") != nil {
		t.Error("a nil syncExact must resolve nothing")
	}

	// A failed load turns the rest of the sync syntactic with one warning.
	if err := os.WriteFile(filepath.Join(resolved, "go.mod"), []byte("module "+syncExactPkg+"\n\ngo 1.999.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newSyncExact(nil, syncExactPkg, resolved, newPrinter(nil, &warn))
	f.load([]string{"app.go"})
	f.load([]string{"cli/cli.go"})
	if n := strings.Count(warn.String(), "warning:"); n != 1 || len(f.resolvers) != 0 {
		t.Errorf("%d warning(s) and %d resolver(s) after a failed load, want 1 and 0: %s", n, len(f.resolvers), warn.String())
	}
}

// The lookups the non-code rule and the carried IMPLEMENTS edges need can
// fail; each failure names the file and fails that file's graph update.
func TestSyncExactLookupFailures(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T) (*graphstore.SQLite, string, string) {
		t.Helper()
		env := newEnv(t)
		docs := syncExactFixture(t)
		writeFile(t, filepath.Join(docs, "notes.md"), "# Notes\n")
		buildFormat2(t, env, docs)
		dbPath := filepath.Join(env.cfg.IndexDir, "g", "g.db")
		st, err := store.Open(dbPath, store.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		resolved, err := filepath.EvalSymlinks(docs)
		if err != nil {
			resolved = docs
		}
		return graphstore.New(st), dbPath, resolved
	}

	t.Run("stored IMPLEMENTS edges", func(t *testing.T) {
		g, dbPath, docs := open(t)
		r, err := indexer.LoadExactDirs(syncExactPkg, docs, []string{"store"})
		if err != nil || !r.HasFile("store/store.go") {
			t.Fatalf("LoadExactDirs(store) = %v, %v", r, err)
		}
		execDB(t, dbPath, `ALTER TABLE graph_edges RENAME COLUMN confidence TO zz`)
		err = applyFileGraph(ctx, g, syncExactPkg, docs, filepath.Join(docs, "store", "store.go"), sxStore, nil, r)
		mustContain(t, err, "reading stored IMPLEMENTS edges of store/store.go")
	})

	t.Run("rows of a non-code file", func(t *testing.T) {
		g, dbPath, docs := open(t)
		notes := filepath.Join(docs, "notes.md")
		if err := g.ApplyFileDelta(ctx, graphstore.FileDelta{
			File:    "notes.md",
			Symbols: []graphstore.Symbol{{FQN: syncExactPkg + ".block_1", Name: "block_1", Kind: "block", File: "notes.md", StartLine: 1, EndLine: 1}},
		}); err != nil {
			t.Fatal(err)
		}
		execDB(t, dbPath, abortOn("DELETE", "symbols"))
		err := incrementalGraphSQLite(ctx, g, []string{notes}, syncExactPkg, docs, newPrinter(nil, nil))
		mustContain(t, err, "removing graph rows of notes.md")

		execDB(t, dbPath, `ALTER TABLE symbols RENAME COLUMN file TO zz`)
		err = incrementalGraphSQLite(ctx, g, []string{notes}, syncExactPkg, docs, newPrinter(nil, nil))
		mustContain(t, err, "looking up graph rows of notes.md")
	})
}
