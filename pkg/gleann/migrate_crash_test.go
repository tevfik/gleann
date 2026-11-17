package gleann

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Crash-safety of the format-2 migration switch (RFC 003 section 6.4 step 4)
// and of rollback: every intermediate state — a panic after any rename or
// guard write, a rollback interrupted between the meta flag and the database
// delete, meta saying format 2 with no database — must be resolved by a
// migrate re-run or by --rollback, without ever creating an empty format-2
// store over a format-1 index.

// panicAt returns a migration hook that panics at one switch boundary,
// simulating a process crash: the deferred undo of the error path does NOT
// run, so the intermediate state is left on disk exactly as a crash would.
func panicAt(point string) func(string) error {
	return func(p string) error {
		if p == point {
			panic("injected crash at " + p)
		}
		return nil
	}
}

// migrateCrash runs MigrateIndex with a panicking hook and recovers the
// panic, leaving the crash state on disk. The migration's deferred lock
// release still runs while the panic unwinds.
func migrateCrash(t *testing.T, dir, name, point string) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected the injected crash at %s", point)
		}
	}()
	_ = MigrateIndex(dir, name, MigrateOptions{crashHook: panicAt(point)})
}

// Every crash point of the switch must be recoverable BOTH ways: a re-run of
// migrate completes the migration with identical BM25 results, and rollback
// restores byte-identical format-1 files.
func TestMigrate_CrashStatesRecoverable(t *testing.T) {
	points := []string{
		migrateAfterPassagesRename,
		migrateAfterManifestRename,
		migrateAfterPassagesGuard,
		migrateAfterManifestGuard,
		migrateAfterDBRename,
	}
	for _, point := range points {
		for _, resolve := range []string{"rerun", "rollback"} {
			t.Run(point+"/"+resolve, func(t *testing.T) {
				dir, name, _ := migrateFixture(t)
				ctx := context.Background()
				base := filepath.Join(dir, name, name)
				queries := migrateWords[:10]

				before := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
				if err := before.Load(ctx, name); err != nil {
					t.Fatalf("Load before: %v", err)
				}
				want := bm25Lists(t, before, queries)
				before.Close()
				passagesSHA := fileSHA(t, base+".passages.db")
				manifestSHA := fileSHA(t, base+".manifest.json")
				indexSHA := fileSHA(t, base+".index")

				migrateCrash(t, dir, name, point)

				// The crash state of the last boundary is the one the old
				// order left unguarded: a live database with meta still 1.
				if point == migrateAfterDBRename {
					if _, err := os.Stat(base + ".db"); err != nil {
						t.Fatalf("the database rename must have happened: %v", err)
					}
					if m, err := GetIndexMeta(dir, name); err != nil || m.StoreFormat == 2 {
						t.Fatalf("meta must still record format 1 at this crash point: %+v (%v)", m, err)
					}
				}

				if resolve == "rerun" {
					if err := MigrateIndex(dir, name, MigrateOptions{}); err != nil {
						t.Fatalf("MigrateIndex re-run after crash at %s: %v", point, err)
					}
					if got := StoreFormatOf(base); got != 2 {
						t.Fatalf("StoreFormatOf after re-run = %d, want 2", got)
					}
					if m, err := GetIndexMeta(dir, name); err != nil || m.StoreFormat != 2 {
						t.Fatalf("meta after re-run = %+v (%v), want store_format 2", m, err)
					}
					if _, err := os.Stat(base + ".db.tmp"); !os.IsNotExist(err) {
						t.Errorf("stale staging database survived the re-run: %v", err)
					}
					after := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
					if err := after.Load(ctx, name); err != nil {
						t.Fatalf("Load after re-run: %v", err)
					}
					defer after.Close()
					if !after.VectorAvailable() {
						t.Error("vectors unavailable after the re-run")
					}
					got := bm25Lists(t, after, queries)
					for i, q := range queries {
						if len(got[i]) != len(want[i]) {
							t.Fatalf("query %q: %d results after re-run, %d before", q, len(got[i]), len(want[i]))
						}
						for j := range want[i] {
							if got[i][j].ID != want[i][j].ID || got[i][j].Text != want[i][j].Text {
								t.Errorf("query %q rank %d: before {ID:%d Text:%q} after {ID:%d Text:%q}",
									q, j, want[i][j].ID, want[i][j].Text, got[i][j].ID, got[i][j].Text)
							}
						}
					}
					return
				}

				if err := RollbackMigration(dir, name); err != nil {
					t.Fatalf("RollbackMigration after crash at %s: %v", point, err)
				}
				if got := fileSHA(t, base+".passages.db"); got != passagesSHA {
					t.Error("rollback did not restore the byte-identical passage store")
				}
				if got := fileSHA(t, base+".manifest.json"); got != manifestSHA {
					t.Error("rollback did not restore the byte-identical manifest")
				}
				if got := fileSHA(t, base+".index"); got != indexSHA {
					t.Error("rollback touched the vector file")
				}
				if got := StoreFormatOf(base); got != 1 {
					t.Errorf("StoreFormatOf after rollback = %d, want 1", got)
				}
				if m, err := GetIndexMeta(dir, name); err != nil || m.StoreFormat != 0 {
					t.Errorf("meta after rollback = %+v (%v), want no store_format", m, err)
				}
				for _, suffix := range []string{".db", ".db.tmp", ".passages.db.v1", ".manifest.json.v1"} {
					if _, err := os.Stat(base + suffix); !os.IsNotExist(err) {
						t.Errorf("leftover %s after rollback: %v", suffix, err)
					}
				}
				after := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
				if err := after.Load(ctx, name); err != nil {
					t.Fatalf("Load after rollback: %v", err)
				}
				defer after.Close()
				if rs, err := after.SearchBM25(ctx, "apple", 5); err != nil || len(rs) == 0 {
					t.Errorf("SearchBM25 after rollback = %+v (%v), want hits", rs, err)
				}
			})
		}
	}
}

