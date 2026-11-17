package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A memory.sqlite that is a directory cannot be opened; the open fails
// instead of creating a second store.
func TestOpenStore_SQLitePathUnusable(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	if err := os.Mkdir(sqlitePathFor(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(legacy)
	if err == nil {
		s.Close()
		t.Fatal("OpenStore over a directory named memory.sqlite succeeded")
	}
	wantErr(t, "OpenStore", err, "connecting memory writer")
}

// A store that was migrated but whose legacy file was replaced by content
// that is neither the guard nor a database refuses to open, and releases
// its handles.
func TestOpenStore_CompleteSwitchFailure(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "memory.db")
	s, err := OpenStore(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta("migrated_from", "memory.db"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.WriteFile(legacy, []byte("someone else's file, longer than the guard text is not required"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(legacy)
	if err == nil {
		s.Close()
		t.Fatal("OpenStore accepted an unreadable legacy file next to a migrated store")
	}
	wantErr(t, "OpenStore", err, "cannot be read")
}

// Adding the same block again reinforces it instead of storing a copy, for
// both short-term (in memory) and persisted blocks.
func TestAdd_DuplicatesAreMerged(t *testing.T) {
	s := openTestStore(t)

	short := &Block{Tier: TierShort, Label: "note", Content: "scratch", Tags: []string{"a"}}
	if err := s.Add(short); err != nil {
		t.Fatal(err)
	}
	again := &Block{Tier: TierShort, Label: "note", Content: "scratch", Tags: []string{"b"}}
	if err := s.Add(again); err != nil {
		t.Fatal(err)
	}
	if len(s.shortTerm) != 1 || s.shortTerm[0].Confirms != 1 || len(s.shortTerm[0].Tags) != 2 {
		t.Fatalf("shortTerm = %+v, want one block, confirmed once, both tags", s.shortTerm)
	}
	if again.ID != short.ID || again.Confirms != 1 {
		t.Fatalf("caller's block = %+v, want the merged block", again)
	}

	long := addLong(t, s, "", "persisted fact")
	dup := &Block{Tier: TierLong, Label: "note", Content: "persisted fact", Tags: []string{"extra"}}
	if err := s.Add(dup); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(long.ID)
	if err != nil || got.Confirms != 1 || len(got.Tags) != 1 || got.Tags[0] != "extra" {
		t.Fatalf("persisted block = %+v, %v; want confirmed once with the new tag", got, err)
	}
	if found := searchIDs(t, s, "persisted"); len(found) != 1 {
		t.Fatalf("lexical index holds %v, want the single block", found)
	}
}

// Every persisted mutation reports a database that lost its blocks table.
func TestStore_MissingBlocksTable(t *testing.T) {
	s := openTestStore(t)
	b := addLong(t, s, "b1", "will be orphaned")
	execSQL(t, s, `DROP TABLE blocks`)

	wantErr(t, "Delete", s.Delete("b1"), "reading block b1")
	wantErr(t, "Update", s.Update(b), "reading block b1")
	_, err := s.MarkSuspect([]string{"x.go"}, nil)
	wantErr(t, "MarkSuspect", err, "marking suspect")
}

// A refused DELETE rolls back the whole bulk removal and says which one.
func TestStore_BulkDeletesAborted(t *testing.T) {
	s := openTestStore(t)
	past := time.Now().Add(-time.Hour)
	addLong(t, s, "keep", "kept content")
	expired := &Block{ID: "old", Tier: TierLong, Label: "note", Content: "expired content", ExpiresAt: &past}
	if err := s.Add(expired); err != nil {
		t.Fatal(err)
	}
	abortOn(t, s, "DELETE", "blocks")

	if _, err := s.ClearTier(TierLong); err == nil {
		t.Error("ClearTier succeeded with deletes aborted")
	} else {
		wantErr(t, "ClearTier", err, "clearing tier long")
	}
	if _, err := s.ClearAll(); err == nil {
		t.Error("ClearAll succeeded with deletes aborted")
	} else {
		wantErr(t, "ClearAll", err, "clearing blocks")
	}
	if _, err := s.PruneExpired(); err == nil {
		t.Error("PruneExpired succeeded with deletes aborted")
	} else {
		wantErr(t, "PruneExpired", err, "pruning expired blocks")
	}
	if _, err := s.DeleteOlderThan(TierLong, -time.Hour); err == nil {
		t.Error("DeleteOlderThan succeeded with deletes aborted")
	} else {
		wantErr(t, "DeleteOlderThan", err, "deleting old blocks in tier long")
	}

	// Nothing was half-removed: the lexical index still finds the block.
	if found := searchIDs(t, s, "kept"); len(found) != 1 {
		t.Fatalf("search after the aborted deletes = %v, want keep", found)
	}
}

// A summary row that cannot be read as text is reported, not skipped.
func TestSummaries_UnreadableRow(t *testing.T) {
	s := openTestStore(t)
	execSQL(t, s, `DROP TABLE summaries`)
	execSQL(t, s, `CREATE TABLE summaries (key TEXT PRIMARY KEY, data TEXT)`)
	execSQL(t, s, `INSERT INTO summaries (key, data) VALUES ('k', NULL)`)

	_, err := s.ListSummaries()
	wantErr(t, "ListSummaries", err, "listing summaries")
	_, err = s.DeleteSummariesOlderThan(time.Hour)
	wantErr(t, "DeleteSummariesOlderThan", err, "listing summaries")
}

// A block whose symbol is qualified is flagged when only its short name
// changed.
func TestMarkSuspect_QualifiedSymbolMatchesShortName(t *testing.T) {
	s := openTestStore(t)
	b := &Block{ID: "q", Tier: TierLong, Label: "note", Content: "uses a helper", Symbols: []string{"pkg.sub.Helper"}}
	if err := s.Add(b); err != nil {
		t.Fatal(err)
	}
	n, err := s.MarkSuspect(nil, []string{"Helper"})
	if err != nil || n != 1 {
		t.Fatalf("MarkSuspect = %d, %v", n, err)
	}
	got, err := s.Get("q")
	if err != nil || !got.Suspect || got.StaleReason != "symbol pkg.sub.Helper was modified" {
		t.Fatalf("block = %+v, %v", got, err)
	}
}
