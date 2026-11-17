package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/analysis"
)

// The tests in this file inject a failure into one SQL statement of a write
// with a trigger that aborts it (RAISE(ABORT)), the way a full disk, an I/O
// error or a constraint would, and check two things: the error says which
// step failed, and the transaction leaves the previous committed state
// exactly as it was (no half-applied source, no advanced manifest).

const injected = "injected fault"

// inject installs a trigger that aborts the matching statement; when (may be
// empty) restricts it. The trigger is dropped at cleanup.
func inject(t *testing.T, s *Store, timing, when string) {
	t.Helper()
	if when != "" {
		when = " WHEN " + when
	}
	q := fmt.Sprintf(`CREATE TRIGGER fault BEFORE %s%s BEGIN SELECT RAISE(ABORT, '%s'); END`, timing, when, injected)
	if _, err := s.WriterDB().Exec(q); err != nil {
		t.Fatalf("installing trigger: %v", err)
	}
	t.Cleanup(func() { _, _ = s.WriterDB().Exec(`DROP TRIGGER IF EXISTS fault`) })
}

func uninject(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.WriterDB().Exec(`DROP TRIGGER IF EXISTS fault`); err != nil {
		t.Fatal(err)
	}
}

// dump renders the committed state the tests compare: passages, lex rows,
// manifest rows and meta keys.
func dump(t *testing.T, s *Store) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT id || ' ' || source || ' ' || chunk_no || ' ' || coalesce(vec_slot, '-') || ' ' || text FROM passages ORDER BY id`,
		`SELECT rowid FROM lex ORDER BY rowid`,
		`SELECT path || ' ' || hash || ' ' || coalesce(graph_hash, '-') || ' ' || graph_dirty FROM files ORDER BY path`,
		`SELECT key || '=' || value FROM meta ORDER BY key`,
	} {
		rows, err := s.ReaderDB().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			b.WriteString(line + "\n")
		}
		rows.Close()
		b.WriteString("--\n")
	}
	return b.String()
}

// seed gives the store two sources with slots, lex rows, a graph hash and a
// meta key.
func seed(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for i, src := range []string{"a.go", "b.go"} {
		_, err := s.ApplySource(ctx, SourceDelta{Source: src, File: FileRow{Hash: "h-" + src}, Passages: []NewPassage{
			{Text: src + " one", Tokens: "one", VecSlot: slot(int64(2 * i))},
			{Text: src + " two", Tokens: "two", VecSlot: slot(int64(2*i + 1))},
		}})
		if err != nil {
			t.Fatal(err)
		}
		markGraphBuilt(t, s, src, "h-"+src)
	}
	if err := s.SetMeta(ctx, "vector_generation", "1"); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFaultsLeaveCommittedStateIntact(t *testing.T) {
	ctx := context.Background()
	inTx := func(s *Store, fn func(tx *sql.Tx) error) error {
		tx, err := s.BeginWrite(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	replaceA := SourceDelta{Source: "a.go", File: FileRow{Hash: "h2"}, Passages: []NewPassage{{Text: "new", Tokens: "new", VecSlot: slot(9)}}}
	cases := []struct {
		name   string
		timing string
		when   string
		op     func(s *Store) error
		want   string
	}{
		{"ApplySource: dropping the old passages", "DELETE ON passages", "",
			func(s *Store) error { _, err := s.ApplySource(ctx, replaceA); return err }, "a.go"},
		{"ApplySource: inserting a passage", "INSERT ON passages", "",
			func(s *Store) error { _, err := s.ApplySource(ctx, replaceA); return err }, "a.go"},
		{"ApplySource: inserting a passage with an explicit id", "INSERT ON passages", "",
			func(s *Store) error {
				id := int64(77)
				_, err := s.ApplySource(ctx, SourceDelta{Source: "c.go", Passages: []NewPassage{{Text: "x", ID: &id}}})
				return err
			}, "c.go"},
		{"ApplySource: writing the files row", "UPDATE ON files", "",
			func(s *Store) error { _, err := s.ApplySource(ctx, replaceA); return err }, "files row"},
		{"ApplySource: inserting the files row", "INSERT ON files", "",
			func(s *Store) error {
				_, err := s.ApplySource(ctx, SourceDelta{Source: "c.go", Passages: []NewPassage{{Text: "x"}}})
				return err
			}, "files row"},
		{"RemoveSource: dropping passages", "DELETE ON passages", "",
			func(s *Store) error { _, err := s.RemoveSource(ctx, "a.go"); return err }, "a.go"},
		{"RemoveSource: deleting the files row", "DELETE ON files", "",
			func(s *Store) error { _, err := s.RemoveSource(ctx, "a.go"); return err }, "files row"},
		{"ReassignSlots: clearing a slot", "UPDATE ON passages", "NEW.vec_slot IS NULL",
			func(s *Store) error { return s.ReassignSlots(ctx, map[int64]int64{0: 1, 1: 0}) }, "clearing slot"},
		{"ReassignSlots: setting a slot", "UPDATE ON passages", "NEW.vec_slot IS NOT NULL",
			func(s *Store) error { return s.ReassignSlots(ctx, map[int64]int64{0: 1, 1: 0}) }, "setting slot"},
		{"AssignSlotsTx: clearing a slot", "UPDATE ON passages", "NEW.vec_slot IS NULL",
			func(s *Store) error {
				return inTx(s, func(tx *sql.Tx) error { return s.AssignSlotsTx(ctx, tx, map[int64]int64{1: 5}) })
			}, "clearing slot"},
		{"AssignSlotsTx: setting a slot", "UPDATE ON passages", "NEW.vec_slot IS NOT NULL",
			func(s *Store) error {
				return inTx(s, func(tx *sql.Tx) error { return s.AssignSlotsTx(ctx, tx, map[int64]int64{1: 5}) })
			}, "setting slot"},
		{"SetMeta", "INSERT ON meta", "",
			func(s *Store) error { return s.SetMeta(ctx, "k", "v") }, `meta key "k"`},
		{"DeleteMetaTx", "DELETE ON meta", "",
			func(s *Store) error {
				return inTx(s, func(tx *sql.Tx) error { return s.DeleteMetaTx(ctx, tx, "vector_generation") })
			}, "deleting meta key"},
		{"ReplacePassagesTx: dropping a vanished source", "DELETE ON passages", "OLD.source = 'b.go'",
			func(s *Store) error {
				return inTx(s, func(tx *sql.Tx) error {
					_, err := s.ReplacePassagesTx(ctx, tx, []SourceDelta{replaceA}, RebuildHooks{})
					return err
				})
			}, "b.go"},
		{"ReplacePassagesTx: deleting the files row of a gone source", "DELETE ON files", "",
			func(s *Store) error {
				return inTx(s, func(tx *sql.Tx) error {
					_, err := s.ReplacePassagesTx(ctx, tx, []SourceDelta{replaceA}, RebuildHooks{})
					return err
				})
			}, "files row"},
		{"ReplacePassagesTx: clearing the manifest of a kept source", "UPDATE ON files", "NEW.hash = ''",
			func(s *Store) error {
				return inTx(s, func(tx *sql.Tx) error {
					_, err := s.ReplacePassagesTx(ctx, tx, []SourceDelta{replaceA}, RebuildHooks{Fate: func(string) SourceFate { return SourceKept }})
					return err
				})
			}, "clearing the manifest row"},
		{"ReplacePassagesTx: marking a changed source graph_dirty", "UPDATE ON files", "NEW.graph_dirty = 1 AND OLD.graph_dirty = 0",
			func(s *Store) error {
				return inTx(s, func(tx *sql.Tx) error {
					_, err := s.ReplacePassagesTx(ctx, tx, []SourceDelta{replaceA, {Source: "b.go", File: FileRow{Hash: "h-b.go"}}}, RebuildHooks{})
					return err
				})
			}, "graph_dirty"},
		{"ReplacePassagesTx: applying a delta", "INSERT ON passages", "",
			func(s *Store) error {
				return inTx(s, func(tx *sql.Tx) error {
					_, err := s.ReplacePassagesTx(ctx, tx, []SourceDelta{replaceA}, RebuildHooks{})
					return err
				})
			}, "a.go"},
		{"ReplaceAll: clearing passages", "DELETE ON passages", "",
			func(s *Store) error {
				return s.ReplaceAll(ctx, func(func(SourcedPassage) error) error { return nil }, nil)
			}, "DELETE FROM passages"},
		{"ReplaceAll: clearing files", "DELETE ON files", "",
			func(s *Store) error {
				return s.ReplaceAll(ctx, func(func(SourcedPassage) error) error { return nil }, nil)
			}, "DELETE FROM files"},
		{"ReplaceAll: inserting a passage", "INSERT ON passages", "",
			func(s *Store) error {
				return s.ReplaceAll(ctx, func(add func(SourcedPassage) error) error {
					return add(SourcedPassage{Source: "z.go", NewPassage: NewPassage{Text: "z"}})
				}, nil)
			}, `passage of "z.go"`},
		{"ReplaceAll: inserting a passage with an explicit id", "INSERT ON passages", "",
			func(s *Store) error {
				id := int64(42)
				return s.ReplaceAll(ctx, func(add func(SourcedPassage) error) error {
					return add(SourcedPassage{Source: "z.go", NewPassage: NewPassage{Text: "z", ID: &id}})
				}, nil)
			}, "inserting passage 42"},
		{"ReplaceAll: writing meta", "INSERT ON meta", "",
			func(s *Store) error {
				return s.ReplaceAll(ctx, func(add func(SourcedPassage) error) error {
					return add(SourcedPassage{Source: "z.go", NewPassage: NewPassage{Text: "z", Tokens: "z", VecSlot: slot(0)}})
				}, map[string]string{"vector_generation": "2"})
			}, "writing meta"},
		{"Lexical Rebuild: recording the analyzer", "INSERT ON meta", "",
			func(s *Store) error { return s.Rebuild(ctx, analysis.New("")) }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			seed(t, s)
			before := dump(t, s)
			inject(t, s, tc.timing, tc.when)
			err := tc.op(s)
			if err == nil || !strings.Contains(err.Error(), injected) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want the injected fault and %q", err, tc.want)
			}
			uninject(t, s)
			if after := dump(t, s); after != before {
				t.Fatalf("a failed write changed the committed state:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// A fill callback that fails, or a cancelled context, aborts ReplaceAll and
// keeps the previous index.
func TestReplaceAllAbortKeepsPreviousState(t *testing.T) {
	s := openTestStore(t)
	seed(t, s)
	before := dump(t, s)

	err := s.ReplaceAll(context.Background(), func(add func(SourcedPassage) error) error {
		if err := add(SourcedPassage{Source: "z.go", NewPassage: NewPassage{Text: "z"}}); err != nil {
			return err
		}
		return fmt.Errorf("walker failed")
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "walker failed") {
		t.Fatalf("err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	err = s.ReplaceAll(ctx, func(add func(SourcedPassage) error) error {
		cancel()
		return add(SourcedPassage{Source: "z.go", NewPassage: NewPassage{Text: "z"}})
	}, nil)
	if err == nil {
		t.Fatal("ReplaceAll ignored a cancelled context")
	}
	if after := dump(t, s); after != before {
		t.Fatalf("an aborted ReplaceAll changed the index:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// ReplaceAll keeps explicit IDs, assigns the others, numbers chunks per
// source in yield order and writes the meta keys.
func TestReplaceAllExplicitIDsAndMeta(t *testing.T) {
	s := openTestStore(t)
	seed(t, s)
	ctx := context.Background()
	id := int64(100)
	err := s.ReplaceAll(ctx, func(add func(SourcedPassage) error) error {
		for _, p := range []SourcedPassage{
			{Source: "x.go", NewPassage: NewPassage{Text: "first", Tokens: "first", ID: &id, VecSlot: slot(0)}},
			{Source: "y.go", NewPassage: NewPassage{Text: "other", Tokens: "other"}},
			{Source: "x.go", NewPassage: NewPassage{Text: "second", Tokens: "second", Kind: "prose", Meta: `{"k":1}`}},
		} {
			if err := add(p); err != nil {
				return err
			}
		}
		return nil
	}, map[string]string{"vector_generation": "7"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Get(ctx, 100)
	if err != nil || p.Text != "first" || p.ChunkNo != 0 || p.VecSlot == nil || *p.VecSlot != 0 {
		t.Fatalf("explicit-id passage = %+v, %v", p, err)
	}
	ps, err := s.SelectByPredicate(ctx, `source = ?`, []any{"x.go"}, 0)
	if err != nil || len(ps) != 2 || ps[1].Kind != "prose" || ps[1].Meta != `{"k":1}` {
		t.Fatalf("x.go passages = %+v, %v", ps, err)
	}
	if v, _ := s.Meta(ctx, "vector_generation"); v != "7" {
		t.Fatalf("vector_generation = %q", v)
	}
	if files, _ := s.Files(ctx); len(files) != 0 {
		t.Fatalf("ReplaceAll kept %d files rows", len(files))
	}
	if n := lexCount(t, s, "one"); n != 0 {
		t.Fatal("lex rows of the old passages survived")
	}
	if n := lexCount(t, s, "second"); n != 1 {
		t.Fatalf("lex rows of the new passages: %d", n)
	}
}
