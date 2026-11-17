package gleann

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// A rebuild replaces the passages inside the existing database: the file
// stays (same inode), so a reader that holds it open keeps working and
// Windows has nothing to refuse to delete, and the graph it holds survives.
func TestFormat2RebuildReplacesPassagesInPlace(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "idx", "idx")

	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphstore.New(st).ApplyFileDelta(ctx, graphstore.FileDelta{
		File: "a.md", GraphHash: "h",
		Symbols: []graphstore.Symbol{{FQN: "m.A", Kind: "function", Name: "A", StartLine: 1, EndLine: 2}},
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// A reader (a running searcher) holds the database open during the rebuild.
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := os.Stat(base + ".db")
	if err != nil {
		t.Fatal(err)
	}

	b2, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.BuildLexicalOnly("idx", f2UpdateItems()); err != nil {
		t.Fatalf("BuildLexicalOnly over an open index: %v", err)
	}
	if b2.DatabaseRecreated() {
		t.Fatal("a healthy database must not be recreated")
	}
	after, err := os.Stat(base + ".db")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("the database file was replaced; the rebuild must work inside it")
	}

	// New passages, old graph rows.
	rows := f2Rows(t, base)
	if _, ok := rows["date syrup cake"]; !ok || len(rows) != 2 {
		t.Errorf("passages after the rebuild = %v", rows)
	}
	chk, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer chk.Close()
	var n int
	if err := chk.ReaderDB().QueryRow(`SELECT count(*) FROM symbols WHERE fqn = 'm.A'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("graph symbol after the rebuild = %d, %v", n, err)
	}
	// The generation moved, so the open searcher sees the change.
	if stale, err := s.Stale(ctx); !stale || err != nil {
		t.Errorf("the open searcher is stale=%v err=%v after the rebuild, want stale", stale, err)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if want := currentVectorGeneration(ctx, chk); m.VectorGeneration != want || want < 2 {
		t.Errorf("meta.json vector_generation = %d, store = %d", m.VectorGeneration, want)
	}
	if !m.LexicalOnly || m.VectorReady {
		t.Errorf("meta after a lexical-only rebuild: lexical_only=%v vector_ready=%v", m.LexicalOnly, m.VectorReady)
	}
}

// A crash between the rebuild's commit and the meta.json write must not make
// recovery mark the slotless passages vector-ready.
func TestFormat2RecoveryAfterRebuildDoesNotMarkVectorsReady(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "idx", "idx")
	b2, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b2.BuildPassages("idx", f2UpdateItems()); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash: meta.json still says the old generation.
	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) {
		m.VectorGeneration = 1
		m.VectorReady, m.LexicalOnly = true, false
	}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := recoverVectorPublication(ctx, st, base); err != nil {
		t.Fatal(err)
	}
	m, err := GetIndexMeta(dir, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if m.VectorReady || !m.LexicalOnly {
		t.Errorf("recovery marked slotless passages vector-ready: %+v", m)
	}
}

// A database that cannot be used (a newer store_format, or damage) is
// recreated, and the builder says so: the graph it held is gone.
func TestFormat2RebuildRecreatesUnusableDatabase(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, base string){
		"newer store_format": func(t *testing.T, base string) {
			st, err := store.Open(base+".db", store.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetMeta(context.Background(), "store_format", "3"); err != nil {
				t.Fatal(err)
			}
			st.Close()
		},
		"not a database": func(t *testing.T, base string) {
			for _, suffix := range []string{"-wal", "-shm"} {
				_ = os.Remove(base + ".db" + suffix)
			}
			if err := os.WriteFile(base+".db", []byte("not a sqlite database, only some bytes that are long enough for a header....................."), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			b, embedder := newFormat2Builder(t, dir, 8)
			ctx := context.Background()
			if err := b.Build(ctx, "idx", f2Items()); err != nil {
				t.Fatal(err)
			}
			damage(t, filepath.Join(dir, "idx", "idx"))

			b2, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
			if err != nil {
				t.Fatal(err)
			}
			if err := b2.Build(ctx, "idx", f2UpdateItems()); err != nil {
				t.Fatalf("Build over an unusable database: %v", err)
			}
			if !b2.DatabaseRecreated() {
				t.Error("DatabaseRecreated() = false after recreating the file")
			}
			f2SelfRetrieval(t, dir, embedder, []string{"apricot jam preserves", "date syrup cake"})
		})
	}
}

// A source that disappeared is retracted together with its passages, one that
// still exists keeps its graph rows, and one the tree cannot be consulted for
// is kept and marked dirty.
func TestReplacePassagesFates(t *testing.T) {
	dir := t.TempDir()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "kept.md"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	items := []Item{
		{Text: "kept file text", Metadata: map[string]any{"source": "kept.md"}},
		{Text: "gone file text!", Metadata: map[string]any{"source": "gone.md"}},
	}
	b.SetSourceDir(srcDir)
	if err := b.Build(ctx, "idx", items); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "idx", "idx")
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := graphstore.New(st)
	for _, f := range []string{"kept.md", "gone.md"} {
		if err := g.ApplyFileDelta(ctx, graphstore.FileDelta{
			File: f, GraphHash: "h",
			Symbols: []graphstore.Symbol{{FQN: "m." + f, Kind: "function", Name: f, StartLine: 1, EndLine: 2}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	b2, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	b2.SetSourceDir(srcDir)
	// Neither source is in the new items; kept.md exists on disk, gone.md does not.
	if err := b2.BuildLexicalOnly("idx", []Item{{Text: "other", Metadata: map[string]any{"source": "other.md"}}}); err != nil {
		t.Fatal(err)
	}
	chk, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer chk.Close()
	count := func(q string) int {
		var n int
		if err := chk.ReaderDB().QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM symbols WHERE file = 'gone.md'`); n != 0 {
		t.Errorf("gone.md symbols = %d, want retracted", n)
	}
	if n := count(`SELECT count(*) FROM files WHERE path = 'gone.md'`); n != 0 {
		t.Errorf("gone.md files row = %d, want removed", n)
	}
	if n := count(`SELECT count(*) FROM symbols WHERE file = 'kept.md'`); n != 1 {
		t.Errorf("kept.md symbols = %d, want kept", n)
	}
	if n := count(`SELECT count(*) FROM files WHERE path = 'kept.md' AND graph_hash IS NOT NULL AND hash = ''`); n != 1 {
		t.Errorf("kept.md must keep graph_hash and lose its manifest hash (%d)", n)
	}
	if n := count(`SELECT count(*) FROM passages WHERE source IN ('kept.md', 'gone.md')`); n != 0 {
		t.Errorf("passages of the dropped sources = %d, want 0", n)
	}
}

