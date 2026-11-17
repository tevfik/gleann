package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// The in-process migration walks steps 1-5 in order and reports each one.
func TestMigrateFromBbolt_StepsInOrder(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(4)
	writeBboltFixture(t, legacy, blocks, summaries, meta)

	var steps []int
	if err := migrateFromBbolt(legacy, sqlitePathFor(legacy), func(s int) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if want := []int{1, 2, 3, 4, 5}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	if !isGuardFile(legacy) || !fileExists(legacy+".v1") {
		t.Fatal("legacy file not renamed aside and replaced by the guard")
	}

	// Running it again is a no-op: the SQLite file is already there.
	steps = nil
	if err := migrateFromBbolt(legacy, sqlitePathFor(legacy), func(s int) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("second migration ran steps %v", steps)
	}
}

// Nothing to migrate (no legacy file, or only the guard): no step runs and no
// SQLite file is created by the migration.
func TestMigrateFromBbolt_NothingToMigrate(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	for _, setup := range []func(){
		func() {},
		func() {
			if err := writeGuardFile(legacy); err != nil {
				t.Fatal(err)
			}
		},
	} {
		setup()
		called := false
		if err := migrateFromBbolt(legacy, sqlitePathFor(legacy), func(int) { called = true }); err != nil {
			t.Fatal(err)
		}
		if called || fileExists(sqlitePathFor(legacy)) {
			t.Fatal("migration ran without a legacy bbolt database")
		}
	}
}

// A legacy block that does not decode stops the migration before the switch:
// the legacy file stays authoritative and no memory.sqlite appears.
func TestMigrateFromBbolt_CorruptLegacyBlock(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	db, err := bolt.Open(legacy, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket(bucketBlocks)
		if err != nil {
			return err
		}
		return b.Put([]byte("broken"), []byte("{not json"))
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = OpenStore(legacy)
	if err == nil || !strings.Contains(err.Error(), "legacy block broken is corrupt") {
		t.Fatalf("OpenStore = %v, want corrupt legacy block error", err)
	}
	if fileExists(sqlitePathFor(legacy)) {
		t.Fatal("memory.sqlite created from a failed migration")
	}
	if fileExists(legacy+".v1") || isGuardFile(legacy) {
		t.Fatal("legacy file moved aside after a failed migration")
	}
}

// A legacy database that has only some buckets (an old binary that never
// wrote a summary or meta key) migrates what is there.
func TestMigrateFromBbolt_MissingBuckets(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	db, err := bolt.Open(legacy, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucket([]byte("unrelated"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := OpenStore(legacy)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()
	if from, _ := s.Meta("migrated_from"); from == "" {
		t.Fatal("migration did not record migrated_from")
	}
	st, err := s.Stats()
	if err != nil || st.TotalCount != 0 {
		t.Fatalf("stats = %+v, %v", st, err)
	}
}

// A crash between the two renames of step 5 leaves memory.sqlite beside the
// legacy bbolt file. When memory.db.v1 also exists and memory.db has data,
// the open refuses to guess which one is right.
func TestMigrate_BothLegacyAndV1WithData(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(2)
	writeBboltFixture(t, legacy, blocks, summaries, meta)
	// A stray .v1 from an earlier migration: step 5 cannot rename aside.
	if err := os.WriteFile(legacy+".v1", []byte("older copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := migrateFromBbolt(legacy, sqlitePathFor(legacy), nil)
	if err == nil || !strings.Contains(err.Error(), "move one aside manually") {
		t.Fatalf("migrate = %v, want manual resolution error", err)
	}
	// The SQLite file was switched in (step 4) and is authoritative.
	if !fileExists(sqlitePathFor(legacy)) {
		t.Fatal("memory.sqlite missing after step 4")
	}
	_, err = OpenStore(legacy)
	if err == nil || !strings.Contains(err.Error(), "has data") {
		t.Fatalf("OpenStore = %v, want 'has data' error", err)
	}

	// Once the person moves the stray .v1 away, the open completes step 5.
	if err := os.Remove(legacy + ".v1"); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(legacy)
	if err != nil {
		t.Fatalf("OpenStore after resolving: %v", err)
	}
	defer s.Close()
	if !isGuardFile(legacy) || !fileExists(legacy+".v1") {
		t.Fatal("step 5 not completed")
	}
	got, err := s.List(TierLong)
	if err != nil || len(got) == 0 {
		t.Fatalf("migrated blocks: %d, %v", len(got), err)
	}
}

// A crash after renaming memory.db aside but before writing the guard leaves
// no file at the legacy path; the next open writes the guard.
func TestCompleteSwitch_WritesMissingGuard(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(2)
	writeBboltFixture(t, legacy, blocks, summaries, meta)
	s, err := OpenStore(legacy)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	s, err = openSQLite(sqlitePathFor(legacy))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.completeSwitch(legacy); err != nil {
		t.Fatal(err)
	}
	if !isGuardFile(legacy) {
		t.Fatal("guard not written")
	}
	// With the guard in place, completing again is a no-op.
	if err := s.completeSwitch(legacy); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateHelpers(t *testing.T) {
	if got := sqlitePathFor("/x/memory"); got != "/x/memory.sqlite" {
		t.Errorf("sqlitePathFor without .db = %q", got)
	}
	dir := t.TempDir()

	// isEmptyBbolt: not a bbolt file, a bbolt file with a block, and one
	// with only empty buckets.
	notBolt := filepath.Join(dir, "text")
	if err := os.WriteFile(notBolt, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isEmptyBbolt(notBolt) {
		t.Error("a text file reported as an empty bbolt database")
	}
	full := filepath.Join(dir, "full.db")
	blocks, _, _ := migrationFixture(2)
	writeBboltFixture(t, full, blocks, nil, nil)
	if isEmptyBbolt(full) {
		t.Error("a bbolt database with a block reported empty")
	}
	empty := filepath.Join(dir, "empty.db")
	writeBboltFixture(t, empty, nil, nil, nil)
	if !isEmptyBbolt(empty) {
		t.Error("a bbolt database with empty buckets reported non-empty")
	}

	// Paths that cannot be opened produce errors, not panics.
	missingDir := filepath.Join(dir, "no", "such", "dir", "memory.db")
	if _, err := acquireMigrateLock(missingDir); err == nil {
		t.Error("acquireMigrateLock in a missing directory succeeded")
	}
	if _, err := lockGuardShared(missingDir); err == nil {
		t.Error("lockGuardShared on a missing file succeeded")
	}
	if err := writeGuardFile(dir); err == nil {
		t.Error("writeGuardFile over a directory succeeded")
	}
	// ensureGuardFile leaves existing content alone.
	if err := ensureGuardFile(notBolt); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(notBolt); string(data) != "hello" {
		t.Errorf("ensureGuardFile overwrote content: %q", data)
	}
}
