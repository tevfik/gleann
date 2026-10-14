//go:build treesitter && !windows

package indexing

import (
	"context"
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
