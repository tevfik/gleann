package gleann

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// seedPassageStore creates a format-2 store with two slotted sources and one
// slotless (lexical-only) passage, and returns it opened read-write.
func seedPassageStore(t *testing.T) (*storePassages, string) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "idx", "idx")
	sp := testPassageStore(base)
	if _, err := sp.Add([]Item{
		{Text: "alpha one", Metadata: map[string]any{"source": "a.go", "lang": "go"}},
		{Text: "alpha two", Metadata: map[string]any{"source": "a.go"}},
		{Text: "beta one", Metadata: map[string]any{"source": "b.md"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.st.ApplySource(context.Background(), store.SourceDelta{Source: "staged.md", Passages: []store.NewPassage{{Text: "staged text", Tokens: "staged text"}}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sp.Close() })
	return sp, base
}

func TestStorePassagesLookups(t *testing.T) {
	sp, _ := seedPassageStore(t)

	// The slotless passage is visible under its negated rowid.
	var staged Passage
	_ = sp.ForEachPassage(func(p Passage) error {
		if p.ID < 0 {
			staged = p
		}
		return nil
	})
	if staged.Text != "staged text" || staged.Metadata["source"] != "staged.md" {
		t.Fatalf("slotless passage = %+v", staged)
	}
	got, err := sp.Get(staged.ID)
	if err != nil || got.Text != "staged text" {
		t.Fatalf("Get(%d) = %+v, %v", staged.ID, got, err)
	}
	if _, err := sp.Get(-9999); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Get of a missing rowid: %v", err)
	}
	if _, err := sp.Get(99); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Get of a free slot: %v", err)
	}

	// GetBatch mixes both ID spaces and keeps the caller's order.
	batch, err := sp.GetBatch([]int64{2, staged.ID, 0})
	if err != nil {
		t.Fatal(err)
	}
	if batch[0].Text != "beta one" || batch[1].Text != "staged text" || batch[2].Text != "alpha one" {
		t.Fatalf("GetBatch order: %+v", batch)
	}
	if batch[2].Metadata["lang"] != "go" || batch[2].Metadata["chunk_index"] != 0 || batch[2].Metadata["source"] != "a.go" {
		t.Fatalf("metadata not re-attached: %+v", batch[2].Metadata)
	}
	if _, err := sp.GetBatch([]int64{0, 42}); err == nil || !strings.Contains(err.Error(), "passage 42 not found") {
		t.Fatalf("GetBatch with a missing ID: %v", err)
	}
	texts, err := sp.GetTexts([]int64{1, 0})
	if err != nil || strings.Join(texts, "|") != "alpha two|alpha one" {
		t.Fatalf("GetTexts = %v, %v", texts, err)
	}
	if _, err := sp.GetTexts([]int64{77}); err == nil {
		t.Fatal("GetTexts of a missing ID succeeded")
	}

	// LoadAllWithLimit caches at most limit passages; All serves the cache.
	if err := sp.LoadAllWithLimit(2); err != nil {
		t.Fatal(err)
	}
	if n := len(sp.All()); n != 2 {
		t.Fatalf("All after a limited load = %d passages, want 2", n)
	}
	if err := sp.LoadAll(); err != nil || len(sp.All()) != 2 {
		t.Fatal("LoadAll must keep an existing cache")
	}
	sp.cached = nil
	if n := len(sp.All()); n != 4 {
		t.Fatalf("All without cache = %d, want 4", n)
	}

	for _, tc := range []struct {
		sources []string
		want    bool
	}{{[]string{"b.md"}, true}, {[]string{"x", "staged.md"}, true}, {[]string{"nope"}, false}, {nil, false}} {
		got, err := sp.HasSources(tc.sources)
		if err != nil || got != tc.want {
			t.Errorf("HasSources(%v) = %v, %v; want %v", tc.sources, got, err, tc.want)
		}
	}

	// One passage has no slot: the store is not dense.
	if dense, err := sp.IsDense(); err != nil || dense {
		t.Fatalf("IsDense = %v, %v; want false", dense, err)
	}
	if err := sp.RollbackAdd([]int64{0}); err == nil {
		t.Fatal("RollbackAdd must be refused on format 2")
	}
}

func TestStorePassagesRemoveAndDelete(t *testing.T) {
	sp, base := seedPassageStore(t)
	removed, err := sp.RemoveBySource([]string{"a.go", "staged.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0] != 0 || removed[1] != 1 {
		t.Fatalf("removed slots = %v, want [0 1] (the slotless passage has none)", removed)
	}
	if sp.Count() != 1 {
		t.Fatalf("Count = %d, want 1", sp.Count())
	}

	ro, err := OpenPassageStore(base, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.RemoveBySource([]string{"b.md"}); err == nil {
		t.Error("read-only RemoveBySource succeeded")
	}
	if _, err := ro.Add([]Item{{Text: "x"}}); err == nil {
		t.Error("read-only Add succeeded")
	}
	if err := ro.Delete(); err == nil {
		t.Error("read-only Delete succeeded")
	}
	ro.Close()

	if err := sp.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base + ".db"); !os.IsNotExist(err) {
		t.Fatalf("Delete left the database: %v", err)
	}
	if DetectStoreFormat(base) != StoreNone {
		t.Fatal("a deleted index is still detected")
	}
}

func TestOpenPassageStoreFormats(t *testing.T) {
	dir := t.TempDir()

	// Missing index.
	if _, err := OpenPassageStore(filepath.Join(dir, "none", "none"), true); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("missing index: %v", err)
	}

	// Old format: a bbolt passages file.
	old := filepath.Join(dir, "old", "old")
	writeSized(t, old+".passages.db", 64)
	if _, err := OpenPassageStore(old, true); !errors.Is(err, ErrOldStoreFormat) {
		t.Fatalf("old format: %v", err)
	}

	// Format 2 recorded in meta.json, database lost: refused, not recreated.
	lost := filepath.Join(dir, "lost", "lost")
	if err := os.MkdirAll(filepath.Dir(lost), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lost+".meta.json", []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPassageStore(lost, true); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("lost database: %v", err)
	}
	if _, err := os.Stat(lost + ".db"); !os.IsNotExist(err) {
		t.Fatal("opening a lost store created an empty database")
	}

	// A migrated index keeps a guard file at the old passages name.
	mig := filepath.Join(dir, "mig", "mig")
	sp := testPassageStore(mig)
	sp.Close()
	if err := os.WriteFile(mig+".passages.db", []byte(storeGuardPrefix+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if DetectStoreFormat(mig) != StoreV2 || StoreFormatOf(mig) != 2 {
		t.Fatal("a guard file made a migrated index look old")
	}
	ps, err := OpenPassageStore(mig, true)
	if err != nil {
		t.Fatal(err)
	}
	ps.Close()

	// A database next to a real old passages file is a half-migrated index.
	writeSized(t, mig+".passages.db", 64)
	if DetectStoreFormat(mig) != StoreOld || StoreFormatOf(mig) != 1 {
		t.Fatal("a real old passages file next to the database must be the old format")
	}

	// A corrupt database is reported, not served empty.
	bad := filepath.Join(dir, "bad", "bad")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad+".db", []byte(strings.Repeat("garbage ", 600)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPassageStore(bad, true); err == nil || !strings.Contains(err.Error(), "opening format-2 store") {
		t.Fatalf("corrupt database: %v", err)
	}
}
