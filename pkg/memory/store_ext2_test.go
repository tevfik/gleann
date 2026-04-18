package memory

import (
	"path/filepath"
	"testing"
	"time"
)

// ── Store operations ───────────────────────────────────────────

func TestStoreDeleteOlderThanShort(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Now().Add(-48 * time.Hour)
	store.shortTerm = append(store.shortTerm, Block{
		ID: "old1", Tier: TierShort, Content: "old", CreatedAt: old,
	})
	store.shortTerm = append(store.shortTerm, Block{
		ID: "new1", Tier: TierShort, Content: "new", CreatedAt: time.Now(),
	})

	count, err := store.DeleteOlderThan(TierShort, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if len(store.shortTerm) != 1 {
		t.Errorf("shortTerm len = %d, want 1", len(store.shortTerm))
	}
}

func TestStoreDeleteOlderThanMedium(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Now().Add(-48 * time.Hour)
	_ = store.Add(&Block{ID: "old-med", Tier: TierMedium, Content: "old", CreatedAt: old})
	_ = store.Add(&Block{ID: "new-med", Tier: TierMedium, Content: "new", CreatedAt: time.Now()})

	count, err := store.DeleteOlderThan(TierMedium, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestStoreDeleteOlderThanLong(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Now().Add(-100 * 24 * time.Hour)
	_ = store.Add(&Block{ID: "old-long", Tier: TierLong, Content: "old", CreatedAt: old})
	count, err := store.DeleteOlderThan(TierLong, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestStoreDeleteSummariesOlderThan(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	old := time.Now().Add(-48 * time.Hour)
	_ = store.SaveSummary(&Summary{
		ConversationID: "conv1",
		Content:        "old summary",
		CreatedAt:      old,
	})
	_ = store.SaveSummary(&Summary{
		ConversationID: "conv2",
		Content:        "new summary",
		CreatedAt:      time.Now(),
	})

	count, err := store.DeleteSummariesOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	sums, _ := store.ListSummaries()
	if len(sums) != 1 {
		t.Errorf("remaining summaries = %d, want 1", len(sums))
	}
}

func TestStorePromoteMediumToLong(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_ = store.Add(&Block{ID: "promote-me", Tier: TierMedium, Content: "promote"})
	err = store.Promote("promote-me", TierLong)
	if err != nil {
		t.Fatal(err)
	}

	block, err := store.Get("promote-me")
	if err != nil {
		t.Fatal(err)
	}
	if block.Tier != TierLong {
		t.Errorf("tier = %s, want long", block.Tier)
	}
}

func TestStorePromoteShortToMedium(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.shortTerm = append(store.shortTerm, Block{
		ID: "short1", Tier: TierShort, Content: "short note",
	})
	err = store.Promote("short1", TierMedium)
	if err != nil {
		t.Fatal(err)
	}

	block, err := store.Get("short1")
	if err != nil {
		t.Fatal(err)
	}
	if block.Tier != TierMedium {
		t.Errorf("tier = %s, want medium", block.Tier)
	}
	if len(store.shortTerm) != 0 {
		t.Error("should be removed from short-term")
	}
}

func TestStorePruneExpiredMixed(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(24 * time.Hour)

	// Expired short-term
	store.shortTerm = append(store.shortTerm, Block{
		ID: "expired-short", Tier: TierShort, Content: "expired", ExpiresAt: &past,
	})
	// Valid short-term
	store.shortTerm = append(store.shortTerm, Block{
		ID: "valid-short", Tier: TierShort, Content: "valid", ExpiresAt: &future,
	})
	// Expired long-term
	_ = store.Add(&Block{ID: "expired-long", Tier: TierLong, Content: "old", ExpiresAt: &past})
	// Valid long-term
	_ = store.Add(&Block{ID: "valid-long", Tier: TierLong, Content: "valid"})

	count, err := store.PruneExpired()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("pruned = %d, want 2", count)
	}
	if len(store.shortTerm) != 1 {
		t.Errorf("shortTerm len = %d, want 1", len(store.shortTerm))
	}
}

func TestStoreBuildContextAllTiers(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.shortTerm = append(store.shortTerm, Block{
		ID: "s1", Tier: TierShort, Content: "short note",
	})
	_ = store.Add(&Block{ID: "m1", Tier: TierMedium, Content: "medium note"})
	_ = store.Add(&Block{ID: "l1", Tier: TierLong, Content: "long note"})

	cw, err := store.BuildContext()
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.ShortTerm) != 1 {
		t.Errorf("ShortTerm = %d", len(cw.ShortTerm))
	}
	if len(cw.MediumTerm) != 1 {
		t.Errorf("MediumTerm = %d", len(cw.MediumTerm))
	}
	if len(cw.LongTerm) != 1 {
		t.Errorf("LongTerm = %d", len(cw.LongTerm))
	}
}

func TestStoreBuildContextWithSummaries(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for i := 0; i < 7; i++ {
		_ = store.SaveSummary(&Summary{
			ConversationID: "conv",
			Content:        "summary",
			CreatedAt:      time.Now(),
		})
	}

	cw, err := store.BuildContext()
	if err != nil {
		t.Fatal(err)
	}
	// Only last 5 summaries should be included
	if len(cw.Summaries) > 5 {
		t.Errorf("Summaries = %d, want <= 5", len(cw.Summaries))
	}
}

func TestStorePathAccessor(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.db")
	store, err := OpenStore(p)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if store.Path() != p {
		t.Errorf("Path = %q, want %q", store.Path(), p)
	}
}

func TestStoreSearchByTag(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_ = store.Add(&Block{
		ID: "tagged", Tier: TierLong, Content: "some content", Tags: []string{"architecture", "go"},
	})
	_ = store.Add(&Block{
		ID: "untagged", Tier: TierLong, Content: "other content", Tags: []string{"python"},
	})

	results, err := store.Search("architecture")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("search results = %d, want 1", len(results))
	}
}

func TestStoreListAllTiersEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	blocks, err := store.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Errorf("got %d blocks, want 0", len(blocks))
	}
}

func TestStoreGetNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.Get("nonexistent")
	if err == nil {
		t.Error("should return error for missing block")
	}
}

func TestStoreDeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.Delete("nonexistent")
	if err == nil {
		t.Error("should return error for missing block")
	}
}

// ── Manager extended tests ─────────────────────────────────────

func TestManagerForgetByID(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()
	mgr := NewManager(store)

	block, _ := mgr.Remember("test fact", "tag1")
	count, err := mgr.Forget(block.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	_, err = store.Get(block.ID)
	if err == nil {
		t.Error("should be deleted")
	}
}

func TestManagerClearAllTiers(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.AddNote(TierShort, "s", "short")
	mgr.AddNote(TierMedium, "m", "medium")
	mgr.AddNote(TierLong, "l", "long")

	count, err := mgr.ClearAll()
	if err != nil {
		t.Fatal(err)
	}
	if count < 2 { // short + medium + long
		t.Errorf("count = %d, want >= 2", count)
	}
}

func TestManagerBuildContextMultipleTiers(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.AddNote(TierShort, "s", "short note")
	mgr.AddNote(TierMedium, "m", "medium note")
	mgr.AddNote(TierLong, "l", "long note")

	cw, err := mgr.BuildContext()
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.ShortTerm)+len(cw.MediumTerm)+len(cw.LongTerm) < 3 {
		t.Error("should have blocks in all tiers")
	}
}

func TestManagerSearchAcrossTiers(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.AddNote(TierShort, "s", "architecture decision")
	mgr.AddNote(TierLong, "l", "architecture pattern")

	results, err := mgr.Search("architecture")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("results = %d, want 2", len(results))
	}
}

func TestManagerListByTier(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "test.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.AddNote(TierShort, "s", "short")
	mgr.AddNote(TierMedium, "m", "medium")
	mgr.AddNote(TierLong, "l", "long")

	shorts, _ := mgr.List(TierShort)
	meds, _ := mgr.List(TierMedium)
	longs, _ := mgr.List(TierLong)

	if len(shorts) != 1 {
		t.Errorf("shorts = %d", len(shorts))
	}
	if len(meds) != 1 {
		t.Errorf("meds = %d", len(meds))
	}
	if len(longs) != 1 {
		t.Errorf("longs = %d", len(longs))
	}
}
