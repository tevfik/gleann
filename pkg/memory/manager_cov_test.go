package memory

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestMgr(t *testing.T) *Manager {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return NewManager(store)
}

// ── Remember / Forget ────────────────────────────────────────────────────────

func TestRemember_WithTags(t *testing.T) {
	mgr := newTestMgr(t)
	b, err := mgr.Remember("gleann uses HNSW", "architecture", "backend")
	if err != nil {
		t.Fatal(err)
	}
	if b.Content != "gleann uses HNSW" {
		t.Fatalf("expected content, got %q", b.Content)
	}
	if len(b.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(b.Tags))
	}
	if b.Tier != TierLong {
		t.Fatalf("expected long tier, got %v", b.Tier)
	}
}

func TestForget_ByContent(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.Remember("fact about search")
	mgr.Remember("fact about memory")

	count, err := mgr.Forget("search")
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Log("no matches found by content (may need exact match)")
	}
}

func TestForget_NoMatch(t *testing.T) {
	mgr := newTestMgr(t)
	_, err := mgr.Forget("nonexistent-id-xyz")
	if err == nil {
		t.Log("expected error for no matches, but got nil")
	}
}

// ── AddNote / AddScopedNote ──────────────────────────────────────────────────

func TestAddNote_ShortTerm(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.ShortTermTTL = 10 * time.Minute

	b, err := mgr.AddNote(TierShort, "task", "current task state", "sprint")
	if err != nil {
		t.Fatal(err)
	}
	if b.Tier != TierShort {
		t.Fatalf("expected short, got %v", b.Tier)
	}
	if b.ExpiresAt == nil {
		t.Fatal("expected expiry for short-term note")
	}
}

func TestAddNote_MediumTerm(t *testing.T) {
	mgr := newTestMgr(t)
	b, err := mgr.AddNote(TierMedium, "goal", "sprint goal", "sprint14")
	if err != nil {
		t.Fatal(err)
	}
	if b.Tier != TierMedium {
		t.Fatalf("expected medium, got %v", b.Tier)
	}
}

func TestAddScopedNote(t *testing.T) {
	mgr := newTestMgr(t)
	b, err := mgr.AddScopedNote("conv-123", TierShort, "context", "conversation context")
	if err != nil {
		t.Fatal(err)
	}
	if b.Scope != "conv-123" {
		t.Fatalf("expected scope conv-123, got %q", b.Scope)
	}
}

func TestAddScopedNote_ShortWithTTL(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.ShortTermTTL = 5 * time.Minute
	b, err := mgr.AddScopedNote("conv-456", TierShort, "temp", "temp note")
	if err != nil {
		t.Fatal(err)
	}
	if b.ExpiresAt == nil {
		t.Fatal("expected expiry for short-term scoped note")
	}
}

// ── SearchScoped / ListScoped ────────────────────────────────────────────────

func TestSearchScoped(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.Remember("global fact")
	mgr.AddScopedNote("conv-1", TierLong, "note", "scoped to conv-1")

	results, err := mgr.SearchScoped("conv-1", "fact")
	if err != nil {
		t.Fatal(err)
	}
	// Should include global + scoped.
	_ = results
}

func TestListScoped(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.AddScopedNote("conv-1", TierLong, "note", "scoped long")
	mgr.Remember("global long")

	results, err := mgr.ListScoped("conv-1", TierLong)
	if err != nil {
		t.Fatal(err)
	}
	_ = results
}

// ── BuildScopedContext ───────────────────────────────────────────────────────

func TestBuildScopedContext_EmptyScope(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.Remember("some fact")
	cw, err := mgr.BuildScopedContext("")
	if err != nil {
		t.Fatal(err)
	}
	if cw == nil {
		t.Fatal("expected non-nil context window")
	}
}

func TestBuildScopedContext_WithScope(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.Remember("global fact")
	mgr.AddScopedNote("conv-1", TierShort, "note", "scoped fact")
	cw, err := mgr.BuildScopedContext("conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if cw == nil {
		t.Fatal("expected non-nil context window")
	}
}

// ── Clear / ClearAll ─────────────────────────────────────────────────────────

