package gleann

import (
	"context"
	"path/filepath"
	"testing"
)

// Slotless passages (vec_slot NULL: the lexical-only staging of a build, RFC
// 003 section 3.6) share the searcher's ID space with vector slots. Their
// visible ID is the NEGATIVE internal rowid, so Get/GetBatch and the FTS
// scorer address them correctly instead of looking up a wrong vec_slot or
// dropping the hit (RFC 003 section 3.4).

// A lexical-only format-2 index must return BM25 hits whose IDs Get resolves
// to the hit's own text, and every passage must be retrievable by its ID.
func TestFormat2_SlotlessSearchAndGet(t *testing.T) {
	ctx := context.Background()
	items := format2LexItems()
	dir := t.TempDir()
	buildFormat2Lexical(t, dir, items)
	base := filepath.Join(dir, "idx", "idx")

	// The staging really is slotless: every row has vec_slot NULL.
	for text, p := range f2Rows(t, base) {
		if p.VecSlot != nil {
			t.Fatalf("passage %q has slot %d in a lexical-only build, want NULL", text, *p.VecSlot)
		}
	}

	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	// Every passage is visible under a negative ID, and Get returns its text.
	all := s.passages.All()
	if len(all) != len(items) {
		t.Fatalf("All() returned %d passages, want %d", len(all), len(items))
	}
	ids := make([]int64, len(all))
	for i, p := range all {
		if p.ID >= 0 {
			t.Errorf("slotless passage %q has visible ID %d, want a negative rowid", p.Text, p.ID)
		}
		got, err := s.passages.Get(p.ID)
		if err != nil || got.Text != p.Text {
			t.Errorf("Get(%d) = %+v (%v), want %q", p.ID, got, err, p.Text)
		}
		ids[i] = p.ID
	}
	batch, err := s.passages.GetBatch(ids)
	if err != nil || len(batch) != len(all) {
		t.Fatalf("GetBatch = %d passages (%v), want %d", len(batch), err, len(all))
	}
	for i, p := range batch {
		if p.Text != all[i].Text {
			t.Errorf("GetBatch[%d] = %q, want %q", i, p.Text, all[i].Text)
		}
	}

	// BM25 hits keep their slotless IDs, and each hit's ID resolves to the
	// hit's own text — the old code looked the ID up as a vec_slot and found
	// the wrong passage or none.
	rs, err := s.SearchBM25(ctx, "recipe", 5)
	if err != nil {
		t.Fatalf("SearchBM25: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("SearchBM25(recipe) returned %d hits, want the 2 recipe passages", len(rs))
	}
	for _, r := range rs {
		if r.ID >= 0 {
			t.Errorf("slotless hit %q has ID %d, want a negative rowid", r.Text, r.ID)
		}
		got, err := s.passages.Get(r.ID)
		if err != nil || got.Text != r.Text {
			t.Errorf("Get(%d) for hit %q = %+v (%v), want the hit's own text", r.ID, r.Text, got, err)
		}
	}
}

// A staged build (BuildPassages with an embedder, vectors not built yet)
// serves its slotless passages lexically; BuildVectors then assigns slot i to
// item i, and the same passages become vector-addressable under non-negative
// IDs.
func TestFormat2_StagedThenVectorIDs(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	items := f2Items()
	ids, err := b.BuildPassages("idx", items)
	if err != nil {
		t.Fatalf("BuildPassages: %v", err)
	}

	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load of the staged index: %v", err)
	}
	rs, err := s.SearchBM25(ctx, "banana", 5)
	if err != nil || len(rs) == 0 || rs[0].Text != "banana bread" {
		t.Fatalf("SearchBM25 of the staged index = %+v (%v)", rs, err)
	}
	if rs[0].ID >= 0 {
		t.Errorf("staged hit ID = %d, want a negative rowid", rs[0].ID)
	}
	if p, err := s.passages.Get(rs[0].ID); err != nil || p.Text != rs[0].Text {
		t.Errorf("Get(%d) = %+v (%v), want the hit's own text", rs[0].ID, p, err)
	}
	s.Close()

	if err := b.BuildVectors(ctx, "idx", items, ids); err != nil {
		t.Fatalf("BuildVectors: %v", err)
	}
	texts := []string{"apple pie recipe", "apple pie recipe with cinnamon sugar", "banana bread", "cherry tart dessert"}
	f2SelfRetrieval(t, dir, embedder, texts)
	f2Healthy(t, dir)
}
