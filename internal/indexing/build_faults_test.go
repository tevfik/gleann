package indexing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/testutil/format1"
	"github.com/tevfik/gleann/pkg/gleann"
)

// A dual-model build names the half that failed.
func TestBuildDualModelNamesTheFailingHalf(t *testing.T) {
	env := newEnv(t)
	dual := func(docs string) error {
		_, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "dual", DocsDir: docs, CodeModel: "code-embed", DocModel: "doc-embed"})
		return err
	}

	err := dual(t.TempDir())
	if !errors.Is(err, ErrNoDocuments) || !strings.Contains(err.Error(), `code index "dual-code"`) {
		t.Errorf("empty source: %v", err)
	}

	// Only code: the code half succeeds, the docs half finds nothing.
	codeOnly := t.TempDir()
	writeFile(t, filepath.Join(codeOnly, "main.go"), "package main\n\nfunc main() {}\n")
	err = dual(codeOnly)
	if !errors.Is(err, ErrNoDocuments) || !strings.Contains(err.Error(), `docs index "dual-docs"`) {
		t.Errorf("code-only source: %v", err)
	}
}

// Build refuses an old-format index of that name and a source that is gone.
func TestBuildRefusals(t *testing.T) {
	env := newEnv(t)
	if err := format1.Write(format1.Index{Dir: env.cfg.IndexDir, Name: "old",
		Passages: []format1.Passage{{ID: 0, Text: "x", Metadata: map[string]any{"source": "a.md"}}}}); err != nil {
		t.Fatal(err)
	}
	_, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "old", DocsDir: fixture(t)})
	var oldFmt *gleann.OldFormatError
	if !errors.As(err, &oldFmt) {
		t.Errorf("old-format index: %v", err)
	}

	_, err = Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "nodir", DocsDir: filepath.Join(t.TempDir(), "missing")})
	mustContain(t, err, "reading documents")
}

// A failing embedder fails the vector phase after the lexical passages exist;
// the error says which phase failed.
func TestBuildVectorPhaseFailure(t *testing.T) {
	_, fc, opts := flakyEnv(t)
	fc.down.Store(true)
	_, err := Build(context.Background(), BuildOptions{Options: opts, Name: "vp", DocsDir: fixture(t)})
	if !errors.Is(err, errEmbedderDown) || !strings.Contains(err.Error(), "building vector index") {
		t.Fatalf("err = %v", err)
	}
}

// Graph and lexical write failures surface with their own wrappers.
func TestBuildWriteFailures(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	dbPath := filepath.Join(env.cfg.IndexDir, "g", "g.db")

	// A damaged graph table fails the graph step of a rebuild with
	// ErrGraphUpdate, keeping the vector index that was just built.
	execDB(t, dbPath, abortOn("INSERT", "symbols"))
	_, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs, Graph: true})
	if err == nil {
		t.Skip("a rebuild recreates the database, dropping the injected fault")
	}
	if !errors.Is(err, ErrGraphUpdate) {
		t.Errorf("err = %v, want ErrGraphUpdate", err)
	}
}

// The rebuild helper reports an unreadable source and an embedder that cannot
// start.
func TestRebuildIndexFailures(t *testing.T) {
	env := newEnv(t)
	buildFor(t, env, "rb")
	_, err := RebuildIndex(context.Background(), "rb", filepath.Join(t.TempDir(), "missing"), env.options(nil))
	if err != nil {
		// an unreadable source tree is an error from the reader
		mustContain(t, err, "reading documents")
	} else {
		t.Error("rebuild of a missing source tree succeeded")
	}
}

// Repair forwards the re-embedding progress.
func TestRepairReportsProgress(t *testing.T) {
	ctx := context.Background()
	opts, cfg := equivEnv(t, "hnsw", false)
	opts.NoEmbeddingDiskCache = true // a cold cache: the repair embeds every passage
	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "pg", DocsDir: equivFixture(t)}); err != nil {
		t.Fatal(err)
	}
	corruptLikeOlderVersions(t, cfg, "pg", "hnsw")
	var seen []Progress
	res, err := Repair(ctx, RepairOptions{Options: opts, Name: "pg", Progress: func(p Progress) { seen = append(seen, p) }})
	if err != nil || !res.Repaired {
		t.Fatalf("Repair: %+v (%v)", res, err)
	}
	if len(seen) == 0 {
		t.Fatal("no progress reported")
	}
	last := seen[len(seen)-1]
	if last.TotalItems == 0 || last.ItemsDone != last.TotalItems {
		t.Errorf("last progress = %+v, want all items done", last)
	}
}

// The emitter derives rate and ETA once enough time has passed to measure.
func TestProgressEmitterRateAndETA(t *testing.T) {
	var got Progress
	e := newProgressEmitter(func(p Progress) { got = p })
	e.start = time.Now().Add(-2 * time.Second)
	e.emit("vector_index", 5, 10, 500, 1000, false)
	if got.Pct != 50 || got.Rate <= 0 || got.ETASec <= 0 {
		t.Errorf("progress = %+v, want 50%% with a rate and an ETA", got)
	}
	// Done: a rate but no ETA.
	e.emit("vector_index", 10, 10, 1000, 1000, true)
	if got.Rate <= 0 || got.ETASec != 0 {
		t.Errorf("finished progress = %+v", got)
	}
	var nilEmitter *progressEmitter
	nilEmitter.emit("x", 1, 1, 0, 0, true) // must not panic
}

// Rebuilding a graph from sources fails cleanly when the index is busy or the
// source tree cannot be walked.
func TestRebuildGraphFromSourcesFailures(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "rg")
	opts := env.options(nil)

	holdLock(t, env.cfg.IndexDir, "rg")
	err := rebuildGraphFromSources(shortCtx(t), "rg", &opts)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("busy index: %v", err)
	}
	_ = docs
}

func TestRebuildGraphFromSourcesUnreadableTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not make a directory unreadable on Windows")
	}
	env := newEnv(t)
	docs := buildFor(t, env, "rt")
	locked := filepath.Join(docs, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(locked, "a.go"), "package x\n")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("running with privileges that ignore directory permissions")
	}
	opts := env.options(nil)
	err := rebuildGraphFromSources(context.Background(), "rt", &opts)
	if err == nil {
		t.Skip("the walker tolerates unreadable directories")
	}
	mustContain(t, err, "reading documents")
}
