package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// writeBboltFixture creates a legacy bbolt memory.db with the given blocks,
// summaries and meta keys, exactly as the old store wrote them (whole-block
// JSON, summary key <created>_<conversation_id>). Fixture timestamps come
// from time.Unix so they carry no monotonic reading and survive the SQLite
// round trip unchanged.
func writeBboltFixture(t *testing.T, path string, blocks []Block, summaries []Summary, meta map[string]string) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketBlocks, bucketSummaries, bucketMeta} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		b := tx.Bucket(bucketBlocks)
		for i := range blocks {
			data, err := json.Marshal(&blocks[i])
			if err != nil {
				return err
			}
			if err := b.Put([]byte(blocks[i].ID), data); err != nil {
				return err
			}
		}
		sb := tx.Bucket(bucketSummaries)
		for i := range summaries {
			data, err := json.Marshal(&summaries[i])
			if err != nil {
				return err
			}
			key := fmt.Sprintf("%s_%s", summaries[i].CreatedAt.Format("20060102_150405"), summaries[i].ConversationID)
			if err := sb.Put([]byte(key), data); err != nil {
				return err
			}
		}
		mb := tx.Bucket(bucketMeta)
		for k, v := range meta {
			if err := mb.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// migrationFixture is a representative legacy database: both persisted tiers,
// provenance, validity counters, expiry, scopes, tags and metadata.
func migrationFixture(n int) ([]Block, []Summary, map[string]string) {
	base := time.Unix(1700000000, 123456789)
	past := base.Add(-time.Hour)
	future := base.Add(100000 * time.Hour)
	blocks := make([]Block, n)
	for i := range blocks {
		tier := TierLong
		if i%3 == 0 {
			tier = TierMedium
		}
		b := Block{
			ID:          fmt.Sprintf("legacy-block-%04d", i),
			Tier:        tier,
			Label:       fmt.Sprintf("label_%d", i),
			Content:     fmt.Sprintf("legacy memory content %d about databases and deployments", i),
			Source:      "user",
			Tags:        []string{fmt.Sprintf("tag%d", i%5), "legacy"},
			Metadata:    map[string]string{"origin": "fixture", "n": fmt.Sprint(i)},
			CreatedAt:   base.Add(time.Duration(i) * time.Minute),
			UpdatedAt:   base.Add(time.Duration(i) * time.Minute).Add(time.Second),
			CharLimit:   i * 10,
			Scope:       fmt.Sprintf("repo-%d", i%2),
			Repo:        "github.com/tevfik/gleann",
			Paths:       []string{fmt.Sprintf("internal/svc%d/handler.go", i)},
			Symbols:     []string{fmt.Sprintf("pkg.Svc%d.Run", i)},
			Commit:      fmt.Sprintf("abc%04d", i),
			Confirms:    i % 4,
			Conflicts:   i % 2,
			AccessCount: i,
		}
		if i%7 == 0 {
			b.ExpiresAt = &future
		}
		if i%11 == 0 {
			b.ExpiresAt = &past // expired: still migrated, filtered by List
		}
		if i%13 == 0 {
			b.Suspect = true
			b.StaleReason = "file changed"
		}
		if i%17 == 0 {
			la := base.Add(time.Duration(i) * time.Minute)
			b.LastAccessedAt = &la
		}
		blocks[i] = b
	}
	// One block with a unique word so the migrated blocks_lex can be probed.
	// Index 1: index 0 is expired (i%11) and would be filtered from Search.
	blocks[1].Content = "zebra uniqueness marker for the lexical row"
	summaries := []Summary{
		{ConversationID: "conv-a", Title: "First", Content: "discussed migrations", MessageCount: 4, IndexNames: []string{"gleann"}, Model: "test-model", CreatedAt: base},
		{ConversationID: "conv-b", Title: "Second", Content: "discussed deployments", MessageCount: 9, CreatedAt: base.Add(time.Minute)},
		{ConversationID: "conv-c", Title: "Third", Content: "discussed databases", MessageCount: 2, CreatedAt: base.Add(2 * time.Minute)},
	}
	meta := map[string]string{"schema_version": "1", "custom_key": "custom value"}
	return blocks, summaries, meta
}

// assertMigratedStore checks the complete post-migration state: every block
// and summary equal to the fixture, the migration metadata, the .v1 backup
// and the guard file.
func assertMigratedStore(t *testing.T, s *Store, legacyPath string, blocks []Block, summaries []Summary, meta map[string]string) {
	t.Helper()
	for i := range blocks {
		got, err := s.Get(blocks[i].ID)
		if err != nil {
			t.Fatalf("Get(%s) after migration: %v", blocks[i].ID, err)
		}
		if !reflect.DeepEqual(got, &blocks[i]) {
			t.Fatalf("block %s changed by migration:\n got  %+v\n want %+v", blocks[i].ID, got, blocks[i])
		}
	}
	gotSums, err := s.ListSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSums) != len(summaries) {
		t.Fatalf("summaries = %d, want %d", len(gotSums), len(summaries))
	}
	byTitle := make(map[string]Summary, len(gotSums))
	for _, sum := range gotSums {
		byTitle[sum.Title] = sum
	}
	for _, want := range summaries {
		got, ok := byTitle[want.Title]
		// The same instant may come back in another *time.Location (time.Local
		// is "UTC" on a UTC machine, not time.UTC), which DeepEqual tells apart.
		if ok && got.CreatedAt.Equal(want.CreatedAt) {
			got.CreatedAt = want.CreatedAt
		}
		if !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("summary %q changed by migration:\n got  %+v\n want %+v", want.Title, got, want)
		}
	}
	// Summary keys preserved byte for byte.
	rows, err := s.reader.Query(`SELECT key FROM summaries ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	var wantKeys []string
	for _, sum := range summaries {
		wantKeys = append(wantKeys, fmt.Sprintf("%s_%s", sum.CreatedAt.Format("20060102_150405"), sum.ConversationID))
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("summary keys = %v, want %v", keys, wantKeys)
	}
	// Meta copied, plus the migration markers.
	for k, want := range meta {
		got, err := s.metaValue(k)
		if err != nil || got != want {
			t.Fatalf("meta %s = %q, %v; want %q", k, got, err, want)
		}
	}
	migratedFrom, err := s.metaValue("migrated_from")
	if err != nil || migratedFrom != filepath.Base(legacyPath) {
		t.Fatalf("migrated_from = %q, %v; want %q", migratedFrom, err, filepath.Base(legacyPath))
	}
	for key, want := range map[string]int{
		"migrated_blocks":    len(blocks),
		"migrated_summaries": len(summaries),
		"migrated_meta":      len(meta),
	} {
		got, err := s.metaValue(key)
		if err != nil || got != fmt.Sprint(want) {
			t.Fatalf("%s = %q, %v; want %d", key, got, err, want)
		}
	}
	// blocks_lex is consistent with blocks (rowid = seq) and searchable.
	var lexCount int
	if err := s.reader.QueryRow(`SELECT COUNT(*) FROM blocks_lex`).Scan(&lexCount); err != nil {
		t.Fatal(err)
	}
	if lexCount != len(blocks) {
		t.Fatalf("blocks_lex rows = %d, want %d", lexCount, len(blocks))
	}
	var hits int
	if err := s.reader.QueryRow(`SELECT COUNT(*) FROM blocks_lex WHERE blocks_lex MATCH 'zebra'`).Scan(&hits); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("blocks_lex MATCH 'zebra' = %d hits, want 1", hits)
	}
	// The legacy file was renamed aside and replaced by the guard.
	if _, err := os.Stat(legacyPath + ".v1"); err != nil {
		t.Fatalf("legacy backup missing: %v", err)
	}
	guard, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("guard file missing: %v", err)
	}
	if len(guard) == 0 || string(guard) != guardText {
		t.Fatalf("guard content = %q, want the non-empty guard text", guard)
	}
	if _, err := os.Stat(sqlitePathFor(legacyPath) + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale .tmp left behind: %v", err)
	}
}

func TestMigrateFromBboltFixture(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(120)
	writeBboltFixture(t, legacyPath, blocks, summaries, meta)

	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatalf("OpenStore after fixture: %v", err)
	}
	defer s.Close()
	assertMigratedStore(t, s, legacyPath, blocks, summaries, meta)

	// List filters expired blocks exactly as before; search keeps ranking.
	listed, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	wantLive := 0
	for i := range blocks {
		if !blocks[i].IsExpired() {
			wantLive++
		}
	}
	if len(listed) != wantLive {
		t.Fatalf("List after migration = %d blocks, want %d live", len(listed), wantLive)
	}
	hits, err := s.Search("zebra uniqueness")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != blocks[1].ID {
		t.Fatalf("Search after migration: top hit = %+v, want %s", hits, blocks[1].ID)
	}

	// The .v1 backup is still the original bbolt database.
	old, err := bolt.Open(legacyPath+".v1", 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("opening .v1 backup: %v", err)
	}
	defer old.Close()
	if err := old.View(func(tx *bolt.Tx) error {
		if n := tx.Bucket(bucketBlocks).Stats().KeyN; n != len(blocks) {
			t.Fatalf(".v1 blocks = %d, want %d", n, len(blocks))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateGuardBlocksOldBbolt(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(10)
	writeBboltFixture(t, legacyPath, blocks, summaries, meta)

	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatal(err)
	}

	// While the store is open, an old binary's exclusive bbolt.Open times
	// out on the shared guard lock and writes nothing.
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bolt.Open(legacyPath, 0o600, &bolt.Options{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("old bbolt.Open succeeded while the SQLite store is open")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("old bbolt.Open error = %v, want the lock timeout", err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("guard file written by old open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// After Close the guard text alone makes bbolt.Open fail without
	// writing: a non-empty non-bbolt file is refused, not initialised.
	_, err = bolt.Open(legacyPath, 0o600, &bolt.Options{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("old bbolt.Open succeeded on the guard file")
	}
	after2, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(before, after2) {
		t.Fatalf("guard file written by old open after Close: %v", err)
	}
	if _, err := os.Stat(sqlitePathFor(legacyPath)); err != nil {
		t.Fatalf("memory.sqlite missing: %v", err)
	}
}

func TestOpenStoreEmptyLegacyFile(t *testing.T) {
	// internal/server's scheduler test creates an empty .db file first;
	// an empty file holds no bbolt data and must not block the open.
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "gleann-memory-123.db")
	f, err := os.Create(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatalf("OpenStore on empty legacy file: %v", err)
	}
	defer s.Close()
	if err := s.Add(&Block{Tier: TierLong, Label: "l", Content: "works"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sqlitePathFor(legacyPath)); err != nil {
		t.Fatalf("sqlite file missing: %v", err)
	}
}

func TestOpenStoreReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(&Block{ID: "persist-me", Tier: TierLong, Label: "l", Content: "durable fact"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSummary(&Summary{ConversationID: "c", Title: "t", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	b, err := s2.Get("persist-me")
	if err != nil || b.Content != "durable fact" {
		t.Fatalf("Get after reopen = %+v, %v", b, err)
	}
	sums, err := s2.ListSummaries()
	if err != nil || len(sums) != 1 {
		t.Fatalf("summaries after reopen = %d, %v", len(sums), err)
	}
}

func TestOpenStoreGuardOnlyLegacy(t *testing.T) {
	// A guard without memory.sqlite (e.g. the user deleted the SQLite file)
	// opens a fresh store and keeps the guard; it is not treated as bbolt.
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	if err := os.WriteFile(legacyPath, []byte(guardText), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatalf("OpenStore with guard only: %v", err)
	}
	defer s.Close()
	if n, err := s.List(""); err != nil || len(n) != 0 {
		t.Fatalf("fresh store has %d blocks, %v", len(n), err)
	}
	guard, err := os.ReadFile(legacyPath)
	if err != nil || string(guard) != guardText {
		t.Fatalf("guard changed: %v", err)
	}
}

func TestDefaultStoreHonoursMemoryDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", dir)
	s, err := DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Path() != filepath.Join(dir, "memory.db") {
		t.Fatalf("Path = %q", s.Path())
	}
	if _, err := os.Stat(filepath.Join(dir, "memory.sqlite")); err != nil {
		t.Fatalf("memory.sqlite not created in GLEANN_MEMORY_DIR: %v", err)
	}
	if err := s.Add(&Block{Tier: TierLong, Label: "l", Content: "scoped"}); err != nil {
		t.Fatal(err)
	}
}

// A legacy memory.db that cannot be read (here: no read permission) is not
// taken for the guard: OpenStore fails and creates no empty SQLite store
// that would hide the user's memories from every later open.
func TestUnreadableLegacyIsNotMigratedAway(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not stop this user here")
	}
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(3)
	writeBboltFixture(t, legacyPath, blocks, summaries, meta)
	if err := os.Chmod(legacyPath, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(legacyPath, 0o600)

	if s, err := OpenStore(legacyPath); err == nil {
		s.Close()
		t.Fatal("OpenStore succeeded over an unreadable legacy memory.db")
	}
	if fileExists(sqlitePathFor(legacyPath)) {
		t.Fatal("an empty memory.sqlite was created beside the unreadable legacy file")
	}
}

// After a crash between renaming memory.db to .v1 and writing the guard, an
// older binary may create a fresh, empty memory.db. The next open replaces
// it with the guard instead of refusing to start.
func TestCompleteSwitchReplacesEmptyBboltWithGuard(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "memory.db")
	blocks, summaries, meta := migrationFixture(3)
	writeBboltFixture(t, legacyPath, blocks, summaries, meta)
	s, err := OpenStore(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Simulate: the guard was never written and an old binary created an
	// empty bbolt file at the old name.
	if err := os.Remove(legacyPath); err != nil {
		t.Fatal(err)
	}
	empty, err := bolt.Open(legacyPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty.Close()

	s, err = OpenStore(legacyPath)
	if err != nil {
		t.Fatalf("OpenStore with an empty bbolt beside .v1: %v", err)
	}
	defer s.Close()
	if !isGuardFile(legacyPath) {
		t.Fatal("the empty bbolt file was not replaced by the guard")
	}
	got, err := s.List(TierLong)
	if err != nil || len(got) == 0 {
		t.Fatalf("migrated blocks lost: %d, %v", len(got), err)
	}
}
