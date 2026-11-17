package memory

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A database written by a newer binary (store_format beyond 2) is refused
// instead of being migrated or written with an older schema.
func TestOpenSQLite_RefusesNewerFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.sqlite")
	s, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, s, `UPDATE memory_meta SET value = '3' WHERE key = 'store_format'`)
	// applyMemoryMigration re-reads the format under the write lock: a
	// newer binary that upgraded the file meanwhile is refused as well.
	err = s.applyMemoryMigration(memoryMigrations[1])
	if err == nil || !strings.Contains(err.Error(), `unsupported store_format "3"`) {
		t.Fatalf("applyMemoryMigration = %v", err)
	}
	s.Close()

	_, err = openSQLite(path)
	if err == nil || !strings.Contains(err.Error(), `unsupported store_format "3"`) {
		t.Fatalf("openSQLite = %v, want unsupported format", err)
	}
}

// Another process may apply a migration step between this process's check
// and its write lock; the step is then skipped, not executed twice.
func TestApplyMemoryMigration_StepAlreadyApplied(t *testing.T) {
	s := openTestStore(t)
	execSQL(t, s, `INSERT INTO reflection_runs (run_id, created_at, data) VALUES ('r1', 1, '{}')`)
	for _, m := range memoryMigrations[1:] {
		if err := s.applyMemoryMigration(m); err != nil {
			t.Fatalf("re-applying %s: %v", m.name, err)
		}
	}
	var n int
	if err := s.reader.QueryRow(`SELECT count(*) FROM reflection_runs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reflection_runs rows = %d, %v; a re-applied step must not touch data", n, err)
	}
}

// A store at format 2 that predates the per-step records (no
// migration:format_2 key) gets the record added without re-running the
// schema, and the later steps are applied.
func TestCreateSchema_RecordsFormat2StepOfOlderStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.sqlite")
	s, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, s, `DELETE FROM memory_meta WHERE key LIKE 'migration:%'`)
	execSQL(t, s, `DROP TABLE reflection_runs`)
	if err := s.applyMemoryMigration(memoryMigrations[0]); err != nil {
		t.Fatal(err)
	}
	var one int
	if err := s.reader.QueryRow(`SELECT 1 FROM memory_meta WHERE key = 'migration:format_2'`).Scan(&one); err != nil {
		t.Fatalf("format_2 step not recorded: %v", err)
	}
	s.Close()

	s, err = openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveChangeSet(&ChangeSet{RunID: "r", Timestamp: time.Now()}); err != nil {
		t.Fatalf("reflection_runs not recreated on reopen: %v", err)
	}
}

// isBusyErr recognises SQLite's busy/locked results (including extended
// codes) and nothing else; retryBusy retries exactly those.
func TestIsBusyErrAndRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.sqlite")
	holder, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.Exec(`CREATE TABLE t (x)`); err != nil {
		t.Fatal(err)
	}
	tx, err := holder.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_, busy := other.Begin()
	if busy == nil {
		t.Fatal("second writer got the write lock")
	}
	if !isBusyErr(busy) {
		t.Fatalf("isBusyErr(%v) = false", busy)
	}
	if !isBusyErr(errors.Join(errors.New("context"), busy)) {
		t.Error("isBusyErr does not see a wrapped busy error")
	}
	if isBusyErr(errors.New("database is locked")) {
		t.Error("a plain error string is not a SQLite busy error")
	}
	_, constraint := holder.Exec(`SELECT * FROM missing_table`)
	if constraint == nil || isBusyErr(constraint) {
		t.Errorf("isBusyErr(%v) = true for a non-busy SQLite error", constraint)
	}

	// retryBusy retries a busy failure and stops at the first other result.
	calls := 0
	err = retryBusy(func() error {
		calls++
		if calls < 3 {
			return busy
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("retryBusy = %v after %d calls, want success after 3", err, calls)
	}
	calls = 0
	plain := errors.New("not busy")
	if err := retryBusy(func() error { calls++; return plain }); !errors.Is(err, plain) || calls != 1 {
		t.Fatalf("retryBusy on a non-busy error = %v after %d calls", err, calls)
	}
}

func TestEntityNamespaceEmpty(t *testing.T) {
	s := openTestStore(t)
	empty, err := s.EntityNamespaceEmpty("proj")
	if err != nil || !empty {
		t.Fatalf("fresh namespace: %v, %v", empty, err)
	}
	execSQL(t, s, `INSERT INTO entities (ns, id, type, content) VALUES ('proj', 'e1', 'concept', 'x')`)
	if empty, err := s.EntityNamespaceEmpty("proj"); err != nil || empty {
		t.Fatalf("namespace with an entity: %v, %v", empty, err)
	}
	if empty, err := s.EntityNamespaceEmpty("other"); err != nil || !empty {
		t.Fatalf("other namespace: %v, %v", empty, err)
	}
}

// A zero time survives the nanosecond round trip as the zero value.
func TestNsToTimeZero(t *testing.T) {
	if !nsToTime(timeToNS(time.Time{})).IsZero() {
		t.Fatal("zero time did not round-trip")
	}
	now := time.Unix(1700000000, 42)
	if got := nsToTime(timeToNS(now)); !got.Equal(now) {
		t.Fatalf("round trip %v -> %v", now, got)
	}
}
