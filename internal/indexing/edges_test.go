package indexing

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// A lexical-only index whose recorded passage count went stale is repaired
// by correcting the count; there are no vectors to rebuild.
func TestRepairLexicalOnlyFixesTheCount(t *testing.T) {
	env := newEnv(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "lx", DocsDir: fixture(t), InstantOnly: true}); err != nil {
		t.Fatal(err)
	}
	meta, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "lx")
	real := meta.NumPassages
	if err := gleann.UpdateIndexMeta(env.cfg.IndexDir, "lx", func(m *gleann.IndexMeta) { m.NumPassages = real + 40 }); err != nil {
		t.Fatal(err)
	}
	if h, _ := gleann.CheckIndexHealth(env.cfg.IndexDir, "lx"); !h.NeedsRepair() {
		t.Skipf("a stale count is not reported as damage here: %+v", h)
	}
	res, err := Repair(context.Background(), RepairOptions{Options: env.options(nil), Name: "lx"})
	if err != nil || !res.Repaired {
		t.Fatalf("Repair = %+v, %v", res, err)
	}
	if after, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "lx"); after.NumPassages != real {
		t.Fatalf("NumPassages = %d, want %d", after.NumPassages, real)
	}
}

// A forced repair whose embedder fails reports the failure and leaves the
// index as it was.
func TestRepairEmbedderFailure(t *testing.T) {
	env, fc, opts := flakyEnv(t)
	if _, err := Build(context.Background(), BuildOptions{Options: opts, Name: "rf", DocsDir: fixture(t)}); err != nil {
		t.Fatal(err)
	}
	built, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "rf")
	fc.down.Store(true)
	_, err := Repair(context.Background(), RepairOptions{Options: opts, Name: "rf", Force: true})
	if err == nil || !strings.Contains(err.Error(), "rebuilding vector index") {
		t.Fatalf("err = %v", err)
	}
	fc.down.Store(false)
	assertUntouched(t, env, "rf", *built)
	//lint:ignore SA1012 Repair documents that a nil context means Background.
	if _, err := Repair(nil, RepairOptions{Options: opts}); err == nil {
		t.Fatal("Repair without a name")
	}
}

// A sync of a lexical-only index over a damaged database recreates it and
// rebuilds the graph from the sources.
func TestLexicalSyncOverDamagedDatabase(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs, InstantOnly: true, Graph: true}); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(env.cfg.IndexDir, "g", "g.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
	if err := os.WriteFile(dbPath, bytes.Repeat([]byte("damaged!"), 512), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(docs, "b.go"), "package fx\n\nfunc Other() int { return Helper(2) }\n")
	var out bytes.Buffer
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "g", Graph: true}); err != nil {
		t.Fatalf("Sync over a damaged database: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "database was recreated") {
		t.Errorf("recreation not announced:\n%s", out.String())
	}
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE name IN ('Helper', 'Other')`); n != 2 {
		t.Errorf("%d of the 2 symbols in the rebuilt graph", n)
	}
}

// A sync with nothing to do still converges graph files left dirty by an
// earlier failed write.
func TestSyncWithNoChangesConvergesDirtyGraph(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if _, err := db.Exec(`UPDATE files SET graph_dirty = 1`); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g", Files: []string{" "}})
	if err != nil || !res.UpToDate {
		t.Fatalf("Sync = %+v, %v", res, err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM files WHERE graph_dirty = 1`); n != 0 {
		t.Errorf("%d files still dirty", n)
	}
}

