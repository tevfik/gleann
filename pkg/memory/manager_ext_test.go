package memory

import (
	"path/filepath"
	"testing"
	"time"
)

// ── Manager Remember/Forget lifecycle ──────────────────────────

func TestManagerRememberForgetExt2(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)
	defer mgr.Close()

	b, err := mgr.Remember("important fact", "tag1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Tier != TierLong {
		t.Errorf("tier = %s, want long", b.Tier)
	}
	if b.Content != "important fact" {
		t.Errorf("content = %q", b.Content)
	}

	n, err := mgr.Forget(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("forget count = %d", n)
	}
}

func TestManagerForgetByContent(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)
	defer mgr.Close()

	mgr.Remember("delete me later", "tag")

	n, err := mgr.Forget("delete me")
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("forget count = %d", n)
	}
}

func TestManagerForgetNotFound(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)
	defer mgr.Close()

	_, err := mgr.Forget("nonexistent")
	if err == nil {
		t.Error("expected error for no match")
	}
}

// ── AddNote with TTL ───────────────────────────────────────────

func TestManagerAddNoteShortTTL(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)
	mgr.ShortTermTTL = 5 * time.Minute

	b, err := mgr.AddNote(TierShort, "task", "current task")
	if err != nil {
		t.Fatal(err)
	}
	if b.ExpiresAt == nil {
		t.Error("short-term note should have expiry")
	}
}

func TestManagerAddNoteMedium(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	b, err := mgr.AddNote(TierMedium, "sprint", "sprint 14 goals")
	if err != nil {
		t.Fatal(err)
	}
	if b.Tier != TierMedium {
		t.Errorf("tier = %s", b.Tier)
	}
	if b.ExpiresAt != nil {
		t.Error("medium note should NOT have expiry")
	}
}

// ── AddScopedNote ──────────────────────────────────────────────

func TestManagerAddScopedNote(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	b, err := mgr.AddScopedNote("conv-123", TierShort, "context", "conversation note")
	if err != nil {
		t.Fatal(err)
	}
	if b.Scope != "conv-123" {
		t.Errorf("scope = %q", b.Scope)
	}
}

// ── Clear ──────────────────────────────────────────────────────

func TestManagerClearTier(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.AddNote(TierShort, "a", "data1")
	mgr.AddNote(TierShort, "b", "data2")
	mgr.Remember("long term data")

	n, err := mgr.Clear(TierShort)
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Errorf("cleared = %d", n)
	}

	// Long-term should still exist.
	blocks, _ := mgr.List(TierLong)
	if len(blocks) < 1 {
		t.Error("long-term should still exist")
	}
}

func TestManagerClearAll(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.Remember("data1")
	mgr.AddNote(TierMedium, "m", "data2")

	n, err := mgr.ClearAll()
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Errorf("cleared = %d", n)
	}
}

// ── SearchScoped ───────────────────────────────────────────────

func TestManagerSearchScoped(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.AddScopedNote("proj-a", TierLong, "note", "alpha project data")
	mgr.AddScopedNote("proj-b", TierLong, "note", "beta project data")
	mgr.Remember("global data") // no scope

	results, err := mgr.SearchScoped("proj-a", "data")
	if err != nil {
		t.Fatal(err)
	}

	// Should include proj-a scoped + global, but NOT proj-b.
	for _, b := range results {
		if b.Scope != "" && b.Scope != "proj-a" {
			t.Errorf("unexpected scope %q in results", b.Scope)
		}
	}
}

// ── ListScoped ─────────────────────────────────────────────────

func TestManagerListScoped(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.AddScopedNote("conv-1", TierLong, "note", "conv1 data")
	mgr.AddScopedNote("conv-2", TierLong, "note", "conv2 data")
	mgr.Remember("shared data") // global

	results, err := mgr.ListScoped("conv-1", TierLong)
	if err != nil {
		t.Fatal(err)
	}

	for _, b := range results {
		if b.Scope != "" && b.Scope != "conv-1" {
			t.Errorf("unexpected scope %q", b.Scope)
		}
	}
}

// ── Stats ──────────────────────────────────────────────────────

func TestManagerStatsAfterOps(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.Remember("fact1")
	mgr.Remember("fact2")
	mgr.AddNote(TierMedium, "m", "medium data")

	stats, err := mgr.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats == nil {
		t.Fatal("stats is nil")
	}
}

