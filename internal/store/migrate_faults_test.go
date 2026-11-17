package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func execRaw(t *testing.T, path, q string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func queryRawInt(t *testing.T, path, q string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// Each migration step runs in its own transaction: when a step fails, Open
// reports the step, the steps before it stay applied, the failed step leaves
// no trace (its schema change is rolled back, its marker is not written), and
// the next Open, once the cause is gone, completes the migration from there.
func TestMigrationStepFailureIsAtomicAndResumable(t *testing.T) {
	cases := []struct {
		name      string
		trigger   string // aborts the INSERT into meta of this key
		step      string
		unchanged string // query whose result must be the same before and after the failed Open
	}{
		{"symbols conversion marker", "migration:symbols_fts", "symbols_fts",
			`SELECT count(*) FROM pragma_table_info('symbols') WHERE name = 'id'`},
		{"seeding the symbol counter", MetaGraphSymbols, "graph_counts",
			`SELECT count(*) FROM meta WHERE key LIKE 'graph_count:%'`},
		{"seeding an edge counter", MetaGraphEdgesPrefix + "IMPLEMENTS", "graph_counts",
			`SELECT count(*) FROM meta WHERE key LIKE 'graph_count:%'`},
		{"graph ids marker", "migration:graph_ids", "graph_ids",
			`SELECT count(*) FROM sqlite_master WHERE name = 'edges' AND type = 'table'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "idx", "idx.db")
			mockOldGraphDB(t, path)
			execRaw(t, path, `CREATE TRIGGER fault BEFORE INSERT ON meta WHEN NEW.key = '`+tc.trigger+`' BEGIN SELECT RAISE(ABORT, 'injected fault'); END`)
			before := queryRawInt(t, path, tc.unchanged)

			_, err := Open(path, Options{})
			if err == nil || !strings.Contains(err.Error(), "migration step "+tc.step) || !strings.Contains(err.Error(), "injected fault") {
				t.Fatalf("Open err = %v, want step %s to fail with the injected fault", err, tc.step)
			}
			if after := queryRawInt(t, path, tc.unchanged); after != before {
				t.Fatalf("the failed step %s left a change behind: %q went from %d to %d", tc.step, tc.unchanged, before, after)
			}
			if n := queryRawInt(t, path, `SELECT count(*) FROM meta WHERE key = 'migration:`+tc.step+`'`); n != 0 {
				t.Fatalf("the failed step %s was recorded as done", tc.step)
			}

			execRaw(t, path, `DROP TRIGGER fault`)
			s, err := Open(path, Options{})
			if err != nil {
				t.Fatalf("Open after removing the fault: %v", err)
			}
			defer s.Close()
			for _, m := range migrations[1:] { // format_2 predates step tracking here
				if n := queryRawInt(t, path, `SELECT count(*) FROM meta WHERE key = 'migration:`+m.name+`'`); n != 1 {
					t.Fatalf("step %s not recorded after the resumed migration", m.name)
				}
			}
			// The graph rows survived the interrupted and resumed migration.
			if n := queryRawInt(t, path, `SELECT count(*) FROM symbols`); n != 3 {
				t.Fatalf("%d symbols after migration, want 3", n)
			}
			if n := queryRawInt(t, path, `SELECT count(*) FROM edges`); n != 3 {
				t.Fatalf("%d edges after migration, want 3", n)
			}
			if n := queryRawInt(t, path, `SELECT CAST(value AS INTEGER) FROM meta WHERE key = '`+MetaGraphEdgesPrefix+`CALLS'`); n != 2 {
				t.Fatalf("CALLS counter = %d, want 2", n)
			}
		})
	}
}

// A symbols table that lacks a column the rowid conversion copies (a
// damaged or hand-edited database) fails the symbols_fts step with a clear
// error and leaves the table as it was.
func TestMigrateSymbolsConversionFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	mockOldGraphDB(t, path)
	execRaw(t, path, `ALTER TABLE symbols DROP COLUMN doc`)
	_, err := Open(path, Options{})
	if err == nil || !strings.Contains(err.Error(), "converting symbols to rowid") {
		t.Fatalf("Open err = %v, want a symbols conversion error", err)
	}
	if n := queryRawInt(t, path, `SELECT count(*) FROM symbols`); n != 3 {
		t.Fatalf("symbols has %d rows after the failed conversion, want 3", n)
	}
	if n := queryRawInt(t, path, `SELECT count(*) FROM pragma_table_info('symbols') WHERE name = 'id'`); n != 0 {
		t.Fatal("the failed conversion left the new symbols shape behind")
	}
}

// A database written by a newer release (store_format 3) is refused, and
// opening it changes nothing.
func TestMigrateRefusesNewerFormatWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx.db")
	execRaw(t, path, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID; INSERT INTO meta VALUES ('store_format', '3')`)
	if _, err := Open(path, Options{}); err == nil || !strings.Contains(err.Error(), `"3"`) {
		t.Fatalf("Open err = %v", err)
	}
	if n := queryRawInt(t, path, `SELECT count(*) FROM sqlite_master`); n != 1 {
		t.Fatalf("opening a newer format created %d schema objects", n-1)
	}
}

// Re-running the graph steps on a database that already has their objects
// (their markers lost, as after a restore of the meta table) is harmless:
// the steps are idempotent and the rows stay.
func TestMigrationStepsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	mockOldGraphDB(t, path)
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	execRaw(t, path, `DELETE FROM meta WHERE key LIKE 'migration:%' AND key != 'migration:format_2'`)
	s, err = Open(path, Options{})
	if err != nil {
		t.Fatalf("re-running the steps: %v", err)
	}
	s.Close()
	if n := queryRawInt(t, path, `SELECT count(*) FROM symbols`); n != 3 {
		t.Fatalf("%d symbols after re-running the steps, want 3", n)
	}
	if n := queryRawInt(t, path, `SELECT count(*) FROM edges`); n != 3 {
		t.Fatalf("%d edges after re-running the steps, want 3", n)
	}
	if n := queryRawInt(t, path, `SELECT count(*) FROM symbols_fts WHERE symbols_fts MATCH '"lph"'`); n != 2 {
		t.Fatalf("symbols_fts finds %d symbols for lph, want 2", n)
	}
}

// A format-2 database missing a table a schema step needs (doc_nodes here)
// fails that step by name instead of being half-migrated.
func TestMigrationSchemaStepFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	mockOldGraphDB(t, path)
	execRaw(t, path, `DROP TABLE doc_nodes`)
	_, err := Open(path, Options{})
	if err == nil || !strings.Contains(err.Error(), "migration step doc_nodes_rpath") || !strings.Contains(err.Error(), "executing schema") {
		t.Fatalf("Open err = %v, want the doc_nodes_rpath step to fail", err)
	}
	if n := queryRawInt(t, path, `SELECT count(*) FROM meta WHERE key = 'migration:doc_nodes_rpath'`); n != 0 {
		t.Fatal("the failed step was recorded")
	}
}

func TestOpenUnderAFileFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(blocker, "idx", "idx.db"), Options{}); err == nil || !strings.Contains(err.Error(), "creating index directory") {
		t.Fatalf("err = %v", err)
	}
}
