package store

import (
	"context"
	"strings"
	"testing"
)

// ApplySource names the step that failed when the schema under it is
// damaged, and a failed ApplySource leaves the old source.
func TestApplySourceReportsDamagedSchema(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		source string // a.txt already has passages, new.txt has none
		setup  func(t *testing.T, s *Store)
		want   string
	}{
		{"passages cannot be prepared", "new.txt", func(t *testing.T, s *Store) { execW(t, s, `ALTER TABLE passages DROP COLUMN meta`) }, "preparing insert"},
		{"lex has the wrong shape", "new.txt", func(t *testing.T, s *Store) {
			execW(t, s, `DROP TABLE lex`)
			execW(t, s, `CREATE TABLE lex (other TEXT)`)
		}, "preparing lex insert"},
		{"a lex row cannot be written", "new.txt", func(t *testing.T, s *Store) {
			execW(t, s, `DROP TABLE lex`)
			execW(t, s, `CREATE TABLE lex (tokens TEXT)`)
			execW(t, s, `CREATE TRIGGER fault BEFORE INSERT ON lex BEGIN SELECT RAISE(ABORT, 'injected fault'); END`)
		}, "inserting lex row"},
		{"an old lex row cannot be deleted", "a.txt", func(t *testing.T, s *Store) {
			execW(t, s, `DROP TABLE lex`)
			execW(t, s, `CREATE TABLE lex (tokens TEXT)`)
			execW(t, s, `INSERT INTO lex (rowid, tokens) SELECT id, 'old' FROM passages`)
			execW(t, s, `CREATE TRIGGER fault BEFORE DELETE ON lex BEGIN SELECT RAISE(ABORT, 'injected fault'); END`)
		}, "deleting lex row"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			applyTexts(t, s, "a.txt", []string{"apple", "banana"})
			tc.setup(t, s)
			_, err := s.ApplySource(ctx, SourceDelta{Source: tc.source, Passages: []NewPassage{{Text: "fresh", Tokens: "fresh"}}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if n, err := s.Count(ctx); err != nil || n != 2 {
				t.Fatalf("after the failed apply Count = %d, %v, want the 2 old passages", n, err)
			}
		})
	}
}

func TestReplacePassagesTxWithoutPassagesTable(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	execW(t, s, `DROP TABLE passages`)
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := s.ReplacePassagesTx(ctx, tx, nil, RebuildHooks{}); err == nil || !strings.Contains(err.Error(), "listing passage sources") {
		t.Fatalf("err = %v", err)
	}
}

// The graph-id conversion names the step that fails on a database whose old
// graph tables are not what the migration expects.
func TestMigrateGraphIDsOnUnexpectedShapes(t *testing.T) {
	cases := []struct {
		name string
		ddl  string
		want string
	}{
		{"old edges without a confidence column",
			`CREATE TABLE symbols (id INTEGER PRIMARY KEY, fqn TEXT); CREATE TABLE edges (src TEXT, dst TEXT, kind TEXT)`,
			"converting edges"},
		{"a graph_edges table without the dst column",
			`CREATE TABLE symbols (id INTEGER PRIMARY KEY, fqn TEXT); CREATE TABLE graph_edges (x INTEGER)`,
			"creating graph_edges_dst"},
		{"no meta table to hold the counters",
			`CREATE TABLE symbols (id INTEGER PRIMARY KEY, fqn TEXT)`,
			"recording graph_count"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openRawMeta(t, tc.ddl)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := migrateGraphIDs(tx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
