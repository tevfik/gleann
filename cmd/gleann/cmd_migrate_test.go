package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/testutil/format1"
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
	outC, errC := make(chan []byte), make(chan []byte)
	go func() { b, _ := io.ReadAll(rOut); outC <- b }()
	go func() { b, _ := io.ReadAll(rErr); errC <- b }()
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
	return string(<-outC), string(<-errC), exitCode
}

// cmdMigrateFixture writes a small index in the v1.9 layout (test-only
// writer, internal/testutil/format1) with the hnsw backend the CLI registers:
// bbolt passages, a vector file, a manifest, a .lex.db sidecar and, with
// graph, a Kùzu <name>_graph directory - the state `gleann index migrate`
// converts. The source directory holds a Go file so the graph rebuild has
// something to extract.
func cmdMigrateFixture(t *testing.T, indexDir, name string, graph bool) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	docsDir := t.TempDir()
	files := map[string]string{
		"a.md":    "apple pie recipe",
		"b.md":    "banana bread recipe",
		"main.go": "package main\n\nfunc helper() int { return 1 }\n\nfunc main() { helper() }\n",
	}
	var ps []format1.Passage
	var texts []string
	for i, src := range []string{"a.md", "b.md", "main.go"} {
		if err := os.WriteFile(filepath.Join(docsDir, src), []byte(files[src]), 0o644); err != nil {
			t.Fatal(err)
		}
		ps = append(ps, format1.Passage{ID: int64(i), Text: files[src], Metadata: map[string]any{"source": src, "chunk_index": 0}})
		texts = append(texts, files[src])
	}
	vecs, err := (&testDefEmbedder{dim: 8}).Compute(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	factory, err := gleann.GetBackend("hnsw")
	if err != nil {
		t.Fatal(err)
	}
	cfg := gleann.DefaultConfig()
	cfg.IndexDir, cfg.Backend = indexDir, "hnsw"
	data, err := factory.NewBuilder(cfg).Build(context.Background(), vecs)
	if err != nil {
		t.Fatal(err)
	}
	if err := format1.Write(format1.Index{
		Dir: indexDir, Name: name, Passages: ps, VectorFile: data, LexSidecar: true, Graph: graph,
		Meta: map[string]any{"backend": "hnsw", "embedding_model": "test-model", "dimensions": 8,
			"vector_ready": true, "source_dir": docsDir, "tags": []string{"work"}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCmdMigrateEndToEnd(t *testing.T) {
	dir, name := t.TempDir(), "fixture"
	cmdMigrateFixture(t, dir, name, true)
	base := filepath.Join(dir, name, name)
	ctx := context.Background()

	out, errOut, code := runMigrate(t, name, "--index-dir", dir)
	if code != 0 || !strings.Contains(out, name+": migrated 3 passages to store format 2") {
		t.Fatalf("migrate failed (exit %d): %q %q", code, out, errOut)
	}
	if !strings.Contains(out, "code graph rebuilt") || !strings.Contains(out, "--cleanup") {
		t.Errorf("migrate output lacks the graph rebuild or the cleanup hint: %q", out)
	}
	if gleann.StoreFormatOf(base) != 2 {
		t.Fatal("index is not format 2 after migrate")
	}
	if !gleann.HasCodeGraph(dir, name) {
		t.Fatal("the old index had a code graph; migrate must rebuild it")
	}
	if _, err := os.Stat(base + ".passages.db"); err != nil {
		t.Errorf("the old passage store must stay until --cleanup: %v", err)
	}

	cfg := gleann.DefaultConfig()
	cfg.IndexDir, cfg.Backend = dir, "hnsw"
	cfg.EmbeddingModel = "test-model"
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

	// A second run skips; --cleanup deletes the old files and prints them.
	out, errOut, code = runMigrate(t, name, "--cleanup", "--index-dir", dir)
	if code != 0 || !strings.Contains(out, "already store format 2") {
		t.Fatalf("second migrate (exit %d): %q %q", code, out, errOut)
	}
	for _, p := range []string{base + ".passages.db", base + ".lex.db", filepath.Join(dir, name+"_graph")} {
		if !strings.Contains(out, "removed "+p) {
			t.Errorf("--cleanup did not print %s: %q", p, out)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("--cleanup left %s (%v)", p, err)
		}
	}
	if !gleann.HasCodeGraph(dir, name) {
		t.Fatal("--cleanup must not touch the new code graph")
	}
}

func TestCmdMigrateAllWithMemoryVectors(t *testing.T) {
	dir := t.TempDir()
	cmdMigrateFixture(t, dir, "one", false)
	cmdMigrateFixture(t, dir, "one_memory_vectors", false)
	cmdMigrateFixture(t, dir, "two", false)
	out, errOut, code := runMigrate(t, "--all", "--index-dir", dir)
	if code != 0 {
		t.Fatalf("--all failed (exit %d): %q %q", code, out, errOut)
	}
	for _, want := range []string{"one: migrated", "one_memory_vectors: migrated", "two: migrated"} {
		if !strings.Contains(out, want) {
			t.Errorf("--all output lacks %q: %q", want, out)
		}
	}
	for _, n := range []string{"one", "one_memory_vectors", "two"} {
		if gleann.StoreFormatOf(filepath.Join(dir, n, n)) != 2 {
			t.Errorf("--all did not migrate %s", n)
		}
	}
}

func TestMigrateHelp(t *testing.T) {
	out, _, code := runMigrate(t, "--help")
	if code != 0 || !strings.Contains(out, "Usage: gleann index migrate") || !strings.Contains(out, "--cleanup") || strings.Contains(out, "--rollback") {
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

func TestMigrateRemovedFlags(t *testing.T) {
	for _, args := range [][]string{{"idx", "--rollback"}, {"idx", "--language", "tr"}} {
		_, errOut, code := runMigrate(t, append(args, "--index-dir", t.TempDir())...)
		if code != 1 || !strings.Contains(errOut, "removed in v1.20") {
			t.Errorf("%v: want exit 1 saying the flag was removed, got %d: %q", args, code, errOut)
		}
	}
}