// Without a source directory the tree cannot be consulted: a source that lost
// its passages keeps its graph rows and is marked graph_dirty, so the next
// graph pass re-extracts it or finds it gone and retracts it.
func TestReplacePassagesUnknownSourceIsKeptAndFlagged(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", []Item{{Text: "unknown file", Metadata: map[string]any{"source": "unk.md"}}}); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "idx", "idx")
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := graphstore.New(st).ApplyFileDelta(ctx, graphstore.FileDelta{
		File: "unk.md", GraphHash: "h",
		Symbols: []graphstore.Symbol{{FQN: "m.U", Kind: "function", Name: "U", StartLine: 1, EndLine: 2}},
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	b2, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.SourceDir = "" }); err != nil {
		t.Fatal(err)
	}
	if err := b2.BuildLexicalOnly("idx", []Item{{Text: "another", Metadata: map[string]any{"source": "x.md"}}}); err != nil {
		t.Fatal(err)
	}
	chk, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer chk.Close()
	var n int
	if err := chk.ReaderDB().QueryRow(`SELECT count(*) FROM files WHERE path = 'unk.md' AND graph_dirty = 1 AND graph_hash IS NOT NULL`).Scan(&n); err != nil || n != 1 {
		t.Errorf("an unresolvable source with graph rows must be kept and marked graph_dirty (%d, %v)", n, err)
	}
	if err := chk.ReaderDB().QueryRow(`SELECT count(*) FROM symbols WHERE file = 'unk.md'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("its symbol = %d, %v", n, err)
	}
}
