package gleann

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/store"
)

// countingPersistedScorer is a PersistedScorer that records how many documents
// Load feeds it. A persisted scorer must receive none (RFC 003 section 7.1,
// row "Searcher").
type countingPersistedScorer struct{ adds int }

func (c *countingPersistedScorer) Score(query string, passages []Passage) []float32 {
	return make([]float32, len(passages))
}
func (c *countingPersistedScorer) AddDocuments(passages []Passage) { c.adds += len(passages) }
func (c *countingPersistedScorer) Persisted() bool                 { return true }

// buildLexicalIndex writes a small lexical-only index (no vectors, so Load
// never needs a vector backend) and returns its base path.
func buildLexicalIndex(t *testing.T, dir, name string, items []Item) {
	t.Helper()
	b := &LeannBuilder{config: Config{IndexDir: dir, Backend: "hnsw"}}
	if err := b.BuildLexicalOnly(name, items); err != nil {
		t.Fatalf("BuildLexicalOnly: %v", err)
	}
}

func TestLoadPersistedScorerSkipsPassageScan(t *testing.T) {
	dir := t.TempDir()
	items := []Item{
		{Text: "alpha beta", Metadata: map[string]any{"source": "a.md"}},
		{Text: "gamma delta", Metadata: map[string]any{"source": "b.md"}},
	}
	buildLexicalIndex(t, dir, "idx", items)

	s := NewSearcher(Config{IndexDir: dir}, nil)
	scorer := &countingPersistedScorer{}
	s.SetScorer(scorer)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	if scorer.adds != 0 {
		t.Errorf("Load called AddDocuments with %d passages for a persisted scorer, want 0", scorer.adds)
	}
	// The small-corpus path would have cached every passage via
	// LoadAllWithLimit; a persisted scorer must skip that scan entirely.
	if pm, ok := s.passages.(*PassageManager); ok && pm.cached != nil {
		t.Errorf("Load iterated/cached passages for a persisted scorer; want no scan")
	}
}

func TestLexicalLegModes(t *testing.T) {
	// The sidecar (<name>.lex.db) is a format-1 mechanism; a format-2 index
	// keeps its lexical rows in <name>.db (covered by TestFormat2_LexicalLeg).
	t.Setenv("GLEANN_STORE_FORMAT", "1")
	initMockBackend()
	items := []Item{
		{Text: "apple pie recipe", Metadata: map[string]any{"source": "recipes.md"}},
		{Text: "banana bread recipe", Metadata: map[string]any{"source": "bread.md"}},
		{Text: "cherry tart dessert", Metadata: map[string]any{"source": "tart.md"}},
	}
	ctx := context.Background()

	for _, mode := range []string{"memory", "fts5"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("GLEANN_LEXICAL", mode)
			dir := t.TempDir()
			cfg := Config{IndexDir: dir, Backend: "mock-hybrid-backend"}

			builder, err := NewBuilder(cfg, nil)
			if err != nil {
				t.Fatalf("NewBuilder: %v", err)
			}
			if err := builder.BuildLexicalOnly("idx", items); err != nil {
				t.Fatalf("BuildLexicalOnly: %v", err)
			}

			lexPath := lexicalSidecarPath(dir, "idx")
			if mode == "fts5" {
				if _, err := os.Stat(lexPath); err != nil {
					t.Fatalf("sidecar not built at index build time: %v", err)
				}
				// Force the lazy Load path: remove the sidecar; Load must rebuild it.
				removeLexicalFiles(lexPath)
			} else if _, err := os.Stat(lexPath); !os.IsNotExist(err) {
				t.Fatalf("memory mode created a sidecar at %s", lexPath)
			}

			searcher := NewSearcher(cfg, nil)
			if err := searcher.Load(ctx, "idx"); err != nil {
				t.Fatalf("Load: %v", err)
			}

			rs, err := searcher.SearchBM25(ctx, "apple", 5)
			if err != nil {
				t.Fatalf("SearchBM25: %v", err)
			}
			if len(rs) == 0 || !strings.Contains(rs[0].Text, "apple") {
				t.Fatalf("top BM25 result = %+v, want the apple passage", rs)
			}

			if mode == "fts5" {
				if _, ok := searcher.scorer.(*FTSScorer); !ok {
					t.Fatalf("scorer = %T, want *FTSScorer", searcher.scorer)
				}
				if _, err := os.Stat(lexPath); err != nil {
					t.Fatalf("Load did not rebuild the missing sidecar: %v", err)
				}
				if pm, ok := searcher.passages.(*PassageManager); ok && pm.cached != nil {
					t.Errorf("Load scanned passages for the persisted scorer; want no AddDocuments build")
				}
			}
			searcher.Close()

			if mode == "fts5" {
				// Delete a source and rebuild (the lexical sync path): the sidecar
				// must be rebuilt so its passage count matches the index again.
				if err := builder.BuildLexicalOnly("idx", items[:2]); err != nil {
					t.Fatalf("rebuild after delete: %v", err)
				}
				pm := NewReadOnlyPassageManager(filepath.Join(dir, "idx", "idx"))
				if err := pm.Load(); err != nil {
					t.Fatal(err)
				}
				want := pm.Count()
				pm.Close()
				assertSidecarCount(t, lexPath, want)

				s2 := NewSearcher(cfg, nil)
				if err := s2.Load(ctx, "idx"); err != nil {
					t.Fatal(err)
				}
				rs, err := s2.SearchBM25(ctx, "cherry", 5)
				if err != nil {
					t.Fatal(err)
				}
				if len(rs) != 0 {
					t.Fatalf("deleted source still searchable: %+v", rs)
				}
				s2.Close()

				// The sidecar lives inside <name>/, so RemoveIndex's removal of
				// the index directory deletes it and its -wal/-shm companions.
				if err := RemoveIndex(dir, "idx"); err != nil {
					t.Fatalf("RemoveIndex: %v", err)
				}
				if _, err := os.Stat(lexPath); !os.IsNotExist(err) {
					t.Fatalf("RemoveIndex left the sidecar behind: %v", err)
				}
			}
		})
	}
}

