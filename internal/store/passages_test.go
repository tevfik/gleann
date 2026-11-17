package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// applyTexts applies one source whose passages are the given texts, with
// Tokens set to the text so the lex rows exist.
func applyTexts(t *testing.T, s *Store, source string, texts []string) []int64 {
	t.Helper()
	ps := make([]NewPassage, len(texts))
	for i, text := range texts {
		ps[i] = NewPassage{Text: text, Tokens: text}
	}
	ids, err := s.ApplySource(context.Background(), SourceDelta{
		Source:   source,
		File:     FileRow{Hash: "hash-" + source},
		Passages: ps,
	})
	if err != nil {
		t.Fatalf("ApplySource(%s): %v", source, err)
	}
	return ids
}

func lexCount(t *testing.T, s *Store, query string) int {
	t.Helper()
	var n int
	if err := s.reader.QueryRow(`SELECT count(*) FROM lex WHERE lex MATCH ?`, query).Scan(&n); err != nil {
		t.Fatalf("lex MATCH %q: %v", query, err)
	}
	return n
}

func TestApplySourceAtomicReplace(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	applyTexts(t, s, "a.go", []string{"old one", "old two"})
	applyTexts(t, s, "b.go", []string{"other file"})
	// Give a.go graph columns; ApplySource must not advance them (they
	// belong to the graph delta, RFC 003 section 6.5).
	if _, err := s.writer.Exec(`UPDATE files SET graph_hash = 'gh', graph_dirty = 1 WHERE path = 'a.go'`); err != nil {
		t.Fatal(err)
	}

	ids, err := s.ApplySource(ctx, SourceDelta{
		Source: "a.go",
		File:   FileRow{Hash: "hash-a.go-v2", Size: 42, MtimeNS: 123},
		Passages: []NewPassage{
			{Text: "new one", Tokens: "new one"},
			{Text: "new two", Tokens: "new two"},
			{Text: "new three", Tokens: "new three"},
		},
	})
	if err != nil {
		t.Fatalf("ApplySource replace: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("got %d ids, want 3", len(ids))
	}
	n, err := s.Count(ctx)
	if err != nil || n != 4 {
		t.Fatalf("Count = %d, %v; want 4 (3 new + 1 from b.go)", n, err)
	}
	if lexCount(t, s, "old") != 0 {
		t.Error("lex rows of the replaced passages were not deleted")
	}
	if lexCount(t, s, "new") != 3 {
		t.Error("lex rows of the new passages missing")
	}
	files, err := s.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := files["a.go"]
	if f.Hash != "hash-a.go-v2" || f.Size != 42 || f.MtimeNS != 123 {
		t.Errorf("manifest row not updated: %+v", f)
	}
	if f.GraphHash != "gh" || !f.GraphDirty {
		t.Errorf("ApplySource touched graph columns: %+v", f)
	}
	if _, ok := files["b.go"]; !ok {
		t.Error("b.go manifest row lost")
	}
	// The old integer IDs must not be reused (AUTOINCREMENT).
	for _, id := range ids {
		if id <= 2 {
			t.Errorf("new id %d reuses an old id", id)
		}
	}
}

// TestApplySourceLargeSourceRollback applies more than 1,000 passages in one
// transaction, then injects a failure mid-way through a replacement (a
// duplicate vec_slot violates the unique index after most rows are already
// inserted) and checks that the old passages, lex rows and manifest hash are
// fully intact (RFC 003 sections 3.6, 7.1).
func TestApplySourceLargeSourceRollback(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const n = 1200

	mkDelta := func(bad bool) SourceDelta {
		ps := make([]NewPassage, n)
		for i := range ps {
			slot := int64(i)
			if bad && i == n-100 {
				slot = 0 // collides with passage 0's slot, late in the insert loop
			}
			ps[i] = NewPassage{
				Text:    fmt.Sprintf("version one chunk %d", i),
				Tokens:  fmt.Sprintf("version one chunk %d", i),
				VecSlot: &slot,
			}
		}
		return SourceDelta{Source: "big.go", File: FileRow{Hash: "v1"}, Passages: ps}
	}
	if _, err := s.ApplySource(ctx, mkDelta(false)); err != nil {
		t.Fatalf("ApplySource %d passages: %v", n, err)
	}
	if got, err := s.Count(ctx); err != nil || got != n {
		t.Fatalf("Count = %d, %v; want %d", got, err, n)
	}

	bad := mkDelta(true)
	for i := range bad.Passages {
		bad.Passages[i].Text = fmt.Sprintf("version two chunk %d", i)
		bad.Passages[i].Tokens = bad.Passages[i].Text
	}
	bad.File.Hash = "v2"
	if _, err := s.ApplySource(ctx, bad); err == nil {
		t.Fatal("ApplySource with duplicate vec_slot succeeded, want constraint failure")
	}

	// Everything must be exactly the old state.
	if got, err := s.Count(ctx); err != nil || got != n {
		t.Fatalf("after rollback Count = %d, %v; want %d", got, err, n)
	}
	if lexCount(t, s, "version AND one") != n {
		t.Error("old lex rows changed after rollback")
	}
	if lexCount(t, s, "two") != 0 {
		t.Error("new lex rows survived the rollback")
	}
	files, err := s.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if files["big.go"].Hash != "v1" {
		t.Errorf("manifest hash advanced despite failure: %+v", files["big.go"])
	}
	p, err := s.Get(ctx, 1)
	if err != nil || p.Text != "version one chunk 0" {
		t.Fatalf("first passage after rollback = %+v, %v", p, err)
	}
}

func TestPIDStabilityOnInsert(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ids1 := applyTexts(t, s, "a.go", []string{"alpha", "beta", "gamma"})
	pidOf := func(ids []int64) map[string]string {
		out := make(map[string]string)
		for _, id := range ids {
			p, err := s.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			out[p.Text] = p.PID
		}
		return out
	}
	before := pidOf(ids1)

	// Insert a line at the top: every chunk_no shifts, unchanged texts keep
	// their pid (RFC 003 "Stable passage IDs").
	ids2 := applyTexts(t, s, "a.go", []string{"inserted at top", "alpha", "beta", "gamma"})
	after := pidOf(ids2)
	for _, text := range []string{"alpha", "beta", "gamma"} {
		if before[text] != after[text] {
			t.Errorf("pid of %q changed after top insertion: %s -> %s", text, before[text], after[text])
		}
	}
	for i, id := range ids2 {
		p, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.ChunkNo != i {
			t.Errorf("chunk_no of %q = %d, want %d", p.Text, p.ChunkNo, i)
		}
	}

	// Identical texts of one source get distinct pids via dup_index.
	ids3 := applyTexts(t, s, "dup.go", []string{"same", "same"})
	p0, err := s.Get(ctx, ids3[0])
	if err != nil {
		t.Fatal(err)
	}
	p1, err := s.Get(ctx, ids3[1])
	if err != nil {
		t.Fatal(err)
	}
	if p0.PID == p1.PID {
		t.Error("identical texts got the same pid")
	}
}

func TestRemoveSource(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyTexts(t, s, "a.go", []string{"apple banana", "apple cherry"})
	applyTexts(t, s, "b.go", []string{"date elderberry"})

	removed, err := s.RemoveSource(ctx, "a.go")
	if err != nil || removed != 2 {
		t.Fatalf("RemoveSource = %d, %v; want 2", removed, err)
	}
	if n, err := s.Count(ctx); err != nil || n != 1 {
		t.Fatalf("Count = %d, %v; want 1", n, err)
	}
	if lexCount(t, s, "apple") != 0 {
		t.Error("lex rows of the removed source survived")
	}
	if lexCount(t, s, "date") != 1 {
		t.Error("lex rows of the kept source were touched")
	}
	files, err := s.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["a.go"]; ok {
		t.Error("files row of the removed source survived")
	}
	if _, ok := files["b.go"]; !ok {
		t.Error("files row of the kept source lost")
	}
	// Removing an unknown source is a clean no-op.
	if removed, err := s.RemoveSource(ctx, "missing.go"); err != nil || removed != 0 {
		t.Fatalf("RemoveSource(missing) = %d, %v; want 0, nil", removed, err)
	}
}

func TestReassignSlots(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mk := func(texts []string) SourceDelta {
		ps := make([]NewPassage, len(texts))
		for i, text := range texts {
			slot := int64(i)
			ps[i] = NewPassage{Text: text, VecSlot: &slot}
		}
		return SourceDelta{Source: "a.go", File: FileRow{Hash: "h"}, Passages: ps}
	}
	if _, err := s.ApplySource(ctx, mk([]string{"a", "b", "c", "d"})); err != nil {
		t.Fatal(err)
	}

	// Simulated compaction: a permutation, so transient unique-index
	// collisions must be avoided by the implementation.
	if err := s.ReassignSlots(ctx, map[int64]int64{0: 2, 1: 0, 2: 1}); err != nil {
		t.Fatalf("ReassignSlots: %v", err)
	}
	want := map[int64]string{0: "b", 1: "c", 2: "a", 3: "d"}
	for slot, text := range want {
		ps, err := s.BySlot(ctx, []int64{slot})
		if err != nil || len(ps) != 1 || ps[0].Text != text {
			t.Fatalf("BySlot(%d) = %+v, %v; want text %q", slot, ps, err, text)
		}
	}

	// Unknown old slot is an error, and nothing changes.
	if err := s.ReassignSlots(ctx, map[int64]int64{99: 0}); err == nil {
		t.Fatal("ReassignSlots with unknown old slot succeeded, want error")
	}
	// A target already held by a passage outside the map must fail and roll
	// back (unique index passages_slot).
	if err := s.ReassignSlots(ctx, map[int64]int64{0: 3}); err == nil {
		t.Fatal("ReassignSlots onto an occupied slot succeeded, want error")
	}
	ps, err := s.BySlot(ctx, []int64{0})
	if err != nil || len(ps) != 1 || ps[0].Text != "b" {
		t.Fatalf("slot 0 changed after failed reassignment: %+v, %v", ps, err)
	}
	// Duplicate targets are rejected with a clear message.
	if err := s.ReassignSlots(ctx, map[int64]int64{0: 5, 1: 5}); err == nil || !strings.Contains(err.Error(), "both map to") {
		t.Fatalf("duplicate target err = %v, want 'both map to'", err)
	}
}

func TestLexTokensHook(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// Tokens are written only when non-empty; ApplySource never tokenizes.
	if _, err := s.ApplySource(ctx, SourceDelta{
		Source:   "a.go",
		File:     FileRow{Hash: "h"},
		Passages: []NewPassage{{Text: "raw text stays untouched"}, {Text: "with tokens", Tokens: "custom tokens"}},
	}); err != nil {
		t.Fatal(err)
	}
	if lexCount(t, s, "custom") != 1 {
		t.Error("tokens hook did not insert the lex row")
	}
	if lexCount(t, s, "raw") != 0 {
		t.Error("ApplySource tokenized text itself; it must only use the Tokens hook")
	}
	// Replacing the source deletes the old lex rows even when the new
	// passages carry no tokens.
	if _, err := s.ApplySource(ctx, SourceDelta{
		Source:   "a.go",
		File:     FileRow{Hash: "h2"},
		Passages: []NewPassage{{Text: "with tokens"}},
	}); err != nil {
		t.Fatal(err)
	}
	if lexCount(t, s, "custom") != 0 {
		t.Error("old lex row survived a replacement without tokens")
	}
}

func TestReadOrderingAndMissing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ids := applyTexts(t, s, "a.go", []string{"a", "b", "c"})
	ids = append(ids, applyTexts(t, s, "b.go", []string{"d", "e"})...)

	// GetMany and BySlot answer in the caller's order, so a ranked list of
	// IDs or vector slots keeps its ranking.
	ps, err := s.GetMany(ctx, []int64{ids[4], ids[0], ids[2]})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || ps[0].ID != ids[4] || ps[1].ID != ids[0] || ps[2].ID != ids[2] {
		t.Fatalf("GetMany not in caller order: %+v", ps)
	}
	// More IDs than one IN list holds; a repeated ID is answered each time.
	many := make([]int64, 0, 1200)
	for i := 0; i < 1200; i++ {
		many = append(many, ids[i%len(ids)])
	}
	if ps, err := s.GetMany(ctx, many); err != nil || len(ps) != len(many) || ps[1199].ID != many[1199] {
		t.Fatalf("GetMany over %d ids = %d passages, %v", len(many), len(ps), err)
	}
	if ps, err := s.GetMany(ctx, nil); err != nil || ps != nil {
		t.Fatalf("GetMany(nil) = %+v, %v; want nil, nil", ps, err)
	}
	// Missing IDs are skipped.
	if ps, err := s.GetMany(ctx, []int64{ids[0], 9999}); err != nil || len(ps) != 1 {
		t.Fatalf("GetMany with missing id = %+v, %v", ps, err)
	}
	// Assign slots, then check BySlot keeps the order of the slots asked for.
	if _, err := s.writer.Exec(`UPDATE passages SET vec_slot = id * 10`); err != nil {
		t.Fatal(err)
	}
	ps, err = s.BySlot(ctx, []int64{ids[3] * 10, ids[1] * 10})
	if err != nil || len(ps) != 2 || ps[0].ID != ids[3] || ps[1].ID != ids[1] {
		t.Fatalf("BySlot not in caller order: %+v, %v", ps, err)
	}

	var seen []int64
	if err := s.ForEach(ctx, func(p Passage) error {
		seen = append(seen, p.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i-1] >= seen[i] {
			t.Fatalf("ForEach not ordered by id: %v", seen)
		}
	}
	// fn errors stop the iteration and surface.
	stop := errors.New("stop")
	if err := s.ForEach(ctx, func(Passage) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("ForEach fn error = %v, want wrapped stop", err)
	}
	// Get of a missing id wraps sql.ErrNoRows.
	if _, err := s.Get(ctx, 9999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get(missing) err = %v, want sql.ErrNoRows", err)
	}
}

// TestBeginWriteApplySourceTx covers the transaction-scoped variant of
// ApplySource (RFC 003 section 6.5): the caller owns the transaction, a
// rollback leaves the old state intact, a commit advances it, and the
// cached lex row count is invalidated inside ApplySourceTx so no caller can
// forget a post-commit reset and serve a stale document-frequency cut.
func TestBeginWriteApplySourceTx(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyTexts(t, s, "a.go", []string{"old one"})
	// Warm the cached lex row count so the reset is observable.
	if _, err := s.Stats(ctx); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.lexRows.Load() != 1 {
		t.Fatalf("lexRows cache = %d, want 1 after Stats", s.lexRows.Load())
	}

	delta := SourceDelta{
		Source: "a.go",
		File:   FileRow{Hash: "h2"},
		Passages: []NewPassage{
			{Text: "new one", Tokens: "new one"},
			{Text: "new two", Tokens: "new two"},
		},
	}

	// Rollback path: nothing advances.
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	ids, err := s.ApplySourceTx(ctx, tx, delta)
	if err != nil {
		tx.Rollback()
		t.Fatalf("ApplySourceTx: %v", err)
	}
	if len(ids) != 2 {
		tx.Rollback()
		t.Fatalf("ids = %v, want 2", ids)
	}
	if s.lexRows.Load() != 0 {
		tx.Rollback()
		t.Fatalf("lexRows cache = %d, want 0: ApplySourceTx must invalidate it before the caller commits", s.lexRows.Load())
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if n, err := s.Count(ctx); err != nil || n != 1 {
		t.Fatalf("Count after rollback = %d, %v; want 1", n, err)
	}
	if lexCount(t, s, "old") != 1 || lexCount(t, s, "new") != 0 {
		t.Fatalf("lex after rollback: old=%d new=%d, want 1/0", lexCount(t, s, "old"), lexCount(t, s, "new"))
	}
	files, err := s.Files(ctx)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if files["a.go"].Hash != "hash-a.go" {
		t.Fatalf("hash after rollback = %q, want hash-a.go", files["a.go"].Hash)
	}

	// Commit path: the caller's commit advances everything.
	tx2, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite commit: %v", err)
	}
	defer tx2.Rollback()
	if _, err := s.ApplySourceTx(ctx, tx2, delta); err != nil {
		t.Fatalf("ApplySourceTx commit: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if n, err := s.Count(ctx); err != nil || n != 2 {
		t.Fatalf("Count after commit = %d, %v; want 2", n, err)
	}
	if lexCount(t, s, "new") != 2 || lexCount(t, s, "old") != 0 {
		t.Fatalf("lex after commit: new=%d old=%d, want 2/0", lexCount(t, s, "new"), lexCount(t, s, "old"))
	}
	files, err = s.Files(ctx)
	if err != nil {
		t.Fatalf("Files after commit: %v", err)
	}
	if files["a.go"].Hash != "h2" {
		t.Fatalf("hash after commit = %q, want h2", files["a.go"].Hash)
	}

	// The manifest-path validation applies to the Tx variant too.
	tx3, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite validation: %v", err)
	}
	defer tx3.Rollback()
	bad := delta
	bad.File.Path = "other.go"
	if _, err := s.ApplySourceTx(ctx, tx3, bad); err == nil || !strings.Contains(err.Error(), "differs from the source") {
		t.Fatalf("mismatched path err = %v, want the manifest-path error", err)
	}
}
