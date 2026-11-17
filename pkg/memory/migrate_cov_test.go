package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// A path the operating system rejects outright (an embedded NUL) is a stat
// failure, not "no legacy database": the caller must not carry on as if there
// were nothing to migrate.
func TestLegacyPathStatFailures(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "mem\x00ory.db")
	if kind, err := classifyLegacy(bad); err == nil || kind != legacyNone || !strings.Contains(err.Error(), "stat legacy memory db") {
		t.Fatalf("classifyLegacy = %v, %v", kind, err)
	}
	if err := ensureGuardFile(bad); err == nil || !strings.Contains(err.Error(), "stat legacy memory db") {
		t.Fatalf("ensureGuardFile = %v", err)
	}
}

// The guard lock is shared among stores but refuses to coexist with an
// exclusive holder; the failure names the file.
func TestLockGuardShared_HeldExclusively(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	if err := writeGuardFile(path); err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := lockFileExclusive(holder); err != nil {
		t.Fatal(err)
	}
	defer unlockFile(holder)

	f, err := lockGuardShared(path)
	if err == nil {
		f.Close()
		t.Fatal("shared guard lock granted while an exclusive lock is held")
	}
	if !strings.Contains(err.Error(), "locking memory guard file") {
		t.Fatalf("error = %v", err)
	}
}

// A migration lock path that cannot be opened (here a directory sits there)
// fails the migration and the switch completion before anything moves.
func TestMigrationLockUnavailable(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(2)
	writeBboltFixture(t, legacy, blocks, summaries, meta)
	if err := os.Mkdir(migrateLockPath(legacy), 0o755); err != nil {
		t.Fatal(err)
	}

	err := migrateFromBbolt(legacy, sqlitePathFor(legacy), nil)
	if err == nil || !strings.Contains(err.Error(), "opening memory migration lock") {
		t.Fatalf("migrateFromBbolt = %v", err)
	}
	if fileExists(sqlitePathFor(legacy)) || fileExists(legacy+".v1") {
		t.Fatal("migration moved files without holding its lock")
	}

	// completeSwitch needs the lock as soon as it finds a legacy bbolt file
	// still in place after the switch.
	s := openTestStore(t)
	if err := s.SetMeta("migrated_from", "memory.db"); err != nil {
		t.Fatal(err)
	}
	err = s.completeSwitch(legacy)
	if err == nil || !strings.Contains(err.Error(), "opening memory migration lock") {
		t.Fatalf("completeSwitch = %v", err)
	}
	if !fileExists(legacy) {
		t.Fatal("completeSwitch renamed the legacy file without its lock")
	}
}

// A legacy file that is neither bbolt nor the guard stops both entry points.
func TestMigrate_UnreadableLegacyContent(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	if err := os.WriteFile(legacy, []byte(strings.Repeat("not a bbolt file ", 400)), 0o600); err != nil {
		t.Fatal(err)
	}
	err := migrateFromBbolt(legacy, sqlitePathFor(legacy), nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("migrateFromBbolt = %v", err)
	}

	s := openTestStore(t)
	if err := s.SetMeta("migrated_from", "memory.db"); err != nil {
		t.Fatal(err)
	}
	err = s.completeSwitch(legacy)
	if err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("completeSwitch = %v", err)
	}
	if got, _ := os.ReadFile(legacy); !strings.HasPrefix(string(got), "not a bbolt file") {
		t.Fatal("unreadable legacy file was modified")
	}
}

// A leftover memory.sqlite.tmp that cannot be discarded (a non-empty
// directory) stops the migration at step 2 and leaves the legacy file alone.
func TestMigrate_TargetCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(2)
	writeBboltFixture(t, legacy, blocks, summaries, meta)
	tmp := sqlitePathFor(legacy) + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := migrateFromBbolt(legacy, sqlitePathFor(legacy), nil)
	if err == nil || !strings.Contains(err.Error(), "creating migration target") {
		t.Fatalf("migrateFromBbolt = %v", err)
	}
	if fileExists(sqlitePathFor(legacy)) || fileExists(legacy+".v1") {
		t.Fatal("failed migration switched stores")
	}
}

// Legacy data that SQLite refuses is reported with the offending item and
// never reaches the switch.
func TestMigrate_CopyFailures(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, legacy string)
		want  string
	}{
		{"two keys, one block id", func(t *testing.T, legacy string) {
			blocks, _, _ := migrationFixture(2)
			data, err := json.Marshal(&blocks[0])
			if err != nil {
				t.Fatal(err)
			}
			db, err := bolt.Open(legacy, 0o600, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.Update(func(tx *bolt.Tx) error {
				b, err := tx.CreateBucket(bucketBlocks)
				if err != nil {
					return err
				}
				if err := b.Put([]byte("k1"), data); err != nil {
					return err
				}
				return b.Put([]byte("k2"), data)
			}); err != nil {
				t.Fatal(err)
			}
		}, "inserting block"},
		{"meta key collides with store_format", func(t *testing.T, legacy string) {
			writeBboltFixture(t, legacy, nil, nil, map[string]string{"store_format": "1"})
		}, "copying meta key store_format"},
		{"meta key shaped like a migration record", func(t *testing.T, legacy string) {
			writeBboltFixture(t, legacy, nil, nil, map[string]string{"migration:legacy": "1"})
		}, "verifying migrated meta"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := filepath.Join(t.TempDir(), "memory.db")
			tc.build(t, legacy)
			err := migrateFromBbolt(legacy, sqlitePathFor(legacy), nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("migrateFromBbolt = %v, want %q", err, tc.want)
			}
			if fileExists(sqlitePathFor(legacy)) || fileExists(legacy+".v1") || isGuardFile(legacy) {
				t.Fatal("failed copy switched stores")
			}
		})
	}
}

// Failures of the two renames of steps 4 and 5 are reported. The step
// callback is the seam: it runs right before each rename.
func TestMigrate_RenameFailures(t *testing.T) {
	t.Run("sqlite target is occupied", func(t *testing.T) {
		legacy := filepath.Join(t.TempDir(), "memory.db")
		blocks, summaries, meta := migrationFixture(2)
		writeBboltFixture(t, legacy, blocks, summaries, meta)
		target := sqlitePathFor(legacy)
		err := migrateFromBbolt(legacy, target, func(step int) {
			if step == 3 {
				if err := os.MkdirAll(filepath.Join(target, "inner"), 0o755); err != nil {
					t.Error(err)
				}
			}
		})
		if err == nil || !strings.Contains(err.Error(), "switching memory store") {
			t.Fatalf("migrateFromBbolt = %v", err)
		}
	})
	t.Run("legacy file vanishes before it is renamed aside", func(t *testing.T) {
		legacy := filepath.Join(t.TempDir(), "memory.db")
		blocks, summaries, meta := migrationFixture(2)
		writeBboltFixture(t, legacy, blocks, summaries, meta)
		err := migrateFromBbolt(legacy, sqlitePathFor(legacy), func(step int) {
			if step == 4 {
				if err := os.Remove(legacy); err != nil {
					t.Error(err)
				}
			}
		})
		if err == nil || !strings.Contains(err.Error(), "renaming legacy memory db aside") {
			t.Fatalf("migrateFromBbolt = %v", err)
		}
	})
}