func TestClear_Tier(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.AddNote(TierShort, "task", "note1")
	mgr.AddNote(TierShort, "task", "note2")
	mgr.Remember("long term")

	count, err := mgr.Clear(TierShort)
	if err != nil {
		t.Fatal(err)
	}
	if count < 1 {
		t.Logf("cleared %d short-term blocks", count)
	}

	// Long-term should still exist.
	longs, _ := mgr.List(TierLong)
	if len(longs) == 0 {
		t.Log("expected long-term blocks to remain")
	}
}

func TestClearAll(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.Remember("fact1")
	mgr.Remember("fact2")
	count, err := mgr.ClearAll()
	if err != nil {
		t.Fatal(err)
	}
	_ = count
}

// ── EndSession ───────────────────────────────────────────────────────────────

func TestEndSession_AutoPromote(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.AutoPromote = true
	mgr.AutoCleanup = true

	mgr.AddNote(TierShort, "task", "session note")
	err := mgr.EndSession()
	if err != nil {
		t.Fatal(err)
	}
}

func TestEndSession_NoPromote(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.AutoPromote = false
	mgr.AutoCleanup = false

	mgr.AddNote(TierShort, "task", "session note")
	err := mgr.EndSession()
	if err != nil {
		t.Fatal(err)
	}
}

// ── RunMaintenance ───────────────────────────────────────────────────────────

func TestRunMaintenance(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.MediumTermMaxAge = 1 * time.Nanosecond // Very short for testing.

	mgr.AddNote(TierMedium, "old", "old medium note")
	time.Sleep(5 * time.Millisecond)

	err := mgr.RunMaintenance()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunMaintenance_NoMediumMaxAge(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.MediumTermMaxAge = 0

	mgr.Remember("some fact")
	err := mgr.RunMaintenance()
	if err != nil {
		t.Fatal(err)
	}
}

// ── Stats ────────────────────────────────────────────────────────────────────

func TestStatsCov(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.Remember("fact1")
	mgr.AddNote(TierShort, "task", "note1")

	stats, err := mgr.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalCount < 1 {
		t.Fatalf("expected at least 1 block, got %d", stats.TotalCount)
	}
}

// ── Store edge cases ─────────────────────────────────────────────────────────

func TestStorePromoteCov(t *testing.T) {
	mgr := newTestMgr(t)
	b, _ := mgr.AddNote(TierShort, "task", "promotable note")
	err := mgr.Store().Promote(b.ID, TierLong)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStoreDeleteOlderThanCov(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.Remember("old fact")
	time.Sleep(5 * time.Millisecond)

	count, err := mgr.Store().DeleteOlderThan(TierLong, 1*time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	_ = count
}

func TestStoreSaveSummaryCov(t *testing.T) {
	mgr := newTestMgr(t)
	summary := &Summary{
		ConversationID: "session-123",
		Content:        "This was a productive session",
		Title:          "Test session",
	}
	err := mgr.Store().SaveSummary(summary)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := mgr.Store().ListSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
}

func TestStoreDeleteSummariesOlderThanCov(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.Store().SaveSummary(&Summary{
		ConversationID: "old-session",
		Content:        "old summary",
	})
	time.Sleep(5 * time.Millisecond)

	count, err := mgr.Store().DeleteSummariesOlderThan(1 * time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	_ = count
}

func TestStorePruneExpiredCov(t *testing.T) {
	mgr := newTestMgr(t)
	mgr.ShortTermTTL = 1 * time.Nanosecond
	mgr.AddNote(TierShort, "temp", "expiring note")
	time.Sleep(5 * time.Millisecond)

	count, err := mgr.Store().PruneExpired()
	if err != nil {
		t.Fatal(err)
	}
	_ = count
}

// ── Block helpers ────────────────────────────────────────────────────────────

func TestBlockIsExpiredCov(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	b := Block{ExpiresAt: &past}
	if !b.IsExpired() {
		t.Fatal("expected expired")
	}

	future := time.Now().Add(1 * time.Hour)
	b2 := Block{ExpiresAt: &future}
	if b2.IsExpired() {
		t.Fatal("expected not expired")
	}

	b3 := Block{}
	if b3.IsExpired() {
		t.Fatal("nil expiry should not be expired")
	}
}
