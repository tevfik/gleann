package memory

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// openSQLite on a path whose parent is a regular file cannot connect; the
// error names the step and no handle leaks.
func TestOpenSQLite_UnreachablePath(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := openSQLite(filepath.Join(parent, "memory.sqlite"))
	if err == nil {
		s.Close()
		t.Fatal("openSQLite under a regular file succeeded")
	}
	wantErr(t, "openSQLite", err, "connecting memory writer")
}

// Every step of createSchema reports its own failure when the database
// cannot serve it.
func TestCreateSchema_Faults(t *testing.T) {
	t.Run("closed writer", func(t *testing.T) {
		s := openTestStore(t)
		s.writer.Close()
		wantErr(t, "createSchema", s.createSchema(), "creating memory_meta table")
	})
	t.Run("memory_meta without value column", func(t *testing.T) {
		s := openTestStore(t)
		execSQL(t, s, `DROP TABLE memory_meta`)
		execSQL(t, s, `CREATE TABLE memory_meta (key TEXT PRIMARY KEY)`)
		wantErr(t, "createSchema", s.createSchema(), "reading store_format")
	})
	t.Run("step check fails", func(t *testing.T) {
		s := openTestStore(t)
		execSQL(t, s, `DROP TABLE memory_meta`)
		_, err := s.memoryMigrationDone(memoryMigrations[1], "2")
		wantErr(t, "memoryMigrationDone", err, "checking memory migration step entity_graph")
	})
	t.Run("step apply fails", func(t *testing.T) {
		s := openTestStore(t)
		execSQL(t, s, `DELETE FROM memory_meta WHERE key = 'migration:reflection_runs'`)
		execSQL(t, s, `DROP TABLE reflection_runs`)
		abortOn(t, s, "INSERT", "memory_meta")
		wantErr(t, "createSchema", s.createSchema(), "applying memory migration step reflection_runs")
	})
}

func TestApplyMemoryMigration_Faults(t *testing.T) {
	t.Run("begin on closed writer", func(t *testing.T) {
		s := openTestStore(t)
		s.writer.Close()
		wantErr(t, "apply", s.applyMemoryMigration(memoryMigrations[1]), "beginning memory migration transaction")
	})
	t.Run("memory_meta gone", func(t *testing.T) {
		s := openTestStore(t)
		execSQL(t, s, `DROP TABLE memory_meta`)
		wantErr(t, "apply", s.applyMemoryMigration(memoryMigrations[1]), "re-reading store_format")
	})
	t.Run("format_2 record fails", func(t *testing.T) {
		s := openTestStore(t)
		execSQL(t, s, `DELETE FROM memory_meta WHERE key = 'migration:format_2'`)
		abortOn(t, s, "INSERT", "memory_meta")
		wantErr(t, "apply", s.applyMemoryMigration(memoryMigrations[0]), "recording memory migration step format_2")
	})
	t.Run("migration sql fails", func(t *testing.T) {
		s := openTestStore(t)
		err := s.applyMemoryMigration(memoryMigration{name: "bogus", format: "2", sql: "NOT SQL"})
		wantErr(t, "apply", err, "executing memory migration bogus")
	})
	t.Run("store_format record fails", func(t *testing.T) {
		s := openTestStore(t)
		abortOn(t, s, "INSERT", "memory_meta")
		err := s.applyMemoryMigration(memoryMigration{name: "extra", format: "2", sql: "SELECT 1"})
		wantErr(t, "apply", err, "recording store_format")
	})
	t.Run("step record fails", func(t *testing.T) {
		s := openTestStore(t)
		execSQL(t, s, `CREATE TRIGGER fault_step BEFORE INSERT ON memory_meta WHEN NEW.key LIKE 'migration:%'
			BEGIN SELECT RAISE(ABORT, 'injected step fault'); END`)
		err := s.applyMemoryMigration(memoryMigration{name: "extra", format: "2", sql: "SELECT 1"})
		wantErr(t, "apply", err, "recording memory migration step extra")
	})
	t.Run("commit fails on a deferred constraint", func(t *testing.T) {
		s := openTestStore(t)
		err := s.applyMemoryMigration(memoryMigration{name: "fk", format: "2", sql: `
CREATE TABLE fk_parent (id INTEGER PRIMARY KEY);
CREATE TABLE fk_child (pid INTEGER REFERENCES fk_parent(id) DEFERRABLE INITIALLY DEFERRED);
INSERT INTO fk_child VALUES (42);`})
		wantErr(t, "apply", err, "committing memory migration fk")
	})
}

// A write whose deferred foreign key is violated fails at COMMIT and leaves
// nothing behind.
func TestWriteTx_CommitFailureRollsBack(t *testing.T) {
	s := openTestStore(t)
	execSQL(t, s, `CREATE TABLE fk_parent (id INTEGER PRIMARY KEY)`)
	execSQL(t, s, `CREATE TABLE fk_child (pid INTEGER REFERENCES fk_parent(id) DEFERRABLE INITIALLY DEFERRED)`)
	err := s.writeTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO fk_child VALUES (7)`)
		return err
	})
	wantErr(t, "writeTx", err, "committing memory write")
	var n int
	if err := s.reader.QueryRow(`SELECT count(*) FROM fk_child`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("fk_child rows = %d, %v; failed commit must roll back", n, err)
	}
}

// A block row that does not decode is reported by the in-transaction read
// that Add's deduplication uses; a missing table is a read error, not
// "absent".
func TestGetBlockTx_Faults(t *testing.T) {
	s := openTestStore(t)
	addLong(t, s, "bad", "content")
	execSQL(t, s, `UPDATE blocks SET tags = 'not json' WHERE id = 'bad'`)
	err := s.writeTx(func(tx *sql.Tx) error {
		_, _, err := getBlockTx(tx, "bad")
		return err
	})
	if err == nil {
		t.Fatal("getBlockTx decoded a row with corrupt tags")
	}
	execSQL(t, s, `DROP TABLE blocks`)
	err = s.writeTx(func(tx *sql.Tx) error {
		_, _, err := getBlockTx(tx, "bad")
		return err
	})
	wantErr(t, "getBlockTx", err, "reading block bad")
}

func TestGetChangeSet_Faults(t *testing.T) {
	s := openTestStore(t)
	execSQL(t, s, `INSERT INTO reflection_runs (run_id, created_at, data) VALUES ('r1', 1, 'not json')`)
	_, err := s.GetChangeSet("r1")
	wantErr(t, "GetChangeSet", err, "decoding reflection run r1")
	_, err = s.GetChangeSet("missing")
	wantErr(t, "GetChangeSet", err, "reflection run not found")
	execSQL(t, s, `DROP TABLE reflection_runs`)
	_, err = s.GetChangeSet("r1")
	wantErr(t, "GetChangeSet", err, "reading reflection run r1")
}
