package gleann

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/internal/testutil/format1"
)

// A source with several chunks keeps its chunk order (chunk_index, not the
// passage ID order), and the old manifest is carried over: the rows of
// indexed files keep their recorded hash, and a manifest entry with no
// passages (an empty file) still gets its files row so the next sync does not
// see it as new.
func TestMigrate_ChunkOrderAndManifestRows(t *testing.T) {
	dir, name := t.TempDir(), "chunks"
	ps := []format1.Passage{
		{ID: 0, Text: "third chunk of the guide mentions walrus", Metadata: map[string]any{"source": "guide.md", "chunk_index": 2}},
		{ID: 1, Text: "first chunk of the guide mentions otter", Metadata: map[string]any{"source": "guide.md", "chunk_index": 0}},
		{ID: 2, Text: "second chunk of the guide mentions badger", Metadata: map[string]any{"source": "guide.md", "chunk_index": 1}},
		{ID: 3, Text: "a passage without any chunk index mentions heron", Metadata: map[string]any{"source": "notes.md"}},
	}
	manifest := Manifest{Version: "1", IndexName: name, Entries: map[string]ManifestEntry{
		"guide.md": {Path: "guide.md", Size: 120, ModTime: 42, Hash: "hash-guide"},
		"empty.md": {Path: "empty.md", Size: 0, ModTime: 7, Hash: "hash-empty"},
	}}
	if err := format1.Write(format1.Index{
		Dir: dir, Name: name, Passages: ps, Manifest: manifest,
		Meta: map[string]any{"backend": "hnsw", "lexical_only": true, "source_dir": t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := MigrateIndex(dir, name, MigrateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passages != 4 || res.Renumbered || res.HadGraph {
		t.Fatalf("result = %+v", res)
	}

	st, err := store.Open(filepath.Join(dir, name, name+".db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	got, err := st.SelectByPredicate(ctx, `source = ?`, []any{"guide.md"}, 0)
	if err != nil || len(got) != 3 {
		t.Fatalf("guide.md passages = %d, %v", len(got), err)
	}
	var order []string
	_ = st.ForEach(ctx, func(p store.Passage) error {
		if p.Source == "guide.md" {
			order = append(order, strings.Fields(p.Text)[0]+"@"+string(rune('0'+p.ChunkNo)))
		}
		return nil
	})
	if strings.Join(order, " ") != "third@2 first@0 second@1" {
		t.Fatalf("chunk numbers by id = %v, want the chunk_index order", order)
	}
	files, err := st.Files(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f := files["guide.md"]; f.Hash != "hash-guide" || f.MtimeNS != 42 {
		t.Fatalf("guide.md row = %+v, want the manifest's", f)
	}
	if f, ok := files["empty.md"]; !ok || f.Hash != "hash-empty" {
		t.Fatalf("empty.md row = %+v, %v; a manifest entry without passages must keep its row", f, ok)
	}
	if _, ok := files["notes.md"]; !ok {
		t.Fatal("notes.md has passages but no files row")
	}
}

func TestMigrate_RefusalsAndCrashCompletion(t *testing.T) {
	dir := t.TempDir()
	if _, err := MigrateIndex(dir, "../up", MigrateOptions{}); err == nil {
		t.Fatal("a path name was accepted")
	}
	if _, err := CleanupMigration(dir, "../up"); err == nil {
		t.Fatal("cleanup accepted a path name")
	}
	if _, err := MigrateIndex(dir, "nothing", MigrateOptions{}); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("missing index: %v", err)
	}

	// Cleanup never deletes the only copy of an unmigrated index.
	oldFixture(t, dir, "old", []int64{0, 1}, false, false)
	if _, err := CleanupMigration(dir, "old"); err == nil || !strings.Contains(err.Error(), "migrate it first") {
		t.Fatalf("cleanup of an old index: %v", err)
	}
	if DetectStoreFormat(filepath.Join(dir, "old", "old")) != StoreOld {
		t.Fatal("cleanup touched an unmigrated index")
	}

	// A crash after the database rename but before the meta flag, in the
	// layout where the old passages file is gone already (format 2 by the
	// files): the re-run only records the flag.
	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), "flagless", f2Items()); err != nil {
		t.Fatal(err)
	}
	if err := UpdateIndexMeta(dir, "flagless", func(m *IndexMeta) { m.StoreFormat = 0 }); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateIndex(dir, "flagless", MigrateOptions{}); err != nil {
		t.Fatalf("completing a flagless format-2 index: %v", err)
	}
	if m, _ := GetIndexMeta(dir, "flagless"); m.StoreFormat != 2 {
		t.Fatalf("store_format = %d after completion", m.StoreFormat)
	}

	// The same crash with the old passages file still next to the database.
	oldFixture(t, dir, "half", []int64{0, 1, 2}, false, false)
	half := filepath.Join(dir, "half", "half")
	sp := testPassageStore(half) // creates half.db: the renamed database
	sp.Close()
	if DetectStoreFormat(half) != StoreOld {
		t.Fatal("fixture: the old passages file must still make it look old")
	}
	if _, err := MigrateIndex(dir, "half", MigrateOptions{}); err != nil {
		t.Fatalf("completing a half-switched index: %v", err)
	}
	if DetectStoreFormat(half) != StoreV2 {
		t.Fatal("the completed index is not format 2")
	}
}

// writeOldPassagesRaw writes a v1.9 bbolt passage store with raw values.
func writeOldPassagesRaw(t *testing.T, base string, values map[uint64]string, bucket bool) {
	t.Helper()
	db, err := bbolt.Open(base+".passages.db", 0o644, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !bucket {
		return
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("passages"))
		if err != nil {
			return err
		}
		for id, v := range values {
			k := make([]byte, 8)
			binary.BigEndian.PutUint64(k, id)
			if err := b.Put(k, []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigrate_UnreadableOldStores(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, base string)
		want  string
	}{
		{"no passages file", func(t *testing.T, base string) {
			writeSized(t, base+".index", 16)
		}, "no old-format passage store"},
		{"no passages bucket", func(t *testing.T, base string) {
			writeOldPassagesRaw(t, base, nil, false)
		}, "no passages to migrate"},
		{"empty bucket", func(t *testing.T, base string) {
			writeOldPassagesRaw(t, base, map[uint64]string{}, true)
		}, "no passages to migrate"},
		{"corrupt passage value", func(t *testing.T, base string) {
			writeOldPassagesRaw(t, base, map[uint64]string{0: `{"text":"ok"}`, 1: `{not json`}, true)
		}, "reading old-format passages"},
		{"not a bbolt file", func(t *testing.T, base string) {
			writeSized(t, base+".passages.db", 10)
			if err := os.WriteFile(base+".passages.db", []byte("definitely not bbolt"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "opening old-format passage store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, name := t.TempDir(), "broken"
			base := filepath.Join(dir, name, name)
			if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(base+".meta.json", []byte(`{"name":"broken","backend":"hnsw","lexical_only":true}`), 0o644); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, base)
			_, err := MigrateIndex(dir, name, MigrateOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(base + ".db"); !os.IsNotExist(err) {
				t.Fatal("a failed migration left a database behind")
			}
			if _, err := os.Stat(base + ".db.tmp"); !os.IsNotExist(err) {
				t.Fatal("a failed migration left its staging database behind")
			}
		})
	}
}

// A damaged vector file stops the migration before the switch: the index
// stays in the old format with its files untouched.
func TestMigrate_DamagedVectorFileStopsBeforeSwitch(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []int64
		want string
	}{
		{"dense ids: verification", []int64{0, 1, 2}, "verify vectors"},
		{"gaps: renumbering", []int64{0, 2, 5}, "cannot be renumbered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, name := t.TempDir(), "dmg"
			oldFixture(t, dir, name, tc.ids, true, false)
			base := filepath.Join(dir, name, name)
			if err := os.WriteFile(base+".index", []byte("this is not a vector index"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := MigrateIndex(dir, name, MigrateOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if DetectStoreFormat(base) != StoreOld {
				t.Fatal("the failed migration switched the index")
			}
			for _, leftover := range []string{".db", ".db.tmp", ".index.migrate", ".index.v1"} {
				if _, err := os.Stat(base + leftover); !os.IsNotExist(err) {
					t.Errorf("%s left behind", leftover)
				}
			}
		})
	}
}

// An interrupted v1.9 migration (guard files at the old names, the real
// stores renamed to .v1) is undone first, then migrated.
func TestMigrate_UndoesInterruptedV19Switch(t *testing.T) {
	dir, name := t.TempDir(), "v19crash"
	ps := oldFixture(t, dir, name, []int64{0, 1, 2}, false, false)
	base := filepath.Join(dir, name, name)
	if err := os.Rename(base+".passages.db", base+".passages.db.v1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".passages.db", []byte(storeGuardPrefix+": migrated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateIndex(dir, name, MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	checkSearches(t, dir, name, ps, false)
}

func TestChunkIndexOf(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want int
	}{{3, 3}, {int64(4), 4}, {float64(5), 5}, {"6", 0}, {nil, 0}} {
		if got := chunkIndexOf(Passage{Metadata: map[string]any{"chunk_index": tc.v}}); got != tc.want {
			t.Errorf("chunkIndexOf(%#v) = %d, want %d", tc.v, got, tc.want)
		}
	}
}

func TestSamplePassagesSpreadsAndSkipsEmpty(t *testing.T) {
	an := analysis.New("")
	var ps []Passage
	for i := 0; i < 50; i++ {
		ps = append(ps, Passage{ID: int64(i), Text: "word" + string(rune('a'+i%26)) + " text"})
	}
	ps = append(ps, Passage{ID: 50, Text: "..."})
	got := samplePassages(ps, an, 10)
	if len(got) != 10 || got[0].ID != 0 || got[9].ID != 45 {
		t.Fatalf("sample = %d passages, first %d last %d", len(got), got[0].ID, got[len(got)-1].ID)
	}
	small := samplePassages(ps[48:], an, 10)
	if len(small) != 2 {
		t.Fatalf("small corpus sample = %d, want the 2 usable passages", len(small))
	}
}
