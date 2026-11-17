package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests in this file inject storage faults into a real memory database
// (SQLite triggers that abort a statement, a dropped table, rows that do not
// decode) and check that every operation reports the fault and that the
// write transaction rolls back as a whole: no half-applied deletes, no block
// row without its lexical row.

// execSQL runs raw SQL on the store's writer connection.
func execSQL(t *testing.T, s *Store, stmt string, args ...any) {
	t.Helper()
	if _, err := s.writer.Exec(stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

// abortOn installs a trigger that aborts every <op> on table.
func abortOn(t *testing.T, s *Store, op, table string) {
	t.Helper()
	name := "fault_" + strings.ToLower(op) + "_" + table
	execSQL(t, s, `CREATE TRIGGER `+name+` BEFORE `+op+` ON `+table+` BEGIN SELECT RAISE(ABORT, 'injected `+op+` fault'); END`)
	t.Cleanup(func() { _, _ = s.writer.Exec(`DROP TRIGGER IF EXISTS ` + name) })
}

func wantErr(t *testing.T, op string, err error, sub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error containing %q, got nil", op, sub)
	}
	if !strings.Contains(err.Error(), sub) {
		t.Fatalf("%s: error %q does not contain %q", op, err, sub)
	}
}

func addLong(t *testing.T, s *Store, id, content string) *Block {
	t.Helper()
	b := &Block{ID: id, Tier: TierLong, Label: "note", Content: content}
	if err := s.Add(b); err != nil {
		t.Fatalf("Add %s: %v", id, err)
	}
	return b
}

func searchIDs(t *testing.T, s *Store, q string) []string {
	t.Helper()
	got, err := s.Search(q)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, b := range got {
		ids = append(ids, b.ID)
	}
	return ids
}

// A failing DELETE on blocks rolls back every destructive operation, so no
// block disappears and its lexical row stays in place.
func TestStoreFaults_DeletesRollBack(t *testing.T) {
	s := openTestStore(t)
	addLong(t, s, "keep-1", "kubernetes cluster upgrade notes")
	exp := time.Now().Add(-time.Hour)
	if err := s.Add(&Block{ID: "expired", Tier: TierMedium, Content: "old expired thing", ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	abortOn(t, s, "DELETE", "blocks")

	wantErr(t, "Delete", s.Delete("keep-1"), "deleting block keep-1")
	_, err := s.ClearTier(TierLong)
	wantErr(t, "ClearTier", err, "clearing tier long")
	_, err = s.ClearAll()
	wantErr(t, "ClearAll", err, "clearing blocks")
	_, err = s.PruneExpired()
	wantErr(t, "PruneExpired", err, "pruning expired blocks")
	_, err = s.DeleteOlderThan(TierLong, -time.Hour)
	wantErr(t, "DeleteOlderThan", err, "deleting old blocks in tier long")
	wantErr(t, "Promote", s.Promote("keep-1", TierMedium), "deleting block keep-1")

	if b, err := s.Get("keep-1"); err != nil || b.Tier != TierLong {
		t.Fatalf("keep-1 after failed deletes: %+v, %v", b, err)
	}
	// Its lexical row survived the rolled-back lexical deletes: a fresh
	// update still finds and replaces exactly one row.
	if ids := searchIDs(t, s, "kubernetes"); len(ids) != 1 || ids[0] != "keep-1" {
		t.Fatalf("search after failed deletes = %v", ids)
	}
	var lex int
	if err := s.reader.QueryRow(`SELECT count(*) FROM blocks_lex WHERE blocks_lex MATCH 'kubernetes'`).Scan(&lex); err != nil {
		t.Fatal(err)
	}
	if lex != 1 {
		t.Fatalf("lexical rows for keep-1 = %d, want 1 (the rolled-back delete must not drop it)", lex)
	}
	var rows int
	if err := s.reader.QueryRow(`SELECT count(*) FROM blocks`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("%d block rows after failed deletes, want 2", rows)
	}
}

// A failing UPDATE leaves the stored block as it was: no confirm count, no
// suspect flag, no new content.
func TestStoreFaults_UpdatesRollBack(t *testing.T) {
	s := openTestStore(t)
	b := addLong(t, s, "u1", "postgres connection pool size is 20")
	b.Paths = []string{"db/pool.go"}
	if err := s.Update(b); err != nil {
		t.Fatal(err)
	}
	abortOn(t, s, "UPDATE", "blocks")

	// Re-adding the same block takes the deduplication path (an UPDATE).
	err := s.Add(&Block{ID: "u1", Tier: TierLong, Content: "postgres connection pool size is 20"})
	wantErr(t, "Add duplicate", err, "updating block u1")
	// The same id in another tier overwrites the row (also an UPDATE).
	err = s.Add(&Block{ID: "u1", Tier: TierMedium, Content: "moved"})
	wantErr(t, "Add other tier", err, "updating block u1")
	changed := *b
	changed.Content = "postgres connection pool size is 50"
	wantErr(t, "Update", s.Update(&changed), "updating block u1")
	n, err := s.MarkSuspect([]string{"db/pool.go"}, nil)
	wantErr(t, "MarkSuspect", err, "updating block u1")
	if n != 0 {
		t.Fatalf("MarkSuspect counted %d blocks despite failing", n)
	}

	got, err := s.Get("u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != TierLong || got.Confirms != 0 || got.Suspect || !strings.Contains(got.Content, "20") {
		t.Fatalf("block changed by failed writes: %+v", got)
	}
}

// A failing INSERT stores nothing; a dropped lexical table makes the insert
// of the lexical row fail and the block row is rolled back with it.
func TestStoreFaults_InsertsRollBack(t *testing.T) {
	s := openTestStore(t)
	abortOn(t, s, "INSERT", "blocks")
	err := s.Add(&Block{ID: "i1", Tier: TierLong, Content: "never stored"})
	wantErr(t, "Add", err, "inserting block i1")
	if _, err := s.Get("i1"); err == nil {
		t.Fatal("block of a failed insert is readable")
	}

	t2 := openTestStore(t)
	addLong(t, t2, "x1", "existing block")
	execSQL(t, t2, `DROP TABLE blocks_lex`)
	err = t2.Add(&Block{ID: "x2", Tier: TierLong, Content: "needs a lexical row"})
	wantErr(t, "Add without blocks_lex", err, "inserting lexical row")
	if _, err := t2.Get("x2"); err == nil {
		t.Fatal("block row kept although its lexical row failed")
	}
	wantErr(t, "Delete without blocks_lex", t2.Delete("x1"), "deleting lexical row for block x1")
	wantErr(t, "Update without blocks_lex", t2.Update(&Block{ID: "x1", Tier: TierLong, Content: "c"}), "deleting lexical row")
	err = t2.Add(&Block{ID: "x1", Tier: TierLong, Content: "existing block"})
	wantErr(t, "Add duplicate without blocks_lex", err, "deleting lexical row")
	_, err = t2.ClearTier(TierLong)
	wantErr(t, "ClearTier without blocks_lex", err, "clearing lexical rows for tier long")
	_, err = t2.ClearAll()
	wantErr(t, "ClearAll without blocks_lex", err, "clearing lexical rows")
	_, err = t2.PruneExpired()
	wantErr(t, "PruneExpired without blocks_lex", err, "pruning lexical rows")
	_, err = t2.DeleteOlderThan(TierLong, time.Hour)
	wantErr(t, "DeleteOlderThan without blocks_lex", err, "deleting lexical rows for tier long")
	if _, err := t2.Get("x1"); err != nil {
		t.Fatalf("x1 lost after failed operations: %v", err)
	}
}

// A row whose JSON columns do not decode is reported by every reader instead
// of being returned half-filled.
func TestStoreFaults_UndecodableBlockRow(t *testing.T) {
	s := openTestStore(t)
	addLong(t, s, "bad", "row with broken tags")
	execSQL(t, s, `UPDATE blocks SET tags = '{not json' WHERE id = 'bad'`)

	_, err := s.Get("bad")
	wantErr(t, "Get", err, "decoding block bad tags")
	_, err = s.List("")
	wantErr(t, "List", err, "listing blocks")
	_, err = s.Search("broken")
	wantErr(t, "Search", err, "listing blocks")
	_, err = s.SearchPhrase("broken")
	wantErr(t, "SearchPhrase", err, "listing blocks")
	_, err = s.BuildContext()
	wantErr(t, "BuildContext", err, "building context")
	_, err = s.MarkSuspect([]string{"x"}, nil)
	wantErr(t, "MarkSuspect", err, "marking suspect")
	err = s.Add(&Block{ID: "bad", Tier: TierLong, Content: "again"})
	wantErr(t, "Add over bad row", err, "decoding block bad tags")
	wantErr(t, "Promote", s.Promote("bad", TierMedium), "decoding block bad tags")
}

// Summaries: a corrupt row is skipped (as the bbolt store did) and failing
// writes or deletes are reported without losing the healthy rows.
func TestStoreFaults_Summaries(t *testing.T) {
	s := openTestStore(t)
	old := &Summary{ConversationID: "c-old", Content: "old talk", CreatedAt: time.Now().Add(-48 * time.Hour)}
	if err := s.SaveSummary(old); err != nil {
		t.Fatal(err)
	}
	execSQL(t, s, `INSERT INTO summaries (key, data) VALUES ('zzz_corrupt', '{broken')`)

	sums, err := s.ListSummaries()
	if err != nil || len(sums) != 1 || sums[0].ConversationID != "c-old" {
		t.Fatalf("ListSummaries = %+v, %v; want only the healthy summary", sums, err)
	}

	abortOn(t, s, "DELETE", "summaries")
	n, err := s.DeleteSummariesOlderThan(time.Hour)
	wantErr(t, "DeleteSummariesOlderThan", err, "deleting summary")
	if n != 0 {
		t.Fatalf("counted %d deletions for a failed delete", n)
	}
	abortOn(t, s, "INSERT", "summaries")
	wantErr(t, "SaveSummary", s.SaveSummary(&Summary{ConversationID: "c-new", Content: "x"}), "saving summary")

	execSQL(t, s, `DROP TRIGGER fault_delete_summaries`)
	n, err = s.DeleteSummariesOlderThan(time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("DeleteSummariesOlderThan = %d, %v; want 1 (the corrupt row is skipped, not deleted)", n, err)
	}
	var left int
	if err := s.reader.QueryRow(`SELECT count(*) FROM summaries`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("%d summary rows left, want the corrupt one", left)
	}
}

// Every operation on a closed store fails cleanly instead of panicking.
func TestStoreFaults_ClosedStore(t *testing.T) {
	s := openTestStore(t)
	addLong(t, s, "c1", "content")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ops := map[string]func() error{
		"Add":         func() error { return s.Add(&Block{Tier: TierLong, Content: "x"}) },
		"Get":         func() error { _, err := s.Get("c1"); return err },
		"Delete":      func() error { return s.Delete("c1") },
		"Update":      func() error { return s.Update(&Block{ID: "c1", Tier: TierLong}) },
		"List":        func() error { _, err := s.List(TierLong); return err },
		"ClearTier":   func() error { _, err := s.ClearTier(TierLong); return err },
		"ClearAll":    func() error { _, err := s.ClearAll(); return err },
		"SaveSummary": func() error { return s.SaveSummary(&Summary{ConversationID: "c"}) },
		"ListSumm":    func() error { _, err := s.ListSummaries(); return err },
		"DelSumm":     func() error { _, err := s.DeleteSummariesOlderThan(time.Hour); return err },
		"Prune":       func() error { _, err := s.PruneExpired(); return err },
		"OlderThan":   func() error { _, err := s.DeleteOlderThan(TierMedium, time.Hour); return err },
		"Stats":       func() error { _, err := s.Stats(); return err },
		"Context":     func() error { _, err := s.BuildContext(); return err },
		"Suspect":     func() error { _, err := s.MarkSuspect([]string{"a"}, nil); return err },
		"NSEmpty":     func() error { _, err := s.EntityNamespaceEmpty("x"); return err },
		"Meta":        func() error { _, err := s.Meta("k"); return err },
		"ChangeSet":   func() error { _, err := s.GetChangeSet("r"); return err },
	}
	for name, op := range ops {
		if err := op(); err == nil {
			t.Errorf("%s on a closed store succeeded", name)
		}
	}
}

// Update of a block that is not stored reports it; a short-term block that
// was persisted under the short tier (only reachable through Update) is
// deduplicated on the next Add.
func TestStore_UpdateMissingAndPersistedShortTier(t *testing.T) {
	s := openTestStore(t)
	wantErr(t, "Update missing", s.Update(&Block{ID: "nope", Tier: TierLong}), "block not found: nope")

	b := addLong(t, s, "p1", "persisted then demoted")
	b.Tier = TierShort
	if err := s.Update(b); err != nil {
		t.Fatal(err)
	}
	again := &Block{ID: "p1", Tier: TierShort, Content: "persisted then demoted", Tags: []string{"extra"}}
	if err := s.Add(again); err != nil {
		t.Fatal(err)
	}
	got, err := s.getPersisted("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Confirms != 1 || !containsTag(got.Tags, "extra") {
		t.Fatalf("persisted short-tier block not reinforced: %+v", got)
	}
	if len(s.shortTerm) != 0 {
		t.Fatalf("dedup also appended to the in-memory short tier: %d", len(s.shortTerm))
	}
}

// Expired persisted blocks stay out of the context window and the counts.
func TestStore_ExpiredBlocksHidden(t *testing.T) {
	s := openTestStore(t)
	past := time.Now().Add(-time.Minute)
	if err := s.Add(&Block{ID: "gone", Tier: TierMedium, Content: "expired fact", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	addLong(t, s, "live", "live fact")
	cw, err := s.BuildContext()
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.MediumTerm) != 0 || len(cw.LongTerm) != 1 {
		t.Fatalf("context = %d medium, %d long; want 0, 1", len(cw.MediumTerm), len(cw.LongTerm))
	}
	st, err := s.Stats()
	if err != nil || st.MediumTermCount != 0 || st.LongTermCount != 1 {
		t.Fatalf("stats = %+v, %v", st, err)
	}
}

func TestDefaultStorePath_Fallbacks(t *testing.T) {
	t.Setenv("GLEANN_MEMORY_DIR", "")
	t.Setenv("HOME", "")
	win := t.TempDir()
	t.Setenv("USERPROFILE", win)
	if got, want := DefaultStorePath(), filepath.Join(win, ".gleann", "memory", "memory.db"); got != want {
		t.Fatalf("USERPROFILE: %q, want %q", got, want)
	}
	t.Setenv("USERPROFILE", "")
	if got := DefaultStorePath(); !strings.HasSuffix(got, filepath.Join(".gleann", "memory", "memory.db")) {
		t.Fatalf("no home: %q", got)
	}
}

// OpenStore reports a directory it cannot create.
func TestOpenStore_UncreatableDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := OpenStore(filepath.Join(f, "sub", "memory.db"))
	wantErr(t, "OpenStore", err, "mkdir")
}
