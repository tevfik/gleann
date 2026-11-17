package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// finishedTx returns a transaction on a fresh database that has already been
// rolled back, so every statement run through it fails the way a statement
// does when the connection under the transaction has gone away.
func finishedTx(t *testing.T) *sql.Tx {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return tx
}

// Every migration helper reports the failure of its first statement with
// context instead of swallowing it or reading on as if it had succeeded.
func TestMigrationHelpersReportFailedStatements(t *testing.T) {
	cases := []struct {
		name string
		run  func(tx *sql.Tx) error
		want string
	}{
		{"tableExists", func(tx *sql.Tx) error { _, err := tableExists(tx, "symbols"); return err }, "checking table symbols"},
		{"tableHasColumn", func(tx *sql.Tx) error { _, err := tableHasColumn(tx, "symbols", "id"); return err }, "reading symbols columns"},
		{"countIfTableExists", func(tx *sql.Tx) error { _, err := countIfTableExists(tx, "symbols", ""); return err }, "checking table symbols"},
		{"upsertMeta", func(tx *sql.Tx) error { return upsertMeta(tx, "k", 1) }, "recording k"},
		{"migrateSymbolsFTS", migrateSymbolsFTS, "checking table symbols"},
		{"migrateGraphCounts", migrateGraphCounts, "checking table symbols"},
		{"migrateGraphIDs", migrateGraphIDs, "creating graph_edges"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(finishedTx(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// migrate, migrationDone and applyMigration on a closed database return the
// step that failed rather than panicking or reporting success.
func TestMigrateOnClosedDatabase(t *testing.T) {
	closed := func(t *testing.T) *sql.DB {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		return db
	}
	if err := migrate(closed(t)); err == nil || !strings.Contains(err.Error(), "creating meta table") {
		t.Fatalf("migrate err = %v", err)
	}
	if _, err := migrationDone(closed(t), migrations[0], ""); err == nil || !strings.Contains(err.Error(), "checking migration step") {
		t.Fatalf("migrationDone err = %v", err)
	}
	if err := applyMigration(closed(t), migrations[0]); err == nil || !strings.Contains(err.Error(), "beginning migration transaction") {
		t.Fatalf("applyMigration err = %v", err)
	}
}
