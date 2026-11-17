package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// runMigrate runs cmdMigrate with exitFunc and the standard streams captured.
func runMigrate(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	origExit, origOut, origErr := exitFunc, os.Stdout, os.Stderr
	defer func() { exitFunc, os.Stdout, os.Stderr = origExit, origOut, origErr }()

	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	exitFunc = func(code int) {
		exitCode = code
		panic(fmt.Sprintf("exit_%d", code))
	}
	func() {
		defer func() {
			if r := recover(); r != nil && !strings.HasPrefix(fmt.Sprint(r), "exit_") {
				panic(r)
			}
		}()
		cmdMigrate(args)
	}()
	wOut.Close()
	wErr.Close()
	o, _ := io.ReadAll(rOut)
	e, _ := io.ReadAll(rErr)
	return string(o), string(e), exitCode
}

// cmdMigrateFixture builds a small format-1 index with the hnsw backend the
// CLI registers, over real source files and a manifest — the state
// `gleann index migrate` converts.
func cmdMigrateFixture(t *testing.T) (indexDir, name string) {
	// migrate turns a format-1 index into format 2, so the fixture must be
	// built as format 1 even when the suite runs with GLEANN_STORE_FORMAT=2.
	t.Setenv("GLEANN_STORE_FORMAT", "1")
	t.Helper()
	indexDir, name = t.TempDir(), "fixture"
	docsDir := t.TempDir()
	items := []gleann.Item{
		{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md", "chunk_index": 0}},
		{Text: "banana bread recipe", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}},
		{Text: "cherry tart dessert plate", Metadata: map[string]any{"source": "c.md", "chunk_index": 0}},
	}
	sources := make([]string, len(items))
	for i, it := range items {
		src := it.Metadata["source"].(string)
		if err := os.WriteFile(filepath.Join(docsDir, src), []byte(it.Text), 0o644); err != nil {
			t.Fatal(err)
		}
		sources[i] = filepath.Join(docsDir, src)
	}

	cfg := gleann.DefaultConfig()
	cfg.IndexDir, cfg.Backend = indexDir, "hnsw"
	cfg.EmbeddingModel = "test-model"
	cfg.HNSWConfig.UseMmap = false // RAM load keeps the test platform-independent
	b, err := gleann.NewBuilder(cfg, &testDefEmbedder{dim: 8})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	b.SetSourceDir(docsDir)
	if err := b.Build(context.Background(), name, items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	m, err := gleann.BuildManifest(name, docsDir, sources)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if err := gleann.SaveManifest(filepath.Join(indexDir, name, name)+".manifest.json", m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	return indexDir, name
}

func TestCmdMigrateEndToEnd(t *testing.T) {
	dir, name := cmdMigrateFixture(t)
	base := filepath.Join(dir, name, name)
	ctx := context.Background()

	out, errOut, code := runMigrate(t, name, "--index-dir", dir)
	if code != 0 || !strings.Contains(out, "migrated to store format 2") {
		t.Fatalf("migrate failed (exit %d): %q %q", code, out, errOut)
	}
	if gleann.StoreFormatOf(base) != 2 {
		t.Fatal("index is not format 2 after migrate")
	}
	for _, suffix := range []string{".passages.db.v1", ".manifest.json.v1"} {
		if _, err := os.Stat(base + suffix); err != nil {
			t.Errorf("preserved %s missing: %v", suffix, err)
		}
	}

	// The migrated index searches: lexical and vector legs both work.
	cfg := gleann.DefaultConfig()
	cfg.IndexDir, cfg.Backend = dir, "hnsw"
	cfg.EmbeddingModel = "test-model"
	cfg.HNSWConfig.UseMmap = false
	s := gleann.NewSearcher(cfg, &testDefEmbedder{dim: 8})
	if err := s.Load(ctx, name); err != nil {
		t.Fatalf("Load after migrate: %v", err)
	}
	rs, err := s.SearchBM25(ctx, "apple", 5)
	if err != nil || len(rs) != 1 || rs[0].Text != "apple pie recipe" {
		t.Errorf("SearchBM25 after migrate = %+v (%v), want the apple passage", rs, err)
	}
	rs, err = s.Search(ctx, "banana bread recipe", gleann.WithTopK(1), gleann.WithHybridAlpha(1.0))
	if err != nil || len(rs) != 1 || rs[0].Text != "banana bread recipe" {
		t.Errorf("vector search after migrate = %+v (%v), want the banana passage", rs, err)
	}
	s.Close()

	// A second migrate is a skip, not a failure.
	out, _, code = runMigrate(t, name, "--index-dir", dir)
	if code != 0 || !strings.Contains(out, "already store format 2") {
		t.Errorf("second migrate = exit %d: %q", code, out)
	}

	// Rollback returns the index to format 1.
	out, errOut, code = runMigrate(t, name, "--index-dir", dir, "--rollback")
	if code != 0 || !strings.Contains(out, "rolled back") {
		t.Fatalf("rollback failed (exit %d): %q %q", code, out, errOut)
	}
	if gleann.StoreFormatOf(base) != 1 {
		t.Fatal("index is not format 1 after rollback")
	}

	// Migrate again, then cleanup removes the preserved files for good.
	if _, _, code = runMigrate(t, name, "--index-dir", dir); code != 0 {
		t.Fatal("re-migrate after rollback failed")
	}
	out, errOut, code = runMigrate(t, name, "--index-dir", dir, "--cleanup")
	if code != 0 || !strings.Contains(out, "rollback is no longer possible") {
		t.Fatalf("cleanup failed (exit %d): %q %q", code, out, errOut)
	}
	for _, suffix := range []string{".passages.db.v1", ".manifest.json.v1"} {
		if _, err := os.Stat(base + suffix); !os.IsNotExist(err) {
			t.Errorf("cleanup left %s behind: %v", suffix, err)
		}
	}
	_, errOut, code = runMigrate(t, name, "--index-dir", dir, "--rollback")
	if code != 1 || !strings.Contains(errOut, "no preserved format-1 store") {
		t.Errorf("rollback after cleanup = exit %d: %q, want a readable refusal", code, errOut)
	}
}

func TestCmdMigrateAll(t *testing.T) {
	dir, name := cmdMigrateFixture(t)
	out, errOut, code := runMigrate(t, "--all", "--index-dir", dir)
	if code != 0 || !strings.Contains(out, name+": migrated to store format 2") {
		t.Fatalf("--all failed (exit %d): %q %q", code, out, errOut)
	}
	if gleann.StoreFormatOf(filepath.Join(dir, name, name)) != 2 {
		t.Fatal("--all did not migrate the fixture")
	}
}

func TestMigrateHelp(t *testing.T) {
	out, _, code := runMigrate(t, "--help")
	if code != 0 || !strings.Contains(out, "Usage: gleann index migrate") || !strings.Contains(out, "--rollback") {
		t.Errorf("help output unexpected (exit %d): %q", code, out)
	}
}

func TestMigrateNeedsANameOrAll(t *testing.T) {
	_, errOut, code := runMigrate(t)
	if code != 1 || !strings.Contains(errOut, "usage:") {
		t.Errorf("want exit 1 with usage, got %d: %q", code, errOut)
	}
}

func TestMigrateAllOnAnEmptyDirectory(t *testing.T) {
	out, _, code := runMigrate(t, "--all", "--index-dir", t.TempDir())
	if code != 0 || !strings.Contains(out, "No indexes found") {
		t.Errorf("want a clean 'No indexes found', got exit %d: %q", code, out)
	}
}

func TestMigrateUnknownIndexFails(t *testing.T) {
	_, errOut, code := runMigrate(t, "does-not-exist", "--index-dir", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "does-not-exist") {
		t.Errorf("want exit 1 naming the index, got %d: %q", code, errOut)
	}
}

func TestMigrateRollbackCleanupConflict(t *testing.T) {
	_, errOut, code := runMigrate(t, "idx", "--rollback", "--cleanup", "--index-dir", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "cannot be combined") {
		t.Errorf("want exit 1 refusing the combination, got %d: %q", code, errOut)
	}
}

func TestMigrateLanguageOnlyForMigration(t *testing.T) {
	_, errOut, code := runMigrate(t, "idx", "--rollback", "--language", "tr", "--index-dir", t.TempDir())
	if code != 1 || !strings.Contains(errOut, "--language") {
		t.Errorf("want exit 1 refusing --language on rollback, got %d: %q", code, errOut)
	}
}