// ── BuildContext / BuildScopedContext ───────────────────────────

func TestManagerBuildContextExt2(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.Remember("architecture: hexagonal")
	mgr.AddNote(TierMedium, "sprint", "sprint 14 goals")

	cw, err := mgr.BuildContext()
	if err != nil {
		t.Fatal(err)
	}
	if cw == nil {
		t.Fatal("context window is nil")
	}
}

func TestManagerBuildScopedContext(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.AddScopedNote("proj-x", TierLong, "n", "proj-x data")
	mgr.AddScopedNote("proj-y", TierLong, "n", "proj-y data")
	mgr.Remember("global knowledge")

	cw, err := mgr.BuildScopedContext("proj-x")
	if err != nil {
		t.Fatal(err)
	}

	// Should include proj-x and global, not proj-y.
	for _, b := range cw.LongTerm {
		if b.Scope != "" && b.Scope != "proj-x" {
			t.Errorf("unexpected scope %q in context", b.Scope)
		}
	}
}

func TestManagerBuildScopedContextEmptyScope(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	mgr.Remember("data")

	cw, err := mgr.BuildScopedContext("")
	if err != nil {
		t.Fatal(err)
	}
	// Empty scope should return all blocks (no filtering).
	if cw == nil {
		t.Fatal("nil context window")
	}
}

// ── EndSession ─────────────────────────────────────────────────

func TestManagerEndSessionPromote(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)
	mgr.AutoPromote = true

	mgr.AddNote(TierShort, "task", "short term task")

	if err := mgr.EndSession(); err != nil {
		t.Fatal(err)
	}

	// Short-term should be promoted to medium.
	blocks, _ := mgr.List(TierMedium)
	found := false
	for _, b := range blocks {
		if b.Content == "short term task" {
			found = true
		}
	}
	if !found {
		t.Error("short-term note should have been promoted to medium")
	}
}

func TestManagerEndSessionNoPromote(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)
	mgr.AutoPromote = false

	mgr.AddNote(TierShort, "task", "ephemeral")

	if err := mgr.EndSession(); err != nil {
		t.Fatal(err)
	}
}

// ── RunMaintenance ─────────────────────────────────────────────

func TestManagerRunMaintenance(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)
	mgr.MediumTermMaxAge = 1 * time.Millisecond // very short

	mgr.AddNote(TierMedium, "old", "ancient data")
	time.Sleep(5 * time.Millisecond) // ensure past max age

	if err := mgr.RunMaintenance(); err != nil {
		t.Fatal(err)
	}
}

// ── applyDefaults / filterScope ────────────────────────────────

func TestApplyDefaultsCharLimit(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)
	mgr.DefaultCharLimit = 100
	mgr.EnforceCharLimit = true

	b := &Block{Content: "x"}
	mgr.applyDefaults(b)
	if b.CharLimit != 100 {
		t.Errorf("char limit = %d", b.CharLimit)
	}
}

func TestFilterScopeEmpty(t *testing.T) {
	blocks := []Block{
		{Content: "a", Scope: ""},
		{Content: "b", Scope: "proj"},
	}
	result := filterScope(blocks, "")
	if len(result) != 2 {
		t.Errorf("got %d, want 2 (empty scope returns all)", len(result))
	}
}

func TestFilterScopeMatch(t *testing.T) {
	blocks := []Block{
		{Content: "global", Scope: ""},
		{Content: "proj-a", Scope: "a"},
		{Content: "proj-b", Scope: "b"},
	}
	result := filterScope(blocks, "a")
	if len(result) != 2 {
		t.Errorf("got %d, want 2 (global + scope-a)", len(result))
	}
}

// ── Manager Store accessor ─────────────────────────────────────

func TestManagerStoreAccessor(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store)

	if mgr.Store() != store {
		t.Error("Store() should return the underlying store")
	}
}

// ── Manager CharLimit from env ─────────────────────────────────

func TestManagerDefaultCharLimitZero(t *testing.T) {
	store, _ := OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	defer store.Close()
	mgr := NewManager(store) // no env set

	if mgr.DefaultCharLimit != 0 {
		t.Errorf("default char limit = %d, want 0", mgr.DefaultCharLimit)
	}
	if mgr.EnforceCharLimit {
		t.Error("enforce should be false by default")
	}
}