// When the hook returns an error instead of panicking, the switch undoes its
// completed renames in reverse order: the format-1 files are byte-identical
// immediately, with no leftovers.
func TestMigrate_ErrorUndoesSwitch(t *testing.T) {
	dir, name, _ := migrateFixture(t)
	base := filepath.Join(dir, name, name)
	passagesSHA := fileSHA(t, base+".passages.db")
	manifestSHA := fileSHA(t, base+".manifest.json")

	err := MigrateIndex(dir, name, MigrateOptions{crashHook: crashAt(migrateAfterManifestGuard)})
	if err == nil {
		t.Fatal("MigrateIndex must fail at the injected error")
	}
	if got := fileSHA(t, base+".passages.db"); got != passagesSHA {
		t.Error("the error path did not restore the byte-identical passage store")
	}
	if got := fileSHA(t, base+".manifest.json"); got != manifestSHA {
		t.Error("the error path did not restore the byte-identical manifest")
	}
	if got := StoreFormatOf(base); got != 1 {
		t.Errorf("StoreFormatOf after the failed switch = %d, want 1", got)
	}
	for _, suffix := range []string{".db", ".db.tmp", ".passages.db.v1", ".manifest.json.v1"} {
		if _, err := os.Stat(base + suffix); !os.IsNotExist(err) {
			t.Errorf("leftover %s after the failed switch: %v", suffix, err)
		}
	}
}

