//go:build treesitter && !windows

package indexing

import (
	"context"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildWithGraph(t *testing.T) {
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
	if _, err := os.Stat(filepath.Join(env.cfg.IndexDir, "g_graph")); err != nil {
		t.Fatalf("graph db not created: %v", err)
	}
	if res.GraphReportPath == "" {
		t.Fatal("expected graph report")
	}
	if _, err := os.Stat(res.GraphReportPath); err != nil {
		t.Fatalf("report missing: %v", err)
	}

	// Incremental sync must also update the existing graph.
	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Other() {}\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

// C-04: a graph created by Sync (index built without one) must cover the whole
// tree, not only the files that changed in that sync.
func TestSyncCreatesFullGraph(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d"} {
		writeFile(t, filepath.Join(docs, n+".go"), "package main\n\nfunc F"+n+"() {}\n")
	}
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs, InstantOnly: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.cfg.IndexDir, "g_graph")); err == nil {
		t.Fatal("precondition: graph must not exist yet")
	}
	writeFile(t, filepath.Join(docs, "a.go"), "package main\n\nfunc Fa() {}\nfunc Extra() {}\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g", Graph: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	db, err := kgraph.Open(filepath.Join(env.cfg.IndexDir, "g_graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 4 {
		t.Fatalf("graph holds %d of 4 source files", st.Files)
	}
}
