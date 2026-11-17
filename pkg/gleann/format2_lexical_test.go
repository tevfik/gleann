package gleann

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// Counterparts of the format-1 sidecar tests (lexical_leg_test.go): a format-2
// index keeps its lexical rows in <name>.db itself, so there is no sidecar to
// build, lock or swap. The same load-time guarantees must hold through the
// store's own lex table (RFC 003 section 8 step 4).

// buildFormat2Lexical builds a lexical-only format-2 index (no vectors, so
// Load never needs a vector backend) and returns its builder.
func buildFormat2Lexical(t *testing.T, dir string, items []Item) *LeannBuilder {
	t.Helper()
	initTestHNSW()
	t.Setenv("GLEANN_STORE_FORMAT", "2")
	b, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.BuildLexicalOnly("idx", items); err != nil {
		t.Fatalf("BuildLexicalOnly: %v", err)
	}
	return b
}

func format2LexItems() []Item {
	return []Item{
		{Text: "apple pie recipe", Metadata: map[string]any{"source": "recipes.md"}},
		{Text: "banana bread recipe", Metadata: map[string]any{"source": "bread.md"}},
		{Text: "cherry tart dessert", Metadata: map[string]any{"source": "tart.md"}},
	}
}

// The persisted lexical leg of a format-2 index is its own lex table in both
// GLEANN_LEXICAL modes: no sidecar is created, Load skips the passage scan,
// a wiped lex table is rebuilt once from the stored texts, and a rebuild after
// a delete keeps the leg in step with the passages.
func TestFormat2_LexicalLeg(t *testing.T) {
	ctx := context.Background()
	items := format2LexItems()

	for _, mode := range []string{"memory", "fts5"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("GLEANN_LEXICAL", mode)
			dir := t.TempDir()
			buildFormat2Lexical(t, dir, items)
			base := filepath.Join(dir, "idx", "idx")
			if _, err := os.Stat(base + ".lex.db"); !os.IsNotExist(err) {
				t.Fatalf("format-2 build created a format-1 sidecar: %v", err)
			}

			s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
			if err := s.Load(ctx, "idx"); err != nil {
				t.Fatalf("Load: %v", err)
			}
			if _, ok := s.scorer.(*FTSScorer); !ok {
				t.Fatalf("scorer = %T, want the persisted *FTSScorer", s.scorer)
			}
			rs, err := s.SearchBM25(ctx, "apple", 5)
			if err != nil {
				t.Fatalf("SearchBM25: %v", err)
			}
			if len(rs) == 0 || rs[0].Text != "apple pie recipe" {
				t.Fatalf("top BM25 result = %+v, want the apple passage", rs)
			}
			if sp, ok := s.passages.(*storePassages); ok && sp.cached != nil {
				t.Error("Load scanned passages for the persisted scorer; want no scan")
			}
			s.Close()
		})
	}

	t.Run("lazy-rebuild-and-delete", func(t *testing.T) {
		dir := t.TempDir()
		b := buildFormat2Lexical(t, dir, items)
		base := filepath.Join(dir, "idx", "idx")

		// Wipe the lex rows and the analyzer stamp: Load must rebuild the
		// table once from the stored texts (the format-2 lazy path).
		db, err := sql.Open("sqlite", base+".db")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DELETE FROM lex`); err != nil {
			t.Fatalf("wipe lex rows: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM meta WHERE key = 'analyzer'`); err != nil {
			t.Fatalf("clear analyzer stamp: %v", err)
		}
		db.Close()

		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatalf("Load with a wiped lex table: %v", err)
		}
		if _, ok := s.scorer.(*FTSScorer); !ok {
			t.Fatalf("scorer = %T, want the rebuilt *FTSScorer", s.scorer)
		}
		if rs, err := s.SearchBM25(ctx, "banana", 5); err != nil || len(rs) == 0 || rs[0].Text != "banana bread recipe" {
			t.Fatalf("SearchBM25 after the lazy rebuild = %+v (%v), want the banana passage", rs, err)
		}
		s.Close()
		st, err := store.Open(base+".db", store.Options{})
		if err != nil {
			t.Fatal(err)
		}
		an, err := st.Meta(ctx, "analyzer")
		st.Close()
		if err != nil || an == "" {
			t.Errorf("analyzer stamp not restored by the rebuild: %q (%v)", an, err)
		}

		// Delete a source and rebuild: the deleted text must not stay
		// searchable through the old lex rows.
		if err := b.BuildLexicalOnly("idx", items[:2]); err != nil {
			t.Fatalf("rebuild after delete: %v", err)
		}
		s2 := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
		if err := s2.Load(ctx, "idx"); err != nil {
			t.Fatal(err)
		}
		defer s2.Close()
		if rs, err := s2.SearchBM25(ctx, "cherry", 5); err != nil || len(rs) != 0 {
			t.Fatalf("deleted source still searchable: %+v (%v)", rs, err)
		}
	})
}

