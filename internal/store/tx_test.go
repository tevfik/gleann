package store

import (
	"context"
	"strings"
	"testing"
)

// The Tx variants exist so a format-2 vector publication can commit EVERY
// database change of one update — source removals, slot renumbering and the
// pending-publication keys — in one transaction (RFC 003 section 3.4). A
// rollback of that transaction must leave none of them behind; a commit must
// land all of them together.

func TestTxVariantsAtomicWithCallerTransaction(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	idsA := applyTexts(t, s, "a.md", []string{"alpha", "beta"})
	idsB := applyTexts(t, s, "b.md", []string{"gamma"})

	// Give the passages dense slots so there is something to renumber.
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	if err := s.AssignSlotsTx(ctx, tx, map[int64]int64{idsA[0]: 0, idsA[1]: 1, idsB[0]: 2}); err != nil {
		t.Fatalf("AssignSlotsTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit slots: %v", err)
	}

	// Roll back a whole publication: nothing may survive it.
	tx, err = s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	if removed, err := s.RemoveSourceTx(ctx, tx, "b.md"); err != nil || removed != 1 {
		t.Fatalf("RemoveSourceTx = (%d, %v), want (1, nil)", removed, err)
	}
	if err := s.ReassignSlotsTx(ctx, tx, map[int64]int64{0: 1, 1: 0}); err != nil {
		t.Fatalf("ReassignSlotsTx: %v", err)
	}
	if err := s.SetMetaTx(ctx, tx, "pending_vector_file", "idx.index.tmp"); err != nil {
		t.Fatalf("SetMetaTx: %v", err)
	}
	if err := s.SetMetaTx(ctx, tx, "pending_vector_generation", "2"); err != nil {
		t.Fatalf("SetMetaTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if n, err := s.Count(ctx); err != nil || n != 3 {
		t.Fatalf("Count after rollback = (%d, %v), want 3", n, err)
	}
	ps, err := s.BySlot(ctx, []int64{0, 1, 2})
	if err != nil || len(ps) != 3 || ps[0].Text != "alpha" || ps[1].Text != "beta" || ps[2].Text != "gamma" {
		t.Fatalf("slots after rollback = %+v (%v), want alpha/beta/gamma at 0/1/2", ps, err)
	}
	if v, _ := s.Meta(ctx, "pending_vector_file"); v != "" {
		t.Fatalf("pending_vector_file survived rollback: %q", v)
	}

	// Commit the same publication: every change lands together.
	tx, err = s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	if _, err := s.RemoveSourceTx(ctx, tx, "b.md"); err != nil {
		t.Fatalf("RemoveSourceTx: %v", err)
	}
	if err := s.ReassignSlotsTx(ctx, tx, map[int64]int64{0: 1, 1: 0}); err != nil {
		t.Fatalf("ReassignSlotsTx: %v", err)
	}
	if err := s.SetMetaTx(ctx, tx, "pending_vector_file", "idx.index.tmp"); err != nil {
		t.Fatalf("SetMetaTx: %v", err)
	}
	if err := s.SetMetaTx(ctx, tx, "pending_vector_generation", "2"); err != nil {
		t.Fatalf("SetMetaTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit publication: %v", err)
	}
	if n, err := s.Count(ctx); err != nil || n != 2 {
		t.Fatalf("Count after commit = (%d, %v), want 2", n, err)
	}
	ps, err = s.BySlot(ctx, []int64{0, 1, 2})
	if err != nil || len(ps) != 2 || ps[0].Text != "beta" || ps[1].Text != "alpha" {
		t.Fatalf("slots after commit = %+v (%v), want beta/alpha at 0/1 and no slot 2", ps, err)
	}
	if v, _ := s.Meta(ctx, "pending_vector_file"); v != "idx.index.tmp" {
		t.Fatalf("pending_vector_file after commit = %q", v)
	}

	// The finish step records the generation and clears the pending keys in
	// one transaction, so a finished generation never coexists with them.
	tx, err = s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	if err := s.SetMetaTx(ctx, tx, "vector_generation", "2"); err != nil {
		t.Fatalf("SetMetaTx: %v", err)
	}
	if err := s.DeleteMetaTx(ctx, tx, "pending_vector_file"); err != nil {
		t.Fatalf("DeleteMetaTx: %v", err)
	}
	if err := s.DeleteMetaTx(ctx, tx, "pending_vector_generation"); err != nil {
		t.Fatalf("DeleteMetaTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit finish: %v", err)
	}
	if v, _ := s.Meta(ctx, "vector_generation"); v != "2" {
		t.Fatalf("vector_generation = %q, want 2", v)
	}
	if v, _ := s.Meta(ctx, "pending_vector_file"); v != "" {
		t.Fatalf("pending_vector_file survived the finish: %q", v)
	}
	if v, _ := s.Meta(ctx, "pending_vector_generation"); v != "" {
		t.Fatalf("pending_vector_generation survived the finish: %q", v)
	}
}

func TestAssignSlotsTx(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ids := applyTexts(t, s, "a.md", []string{"alpha", "beta", "gamma"})

	// A permuting assignment must not trip the unique slot index mid-update.
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	if err := s.AssignSlotsTx(ctx, tx, map[int64]int64{ids[0]: 2, ids[1]: 0, ids[2]: 1}); err != nil {
		t.Fatalf("AssignSlotsTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	ps, err := s.BySlot(ctx, []int64{0, 1, 2})
	if err != nil || len(ps) != 3 || ps[0].Text != "beta" || ps[1].Text != "gamma" || ps[2].Text != "alpha" {
		t.Fatalf("slots = %+v (%v), want beta/gamma/alpha at 0/1/2", ps, err)
	}

	tx, err = s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	err = s.AssignSlotsTx(ctx, tx, map[int64]int64{999: 0})
	if err == nil || !strings.Contains(err.Error(), "no passage with id 999") {
		t.Fatalf("AssignSlotsTx(missing id) = %v, want a missing-passage error", err)
	}
	_ = tx.Rollback()

	tx, err = s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	err = s.AssignSlotsTx(ctx, tx, map[int64]int64{ids[0]: 0, ids[1]: 0})
	if err == nil || !strings.Contains(err.Error(), "both map to slot 0") {
		t.Fatalf("AssignSlotsTx(duplicate slot) = %v, want a duplicate-target error", err)
	}
	_ = tx.Rollback()
}
