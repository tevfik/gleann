package gleann

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// This file covers the format-1 → format-2 migration (RFC 003 sections 6.3,
// 6.4 and 7.1): identical lexical results across the switch, byte-identical
// rollback, refusal of non-dense stores, guard files that old readers cannot
// open (and that stay untouched when they try), and fail-fast locking.

// migrateWords gives every fixture passage a keyword of its own, so each BM25
// query matches exactly one passage and result lists have no score ties.
var migrateWords = []string{
	"apple", "banana", "cherry", "damson", "elderberry", "fig",
	"grape", "hazelnut", "kiwi", "lemon", "mango", "nectarine",
}

// migrateFixture builds a format-1 index the way the pipeline does: bbolt
// passages, a vector file, real source files and a manifest. GLEANN_LEXICAL
// is set to fts5 so the "before" side of the comparison uses the same FTS5
// scorer the format-2 store will use.
func migrateFixture(t *testing.T) (dir, name string, items []Item) {
	t.Helper()
	initTestHNSW()
	// Migration converts format 1 to format 2, so the fixture must be built as
	// format 1 even when GLEANN_STORE_FORMAT=2 selects format 2 for new indexes.
	t.Setenv("GLEANN_STORE_FORMAT", "1")
	t.Setenv("GLEANN_LEXICAL", "fts5")
	dir, name = t.TempDir(), "idx"
	docsDir := t.TempDir()

	items = make([]Item, len(migrateWords))
	sources := make([]string, len(migrateWords))
	for i, w := range migrateWords {
		src := fmt.Sprintf("doc%02d.md", i)
		text := fmt.Sprintf("%s preserves and %s jam recipe number %d", w, w, i)
		if err := os.WriteFile(filepath.Join(docsDir, src), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		items[i] = Item{Text: text, Metadata: map[string]any{"source": src, "chunk_index": 0}}
		sources[i] = filepath.Join(docsDir, src)
	}

	b, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	b.SetSourceDir(docsDir)
	if err := b.Build(context.Background(), name, items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	m, err := BuildManifest(name, docsDir, sources)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if err := SaveManifest(filepath.Join(dir, name, name)+".manifest.json", m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if StoreFormatOf(filepath.Join(dir, name, name)) != 1 {
		t.Fatal("fixture must be format 1")
	}
	return dir, name, items
}

// fileSHA returns the hex sha256 of a file.
func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// bm25Lists runs the queries through a searcher's BM25-only leg.
func bm25Lists(t *testing.T, s *LeannSearcher, queries []string) [][]SearchResult {
	t.Helper()
	out := make([][]SearchResult, len(queries))
	for i, q := range queries {
		rs, err := s.SearchBM25(context.Background(), q, 5)
		if err != nil {
			t.Fatalf("SearchBM25(%q): %v", q, err)
		}
		out[i] = rs
	}
	return out
}

func TestMigrate_BM25ResultsIdentical(t *testing.T) {
	dir, name, _ := migrateFixture(t)
	ctx := context.Background()
	base := filepath.Join(dir, name, name)
	queries := migrateWords[:10]

	before := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err := before.Load(ctx, name); err != nil {
		t.Fatalf("Load before: %v", err)
	}
	if _, ok := before.scorer.(*FTSScorer); !ok {
		t.Fatalf("before must use the FTS5 sidecar, got %T", before.scorer)
	}
	want := bm25Lists(t, before, queries)
	before.Close()

	if err := MigrateIndex(dir, name, MigrateOptions{}); err != nil {
		t.Fatalf("MigrateIndex: %v", err)
	}

	after := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err := after.Load(ctx, name); err != nil {
		t.Fatalf("Load after: %v", err)
	}
	defer after.Close()
	if _, ok := after.scorer.(*FTSScorer); !ok {
		t.Fatalf("after must use the format-2 lex table, got %T", after.scorer)
	}
	got := bm25Lists(t, after, queries)
	for i, q := range queries {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("query %q: %d results after migration, %d before", q, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			if got[i][j].ID != want[i][j].ID || got[i][j].Text != want[i][j].Text {
				t.Errorf("query %q rank %d: before {ID:%d Text:%q} after {ID:%d Text:%q}",
					q, j, want[i][j].ID, want[i][j].Text, got[i][j].ID, got[i][j].Text)
			}
			if d := got[i][j].Score - want[i][j].Score; d > 1e-6 || d < -1e-6 {
				t.Errorf("query %q rank %d: score %v before, %v after", q, j, want[i][j].Score, got[i][j].Score)
			}
		}
	}

	// The vector file is untouched by the migration, and vectors still load.
	if !after.VectorAvailable() {
		t.Error("vectors unavailable after migration")
	}
	if got := StoreFormatOf(base); got != 2 {
		t.Errorf("StoreFormatOf after migration = %d, want 2", got)
	}
}

func TestMigrate_RollbackRestoresBytes(t *testing.T) {
	dir, name, _ := migrateFixture(t)
	base := filepath.Join(dir, name, name)
	oldPassages := fileSHA(t, base+".passages.db")
	oldManifest := fileSHA(t, base+".manifest.json")
	oldVectors := fileSHA(t, base+".index")

	if err := MigrateIndex(dir, name, MigrateOptions{}); err != nil {
		t.Fatalf("MigrateIndex: %v", err)
	}
	if fileSHA(t, base+".index") != oldVectors {
		t.Error("migration modified the vector file")
	}
	if fileSHA(t, base+".passages.db.v1") != oldPassages {
		t.Error("preserved passages.db.v1 differs from the original")
	}
	if fileSHA(t, base+".manifest.json.v1") != oldManifest {
		t.Error("preserved manifest.json.v1 differs from the original")
	}

	if err := RollbackMigration(dir, name); err != nil {
		t.Fatalf("RollbackMigration: %v", err)
	}
	if fileSHA(t, base+".passages.db") != oldPassages {
		t.Error("rollback did not restore the passage store byte-identically")
	}
	if fileSHA(t, base+".manifest.json") != oldManifest {
		t.Error("rollback did not restore the manifest byte-identically")
	}
	if fileSHA(t, base+".index") != oldVectors {
		t.Error("rollback modified the vector file")
	}
	if StoreFormatOf(base) != 1 {
		t.Error("rollback did not return the index to format 1")
	}
	if _, err := os.Stat(base + ".db"); !os.IsNotExist(err) {
		t.Errorf("format-2 store survived rollback: %v", err)
	}
	meta, err := GetIndexMeta(dir, name)
	if err != nil || meta.StoreFormat != 0 {
		t.Errorf("meta store_format after rollback = %+v (%v), want 0", meta, err)
	}

	// The restored index searches again through its format-1 sidecar.
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err := s.Load(context.Background(), name); err != nil {
		t.Fatalf("Load after rollback: %v", err)
	}
	defer s.Close()
	rs, err := s.SearchBM25(context.Background(), "apple", 5)
	if err != nil || len(rs) != 1 || !strings.Contains(rs[0].Text, "apple preserves") {
		t.Errorf("SearchBM25 after rollback = %+v (%v), want the apple passage", rs, err)
	}
}

func TestMigrate_RefusesNonDenseIDs(t *testing.T) {
	dir, name, _ := migrateFixture(t)
	base := filepath.Join(dir, name, name)

	// Deleting one source leaves an ID gap in the bbolt store.
	pm := NewPassageManager(base)
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.RemoveBySource([]string{"doc00.md"}); err != nil {
		t.Fatal(err)
	}
	if err := pm.Close(); err != nil {
		t.Fatal(err)
	}

	err := MigrateIndex(dir, name, MigrateOptions{})
	if err == nil || !strings.Contains(err.Error(), "non-contiguous passage IDs") {
		t.Fatalf("MigrateIndex on a gapped store = %v, want a non-contiguous-ID refusal", err)
	}
	if StoreFormatOf(base) != 1 {
		t.Error("a refused migration switched the format anyway")
	}
	if _, err := os.Stat(base + ".db"); !os.IsNotExist(err) {
		t.Errorf("a refused migration created a format-2 store: %v", err)
	}
	if _, err := os.Stat(base + ".db.tmp"); !os.IsNotExist(err) {
		t.Errorf("a refused migration left a staging file: %v", err)
	}
	// The format-1 store is still readable and intact.
	pm2 := NewReadOnlyPassageManager(base)
	if err := pm2.Load(); err != nil {
		t.Fatalf("format-1 store unreadable after refusal: %v", err)
	}
	defer pm2.Close()
	if n := pm2.Count(); n != len(migrateWords)-1 {
		t.Errorf("passage count after refusal = %d, want %d", n, len(migrateWords)-1)
	}
}

func TestMigrate_GuardFileBlocksOldReader(t *testing.T) {
	dir, name, _ := migrateFixture(t)
	base := filepath.Join(dir, name, name)
	if err := MigrateIndex(dir, name, MigrateOptions{}); err != nil {
		t.Fatalf("MigrateIndex: %v", err)
	}

	for _, path := range []string{base + ".passages.db", base + ".manifest.json"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("guard missing: %v", err)
		}
		if info.Size() == 0 {
			t.Fatalf("guard %s is empty; bbolt would initialize an empty file instead of failing", path)
		}
		size, sum := info.Size(), fileSHA(t, path)

		// Both format-1 bbolt openers must fail on the guard...
		pm := NewPassageManager(base)
		if err := pm.Load(); err == nil {
			pm.Close()
			t.Fatalf("writable bbolt opener accepted the guard at %s", path)
		}
		ro := NewReadOnlyPassageManager(base)
		if err := ro.Load(); err == nil {
			ro.Close()
			t.Fatalf("read-only bbolt opener accepted the guard at %s", path)
		}

		// ...and the guard is unchanged afterwards (size and sha256).
		info2, err := os.Stat(path)
		if err != nil {
			t.Fatalf("guard vanished: %v", err)
		}
		if info2.Size() != size {
			t.Errorf("guard %s size changed after failed opens: %d -> %d", path, size, info2.Size())
		}
		if fileSHA(t, path) != sum {
			t.Errorf("guard %s bytes changed after failed opens", path)
		}
	}

	guard, err := os.ReadFile(base + ".passages.db")
	if err != nil || !strings.HasPrefix(string(guard), storeGuardPrefix) {
		t.Errorf("guard does not start with %q: %q (%v)", storeGuardPrefix, guard, err)
	}
	// The format-2 store itself is intact.
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if n, err := st.Count(context.Background()); err != nil || n != len(migrateWords) {
		t.Errorf("format-2 count = %d (%v), want %d", n, err, len(migrateWords))
	}
}

// A natively built format-2 index has nothing to migrate: the switch must be
// refused without touching the store (the format-2 side of the migration).
func TestMigrate_RefusesFormat2Index(t *testing.T) {
	initTestHNSW()
	t.Setenv("GLEANN_STORE_FORMAT", "2")
	dir := t.TempDir()
	b, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.Build(context.Background(), "idx", []Item{{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md"}}}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	sum := fileSHA(t, base+".db")

	err = MigrateIndex(dir, "idx", MigrateOptions{})
	if err == nil || !strings.Contains(err.Error(), "already store format 2") {
		t.Fatalf("MigrateIndex on a format-2 index = %v, want an already-format-2 refusal", err)
	}
	if fileSHA(t, base+".db") != sum {
		t.Error("a refused migration modified the format-2 store")
	}
	// There is no preserved format-1 store, so rollback must refuse too.
	if err := RollbackMigration(dir, "idx"); err == nil || !strings.Contains(err.Error(), "no preserved format-1 store") {
		t.Fatalf("RollbackMigration without a .v1 store = %v, want a refusal", err)
	}
}

func TestMigrate_FailsFastWhenLocked(t *testing.T) {
	dir, name, _ := migrateFixture(t)
	base := filepath.Join(dir, name, name)
	lock, err := store.AcquireLock(filepath.Join(dir, name+".lock"))
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer lock.Release()

	err = MigrateIndex(dir, name, MigrateOptions{})
	if err == nil || !strings.Contains(err.Error(), "held by another process") {
		t.Fatalf("MigrateIndex with the lock held = %v, want a readable lock message", err)
	}
	if StoreFormatOf(base) != 1 {
		t.Error("a locked-out migration switched the format anyway")
	}
	// Rollback and cleanup take the same lock.
	if err := RollbackMigration(dir, name); err == nil || !strings.Contains(err.Error(), "held by another process") {
		t.Errorf("RollbackMigration with the lock held = %v, want the lock message", err)
	}
	if err := CleanupMigration(dir, name); err == nil || !strings.Contains(err.Error(), "held by another process") {
		t.Errorf("CleanupMigration with the lock held = %v, want the lock message", err)
	}
}
