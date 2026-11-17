package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// A new files row takes the graph columns of the delta; an existing row
// keeps its own: graph_hash and graph_dirty belong to the graph store, and
// ApplySource must not move them (RFC 003 section 6.5).
func TestApplySourceGraphColumnsOnlyOnInsert(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.ApplySource(ctx, SourceDelta{Source: "a.go", File: FileRow{Hash: "h1", GraphHash: "g1", GraphDirty: true}}); err != nil {
		t.Fatal(err)
	}
	f, _ := fileRow(t, s, "a.go")
	if f.GraphHash != "g1" || !f.GraphDirty || f.Hash != "h1" {
		t.Fatalf("new row = %+v", f)
	}
	if _, err := s.ApplySource(ctx, SourceDelta{Source: "a.go", File: FileRow{Hash: "h2", Size: 9}}); err != nil {
		t.Fatal(err)
	}
	f, _ = fileRow(t, s, "a.go")
	if f.GraphHash != "g1" || !f.GraphDirty || f.Hash != "h2" || f.Size != 9 {
		t.Fatalf("updated row = %+v; want the manifest columns advanced and the graph columns kept", f)
	}
}

// Empty slot mappings are no-ops, and BySlot leaves out slots no passage
// holds.
func TestSlotMappingEdgeCases(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applySlotted(t, s, "a.go", "h", 0, 1)
	before := dump(t, s)
	if err := s.ReassignSlots(ctx, nil); err != nil {
		t.Fatal(err)
	}
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReassignSlotsTx(ctx, tx, map[int64]int64{}); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignSlotsTx(ctx, tx, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if after := dump(t, s); after != before {
		t.Fatal("an empty mapping changed the store")
	}
	ps, err := s.BySlot(ctx, []int64{1, 7, 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("BySlot returned %d passages, want 2 (slot 7 is free)", len(ps))
	}
	got := map[int64]bool{}
	for _, p := range ps {
		got[*p.VecSlot] = true
	}
	if !got[0] || !got[1] {
		t.Fatalf("BySlot = %+v, want slots 0 and 1", ps)
	}
}

// The Tx variants report the caller's finished transaction instead of
// writing outside it.
func TestTxVariantsOnFinishedTransaction(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applySlotted(t, s, "a.go", "h", 0)
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	before := dump(t, s)
	checks := map[string]error{}
	_, checks["ApplySourceTx"] = s.ApplySourceTx(ctx, tx, SourceDelta{Source: "b.go", Passages: []NewPassage{{Text: "x"}}})
	_, checks["RemoveSourceTx"] = s.RemoveSourceTx(ctx, tx, "a.go")
	checks["ReassignSlotsTx"] = s.ReassignSlotsTx(ctx, tx, map[int64]int64{0: 3})
	checks["AssignSlotsTx"] = s.AssignSlotsTx(ctx, tx, map[int64]int64{1: 3})
	_, checks["ReplacePassagesTx"] = s.ReplacePassagesTx(ctx, tx, nil, RebuildHooks{})
	checks["SetMetaTx"] = s.SetMetaTx(ctx, tx, "k", "v")
	for name, err := range checks {
		if !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("%s on a finished transaction: err = %v, want sql.ErrTxDone", name, err)
		}
	}
	if after := dump(t, s); after != before {
		t.Fatal("a write on a finished transaction reached the store")
	}
}
