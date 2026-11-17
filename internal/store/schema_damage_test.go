package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/analysis"
)

// execW runs a statement on the writer, failing the test when it fails.
func execW(t *testing.T, s *Store, q string) {
	t.Helper()
	if _, err := s.WriterDB().Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// A database whose lexical table was dropped or replaced by something of the
// wrong shape (a half-finished manual repair, a foreign tool) must make every
// lexical operation report what failed rather than return empty results.
func TestLexicalOperationsReportDamagedSchema(t *testing.T) {
	ctx := context.Background()
	an := analysis.New("")

	cases := []struct {
		name  string
		setup func(t *testing.T, s *Store)
		run   func(s *Store) error
		want  string
	}{
		{"doc-freq cut without lex", func(t *testing.T, s *Store) { execW(t, s, `DROP TABLE lex`) },
			func(s *Store) error { _, err := s.filterDocFreq(ctx, [][]string{{"apple"}}, 0.5); return err },
			"counting lexical rows"},
		{"doc-freq cut without lex_vocab", func(t *testing.T, s *Store) { execW(t, s, `DROP TABLE lex_vocab`) },
			func(s *Store) error { _, err := s.filterDocFreq(ctx, [][]string{{"apple"}}, 0.5); return err },
			"preparing lex_vocab query"},
		{"doc-freq cut with a damaged term frequency", func(t *testing.T, s *Store) {
			execW(t, s, `DROP TABLE lex_vocab`)
			execW(t, s, `CREATE TABLE lex_vocab (term TEXT, doc TEXT)`)
			execW(t, s, `INSERT INTO lex_vocab VALUES ('apple', 'many')`)
		},
			func(s *Store) error { _, err := s.filterDocFreq(ctx, [][]string{{"apple"}}, 0.5); return err },
			`querying term doc frequency for "apple"`},
		{"explain with the doc-freq cut and no lex_vocab", func(t *testing.T, s *Store) { execW(t, s, `DROP TABLE lex_vocab`) },
			func(s *Store) error {
				q := lexQuery("apple")
				q.Options.DocFreqCut = 0.5
				_, err := s.ExplainTerms(ctx, q, 1)
				return err
			},
			"explaining terms"},
		{"explain without lex", func(t *testing.T, s *Store) { execW(t, s, `DROP TABLE lex`) },
			func(s *Store) error { _, err := s.ExplainTerms(ctx, lexQuery("apple"), 1); return err },
			"explaining terms"},
		{"score a group without lex", func(t *testing.T, s *Store) { execW(t, s, `DROP TABLE lex`) },
			func(s *Store) error { _, err := s.scoreGroup(ctx, []string{"apple"}, 1); return err },
			"no such table"},
		{"rebuild without lex", func(t *testing.T, s *Store) { execW(t, s, `DROP TABLE lex`) },
			func(s *Store) error { return s.Rebuild(ctx, an) },
			"clearing lex"},
		{"rebuild with a lex table of the wrong shape", func(t *testing.T, s *Store) {
			execW(t, s, `DROP TABLE lex`)
			execW(t, s, `CREATE TABLE lex (other TEXT)`)
		},
			func(s *Store) error { return s.Rebuild(ctx, an) },
			"preparing insert"},
		{"rebuild without passages", func(t *testing.T, s *Store) { execW(t, s, `DROP TABLE passages`) },
			func(s *Store) error { return s.Rebuild(ctx, an) },
			"reading passages"},
		{"rebuild when a lex row cannot be written", func(t *testing.T, s *Store) {
			execW(t, s, `DROP TABLE lex`)
			execW(t, s, `CREATE TABLE lex (tokens TEXT)`)
			execW(t, s, `CREATE TRIGGER fault BEFORE INSERT ON lex BEGIN SELECT RAISE(ABORT, 'injected fault'); END`)
		},
			func(s *Store) error { return s.Rebuild(ctx, an) },
			"inserting lex row"},
		{"rebuild when the analyzer meta cannot be written", func(t *testing.T, s *Store) {
			execW(t, s, `CREATE TRIGGER fault BEFORE INSERT ON meta BEGIN SELECT RAISE(ABORT, 'injected fault'); END`)
		},
			func(s *Store) error { return s.Rebuild(ctx, an) },
			"writing meta"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			applyTexts(t, s, "a.txt", []string{"apple banana", "apple cherry"})
			tc.setup(t, s)
			err := tc.run(s)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// The document-frequency filter tolerates empty forms and an empty query: an
// empty form is skipped when looking terms up, and with no groups there is
// nothing to filter.
func TestFilterDocFreqSkipsEmptyForms(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyTexts(t, s, "a.txt", []string{"apple banana", "apple cherry"})

	if got, err := s.filterDocFreq(ctx, nil, 0.5); err != nil || got != nil {
		t.Fatalf("no groups = %v, %v", got, err)
	}
	if got, err := s.filterDocFreq(ctx, [][]string{{"apple"}}, 0); err != nil || len(got) != 1 {
		t.Fatalf("a zero cut must keep every group, got %v, %v", got, err)
	}
	got, err := s.filterDocFreq(ctx, [][]string{{"", "banana"}, {""}}, 0.9)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("groups with an empty form: %v, want both kept (their df is below the cut)", got)
	}
	if score, err := s.scoreGroup(ctx, []string{""}, 1); err != nil || score != 0 {
		t.Fatalf("scoreGroup of an empty form = %v, %v, want 0", score, err)
	}
}

// ReplaceAll names the step that failed when the schema under it is damaged,
// and leaves the previous contents in place.
func TestReplaceAllReportsDamagedSchema(t *testing.T) {
	ctx := context.Background()
	fill := func(add func(SourcedPassage) error) error {
		return add(SourcedPassage{Source: "n.go", NewPassage: NewPassage{Text: "new", Tokens: "new"}})
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, s *Store)
		want  string
	}{
		{"passages cannot be prepared", func(t *testing.T, s *Store) { execW(t, s, `ALTER TABLE passages DROP COLUMN meta`) }, "preparing insert"},
		{"lex has the wrong shape", func(t *testing.T, s *Store) {
			execW(t, s, `DROP TABLE lex`)
			execW(t, s, `CREATE TABLE lex (other TEXT)`)
		}, "preparing lex insert"},
		{"a lex row cannot be written", func(t *testing.T, s *Store) {
			execW(t, s, `DROP TABLE lex`)
			execW(t, s, `CREATE TABLE lex (tokens TEXT)`)
			execW(t, s, `CREATE TRIGGER fault BEFORE INSERT ON lex BEGIN SELECT RAISE(ABORT, 'injected fault'); END`)
		}, "inserting lex row"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			applyTexts(t, s, "a.txt", []string{"old"})
			tc.setup(t, s)
			err := s.ReplaceAll(ctx, fill, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if n, err := s.Count(ctx); err != nil || n != 1 {
				t.Fatalf("after the failed replace Count = %d, %v, want the old passage kept", n, err)
			}
		})
	}

	t.Run("closed store", func(t *testing.T) {
		s := openTestStore(t)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.ReplaceAll(ctx, fill, nil); err == nil || !strings.Contains(err.Error(), "beginning transaction") {
			t.Fatalf("err = %v", err)
		}
	})
}

// openRawMeta returns a database that holds only the given meta DDL, for
// driving migrate and applyMigration on schemas Open would never produce.
func openRawMeta(t *testing.T, ddl string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

// A meta table without a value column cannot give the store format; migrate
// and applyMigration say so instead of treating the database as new.
func TestMigrateReportsUnreadableStoreFormat(t *testing.T) {
	db := openRawMeta(t, `CREATE TABLE meta (key TEXT PRIMARY KEY)`)
	if err := migrate(db); err == nil || !strings.Contains(err.Error(), "reading store_format") {
		t.Fatalf("migrate err = %v", err)
	}
	if err := applyMigration(db, migrations[0]); err == nil || !strings.Contains(err.Error(), "re-reading store_format") {
		t.Fatalf("applyMigration err = %v", err)
	}
}

// A newer format found under the write lock (another process migrated the
// database between the plain read and the lock) is refused, not overwritten.
func TestApplyMigrationRefusesNewerFormatUnderLock(t *testing.T) {
	db := openRawMeta(t, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
INSERT INTO meta VALUES ('store_format', '3')`)
	err := applyMigration(db, migrations[0])
	if err == nil || !strings.Contains(err.Error(), "this binary understands formats up to") {
		t.Fatalf("err = %v", err)
	}
}

// Recording a step as done, or the new format, can fail; the step then does
// not count as applied.
func TestApplyMigrationRecordingFailures(t *testing.T) {
	trigger := func(key string) string {
		return `CREATE TRIGGER fault BEFORE INSERT ON meta WHEN NEW.key = '` + key + `' BEGIN SELECT RAISE(ABORT, 'injected fault'); END`
	}
	t.Run("format 2 database before step tracking", func(t *testing.T) {
		db := openRawMeta(t, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
INSERT INTO meta VALUES ('store_format', '2');`+trigger("migration:format_2"))
		err := applyMigration(db, migrations[0])
		if err == nil || !strings.Contains(err.Error(), "recording migration step format_2") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("store_format", func(t *testing.T) {
		db := openRawMeta(t, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;`+trigger("store_format"))
		err := applyMigration(db, migrations[len(migrations)-1])
		if err == nil || !strings.Contains(err.Error(), "store_format") {
			t.Fatalf("err = %v", err)
		}
	})
}

// The symbols conversion reports each step that fails against a symbols table
// of an unexpected shape.
func TestMigrateSymbolsFTSOnUnexpectedShapes(t *testing.T) {
	cases := []struct {
		name string
		ddl  string
		want string
	}{
		{"a view named symbols", `CREATE VIEW symbols AS SELECT 1 AS x`, "creating symbols"},
		{"no file column", `CREATE TABLE symbols (id INTEGER PRIMARY KEY, fqn TEXT, name TEXT)`, "creating symbols indexes"},
		{"no fqn column", `CREATE TABLE symbols (id INTEGER PRIMARY KEY, file TEXT, name TEXT)`, "rebuilding symbols_fts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openRawMeta(t, tc.ddl)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := migrateSymbolsFTS(tx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// Counting the edges of a graph whose edges table lacks the kind column
// fails with the table named.
func TestCountIfTableExistsBadColumn(t *testing.T) {
	db := openRawMeta(t, `CREATE TABLE edges (src TEXT); CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := countIfTableExists(tx, "edges", "CALLS"); err == nil || !strings.Contains(err.Error(), "counting edges") {
		t.Fatalf("err = %v", err)
	}
	// migrateGraphCounts stops at that failure and returns it.
	if err := migrateGraphCounts(tx); err == nil || !strings.Contains(err.Error(), "counting edges") {
		t.Fatalf("migrateGraphCounts err = %v", err)
	}
}
