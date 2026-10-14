package indexing

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// A full rebuild recreates meta.json; what the user set on the index (tags,
// description, MCP exposure, creation time) must survive it.
func TestRebuildPreservesUserManagedMeta(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	hidden := false
	if _, err := Build(context.Background(), BuildOptions{
		Options:     env.options(nil),
		Name:        "keep",
		DocsDir:     docs,
		Tags:        []string{"work"},
		Description: "kept across rebuilds",
		MCPExposed:  &hidden,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := gleann.GetIndexMeta(env.cfg.IndexDir, "keep")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := RebuildIndex(context.Background(), "keep", docs, env.options(nil)); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	after, err := gleann.GetIndexMeta(env.cfg.IndexDir, "keep")
	if err != nil {
		t.Fatal(err)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("CreatedAt changed: %v -> %v", before.CreatedAt, after.CreatedAt)
	}
	if after.Description != "kept across rebuilds" || !after.HasTag("work") {
		t.Errorf("description/tags lost: %+v", after)
	}
	if after.MCPExposed == nil || *after.MCPExposed {
		t.Errorf("MCPExposed lost: %v", after.MCPExposed)
	}
}

// The vector file must be edited with the backend that wrote it. A caller
// whose flag or default names another backend (or none) used to make every
// sync that touched a file fail with `backend "" not registered`.
func TestSyncUsesTheBackendTheIndexWasBuiltWith(t *testing.T) {
	for _, other := range []string{"", "hnsw"} {
		t.Run("caller backend "+other, func(t *testing.T) {
			env := newEnv(t)
			docs := fixture(t)
			if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "be", DocsDir: docs}); err != nil {
				t.Fatal(err)
			}
			built, err := gleann.GetIndexMeta(env.cfg.IndexDir, "be")
			if err != nil || built.Backend == "" {
				t.Fatalf("index meta: %+v (%v)", built, err)
			}

			writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Mul(a, b int) int { return a * b }\n")
			opts := env.options(nil)
			opts.Config.Backend = other
			if _, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "be"}); err != nil {
				t.Fatalf("Sync with backend %q: %v", other, err)
			}
			after, err := gleann.GetIndexMeta(env.cfg.IndexDir, "be")
			if err != nil {
				t.Fatal(err)
			}
			if after.Backend != built.Backend {
				t.Errorf("backend changed %q -> %q", built.Backend, after.Backend)
			}
		})
	}
}
