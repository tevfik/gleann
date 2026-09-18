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