// When the lex table cannot be made fresh (here: it is damaged and the rebuild
// fails), Load does not fail: it serves this load with the in-memory BM25
// adapter, the format-2 counterpart of the busy-sidecar fallback.
func TestFormat2_LoadFallsBackWhenLexRebuildFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	buildFormat2Lexical(t, dir, format2LexItems()[:1])
	base := filepath.Join(dir, "idx", "idx")

	// Drop the lex table and stale the analyzer stamp so Load attempts a
	// rebuild, which must fail on the missing table.
	db, err := sql.Open("sqlite", base+".db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE lex_vocab`); err != nil {
		t.Fatalf("drop lex_vocab: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE lex`); err != nil {
		t.Fatalf("drop lex: %v", err)
	}
	if _, err := db.Exec(`UPDATE meta SET value = 'damaged' WHERE key = 'analyzer'`); err != nil {
		t.Fatalf("stale analyzer stamp: %v", err)
	}
	db.Close()

	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load with a damaged lex table: %v", err)
	}
	defer s.Close()
	if _, ok := s.scorer.(*BM25Adapter); !ok {
		t.Fatalf("scorer = %T, want the in-memory *BM25Adapter", s.scorer)
	}
	if rs, err := s.SearchBM25(ctx, "apple", 5); err != nil || len(rs) == 0 {
		t.Fatalf("search during fallback = %v, %v", rs, err)
	}
}

// A sync writes through its own store handle while a searcher holds the index
// open: the open scorer sees the new passages after the commit and the deleted
// ones disappear, without reloading (the format-2 counterpart of the in-place
// sidecar rebuild).
func TestFormat2_UpdateWhileSearcherOpen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	if err := b.Build(ctx, "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok := s.scorer.(*FTSScorer); !ok {
		t.Fatalf("scorer = %T, want *FTSScorer", s.scorer)
	}

	newItems := []Item{{Text: "walnut cake recipe", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}}}
	if err := b.UpdateIndex(ctx, "idx", newItems, nil); err != nil {
		t.Fatalf("update with an open searcher: %v", err)
	}
	ids, _ := s.scorer.(*FTSScorer).TopK("walnut", 5)
	if len(ids) != 1 {
		t.Fatalf("open searcher after the update: %d hits, want 1", len(ids))
	}
	rs, err := s.SearchBM25(ctx, "walnut", 5)
	if err != nil || len(rs) != 1 || rs[0].Text != "walnut cake recipe" {
		t.Fatalf("SearchBM25 through the open searcher = %+v (%v), want the new passage", rs, err)
	}

	if err := b.UpdateIndex(ctx, "idx", nil, []string{"b.md"}); err != nil {
		t.Fatalf("removal with an open searcher: %v", err)
	}
	if rs, err := s.SearchBM25(ctx, "banana", 5); err != nil || len(rs) != 0 {
		t.Fatalf("removed source still searchable through the open searcher: %+v (%v)", rs, err)
	}
}
