package memory_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

func TestMemoryCompactionAndPruning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gleann-memory-compact-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	mgr := memory.NewManager(store)

	// 1. Add valid long-term memory
	validBlock, err := mgr.Remember("Valid persistent preference", "pref")
	if err != nil {
		t.Fatalf("remember valid: %v", err)
	}
	validBlock.Confirm()
	validBlock.Confirm()
	_ = store.Update(validBlock)

	// 2. Add expiring short-term memory (already expired)
	past := time.Now().Add(-1 * time.Hour)
	expBlock := &memory.Block{
		Tier:      memory.TierShort,
		Content:   "Ephemeral note that expired",
		Label:     "temp",
		ExpiresAt: &past,
	}
	_ = store.Add(expBlock)

	// 3. Add unreliable memory with multiple conflicts
	unreliableBlock := &memory.Block{
		Tier:      memory.TierLong,
		Content:   "False statement with contradictions",
		Label:     "false",
		Conflicts: 10,
	}
	_ = store.Add(unreliableBlock)

	// Verify initial validity score
	if unreliableBlock.ValidityScore() >= 0.2 {
		t.Errorf("expected unreliable block score < 0.2, got %f", unreliableBlock.ValidityScore())
	}
	if validBlock.ValidityScore() < 0.5 {
		t.Errorf("expected valid block score >= 0.5, got %f", validBlock.ValidityScore())
	}

	// 4. BuildContext should automatically filter out unreliable & expired blocks
	cw, err := mgr.BuildContext()
	if err != nil {
		t.Fatalf("BuildContext error: %v", err)
	}

	foundUnreliable := false
	foundValid := false
	for _, b := range cw.LongTerm {
		if b.Content == unreliableBlock.Content {
			foundUnreliable = true
		}
		if b.Content == validBlock.Content {
			foundValid = true
		}
	}

	if foundUnreliable {
		t.Error("BuildContext should have filtered out unreliable block")
	}
	if !foundValid {
		t.Error("BuildContext should have included valid block")
	}

	// 5. Compact(0.2) should prune expired and unreliable blocks
	pruned, err := mgr.Compact(0.2)
	if err != nil {
		t.Fatalf("Compact error: %v", err)
	}
	if pruned < 2 {
		t.Errorf("expected at least 2 blocks pruned (expired + unreliable), got %d", pruned)
	}

	// Verify unreliable block is deleted from store
	got, _ := store.Get(unreliableBlock.ID)
	if got != nil {
		t.Error("expected unreliable block to be deleted after Compact")
	}

	// Verify valid block remains
	gotValid, _ := store.Get(validBlock.ID)
	if gotValid == nil {
		t.Error("expected valid block to remain after Compact")
	}
}

// backdate persists a block and then ages it: Store.Add stamps UpdatedAt
// with time.Now(), so the age is applied through Update afterwards.
func backdate(t *testing.T, store *memory.Store, b *memory.Block, age time.Duration) {
	t.Helper()
	if err := store.Add(b); err != nil {
		t.Fatalf("add block: %v", err)
	}
	b.CreatedAt = time.Now().Add(-age)
	b.UpdatedAt = time.Now().Add(-age)
	if err := store.Update(b); err != nil {
		t.Fatalf("backdate block: %v", err)
	}
}

func containsContent(blocks []memory.Block, content string) bool {
	for _, b := range blocks {
		if b.Content == content {
			return true
		}
	}
	return false
}