// meta.json saying format 2 with no <name>.db (a crash between the database
// delete and the meta write of the OLD rollback order, or a lost file): the
// opener must refuse with a clear error instead of creating an empty store,
// and rollback must restore the format-1 index.
func TestFormat2_MissingStoreRefused(t *testing.T) {
	dir, name, _ := migrateFixture(t)
	ctx := context.Background()
	base := filepath.Join(dir, name, name)
	passagesSHA := fileSHA(t, base+".passages.db")
	// Simulate the crash state: the flag says 2, the database is gone, the
	// format-1 passage store is intact.
	if err := UpdateIndexMeta(dir, name, func(m *IndexMeta) { m.StoreFormat = 2 }); err != nil {
		t.Fatalf("UpdateIndexMeta: %v", err)
	}

	_, err := OpenPassageStore(base, true)
	if err == nil || !strings.Contains(err.Error(), "migrate --rollback") || !strings.Contains(err.Error(), "repair") {
		t.Fatalf("OpenPassageStore must refuse with a rollback/repair hint, got %v", err)
	}
	if _, err := os.Stat(base + ".db"); !os.IsNotExist(err) {
		t.Fatalf("the refused open created an empty database: %v", err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err := s.Load(ctx, name); err == nil || !strings.Contains(err.Error(), "migrate --rollback") {
		t.Fatalf("Load must refuse the missing store, got %v", err)
	}
	if _, err := os.Stat(base + ".db"); !os.IsNotExist(err) {
		t.Fatalf("Load created an empty database: %v", err)
	}
	// A migrate re-run must not silently migrate over the crash state either.
	if err := MigrateIndex(dir, name, MigrateOptions{}); err == nil || !strings.Contains(err.Error(), "migrate --rollback") {
		t.Fatalf("MigrateIndex must refuse the crash state with a hint, got %v", err)
	}

	if err := RollbackMigration(dir, name); err != nil {
		t.Fatalf("RollbackMigration of the crash state: %v", err)
	}
	if got := fileSHA(t, base+".passages.db"); got != passagesSHA {
		t.Error("rollback did not restore the byte-identical passage store")
	}
	if m, err := GetIndexMeta(dir, name); err != nil || m.StoreFormat != 0 {
		t.Errorf("meta after rollback = %+v (%v), want no store_format", m, err)
	}
	if _, err := os.Stat(base + ".db"); !os.IsNotExist(err) {
		t.Errorf("rollback left a database behind: %v", err)
	}
	after := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err := after.Load(ctx, name); err != nil {
		t.Fatalf("Load after rollback: %v", err)
	}
	defer after.Close()
	if rs, err := after.SearchBM25(ctx, "apple", 5); err != nil || len(rs) == 0 {
		t.Errorf("SearchBM25 after rollback = %+v (%v), want hits", rs, err)
	}
}

// Rollback is re-runnable: a crash after the meta flag was cleared (with the
// database still present, or already deleted) leaves a state a second
// rollback completes, restoring byte-identical format-1 files.
func TestRollback_RerunAfterCrash(t *testing.T) {
	for _, dbDeleted := range []bool{false, true} {
		name := "db_present"
		if dbDeleted {
			name = "db_deleted"
		}
		t.Run(name, func(t *testing.T) {
			dir, idxName, _ := migrateFixture(t)
			base := filepath.Join(dir, idxName, idxName)
			if err := MigrateIndex(dir, idxName, MigrateOptions{}); err != nil {
				t.Fatalf("MigrateIndex: %v", err)
			}
			passagesSHA := fileSHA(t, base+".passages.db.v1")
			manifestSHA := fileSHA(t, base+".manifest.json.v1")

			// Simulate the crash after the FIRST step of the new rollback
			// order: the meta flag is already cleared.
			if err := UpdateIndexMeta(dir, idxName, func(m *IndexMeta) { m.StoreFormat = 0 }); err != nil {
				t.Fatalf("UpdateIndexMeta: %v", err)
			}
			if dbDeleted {
				removeLexicalFiles(base + ".db")
			}
			if err := RollbackMigration(dir, idxName); err != nil {
				t.Fatalf("RollbackMigration re-run: %v", err)
			}
			if got := fileSHA(t, base+".passages.db"); got != passagesSHA {
				t.Error("rollback re-run did not restore the byte-identical passage store")
			}
			if got := fileSHA(t, base+".manifest.json"); got != manifestSHA {
				t.Error("rollback re-run did not restore the byte-identical manifest")
			}
			if got := StoreFormatOf(base); got != 1 {
				t.Errorf("StoreFormatOf after rollback re-run = %d, want 1", got)
			}
			for _, suffix := range []string{".db", ".db.tmp", ".passages.db.v1", ".manifest.json.v1"} {
				if _, err := os.Stat(base + suffix); !os.IsNotExist(err) {
					t.Errorf("leftover %s after rollback re-run: %v", suffix, err)
				}
			}
		})
	}
}

// A stale <name>.db.tmp from an interrupted copy phase must not block a
// re-run: migrate discards it and completes.
func TestMigrate_StaleTmpDiscarded(t *testing.T) {
	dir, name, _ := migrateFixture(t)
	base := filepath.Join(dir, name, name)
	if err := os.WriteFile(base+".db.tmp", []byte("stale staging database"), 0o644); err != nil {
		t.Fatalf("write stale tmp: %v", err)
	}
	if err := MigrateIndex(dir, name, MigrateOptions{}); err != nil {
		t.Fatalf("MigrateIndex with a stale tmp: %v", err)
	}
	if got := StoreFormatOf(base); got != 2 {
		t.Errorf("StoreFormatOf = %d, want 2", got)
	}
	if _, err := os.Stat(base + ".db.tmp"); !os.IsNotExist(err) {
		t.Errorf("stale tmp survived the migration: %v", err)
	}
	// Rollback still restores the format-1 index.
	if err := RollbackMigration(dir, name); err != nil {
		t.Fatalf("RollbackMigration: %v", err)
	}
	if got := StoreFormatOf(base); got != 1 {
		t.Errorf("StoreFormatOf after rollback = %d, want 1", got)
	}
}