// The collector applies the per-mode content checks: in code mode a file of
// an unknown extension must sniff as code or config, and a code or doc file
// must not hide binary content; in docs mode a documentation file must not be
// binary.
func TestCollectEligibleFilesContentChecks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "disguised.go"), "package x\x00\x00\x00\x01binary")
	writeFile(t, filepath.Join(dir, "story.weird"), "Once upon a time there was a long prose paragraph, not code at all, about a fox.")
	writeFile(t, filepath.Join(dir, "packed.md"), "# T\x00\x00\x01\x02")
	writeFile(t, filepath.Join(dir, "ok.go"), "package x\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(dir, "ok.md"), "# Title\n\ntext\n")
	names := func(mode Mode) map[string]bool {
		//lint:ignore SA1012 CollectEligibleFiles documents that a nil context means Background.
		entries, err := CollectEligibleFiles(nil, dir, CollectOptions{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, e := range entries {
			m[filepath.Base(e.Path)] = true
		}
		return m
	}
	code := names(ModeCode)
	if code["disguised.go"] || code["story.weird"] || !code["ok.go"] || !code["ok.md"] {
		t.Errorf("code mode: %v", code)
	}
	docs := names(ModeDocs)
	if docs["packed.md"] || !docs["ok.md"] || docs["ok.go"] {
		t.Errorf("docs mode: %v", docs)
	}
	if _, err := CollectEligibleFiles(context.Background(), filepath.Join(dir, "missing"), CollectOptions{}); err == nil {
		t.Error("walking a missing directory succeeded")
	}
}

// An unreadable file is skipped by both readers (and keeps its tracker
// record in the full read); a whitespace-only source file produces nothing;
// a script without an extension is chunked as code.
func TestReadersSkipUnreadableAndBlankFiles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked.go")
	writeFile(t, locked, "package x\n\nfunc Locked() {}\n")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	writeFile(t, filepath.Join(dir, "blank.go"), "   \n\n")
	writeFile(t, filepath.Join(dir, "deploy"), "#!/bin/sh\nset -e\necho deploying the falcon service\n")

	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, NoPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	got := bySource(items)
	if got["locked.go"] != "" || got["blank.go"] != "" {
		t.Errorf("unreadable or blank files produced passages: %v", got)
	}
	if !strings.Contains(got["deploy"], "falcon") {
		t.Errorf("extensionless script not read: %v", got)
	}
	items, _, _, err = readDocumentsForFiles(context.Background(), dir, []string{locked}, ReadOptions{NoPlugins: true}, newPrinter(nil, nil))
	if err != nil || len(items) != 0 {
		t.Fatalf("incremental read of an unreadable file: %d items, %v", len(items), err)
	}
}

// An incremental graph update that cannot read a changed file reports it,
// marks it dirty for the next sync and skips it in the dirty pass, while a
// dirty file that disappeared is retracted.
func TestIncrementalGraphUnreadableAndVanishedFiles(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)

	mainGo := filepath.Join(docs, "main.go")
	if err := os.Chmod(mainGo, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(mainGo, 0o644) })
	if err := os.Remove(filepath.Join(docs, "a.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET graph_dirty = 1 WHERE path = 'a.go'`); err != nil {
		t.Fatal(err)
	}

	err := BuildGraphIndexErr(GraphOptions{Name: "g", DocsDir: docs, IndexDir: env.cfg.IndexDir, ChangedFiles: []string{mainGo}})
	if err == nil || !strings.Contains(err.Error(), "read ") {
		t.Fatalf("err = %v, want the read failure", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE file = 'a.go'`); n != 0 {
		t.Errorf("%d symbols of the vanished dirty file remain", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM files WHERE path = 'main.go' AND graph_dirty = 1`); n != 1 {
		t.Error("the unreadable file was not marked dirty for the next sync")
	}
}

// RebuildIndex of an empty tree has nothing to write; UpdateIncremental of
// files outside the mode leaves the index untouched; a caller-provided
// cached embedder is used as is.
func TestRebuildAndIncrementalEdges(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	opts := env.options(nil)
	if _, err := Build(context.Background(), BuildOptions{Options: opts, Name: "e", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	before := passageSet(t, env.cfg, "e")

	if pd, err := RebuildIndex(context.Background(), "empty", t.TempDir(), opts); err != nil || pd != nil {
		t.Fatalf("RebuildIndex of an empty tree = %v, %v", pd, err)
	}

	writeFile(t, filepath.Join(docs, "table.csv"), "a,b\n1,2\n")
	code := opts
	code.Mode = ModeCode
	code.Embedder = embedding.NewCachedComputer(&flakyComputer{model: env.cfg.EmbeddingModel}, embedding.CacheOptions{MemoryOnly: true})
	if _, err := UpdateIncremental(context.Background(), "e", docs, []string{filepath.Join(docs, "table.csv")}, code); err != nil {
		t.Fatalf("UpdateIncremental: %v", err)
	}
	if after := passageSet(t, env.cfg, "e"); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Error("a file outside the mode changed the index")
	}

	// Signatures-only chunking is announced.
	sig := env.options(nil)
	var out bytes.Buffer
	w := &lockedBuffer{b: &out}
	sig.Stdout = w
	sig.Config.ChunkConfig.SignaturesOnly = true
	if _, err := Build(context.Background(), BuildOptions{Options: sig, Name: "sig", DocsDir: docs, InstantOnly: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Signatures-only") {
		t.Errorf("signatures-only not announced:\n%s", out.String())
	}

	// A cancelled context stops the incremental update.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := UpdateIncremental(ctx, "e", docs, []string{filepath.Join(docs, "main.go")}, opts); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled UpdateIncremental: %v", err)
	}
}
