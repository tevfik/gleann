package memory

import (
	"context"
	"testing"
	"time"
)

// v3 repair tests (second independent review, memory-fixes-v2.gemini.md).
// MAJOR: RevertReflection must unwind multiple confirmations of the same
// block in LIFO order. Forward iteration leaves Confirms and UpdatedAt one
// step short of the pre-run baseline when a single reflection run confirmed
// the same block more than once.

// TestDefect_RevertMultipleConfirmationsSameBlock is the review's exact
// scenario: block b starts at Confirms=0/UpdatedAt=t0; one run confirms it
// twice (0→1 at t1, then 1→2 at t2). Revert must restore Confirms=0 and
// UpdatedAt=t0, not Confirms=1/UpdatedAt=t1.
func TestDefect_RevertMultipleConfirmationsSameBlock(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	s := openTestStore(t)
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })

	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)

	b := &Block{ID: "blk-lifo-0001", Tier: TierLong, Label: "user_memory",
		Content: "Gleann uses pure Go for the default build", CreatedAt: t0, UpdatedAt: t0}
	if err := s.Add(b); err != nil {
		t.Fatal(err)
	}
	// State after the run confirmed this block twice.
	b.Confirms = 2
	b.UpdatedAt = t2
	if err := s.Update(b); err != nil {
		t.Fatal(err)
	}
	cs := &ChangeSet{RunID: "reflect-lifo-0001", Timestamp: t2,
		BlocksConfirmed: []BlockConfirmation{
			{ID: b.ID, PriorConfirms: 0, PriorUpdatedAt: t0, NewUpdatedAt: t1},
			{ID: b.ID, PriorConfirms: 1, PriorUpdatedAt: t1, NewUpdatedAt: t2},
		}}
	if err := s.SaveChangeSet(cs); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.RevertReflection(cs.RunID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(b.ID)
	if err != nil {
		t.Fatalf("revert deleted the confirmed block: %v", err)
	}
	if got.Confirms != 0 {
		t.Errorf("forward iteration failed to unwind both confirmations: Confirms = %d, want 0", got.Confirms)
	}
	if !got.UpdatedAt.Equal(t0) {
		t.Errorf("UpdatedAt = %v, want the original %v", got.UpdatedAt, t0)
	}
}

// TestDefect_RevertMultipleConfirmationsKeepsLaterEdit proves the
// NewUpdatedAt guard survives LIFO unwinding: an external edit after the run
// re-stamps UpdatedAt, and revert gives back the confirmations without
// clobbering the newer timestamp.
func TestDefect_RevertMultipleConfirmationsKeepsLaterEdit(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	s := openTestStore(t)
	mgr := NewManager(s)
	t.Cleanup(func() { mgr.Close() })

	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)
	t3 := t2.Add(time.Minute)

	b := &Block{ID: "blk-lifo-0002", Tier: TierLong, Label: "user_memory",
		Content: "Gleann uses pure Go for the default build", CreatedAt: t0, UpdatedAt: t0}
	if err := s.Add(b); err != nil {
		t.Fatal(err)
	}
	b.Confirms = 2
	b.UpdatedAt = t3 // external edit AFTER the run (run had stamped t2)
	if err := s.Update(b); err != nil {
		t.Fatal(err)
	}
	cs := &ChangeSet{RunID: "reflect-lifo-0002", Timestamp: t2,
		BlocksConfirmed: []BlockConfirmation{
			{ID: b.ID, PriorConfirms: 0, PriorUpdatedAt: t0, NewUpdatedAt: t1},
			{ID: b.ID, PriorConfirms: 1, PriorUpdatedAt: t1, NewUpdatedAt: t2},
		}}
	if err := s.SaveChangeSet(cs); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.RevertReflection(cs.RunID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Confirms != 0 {
		t.Errorf("Confirms = %d, want 0 (both confirmations given back)", got.Confirms)
	}
	if !got.UpdatedAt.Equal(t3) {
		t.Errorf("UpdatedAt = %v, want the later external edit %v preserved (NewUpdatedAt guard)", got.UpdatedAt, t3)
	}
}

// TestDefect_RevertTwoConversationsConfirmingSameBlock drives the scenario
// end-to-end: two conversations in one reflection cycle both extract a near
// duplicate of the same stored block, producing two BlocksConfirmed entries
// for one ID. Revert must restore the exact pre-run Confirms and UpdatedAt.
func TestDefect_RevertTwoConversationsConfirmingSameBlock(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	e := newSleepEnv(t, "fact: Gleann uses pure Go for the default build now\n")
	orig, err := e.mgr.Remember("Gleann uses pure Go for default build", "architecture")
	if err != nil {
		t.Fatal(err)
	}
	priorConfirms := orig.Confirms
	priorUpdatedAt := orig.UpdatedAt
	e.save(t, "conv-lifo-0001", 2*time.Minute, userMsg("Gleann uses pure Go for the default build now."))
	e.save(t, "conv-lifo-0002", time.Minute, userMsg("Gleann uses pure Go for the default build now."))

	engine := NewSleepTimeEngine(e.mgr, e.cfg)
	cs, err := engine.Reflect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.BlocksConfirmed) != 2 {
		t.Fatalf("BlocksConfirmed = %+v, want two confirmations of the same block", cs.BlocksConfirmed)
	}
	for _, conf := range cs.BlocksConfirmed {
		if conf.ID != orig.ID {
			t.Fatalf("confirmation of unexpected block %s, want %s", conf.ID, orig.ID)
		}
	}
	after, err := e.store.Get(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Confirms != priorConfirms+2 {
		t.Fatalf("Confirms after run = %d, want %d", after.Confirms, priorConfirms+2)
	}

	if _, err := engine.Revert(cs.RunID); err != nil {
		t.Fatal(err)
	}
	reverted, err := e.store.Get(orig.ID)
	if err != nil {
		t.Fatalf("revert deleted the user's block: %v", err)
	}
	if reverted.Confirms != priorConfirms {
		t.Errorf("revert left Confirms = %d, want %d", reverted.Confirms, priorConfirms)
	}
	if !reverted.UpdatedAt.Equal(priorUpdatedAt) {
		t.Errorf("revert left UpdatedAt = %v, want the pre-run %v", reverted.UpdatedAt, priorUpdatedAt)
	}
}
