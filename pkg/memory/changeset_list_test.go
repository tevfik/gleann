package memory

import (
	"testing"
	"time"
)

// ListChangeSets is what `gleann memory reflect --list` reads: persisted runs,
// newest first, capped by limit, without an LLM.

func TestListChangeSetsOrdersNewestFirst(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	// Saved oldest-last on purpose: the order of the rows must not matter.
	runs := []*ChangeSet{
		{RunID: "run-old", Timestamp: base, BlocksAdded: []string{"a1"}},
		{RunID: "run-new", Timestamp: base.Add(2 * time.Hour), BlocksSuperseded: []string{"b1", "b2"}},
		{RunID: "run-mid", Timestamp: base.Add(time.Hour), BlocksConfirmed: []BlockConfirmation{{ID: "c1", PriorConfirms: 0}}},
	}
	for _, cs := range runs {
		if err := s.SaveChangeSet(cs); err != nil {
			t.Fatalf("SaveChangeSet %s: %v", cs.RunID, err)
		}
	}

	got, err := s.ListChangeSets(0)
	if err != nil {
		t.Fatalf("ListChangeSets(0): %v", err)
	}
	want := []string{"run-new", "run-mid", "run-old"}
	if len(got) != len(want) {
		t.Fatalf("ListChangeSets(0) returned %d runs, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].RunID != id {
			t.Errorf("run %d = %q, want %q", i, got[i].RunID, id)
		}
	}

	// The decoded payload keeps its diff, not just the run ID.
	if len(got[0].BlocksSuperseded) != 2 {
		t.Errorf("newest run superseded %d blocks, want 2", len(got[0].BlocksSuperseded))
	}
	if len(got[1].BlocksConfirmed) != 1 || got[1].BlocksConfirmed[0].ID != "c1" {
		t.Errorf("middle run confirmations = %+v, want one for c1", got[1].BlocksConfirmed)
	}
	if got[2].Timestamp.Equal((time.Time{})) {
		t.Error("oldest run lost its timestamp")
	}
}

func TestListChangeSetsTieBreaksOnRunID(t *testing.T) {
	s := openTestStore(t)
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	for _, id := range []string{"run-a", "run-c", "run-b"} {
		if err := s.SaveChangeSet(&ChangeSet{RunID: id, Timestamp: at}); err != nil {
			t.Fatalf("SaveChangeSet %s: %v", id, err)
		}
	}
	got, err := s.ListChangeSets(-1)
	if err != nil {
		t.Fatalf("ListChangeSets(-1): %v", err)
	}
	want := []string{"run-c", "run-b", "run-a"}
	if len(got) != len(want) {
		t.Fatalf("ListChangeSets(-1) returned %d runs, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].RunID != id {
			t.Errorf("run %d = %q, want %q (same timestamp must order by run ID desc)", i, got[i].RunID, id)
		}
	}
}

func TestListChangeSetsLimit(t *testing.T) {
	s := openTestStore(t)
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id := "run-" + string(rune('a'+i))
		if err := s.SaveChangeSet(&ChangeSet{RunID: id, Timestamp: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("SaveChangeSet %s: %v", id, err)
		}
	}

	for _, limit := range []int{0, -1, 99} {
		got, err := s.ListChangeSets(limit)
		if err != nil {
			t.Fatalf("ListChangeSets(%d): %v", limit, err)
		}
		if len(got) != 5 {
			t.Errorf("ListChangeSets(%d) returned %d runs, want all 5", limit, len(got))
		}
	}

	got, err := s.ListChangeSets(2)
	if err != nil {
		t.Fatalf("ListChangeSets(2): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListChangeSets(2) returned %d runs, want 2", len(got))
	}
	// The cap must keep the newest runs, not the first rows written.
	if got[0].RunID != "run-e" || got[1].RunID != "run-d" {
		t.Errorf("ListChangeSets(2) = %q, %q, want run-e, run-d", got[0].RunID, got[1].RunID)
	}
}

func TestListChangeSetsEmpty(t *testing.T) {
	s := openTestStore(t)
	got, err := s.ListChangeSets(0)
	if err != nil {
		t.Fatalf("ListChangeSets on an empty store: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty store listed %d runs", len(got))
	}
}

func TestManagerListReflectionsTracksRevert(t *testing.T) {
	s := openTestStore(t)
	mgr := NewManager(s)

	base := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	for i, id := range []string{"run-1", "run-2"} {
		cs := &ChangeSet{
			RunID:       id,
			Timestamp:   base.Add(time.Duration(i) * time.Hour),
			BlocksAdded: []string{"ffffffff00000001"},
		}
		if err := s.SaveChangeSet(cs); err != nil {
			t.Fatalf("SaveChangeSet %s: %v", id, err)
		}
	}

	before, err := mgr.ListReflections(0)
	if err != nil {
		t.Fatalf("ListReflections: %v", err)
	}
	if len(before) != 2 || before[0].RunID != "run-2" {
		t.Fatalf("ListReflections = %+v, want run-2 then run-1", before)
	}

	if _, err := mgr.RevertReflection("run-2"); err != nil {
		t.Fatalf("RevertReflection: %v", err)
	}

	after, err := mgr.ListReflections(0)
	if err != nil {
		t.Fatalf("ListReflections after revert: %v", err)
	}
	if len(after) != 1 || after[0].RunID != "run-1" {
		t.Errorf("after revert ListReflections = %+v, want only run-1", after)
	}

	limited, err := mgr.ListReflections(0)
	if err != nil {
		t.Fatalf("ListReflections(0): %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("ListReflections after revert returned %d runs, want 1", len(limited))
	}
}
