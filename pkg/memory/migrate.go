package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// The bbolt code lives only here: it is the migration reader for the legacy
// memory.db (RFC 003 section 6.4 Memory). The live store is SQLite.
var (
	bucketBlocks    = []byte("blocks")
	bucketSummaries = []byte("summaries")
	bucketMeta      = []byte("meta")
)

// guardText is written at the legacy memory.db path after the switch (and on
// a fresh open). It must be non-empty and must not look like a bbolt file:
// bbolt.Open initialises a missing or empty file, but fails on a non-empty
// non-bbolt file, so an older binary stops with an error instead of creating
// an empty memory store and serving stale data (RFC 003 sections 3.3, 6.4).
const guardText = "this memory store uses gleann store format 2 (SQLite); the data lives in the .sqlite file beside this one. Use gleann v1.10.0 or newer.\n"

// migrateLockTimeout bounds how long an open waits for another process's
// migration. The copy is a one-shot under the exclusive lock, so the wait is
// normally milliseconds; the budget covers a large legacy database.
const migrateLockTimeout = 30 * time.Second

// sqlitePathFor maps the legacy bbolt path to the SQLite path: memory.db ->
// memory.sqlite beside it (RFC 003 section 3.3). A new name is required so an
// older binary never opens the SQLite file as bbolt. Paths without a .db
// suffix get .sqlite appended.
func sqlitePathFor(path string) string {
	if strings.HasSuffix(path, ".db") {
		return strings.TrimSuffix(path, ".db") + ".sqlite"
	}
	return path + ".sqlite"
}

// migrateLockPath is the exclusive migration lock beside the database:
// memory.db -> memory.migrate.lock (RFC 003 section 6.4 Memory step 1).
func migrateLockPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".db")
	return filepath.Join(filepath.Dir(path), base+".migrate.lock")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// lockedMemoryErr is the existing "locked by another process" message, kept
// word for word so scripts and users see the same guidance as before.
func lockedMemoryErr(path string) error {
	return fmt.Errorf("open memory db: timeout: database file %q is locked by another process (gleann serve, another MCP server, or stale process); start gleann service to route requests via REST or stop the locking process", path)
}

func isBboltTimeout(err error) bool {
	return errors.Is(err, bolterrors.ErrTimeout) || strings.Contains(err.Error(), "timeout")
}

type legacyKind int

const (
	legacyNone  legacyKind = iota // missing or empty: nothing to migrate
	legacyBbolt                   // a real bbolt database
	legacyGuard                   // non-empty and not bbolt: a guard file
)

// classifyLegacy reports what sits at the legacy path. The read-only bbolt
// open is the detector: it succeeds only on a real bbolt file, times out when
// a running process holds the exclusive lock, and fails on the guard text.
func classifyLegacy(path string) (legacyKind, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return legacyNone, nil
		}
		return legacyNone, fmt.Errorf("stat legacy memory db %s: %w", path, err)
	}
	if fi.Size() == 0 {
		// bbolt would initialise an empty file; there is no data to migrate.
		return legacyNone, nil
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err == nil {
		db.Close()
		return legacyBbolt, nil
	}
	if isBboltTimeout(err) {
		return legacyNone, lockedMemoryErr(path)
	}
	// Only the guard text is a guard. Any other failure (permissions, too
	// many open files, a damaged database) must not be taken for "already
	// migrated": the store would start empty while the user's memories stay
	// in a file nobody reads again.
	if isGuardFile(path) {
		return legacyGuard, nil
	}
	return legacyNone, fmt.Errorf("legacy memory db %s cannot be read (%v); not migrating it — fix the file or move it aside", path, err)
}

// isGuardFile reports whether path holds the guard text written by the
// migration.
func isGuardFile(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && string(data) == guardText
}

// isEmptyBbolt reports whether path is a bbolt memory database without any
// block or summary: what an older binary creates when it finds no memory.db.
func isEmptyBbolt(path string) bool {
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return false
	}
	defer db.Close()
	empty := true
	_ = db.View(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketBlocks, bucketSummaries} {
			if b := tx.Bucket(name); b != nil {
				if k, _ := b.Cursor().First(); k != nil {
					empty = false
				}
			}
		}
		return nil
	})
	return empty
}

// writeGuardFile writes the guard text at path, replacing whatever is there.
// Callers use it only when the legacy data has been renamed aside or never
// existed.
func writeGuardFile(path string) error {
	if err := os.WriteFile(path, []byte(guardText), 0o600); err != nil {
		return fmt.Errorf("writing memory guard file %s: %w", path, err)
	}
	return nil
}

// ensureGuardFile writes the guard only when the legacy path is missing or
// empty; existing content (the guard, or a legacy database awaiting the
// switch) is left untouched.
func ensureGuardFile(path string) error {
	fi, err := os.Stat(path)
	if err == nil && fi.Size() > 0 {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat legacy memory db %s: %w", path, err)
	}
	return writeGuardFile(path)
}