// TestLongTierSurvivesBuildContextAndCompact pins the maintainer decision
// (2026-11-21): a 180-day-old unconfirmed TierLong block stays in the
// injected context and survives Compact, while expired, heavily
// conflicted and aged medium-tier blocks are still filtered and pruned.
func TestLongTierSurvivesBuildContextAndCompact(t *testing.T) {
	store, err := memory.OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	mgr := memory.NewManager(store)

	const agedLongContent = "Long-term convention that must survive by age alone"
	agedLong := &memory.Block{Tier: memory.TierLong, Label: "convention", Content: agedLongContent}
	backdate(t, store, agedLong, 180*24*time.Hour)

	// Explicit expiry is caller intent, not age: it still applies to long.
	past := time.Now().Add(-1 * time.Hour)
	expiredLong := &memory.Block{Tier: memory.TierLong, Label: "expired", Content: "Long block with an explicit past expiry", ExpiresAt: &past}
	backdate(t, store, expiredLong, 24*time.Hour)

	conflictedLong := &memory.Block{Tier: memory.TierLong, Label: "false", Content: "Long statement contradicted ten times", Conflicts: 10}
	backdate(t, store, conflictedLong, 24*time.Hour)

	agedMedium := &memory.Block{Tier: memory.TierMedium, Label: "digest", Content: "Medium digest aged past its decay horizon"}
	backdate(t, store, agedMedium, 365*24*time.Hour)

	if agedLong.IsStale() {
		t.Error("180-day-old TierLong block must not be stale by age")
	}
	if score := agedLong.ValidityScore(); score < 0.5 {
		t.Errorf("180-day-old unconfirmed TierLong score = %f, want >= 0.5", score)
	}

	// StaleBlocks reports the aged medium block, never the aged long one.
	stale, err := mgr.StaleBlocks()
	if err != nil {
		t.Fatalf("StaleBlocks: %v", err)
	}
	if containsContent(stale, agedLongContent) {
		t.Error("StaleBlocks must not report the aged TierLong block")
	}
	if !containsContent(stale, agedMedium.Content) {
		t.Error("StaleBlocks should report the 365-day-old TierMedium block")
	}

	// Both context builders include the aged long block and exclude the
	// expired, conflicted and decayed ones.
	for _, tc := range []struct {
		name  string
		build func() (*memory.ContextWindow, error)
	}{
		{"BuildContext", mgr.BuildContext},
		{"BuildScopedContext", func() (*memory.ContextWindow, error) { return mgr.BuildScopedContext("") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cw, err := tc.build()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !containsContent(cw.LongTerm, agedLongContent) {
				t.Errorf("%s must include the 180-day-old TierLong block", tc.name)
			}
			if containsContent(cw.LongTerm, expiredLong.Content) {
				t.Errorf("%s must exclude the explicitly expired long block", tc.name)
			}
			if containsContent(cw.LongTerm, conflictedLong.Content) {
				t.Errorf("%s must exclude the heavily conflicted long block", tc.name)
			}
			if containsContent(cw.MediumTerm, agedMedium.Content) {
				t.Errorf("%s must exclude the decayed medium block", tc.name)
			}
		})
	}

	// Compact(0.2) prunes expired + conflicted + decayed medium (3),
	// and preserves the aged long block.
	pruned, err := mgr.Compact(0.2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if pruned != 3 {
		t.Errorf("Compact pruned %d blocks, want 3 (expired + conflicted + aged medium)", pruned)
	}
	if got, err := store.Get(agedLong.ID); err != nil || got == nil {
		t.Errorf("Compact must preserve the 180-day-old TierLong block (err=%v)", err)
	}
	for _, gone := range []*memory.Block{expiredLong, conflictedLong, agedMedium} {
		if got, _ := store.Get(gone.ID); got != nil {
			t.Errorf("Compact should have pruned %q", gone.Content)
		}
	}

	// The survivor is still injected after compaction.
	cw, err := mgr.BuildContext()
	if err != nil {
		t.Fatalf("BuildContext after Compact: %v", err)
	}
	if !containsContent(cw.LongTerm, agedLongContent) {
		t.Error("BuildContext after Compact must still include the aged TierLong block")
	}
}

// TestLongTierRemainsChangeable verifies the other half of the decision:
// no age-based expiry does not mean immutable. An aged TierLong block can
// be edited, reinforced by a duplicate add, contradicted below the prune
// threshold, explicitly expired by the caller, and forgotten by ID.
func TestLongTierRemainsChangeable(t *testing.T) {
	store, err := memory.OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	mgr := memory.NewManager(store)

	editable := &memory.Block{Tier: memory.TierLong, Label: "fact", Content: "Original aged long fact"}
	backdate(t, store, editable, 365*24*time.Hour)

	forgettable := &memory.Block{Tier: memory.TierLong, Label: "fact", Content: "Aged long fact to forget explicitly"}
	backdate(t, store, forgettable, 10*365*24*time.Hour)

	past := time.Now().Add(-1 * time.Hour)
	explicitExpiry := &memory.Block{Tier: memory.TierLong, Label: "fact", Content: "Aged long fact with caller-set expiry", ExpiresAt: &past}
	backdate(t, store, explicitExpiry, 365*24*time.Hour)

	for _, b := range []*memory.Block{editable, forgettable} {
		if b.IsStale() {
			t.Errorf("aged TierLong block %q must not be stale", b.Content)
		}
		if score := b.ValidityScore(); score < 0.5 {
			t.Errorf("aged TierLong block %q score = %f, want >= 0.5", b.Content, score)
		}
	}
	if !explicitExpiry.IsExpired() {
		t.Error("explicit past ExpiresAt must still expire a TierLong block")
	}

	// Explicit expiry is filtered from context even though age is not.
	cw, err := mgr.BuildContext()
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if containsContent(cw.LongTerm, explicitExpiry.Content) {
		t.Error("BuildContext must exclude a TierLong block with explicit past ExpiresAt")
	}

	// 1. Explicit edit via Store.Update.
	editable.Content = "Edited aged long fact"
	if err := store.Update(editable); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := store.Get(editable.ID)
	if err != nil || got.Content != editable.Content {
		t.Fatalf("edited content not persisted: %q (err=%v)", got.Content, err)
	}

	// 2. Duplicate add merges into the same block (Confirms++), not a copy.
	merged, err := mgr.AddNote(memory.TierLong, "fact", editable.Content)
	if err != nil {
		t.Fatalf("AddNote merge: %v", err)
	}
	if merged.ID != editable.ID {
		t.Errorf("duplicate add created a new block %s, want merge into %s", merged.ID, editable.ID)
	}
	if merged.Confirms < 1 {
		t.Errorf("duplicate add should confirm the existing block, Confirms = %d", merged.Confirms)
	}

	// 3. Contradiction drops the score below 0.2 and Compact prunes it —
	// on conflict, not on age.
	got, err = store.Get(editable.ID)
	if err != nil {
		t.Fatalf("Get before conflict: %v", err)
	}
	for i := 0; i < 10; i++ {
		got.Conflict()
	}
	if err := store.Update(got); err != nil {
		t.Fatalf("Update conflicts: %v", err)
	}
	if score := got.ValidityScore(); score >= 0.2 {
		t.Fatalf("heavily conflicted aged long block score = %f, want < 0.2", score)
	}

	// 4. Explicit forget by ID.
	n, err := mgr.Forget(forgettable.ID)
	if err != nil || n != 1 {
		t.Fatalf("Forget = %d, %v; want 1, nil", n, err)
	}
	if got, _ := store.Get(forgettable.ID); got != nil {
		t.Error("forgotten block still present")
	}

	// Compact prunes the conflicted block and the explicitly expired one.
	pruned, err := mgr.Compact(0.2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if pruned != 2 {
		t.Errorf("Compact pruned %d blocks, want 2 (conflicted + explicitly expired)", pruned)
	}
	for _, gone := range []*memory.Block{editable, explicitExpiry} {
		if got, _ := store.Get(gone.ID); got != nil {
			t.Errorf("Compact should have pruned %q", gone.Content)
		}
	}
}
