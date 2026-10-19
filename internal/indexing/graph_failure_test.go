//go:build treesitter

package indexing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// When the graph cannot be updated the sync must fail and leave the index
// "not up to date": it used to print a warning, report success and record the
// new manifest, so every later sync said "already up to date" over a stale graph.
func TestSyncFailsAndRetriesWhenTheGraphCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	env := newEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs, Graph: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	graph := filepath.Join(env.cfg.IndexDir, "g_graph")
	fi, err := os.Stat(graph)
	if err != nil {
		t.Skipf("no graph at %s: %v", graph, err)
	}
	if fi.IsDir() {
		t.Skip("directory-format graph; this test locks the single-file format")
	}

	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Mul(a, b int) int { return a * b }\n")
	if err := os.Chmod(graph, 0o444); err != nil {
		t.Fatal(err)
	}
	_, err = Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g", Graph: true})
	if !errors.Is(err, ErrGraphUpdate) {
		t.Fatalf("Sync err = %v, want ErrGraphUpdate", err)
	}

	// Once the graph is writable again the change is picked up, not skipped.
	if err := os.Chmod(graph, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g", Graph: true})
	if err != nil {
		t.Fatalf("retry Sync: %v", err)
	}
	if res.UpToDate {
		t.Error("retry reported the index up to date over a graph that was never updated")
	}
}