// acquireMigrateLock takes the exclusive migration lock, waiting for a
// running migration instead of failing fast: two processes opening at once
// must both succeed, the second continuing as a normal open (RFC 003 section
// 6.4 Memory step 1). The lock belongs to the open file handle, so a killed
// holder never leaves a stale lock.
func acquireMigrateLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening memory migration lock %s: %w", path, err)
	}
	deadline := time.Now().Add(migrateLockTimeout)
	for {
		err := lockFileExclusive(f)
		if err == nil {
			return f, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("memory migration lock %s is held by another process; wait for it to finish or stop it: %w", path, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func releaseLockFile(f *os.File) error {
	return errors.Join(unlockFile(f), f.Close())
}

// lockGuardShared opens the legacy-path file and holds a shared lock on it
// for the store's lifetime. Any number of SQLite processes hold the shared
// lock at once, while an old binary's exclusive bbolt.Open on the same path
// times out — the same "locked by another process" behaviour as before the
// switch. The guard text itself makes that open fail even after Close.
func lockGuardShared(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening memory guard file %s: %w", path, err)
	}
	if err := lockFileShared(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking memory guard file %s: %w", path, err)
	}
	return f, nil
}

func notifyStep(notify func(int), step int) {
	if notify != nil {
		notify(step)
	}
}

// migrateFromBbolt copies the legacy bbolt memory.db into SQLite following
// RFC 003 section 6.4 Memory steps 1-5: exclusive lock with re-check, stale
// .tmp removal, copy into memory.sqlite.tmp preserving block IDs and
// timestamps, count verification plus memory_meta.migrated_from, rename to
// memory.sqlite, then rename memory.db to memory.db.v1 and write the guard.
//
// A crash before the rename leaves only the .tmp, which the next open
// discards and redoes; a crash between the renames leaves memory.sqlite
// authoritative (migrated_from is set) and the next open completes step 5.
//
// notify, when non-nil, is called at each step boundary (1-5); the migration
// kill tests use it to stop the process at a deterministic point. OpenStore
// passes nil.
func migrateFromBbolt(legacyPath, sqlitePath string, notify func(int)) error {
	lock, err := acquireMigrateLock(migrateLockPath(legacyPath))
	if err != nil {
		return err
	}
	defer releaseLockFile(lock)

	// Step 1: re-check after taking the lock; another process may have
	// finished meanwhile, in which case this continues as a normal open.
	if fileExists(sqlitePath) {
		return nil
	}
	kind, err := classifyLegacy(legacyPath)
	if err != nil {
		return err
	}
	if kind != legacyBbolt {
		return nil
	}
	notifyStep(notify, 1)

	// Step 2: discard any stale .tmp from a killed earlier attempt and copy
	// into a fresh one. memory.sqlite never exists in a partial state.
	tmpPath := sqlitePath + ".tmp"
	removeSQLiteFiles(tmpPath)

	src, err := bolt.Open(legacyPath, 0o600, &bolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		if isBboltTimeout(err) {
			return lockedMemoryErr(legacyPath)
		}
		return fmt.Errorf("opening legacy memory db %s: %w", legacyPath, err)
	}
	defer src.Close()

	tmp, err := openSQLite(tmpPath)
	if err != nil {
		return fmt.Errorf("creating migration target %s: %w", tmpPath, err)
	}
	defer tmp.Close()
	notifyStep(notify, 2)

	var nBlocks, nSummaries, nMeta int
	err = tmp.writeTx(func(tx *sql.Tx) error {
		// Blocks in bbolt key order, IDs and timestamps preserved. The
		// lexical rows are filled with the same analyzer tokens a fresh
		// write would produce, so blocks_lex stays consistent with blocks.
		err := src.View(func(btx *bolt.Tx) error {
			b := btx.Bucket(bucketBlocks)
			if b == nil {
				return nil
			}
			return b.ForEach(func(k, v []byte) error {
				var block Block
				if err := json.Unmarshal(v, &block); err != nil {
					return fmt.Errorf("legacy block %s is corrupt: %w", k, err)
				}
				seq, err := insertBlockTx(tx, &block)
				if err != nil {
					return err
				}
				if err := insertLexTx(tx, seq, tmp.lexTokens(&block)); err != nil {
					return err
				}
				nBlocks++
				return nil
			})
		})
		if err != nil {
			return err
		}
		// Summaries and meta keep their keys and raw JSON byte for byte.
		err = src.View(func(btx *bolt.Tx) error {
			b := btx.Bucket(bucketSummaries)
			if b == nil {
				return nil
			}
			return b.ForEach(func(k, v []byte) error {
				if _, err := tx.Exec(`INSERT INTO summaries (key, data) VALUES (?, ?)`, string(k), string(v)); err != nil {
					return fmt.Errorf("copying summary %s: %w", k, err)
				}
				nSummaries++
				return nil
			})
		})
		if err != nil {
			return err
		}
		return src.View(func(btx *bolt.Tx) error {
			b := btx.Bucket(bucketMeta)
			if b == nil {
				return nil
			}
			return b.ForEach(func(k, v []byte) error {
				if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, ?)`, string(k), string(v)); err != nil {
					return fmt.Errorf("copying meta key %s: %w", k, err)
				}
				nMeta++
				return nil
			})
		})
	})
	if err != nil {
		return fmt.Errorf("copying legacy memory data: %w", err)
	}

	// Step 3: verify every table's row count against the source, then mark
	// the copy as migrated. memory_meta already holds store_format from the
	// schema, so it is excluded from the meta count.
	for _, check := range []struct {
		query string
		want  int
		name  string
	}{
		{`SELECT COUNT(*) FROM blocks`, nBlocks, "blocks"},
		{`SELECT COUNT(*) FROM summaries`, nSummaries, "summaries"},
		{`SELECT COUNT(*) FROM memory_meta WHERE key <> 'store_format' AND key NOT LIKE 'migration:%'`, nMeta, "meta"},
	} {
		var got int
		if err := tmp.reader.QueryRow(check.query).Scan(&got); err != nil {
			return fmt.Errorf("verifying migrated %s count: %w", check.name, err)
		}
		if got != check.want {
			return fmt.Errorf("verifying migrated %s: copied %d rows, source has %d", check.name, got, check.want)
		}
	}
	err = tmp.writeTx(func(tx *sql.Tx) error {
		for _, kv := range [][2]string{
			{"migrated_from", filepath.Base(legacyPath)},
			{"migrated_blocks", strconv.Itoa(nBlocks)},
			{"migrated_summaries", strconv.Itoa(nSummaries)},
			{"migrated_meta", strconv.Itoa(nMeta)},
			{"migrated_at", strconv.FormatInt(time.Now().UnixNano(), 10)},
		} {
			if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, kv[0], kv[1]); err != nil {
				return fmt.Errorf("recording %s: %w", kv[0], err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("recording migration metadata: %w", err)
	}
	notifyStep(notify, 3)

	// Step 4: close every database handle before renaming (Windows cannot
	// rename an open file). The checkpoint folds the WAL into the .tmp so
	// the renamed file is complete on its own.
	if _, err := tmp.writer.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpointing migration target: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing migration target: %w", err)
	}
	// On unix the read-only source stays open, and with it bbolt's shared
	// lock, until the guard is written: an older binary that starts now waits
	// on that lock instead of opening memory.db between the copy and the
	// switch and writing into the file that is about to become .v1. Windows
	// cannot rename an open file, so there the handle is closed first and
	// only the moment of the rename is unprotected.
	if runtime.GOOS == "windows" {
		if err := src.Close(); err != nil {
			return fmt.Errorf("closing legacy memory db: %w", err)
		}
	}
	if err := os.Rename(tmpPath, sqlitePath); err != nil {
		return fmt.Errorf("switching memory store to %s: %w", sqlitePath, err)
	}
	notifyStep(notify, 4)

	// Step 5: rename the legacy file aside and write the guard. A crash
	// between the two renames is completed by the next open (completeSwitch).
	if fileExists(legacyPath + ".v1") {
		return fmt.Errorf("completing memory migration: both %s and %s.v1 exist; move one aside manually", legacyPath, legacyPath)
	}
	if err := os.Rename(legacyPath, legacyPath+".v1"); err != nil {
		return fmt.Errorf("renaming legacy memory db aside: %w", err)
	}
	notifyStep(notify, 5)
	return writeGuardFile(legacyPath)
}

// completeSwitch finishes RFC 003 section 6.4 Memory step 5 when a crash hit
// between the two renames: memory_meta.migrated_from marks memory.sqlite as
// authoritative, so the legacy bbolt file is renamed aside and the guard
// written. It runs under the migration lock so concurrent opens do not race
// on the rename.
func (s *Store) completeSwitch(legacyPath string) error {
	migratedFrom, err := s.metaValue("migrated_from")
	if err != nil || migratedFrom == "" {
		return err
	}
	kind, err := classifyLegacy(legacyPath)
	if err != nil {
		return err
	}
	switch kind {
	case legacyGuard:
		return nil
	case legacyNone:
		// Crash after the rename but before the guard write.
		return ensureGuardFile(legacyPath)
	}
	lock, err := acquireMigrateLock(migrateLockPath(legacyPath))
	if err != nil {
		return err
	}
	defer releaseLockFile(lock)
	// Re-check under the lock: another process may have completed step 5.
	kind, err = classifyLegacy(legacyPath)
	if err != nil {
		return err
	}
	switch kind {
	case legacyBbolt:
		if fileExists(legacyPath + ".v1") {
			// A crash between the rename and the guard write, then an older
			// binary created a fresh memory.db. An empty one holds nothing:
			// replace it with the guard. One with data needs a person.
			if isEmptyBbolt(legacyPath) {
				return writeGuardFile(legacyPath)
			}
			return fmt.Errorf("completing memory migration: both %s and %s.v1 exist and %s has data; move one aside manually", legacyPath, legacyPath, legacyPath)
		}
		if err := os.Rename(legacyPath, legacyPath+".v1"); err != nil {
			return fmt.Errorf("renaming legacy memory db aside: %w", err)
		}
		return writeGuardFile(legacyPath)
	case legacyNone:
		return ensureGuardFile(legacyPath)
	default:
		return nil
	}
}
