package gleann

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/internal/testutil/format1"
)

// This file covers `gleann index migrate` for v1.20 (RFC 004 B3): an index
// written in the v1.9 layout by a test-only writer (internal/testutil/format1,
// no committed *.db fixtures) is converted with its vectors reused, gaps in
// the passage IDs are renumbered, every interrupted state is completed by a
// re-run, and the old files stay until --cleanup. Every other operation on
// an unmigrated index is refused with the migration hint.

// migrateWords gives every fixture passage a keyword of its own, so each
// lexical query matches exactly one passage.
var migrateWords = []string{
	"apple", "banana", "cherry", "damson", "elderberry", "fig",
	"grape", "hazelnut", "kiwi", "lemon", "mango", "nectarine",
}

// oldFixture writes a v1.9 index whose passages have the given IDs (gaps
// allowed). Like v1.9 after an in-place removal, the vector file holds a
// vector for every ID up to the highest one, so a gap is a vector without a
// passage. The test embedder's vector depends only on the text length, and
// every text has a distinct length, so a query equal to a passage text finds
// exactly that passage.
func oldFixture(t *testing.T, dir, name string, ids []int64, withVectors, withGraph bool) []format1.Passage {
	t.Helper()
	initTestHNSW()
	docs := t.TempDir()
	var ps []format1.Passage
	text := func(i int) string {
		w := migrateWords[i%len(migrateWords)]
		return fmt.Sprintf("%s preserves and %s jam recipe number %d%s", w, w, i, strings.Repeat(".", i))
	}
	for i, id := range ids {
		src := fmt.Sprintf("doc%02d.md", i)
		if err := os.WriteFile(filepath.Join(docs, src), []byte(text(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		ps = append(ps, format1.Passage{ID: id, Text: text(i), Metadata: map[string]any{"source": src, "chunk_index": 0}})
	}
	meta := map[string]any{"backend": "hnsw", "source_dir": docs, "tags": []string{"keep"}}
	var vecFile []byte
	if withVectors {
		emb := &testEmbedder{dim: 8}
		maxID := ids[len(ids)-1]
		texts := make([]string, maxID+1)
		for j := range texts {
			texts[j] = fmt.Sprintf("removed passage %d %s", j, strings.Repeat("#", 200+j))
		}
		for _, p := range ps {
			texts[p.ID] = p.Text
		}
		vecs, err := emb.Compute(context.Background(), texts)
		if err != nil {
			t.Fatal(err)
		}
		vecFile, err = (&testHNSWFactory{}).NewBuilder(Config{IndexDir: dir}).Build(context.Background(), vecs)
		if err != nil {
			t.Fatal(err)
		}
		meta["embedding_model"] = emb.ModelName()
		meta["dimensions"] = 8
		meta["vector_ready"] = true
	} else {
		meta["lexical_only"] = true
	}
	if err := format1.Write(format1.Index{
		Dir: dir, Name: name, Passages: ps, Meta: meta, VectorFile: vecFile,
		LexSidecar: true, Graph: withGraph,
	}); err != nil {
		t.Fatal(err)
	}
	if got := DetectStoreFormat(filepath.Join(dir, name, name)); got != StoreOld {
		t.Fatalf("fixture = %v, want StoreOld", got)
	}
	return ps
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// checkSearches loads the index and checks that every passage is its own
// first vector hit and its own first lexical hit.
func checkSearches(t *testing.T, dir, name string, ps []format1.Passage, vectors bool) {
	t.Helper()
	ctx := context.Background()
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err := s.Load(ctx, name); err != nil {
		t.Fatalf("Load after migrate: %v", err)
	}
	defer s.Close()
	if s.VectorAvailable() != vectors {
		t.Fatalf("VectorAvailable = %v, want %v", s.VectorAvailable(), vectors)
	}
	for _, p := range ps {
		src := p.Metadata["source"]
		if vectors {
			rs, err := s.Search(ctx, p.Text, WithTopK(1), WithHybridAlpha(1.0))
			if err != nil || len(rs) == 0 {
				t.Fatalf("vector search %q: %v (%d hits)", p.Text, err, len(rs))
			}
			if rs[0].Text != p.Text || rs[0].Metadata["source"] != src {
				t.Errorf("vector search for %s: got %q (%v)", src, rs[0].Text, rs[0].Metadata["source"])
			}
		}
		w := strings.Fields(p.Text)[0]
		rs, err := s.SearchBM25(ctx, w+" preserves", 1)
		if err != nil || len(rs) == 0 {
			t.Fatalf("lexical search %q: %v (%d hits)", w, err, len(rs))
		}
		if rs[0].Metadata["source"] != src {
			t.Errorf("lexical search for %s: got %v", src, rs[0].Metadata["source"])
		}
	}
	if h, err := CheckIndexHealth(dir, name); err != nil || h.NeedsRepair() {
		t.Fatalf("health after migrate: %+v (%v)", h, err)
	}
}

func TestMigrate_DenseReusesVectorsAndKeepsLeftovers(t *testing.T) {
	dir, name := t.TempDir(), "idx"
	ps := oldFixture(t, dir, name, []int64{0, 1, 2, 3, 4, 5}, true, true)
	base := filepath.Join(dir, name, name)
	vecBefore := fileSHA(t, base+".index")

	res, err := MigrateIndex(dir, name, MigrateOptions{})
	if err != nil {
		t.Fatalf("MigrateIndex: %v", err)
	}
	if res.Passages != len(ps) || res.Renumbered || !res.HadGraph {
		t.Fatalf("result = %+v, want %d passages, not renumbered, had graph", res, len(ps))
	}
	if DetectStoreFormat(base) != StoreV2 {
		t.Fatal("index is not format 2 after migrate")
	}
	if got := fileSHA(t, base+".index"); got != vecBefore {
		t.Fatal("a dense migration must reuse the vector file unchanged")
	}
	m, _ := GetIndexMeta(dir, name)
	if m.StoreFormat != 2 || m.NumPassages != len(ps) || len(m.Tags) != 1 {
		t.Fatalf("meta after migrate: %+v", m)
	}
	checkSearches(t, dir, name, ps, true)

	if _, err := MigrateIndex(dir, name, MigrateOptions{}); !errors.Is(err, ErrAlreadyFormat2) {
		t.Fatalf("second migrate = %v, want ErrAlreadyFormat2", err)
	}

	left := MigrationLeftovers(dir, name)
	for _, want := range []string{base + ".passages.db", base + ".lex.db", filepath.Join(dir, name+"_graph"), filepath.Join(dir, name+"_graph_file_hashes.db")} {
		found := false
		for _, l := range left {
			found = found || l == want
		}
		if !found {
			t.Errorf("leftover %s not listed in %v", want, left)
		}
	}
	removed, err := CleanupMigration(dir, name)
	if err != nil || len(removed) != len(left) {
		t.Fatalf("CleanupMigration = %v, %v; want the %d leftovers", removed, err, len(left))
	}
	if l := MigrationLeftovers(dir, name); len(l) != 0 {
		t.Fatalf("leftovers after cleanup: %v", l)
	}
	checkSearches(t, dir, name, ps, true)
}

func TestMigrate_RenumbersGapsAndRebuildsVectors(t *testing.T) {
	dir, name := t.TempDir(), "gaps"
	ps := oldFixture(t, dir, name, []int64{0, 1, 3, 4, 7, 9}, true, false)
	base := filepath.Join(dir, name, name)

	res, err := MigrateIndex(dir, name, MigrateOptions{})
	if err != nil {
		t.Fatalf("MigrateIndex: %v", err)
	}
	if !res.Renumbered || res.HadGraph {
		t.Fatalf("result = %+v, want renumbered, no graph", res)
	}
	h, err := CheckIndexHealth(dir, name)
	if err != nil || !h.Dense || h.MaxID != int64(len(ps)-1) {
		t.Fatalf("health = %+v (%v), want dense slots 0..%d", h, err, len(ps)-1)
	}
	if _, err := os.Stat(base + ".index.v1"); err != nil {
		t.Fatalf("the old vector file must be kept as .index.v1: %v", err)
	}
	checkSearches(t, dir, name, ps, true)
}

func TestMigrate_LexicalOnlyIndex(t *testing.T) {
	dir, name := t.TempDir(), "lex"
	ps := oldFixture(t, dir, name, []int64{0, 2, 5}, false, false)
	if _, err := MigrateIndex(dir, name, MigrateOptions{}); err != nil {
		t.Fatalf("MigrateIndex: %v", err)
	}
	checkSearches(t, dir, name, ps, false)
}

// Every interrupted state of the switch is completed or redone by a re-run.
func TestMigrate_CrashStatesRecoverable(t *testing.T) {
	for _, point := range []string{migrateAfterIndexAside, migrateAfterIndexNew, migrateAfterDBRename} {
		t.Run(point, func(t *testing.T) {
			dir, name := t.TempDir(), "crash"
			ps := oldFixture(t, dir, name, []int64{0, 2, 3, 6}, true, false)
			crash := errors.New("injected crash")
			_, err := MigrateIndex(dir, name, MigrateOptions{crashHook: func(p string) error {
				if p == point {
					return crash
				}
				return nil
			}})
			if !errors.Is(err, crash) {
				t.Fatalf("MigrateIndex = %v, want the injected crash", err)
			}
			// Until the re-run, nothing but migrate reads the index.
			if point != migrateAfterDBRename {
				if got := DetectStoreFormat(filepath.Join(dir, name, name)); got != StoreOld {
					t.Fatalf("after a crash at %s: %v, want StoreOld", point, got)
				}
			}
			if _, err := MigrateIndex(dir, name, MigrateOptions{}); err != nil {
				t.Fatalf("re-run after a crash at %s: %v", point, err)
			}
			checkSearches(t, dir, name, ps, true)
		})
	}
}

func TestMigrate_FailsFastWhenLocked(t *testing.T) {
	dir, name := t.TempDir(), "locked"
	oldFixture(t, dir, name, []int64{0, 1}, true, false)
	lock, err := store.AcquireLock(filepath.Join(dir, name+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := MigrateIndex(dir, name, MigrateOptions{}); err == nil {
		t.Fatal("MigrateIndex must fail while another process holds the index lock")
	}
	if DetectStoreFormat(filepath.Join(dir, name, name)) != StoreOld {
		t.Fatal("a refused migration must leave the index as it was")
	}
}

// The v1.9 migration left guard files and .v1 copies; the index is format 2
// and those files are leftovers that --cleanup deletes.
func TestMigrate_V19MigratedIndexLeftovers(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), "v19", f2Items()); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "v19", "v19")
	guard := []byte(storeGuardPrefix + ": this index uses the SQLite record store.\n")
	for _, f := range []string{".passages.db", ".manifest.json"} {
		if err := os.WriteFile(base+f, guard, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+f+".v1", []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if DetectStoreFormat(base) != StoreV2 {
		t.Fatal("a v1.9-migrated index is format 2")
	}
	if l := MigrationLeftovers(dir, "v19"); len(l) != 4 {
		t.Fatalf("leftovers = %v, want the 2 guards and 2 .v1 copies", l)
	}
	if _, err := CleanupMigration(dir, "v19"); err != nil {
		t.Fatal(err)
	}
	if l := MigrationLeftovers(dir, "v19"); len(l) != 0 {
		t.Fatalf("leftovers after cleanup: %v", l)
	}
}

// Every operation but migrate refuses an old-format index with the hint.
func TestOldFormatRefused(t *testing.T) {
	dir, name := t.TempDir(), "old"
	oldFixture(t, dir, name, []int64{0, 1, 2}, true, true)
	want := "index old uses the old store format; run: gleann index migrate old"
	check := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, ErrOldStoreFormat) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s = %v, want %q", what, err, want)
		}
	}
	ctx := context.Background()
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	check("Load", s.Load(ctx, name))
	b, _ := newFormat2Builder(t, dir, 8)
	check("Build", b.Build(ctx, name, f2Items()))
	check("AddToIndex", b.AddToIndex(ctx, name, f2Items()))
	check("UpdateIndex", b.UpdateIndex(ctx, name, nil, []string{"doc00.md"}))
	_, err := b.Compact(ctx, name)
	check("Compact", err)
	_, err = LoadIndexManifest(dir, name)
	check("LoadIndexManifest", err)
	check("NoCodeGraphError", NoCodeGraphError(dir, name))
	if _, err := CleanupMigration(dir, name); err == nil {
		t.Error("CleanupMigration must refuse an unmigrated index")
	}
	h, err := CheckIndexHealth(dir, name)
	if err != nil || len(h.Problems) != 1 || h.Problems[0] != want {
		t.Errorf("CheckIndexHealth = %+v (%v), want the refusal as the problem", h, err)
	}
	if DetectStoreFormat(filepath.Join(dir, name, name)) != StoreOld {
		t.Fatal("a refused operation changed the index")
	}
}
