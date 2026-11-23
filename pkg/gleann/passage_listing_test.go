package gleann

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// seedListingIndex creates a format-2 index named "idx" under a temp index
// dir, with two sources: a markdown-style section and a section-less text.
func seedListingIndex(t *testing.T) (indexDir string) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "idx", "idx")
	sp := testPassageStore(base)
	t.Cleanup(func() { sp.Close() })
	if _, err := sp.st.ApplySource(context.Background(), store.SourceDelta{
		Source: "guide.md",
		Passages: []store.NewPassage{
			{Text: "intro", Meta: `{"section_id":"s0","section_title":"Introduction","section_path":"Introduction"}`},
			{Text: "detail", Meta: `{"section_id":"s1","section_title":"Details"}`},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.st.ApplySource(context.Background(), store.SourceDelta{
		Source:   "plain.txt",
		Passages: []store.NewPassage{{Text: "body"}},
	}); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(base))
}

func TestListStoredPassages(t *testing.T) {
	indexDir := seedListingIndex(t)
	ctx := context.Background()

	got, err := ListStoredPassages(ctx, indexDir, "idx", 2, 0)
	if err != nil {
		t.Fatalf("ListStoredPassages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d passages, want 2", len(got))
	}
	if got[0].Source != "guide.md" || got[0].Text != "intro" || got[0].Metadata["section_id"] != "s0" {
		t.Errorf("first passage = %+v", got[0])
	}
	if got[0].VecSlot != nil {
		t.Errorf("a lexical-only passage must have no vec_slot: %+v", got[0])
	}
	if got[1].ChunkNo != 1 {
		t.Errorf("second passage chunk_no = %d, want 1", got[1].ChunkNo)
	}
	if got[0].ID == 0 || got[0].ID >= got[1].ID {
		t.Errorf("ids are not ascending: %d then %d", got[0].ID, got[1].ID)
	}
}

func TestListStoredSections(t *testing.T) {
	indexDir := seedListingIndex(t)
	got, err := ListStoredSections(context.Background(), indexDir, "idx", 10, 0)
	if err != nil {
		t.Fatalf("ListStoredSections: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sections, want 2: %+v", len(got), got)
	}
	if got[0].SectionID != "s0" || got[0].Title != "Introduction" || got[0].PassageCount != 1 {
		t.Errorf("first section = %+v", got[0])
	}
	if got[1].SectionID != "s1" || got[1].Title != "Details" {
		t.Errorf("second section = %+v", got[1])
	}
	for _, sec := range got {
		if sec.Source == "plain.txt" {
			t.Errorf("the section-less source must not become a group: %+v", sec)
		}
	}
}

// TestListStored_MissingAndOldIndex: a missing index is ErrIndexNotFound and
// creates no database, an old-format index is the migration refusal.
func TestListStored_MissingAndOldIndex(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	if _, err := ListStoredPassages(ctx, dir, "ghost", 10, 0); !errors.Is(err, ErrIndexNotFound) {
		t.Errorf("missing index: %v, want ErrIndexNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ghost", "ghost.db")); !os.IsNotExist(err) {
		t.Errorf("listing a missing index created a database: %v", err)
	}

	oldDir := filepath.Join(dir, "old")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "old.meta.json"), []byte(`{"name":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ListStoredSections(ctx, dir, "old", 10, 0); !errors.Is(err, ErrOldStoreFormat) {
		t.Errorf("old index: %v, want ErrOldStoreFormat", err)
	}
}

// TestListStored_Bounds: the store bounds are enforced through the wrapper.
func TestListStored_Bounds(t *testing.T) {
	indexDir := seedListingIndex(t)
	if _, err := ListStoredPassages(context.Background(), indexDir, "idx", -1, 0); err == nil {
		t.Error("negative limit must be refused")
	}
	if _, err := ListStoredSections(context.Background(), indexDir, "idx", 0, -1); err == nil {
		t.Error("negative offset must be refused")
	}
}

// TestIndexPassage_NullMetaReturnsEmptyObject ensures a raw store row whose
// meta column holds the JSON scalar "null" serializes as an empty JSON object
// {} matching StoredPassage.metadata in the OpenAPI schema.
func TestIndexPassage_NullMetaReturnsEmptyObject(t *testing.T) {
	p := indexPassage(store.Passage{
		ID:      1,
		PID:     "pid-null",
		Source:  "a.md",
		ChunkNo: 0,
		Kind:    "code",
		Text:    "null-meta",
		Meta:    "null",
	})
	if p.Metadata == nil {
		t.Fatalf("Metadata = nil, want initialized map{}")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	md, ok := obj["metadata"].(map[string]any)
	if !ok || md == nil {
		t.Fatalf("serialized metadata = %T (%v), want JSON object {}", obj["metadata"], obj["metadata"])
	}
}
