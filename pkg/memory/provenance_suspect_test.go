package memory

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore_MarkSuspectAndProvenance(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "suspect_test.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	// 1. Empty input
	cnt, err := s.MarkSuspect(nil, nil)
	if err != nil || cnt != 0 {
		t.Fatalf("expected 0 for empty suspect check: cnt=%d, err=%v", cnt, err)
	}

	// 2. Add short-term block with paths and symbols
	bShort := &Block{
		Tier:      TierShort,
		Label:     "short",
		Content:   "short term block",
		Paths:     []string{"pkg/auth/login.go"},
		Symbols:   []string{"auth.Login"},
		CreatedAt: time.Now(),
	}
	if err := s.Add(bShort); err != nil {
		t.Fatalf("Add short: %v", err)
	}

	// 3. Add long-term block with paths and symbols
	bLong := &Block{
		Tier:      TierLong,
		Label:     "long",
		Content:   "long term block",
		Paths:     []string{"cmd/main.go"},
		Symbols:   []string{"main.RunServer"},
		CreatedAt: time.Now(),
	}
	if err := s.Add(bLong); err != nil {
		t.Fatalf("Add long: %v", err)
	}

	// 4. Mark suspect by symbol
	marked, err := s.MarkSuspect(nil, []string{"auth.Login"})
	if err != nil {
		t.Fatalf("MarkSuspect symbol: %v", err)
	}
	if marked != 1 {
		t.Fatalf("expected 1 block marked, got %d", marked)
	}

	// 5. Check that already suspect block is skipped
	markedAgain, err := s.MarkSuspect(nil, []string{"auth.Login"})
	if err != nil || markedAgain != 0 {
		t.Fatalf("expected 0 blocks marked again, got %d", markedAgain)
	}

	// 6. Mark suspect by file path on persistent long-term block
	markedFile, err := s.MarkSuspect([]string{"main.go"}, nil)
	if err != nil {
		t.Fatalf("MarkSuspect file: %v", err)
	}
	if markedFile != 1 {
		t.Fatalf("expected 1 persistent block marked, got %d", markedFile)
	}

	// Verify the long block has suspect flag
	retrieved, err := s.Get(bLong.ID)
	if err != nil || !retrieved.Suspect {
		t.Fatalf("expected retrieved block to be suspect: %v", retrieved)
	}
}

func TestManager_SuspectStaleAccess(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "mgr_suspect_test.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	mgr := NewManager(s)
	defer mgr.Close()

	// 1. RememberBlock
	b := &Block{
		Tier:      TierMedium,
		Label:     "refactor",
		Content:   "refactor handler to v2",
		Paths:     []string{"handler/v1.go"},
		Symbols:   []string{"handler.HandleV1"},
		CreatedAt: time.Now(),
	}
	retBlock, err := mgr.RememberBlock(b)
	if err != nil {
		t.Fatalf("RememberBlock: %v", err)
	}
	b = retBlock

	// 2. MarkSuspect via Manager
	marked, err := mgr.MarkSuspect([]string{"handler/v1.go"}, nil)
	if err != nil {
		t.Fatalf("mgr.MarkSuspect: %v", err)
	}
	if marked != 1 {
		t.Fatalf("expected 1 marked by manager, got %d", marked)
	}

	// Add an old block that is stale
	oldBlock := &Block{
		Tier:    TierShort,
		Label:   "old",
		Content: "old short term block",
	}
	if err := s.Add(oldBlock); err != nil {
		t.Fatalf("Add oldBlock: %v", err)
	}
	oldBlock.UpdatedAt = time.Now().Add(-48 * time.Hour)
	if err := s.Update(oldBlock); err != nil {
		t.Fatalf("Update oldBlock: %v", err)
	}

	// 3. StaleBlocks
	stale, err := mgr.StaleBlocks()
	if err != nil {
		t.Fatalf("mgr.StaleBlocks: %v", err)
	}
	if len(stale) == 0 {
		t.Fatalf("expected at least 1 stale block")
	}

	// 4. RecordAccess
	if err := mgr.RecordAccess(b.ID); err != nil {
		t.Fatalf("mgr.RecordAccess: %v", err)
	}

	// Access non-existent block
	if err := mgr.RecordAccess("non-existent-id"); err == nil {
		t.Fatalf("expected error accessing non-existent block")
	}
}