func TestSidecarRebuiltAfterUpdate(t *testing.T) {
	dir := t.TempDir()
	builder, _ := newTestBuilder(t, dir, 8)
	builder.lexicalMode = "fts5"
	ctx := context.Background()

	items := []Item{
		{Text: "alpha from file-a", Metadata: map[string]any{"source": "a.md"}},
		{Text: "beta from file-a", Metadata: map[string]any{"source": "a.md"}},
		{Text: "gamma from file-b", Metadata: map[string]any{"source": "b.md"}},
	}
	if err := builder.Build(ctx, "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	lexPath := lexicalSidecarPath(dir, "idx")
	assertSidecarCount(t, lexPath, 3)

	// Removing a source compacts the index; the sidecar must be rebuilt to the
	// new passage count (full rebuild is acceptable in this step).
	if err := builder.UpdateIndex(ctx, "idx", nil, []string{"a.md"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
	assertSidecarCount(t, lexPath, 1)

	pm := NewReadOnlyPassageManager(filepath.Join(dir, "idx", "idx"))
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	n := pm.Count()
	pm.Close()
	if n != 1 {
		t.Fatalf("passage count = %d, want 1", n)
	}
}

// assertSidecarCount checks the sidecar's recorded passage_count meta key.
func assertSidecarCount(t *testing.T, lexPath string, want int) {
	t.Helper()
	st, err := store.Open(lexPath, store.Options{})
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	defer st.Close()
	got, err := st.Meta(context.Background(), "passage_count")
	if err != nil {
		t.Fatalf("read passage_count: %v", err)
	}
	if got != strconv.Itoa(want) {
		t.Fatalf("sidecar passage_count = %s, want %d", got, want)
	}
}

// An index edited while the switch was off, with the same passage count,
// must not be searched through the old sidecar.
func TestSidecarStaleAfterEditWithSameCount(t *testing.T) {
	initMockBackend()
	ctx := context.Background()
	dir := t.TempDir()
	cfg := Config{IndexDir: dir, Backend: "mock-hybrid-backend"}

	t.Setenv("GLEANN_LEXICAL", "fts5")
	b, err := NewBuilder(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BuildLexicalOnly("idx", []Item{{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md"}}}); err != nil {
		t.Fatal(err)
	}

	// Rebuild with the switch off: same count, different text, no sidecar update.
	t.Setenv("GLEANN_LEXICAL", "memory")
	b2, err := NewBuilder(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // a visible modification time step on coarse file systems
	if err := b2.BuildLexicalOnly("idx", []Item{{Text: "walnut cake recipe", Metadata: map[string]any{"source": "a.md"}}}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GLEANN_LEXICAL", "fts5")
	s := NewSearcher(cfg, nil)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rs, err := s.SearchBM25(ctx, "walnut", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) == 0 {
		t.Fatal("search for the new text found nothing: the stale sidecar was used")
	}
}

// When another process holds the sidecar's rebuild lock, Load does not wait
// or fail: it serves this load with the in-memory BM25 adapter.
func TestLoadFallsBackWhenSidecarBusy(t *testing.T) {
	// The sidecar rebuild lock is format-1 machinery; the format-2 fallback
	// is covered by TestFormat2_LoadFallsBackWhenLexRebuildFails.
	t.Setenv("GLEANN_STORE_FORMAT", "1")
	initMockBackend()
	ctx := context.Background()
	dir := t.TempDir()
	cfg := Config{IndexDir: dir, Backend: "mock-hybrid-backend"}
	t.Setenv("GLEANN_LEXICAL", "memory")
	b, err := NewBuilder(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BuildLexicalOnly("idx", []Item{{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md"}}}); err != nil {
		t.Fatal(err)
	}
	lock, err := store.AcquireLock(lexicalLockPath(dir, "idx"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	t.Setenv("GLEANN_LEXICAL", "fts5")
	s := NewSearcher(cfg, nil)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load with a busy sidecar lock: %v", err)
	}
	defer s.Close()
	if _, ok := s.scorer.(*BM25Adapter); !ok {
		t.Fatalf("scorer = %T, want the in-memory *BM25Adapter", s.scorer)
	}
	if rs, err := s.SearchBM25(ctx, "apple", 5); err != nil || len(rs) == 0 {
		t.Fatalf("search during fallback = %v, %v", rs, err)
	}
}

// A sync rebuilds the sidecar in place while a searcher has it open: the
// rebuild succeeds (no file swap that Windows would refuse) and the open
// searcher sees the new passages after the commit without reloading.
func TestSidecarRebuildWhileSearcherOpen(t *testing.T) {
	// In-place sidecar rebuilds are format-1 machinery; the format-2 case is
	// covered by TestFormat2_UpdateWhileSearcherOpen.
	t.Setenv("GLEANN_STORE_FORMAT", "1")
	initMockBackend()
	ctx := context.Background()
	dir := t.TempDir()
	cfg := Config{IndexDir: dir, Backend: "mock-hybrid-backend"}
	t.Setenv("GLEANN_LEXICAL", "fts5")
	b, err := NewBuilder(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BuildLexicalOnly("idx", []Item{{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md"}}}); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(cfg, nil)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok := s.scorer.(*FTSScorer); !ok {
		t.Fatalf("scorer = %T, want *FTSScorer", s.scorer)
	}

	if err := buildLexicalSidecar(ctx, dir, "idx"); err != nil {
		t.Fatalf("rebuild with an open searcher: %v", err)
	}
	ids, _ := s.scorer.(*FTSScorer).TopK("apple", 5)
	if len(ids) != 1 {
		t.Fatalf("open searcher after in-place rebuild: %d hits, want 1", len(ids))
	}
}

func TestParseLexicalOpts(t *testing.T) {
	cases := []struct {
		input     string
		wantAND   bool
		wantDFCut float64
	}{
		{"", false, 0},
		{"and", true, 0},
		{"and-first", true, 0},
		{"and_first", true, 0},
		{"df=0.05", false, 0.05},
		{"and,df=0.1", true, 0.1},
		{"and-first, df=0.2", true, 0.2},
	}
	for _, tc := range cases {
		got := parseLexicalOpts(tc.input)
		if got.ANDFirst != tc.wantAND || got.DocFreqCut != tc.wantDFCut {
			t.Errorf("parseLexicalOpts(%q) = %+v, want ANDFirst=%v DocFreqCut=%v",
				tc.input, got, tc.wantAND, tc.wantDFCut)
		}
	}
}
