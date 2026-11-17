package indexing

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// A build with --graph writes the code graph and the report; an incremental
// sync updates the existing graph. Every build, CGO_ENABLED=0 included.
func TestBuildWithGraph(t *testing.T) {
	t.Setenv("GLEANN_STORE_FORMAT", "2")
	env := newEnv(t)
	docs := fixture(t)
	res, err := Build(context.Background(), BuildOptions{
		Options:     env.options(nil),
		Name:        "g",
		DocsDir:     docs,
		InstantOnly: true,
		Graph:       true,
		GraphReport: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !gleann.HasCodeGraph(env.cfg.IndexDir, "g") {
		t.Fatal("no code graph after a build with --graph")
	}
	if res.GraphReportPath == "" {
		t.Fatal("expected graph report")
	}
	if _, err := os.Stat(res.GraphReportPath); err != nil {
		t.Fatalf("report missing: %v", err)
	}

	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Other() {}\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if n := countRows(t, openGraphDB(t, env), `SELECT count(*) FROM symbols WHERE name = 'Other' AND file = 'main.go'`); n != 1 {
		t.Fatalf("the sync did not update the graph (Other: %d rows)", n)
	}
}

// C-04: a graph created by Sync (index built without one) must cover the whole
// tree, not only the files that changed in that sync.
func TestSyncCreatesFullGraph(t *testing.T) {
	t.Setenv("GLEANN_STORE_FORMAT", "2")
	env := newEnv(t)
	docs := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d"} {
		writeFile(t, filepath.Join(docs, n+".go"), "package main\n\nfunc F"+n+"() {}\n")
	}
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs, InstantOnly: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if gleann.HasCodeGraph(env.cfg.IndexDir, "g") {
		t.Fatal("precondition: graph must not exist yet")
	}
	writeFile(t, filepath.Join(docs, "a.go"), "package main\n\nfunc Fa() {}\nfunc Extra() {}\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g", Graph: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if n := countRows(t, openGraphDB(t, env), `SELECT count(DISTINCT file) FROM symbols WHERE file <> ''`); n != 4 {
		t.Fatalf("graph holds %d of 4 source files", n)
	}
}
