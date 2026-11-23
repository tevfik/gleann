package gleann

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/metafault"
	"github.com/tevfik/gleann/internal/store"
)

// blockMetaWrites makes every later UpdateIndexMeta fail (through the metafault
// seam, so the store of the index stays usable). It returns the function that
// undoes it.
func blockMetaWrites(t *testing.T) (unblock func()) {
	t.Helper()
	unblock = metafault.Set(func(string) error { return errors.New("injected metadata write failure") })
	t.Cleanup(unblock)
	return unblock
}

// Cleanup fails fast while another process builds or syncs the index.
func TestCleanupMigrationFailsWhileTheIndexIsLocked(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
		t.Fatal(err)
	}
	lock, err := store.AcquireLock(filepath.Join(dir, "idx.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := CleanupMigration(dir, "idx"); err == nil {
		t.Fatal("CleanupMigration must fail while the index is locked")
	}
}

// A migration that cannot record the store format reports it, whichever of
// the two crash-completion paths it took.
func TestMigrateCannotRecordTheStoreFormat(t *testing.T) {
	t.Run("format 2 database without the flag", func(t *testing.T) {
		dir := t.TempDir()
		b, _ := newFormat2Builder(t, dir, 8)
		if err := b.Build(context.Background(), "flagless", f2Items()); err != nil {
			t.Fatal(err)
		}
		if err := UpdateIndexMeta(dir, "flagless", func(m *IndexMeta) { m.StoreFormat = 0 }); err != nil {
			t.Fatal(err)
		}
		blockMetaWrites(t)
		_, err := MigrateIndex(dir, "flagless", MigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "recording store format") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("renamed database next to the old store", func(t *testing.T) {
		dir := t.TempDir()
		oldFixture(t, dir, "half", []int64{0, 1, 2}, false, false)
		sp := testPassageStore(filepath.Join(dir, "half", "half"))
		sp.Close()
		blockMetaWrites(t)
		_, err := MigrateIndex(dir, "half", MigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "recording store format") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("after the switch", func(t *testing.T) {
		dir := t.TempDir()
		oldFixture(t, dir, "late", []int64{0, 1, 2}, false, false)
		var unblock func()
		_, err := MigrateIndex(dir, "late", MigrateOptions{crashHook: func(point string) error {
			if point == migrateAfterDBRename {
				unblock = blockMetaWrites(t)
			}
			return nil
		}})
		if err == nil || !strings.Contains(err.Error(), "recording store format") {
			t.Fatalf("err = %v", err)
		}
		// The database is switched already: the re-run only records the flag.
		unblock()
		if _, err := MigrateIndex(dir, "late", MigrateOptions{}); err != nil {
			t.Fatalf("re-run: %v", err)
		}
	})
}

// Steps before the copy refuse with the reason: a leftover that cannot be
// restored, an unreadable meta.json and a temporary database that is in the way.
func TestMigrateRefusals(t *testing.T) {
	t.Run("old files cannot be restored", func(t *testing.T) {
		dir := t.TempDir()
		oldFixture(t, dir, "undo", []int64{0, 1, 2}, false, false)
		base := filepath.Join(dir, "undo", "undo")
		// A preserved copy of the manifest and a directory where it belongs.
		if err := os.WriteFile(base+".manifest.json.v1", []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(base+".manifest.json", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := MigrateIndex(dir, "undo", MigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "restoring old .manifest.json") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no meta.json", func(t *testing.T) {
		dir := t.TempDir()
		oldFixture(t, dir, "nometa", []int64{0, 1, 2}, false, false)
		if err := os.Remove(filepath.Join(dir, "nometa", "nometa.meta.json")); err != nil {
			t.Fatal(err)
		}
		_, err := MigrateIndex(dir, "nometa", MigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "reading index meta") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("temporary database in the way", func(t *testing.T) {
		dir := t.TempDir()
		oldFixture(t, dir, "tmp", []int64{0, 1, 2}, false, false)
		if err := os.MkdirAll(filepath.Join(dir, "tmp", "tmp.db.tmp", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := MigrateIndex(dir, "tmp", MigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "creating format-2 store") {
			t.Fatalf("err = %v", err)
		}
	})
}

// The switch of a renumbered index reports each file operation that fails and
// leaves the old index in a state a re-run completes.
func TestMigrateSwitchFailures(t *testing.T) {
	ids := []int64{0, 2, 3, 6}
	t.Run("staging the vector file", func(t *testing.T) {
		dir := t.TempDir()
		oldFixture(t, dir, "stage", ids, true, false)
		if err := os.MkdirAll(filepath.Join(dir, "stage", "stage.index.migrate", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := MigrateIndex(dir, "stage", MigrateOptions{})
		if err == nil || !strings.Contains(err.Error(), "staging the renumbered vector file") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "stage", "stage.db.tmp")); !os.IsNotExist(err) {
			t.Error("the temporary database was not removed after the failure")
		}
	})
	t.Run("publishing the vector file", func(t *testing.T) {
		dir := t.TempDir()
		oldFixture(t, dir, "pub", ids, true, false)
		base := filepath.Join(dir, "pub", "pub")
		_, err := MigrateIndex(dir, "pub", MigrateOptions{crashHook: func(point string) error {
			if point == migrateAfterIndexAside {
				return os.Remove(base + ".index.migrate") // the staged file vanishes
			}
			return nil
		}})
		if err == nil || !strings.Contains(err.Error(), "publishing the renumbered vector file") {
			t.Fatalf("err = %v", err)
		}
		if _, err := MigrateIndex(dir, "pub", MigrateOptions{}); err != nil {
			t.Fatalf("re-run: %v", err)
		}
	})
	t.Run("switching the database", func(t *testing.T) {
		dir := t.TempDir()
		oldFixture(t, dir, "sw", ids, true, false)
		base := filepath.Join(dir, "sw", "sw")
		_, err := MigrateIndex(dir, "sw", MigrateOptions{crashHook: func(point string) error {
			if point == migrateAfterIndexNew {
				return os.MkdirAll(filepath.Join(base+".db", "x"), 0o755)
			}
			return nil
		}})
		if err == nil || !strings.Contains(err.Error(), "switching to format 2") {
			t.Fatalf("err = %v", err)
		}
	})
}

// renumbering explains why the vectors cannot be carried over.
func TestRenumberVectorsFailures(t *testing.T) {
	ctx := context.Background()
	ps := []Passage{{ID: 0}, {ID: 2}, {ID: 40}}
	prep := func(t *testing.T) (string, string, *IndexMeta) {
		t.Helper()
		dir := t.TempDir()
		oldFixture(t, dir, "rn", []int64{0, 2, 3}, true, false)
		meta, err := GetIndexMeta(dir, "rn")
		if err != nil {
			t.Fatal(err)
		}
		return dir, filepath.Join(dir, "rn", "rn"), meta
	}
	t.Run("unknown backend", func(t *testing.T) {
		dir, base, meta := prep(t)
		meta.Backend = "no-such-backend"
		_, err := renumberVectors(ctx, dir, base, meta, ps, nil)
		if err == nil || !strings.Contains(err.Error(), "getting backend") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unreadable vector file", func(t *testing.T) {
		dir, base, meta := prep(t)
		if err := os.Remove(base + ".index"); err != nil {
			t.Fatal(err)
		}
		_, err := renumberVectors(ctx, dir, base, meta, ps, nil)
		if err == nil || !strings.Contains(err.Error(), "reading the vector file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("passage without a stored vector", func(t *testing.T) {
		dir, base, meta := prep(t)
		cfg := DefaultConfig()
		_, err := renumberVectors(ctx, dir, base, meta, ps, &cfg)
		if err == nil {
			t.Fatal("a passage ID beyond the vector file must fail the renumbering")
		}
	})
	t.Run("recorded vector dtype", func(t *testing.T) {
		dir, base, meta := prep(t)
		meta.VectorDType = "f32"
		out, err := renumberVectors(ctx, dir, base, meta, []Passage{{ID: 0}, {ID: 2}, {ID: 3}}, nil)
		if err != nil || len(out) == 0 {
			t.Fatalf("renumbering with a recorded dtype: %d bytes, %v", len(out), err)
		}
	})
}

// verifyMigration refuses a copy that does not match the source.
func TestVerifyMigrationRefusals(t *testing.T) {
	ctx := context.Background()
	an := analysis.New("")
	text := "elderberry cordial recipe"
	var base string
	setup := func(t *testing.T) (*store.Store, []Passage) {
		t.Helper()
		base = filepath.Join(t.TempDir(), "v")
		st, err := store.Open(base+".db", store.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		id := int64(1)
		_, err = st.ApplySource(ctx, store.SourceDelta{Source: "a.md", Passages: []store.NewPassage{{
			ID: &id, Text: text, Kind: "text", Tokens: strings.Join(an.Tokens(text, analysis.Kind("text")), " "),
		}}})
		if err != nil {
			t.Fatal(err)
		}
		return st, []Passage{{ID: 0, Text: text, Metadata: map[string]any{"source": "a.md"}}}
	}
	t.Run("matches", func(t *testing.T) {
		st, ps := setup(t)
		if err := verifyMigration(ctx, st, an, ps, map[int64]int64{0: 0}, 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("count differs", func(t *testing.T) {
		st, ps := setup(t)
		err := verifyMigration(ctx, st, an, ps, map[int64]int64{0: 0}, 2)
		if err == nil || !strings.Contains(err.Error(), "copied 1 passages, expected 2") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("count unreadable", func(t *testing.T) {
		st, ps := setup(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		err := verifyMigration(cctx, st, an, ps, map[int64]int64{0: 0}, 1)
		if err == nil || !strings.Contains(err.Error(), "verify count") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("passage is not its own hit", func(t *testing.T) {
		st, ps := setup(t)
		err := verifyMigration(ctx, st, an, ps, map[int64]int64{0: 5}, 1)
		if err == nil || !strings.Contains(err.Error(), "not its own top-10 lexical hit") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("lexical search fails", func(t *testing.T) {
		st, ps := setup(t)
		fxExec(t, base, `DROP TABLE lex`)
		err := verifyMigration(ctx, st, an, ps, map[int64]int64{0: 0}, 1)
		if err == nil || !strings.Contains(err.Error(), "verify lexical search") {
			t.Fatalf("err = %v", err)
		}
	})
}

// A passage outranked on its own words by ten or more near-copies (a PDF's
// chart tick labels) is copied right and must pass; a passage mapped to
// another row, or whose lexical row belongs to another passage that shares
// a few of its words, must still be refused.
func TestVerifyMigrationNearCopies(t *testing.T) {
	ctx := context.Background()
	an := analysis.New("")
	target := "aux loss free layer wikipedia github mathematics 1 2 3 4 5 6 7 8"
	other := "layer of elderberry cordial"
	tokens := func(text string) string { return strings.Join(an.Tokens(text, analysis.Kind("text")), " ") }
	// setup stores the target as rowid 1 with the given lexical tokens, the
	// other passage as rowid 2, and 15 near-copies of the target that hold
	// its words twice and so outrank it.
	setup := func(t *testing.T, targetTokens string) *store.Store {
		t.Helper()
		st, err := store.Open(filepath.Join(t.TempDir(), "n.db"), store.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		var nps []store.NewPassage
		add := func(text, toks string) {
			id := int64(len(nps) + 1)
			nps = append(nps, store.NewPassage{ID: &id, Text: text, Kind: "text", Tokens: toks})
		}
		add(target, targetTokens)
		add(other, tokens(other))
		for i := 0; i < 15; i++ {
			add(target+" "+target, tokens(target+" "+target))
		}
		if _, err := st.ApplySource(ctx, store.SourceDelta{Source: "chart.pdf", Passages: nps}); err != nil {
			t.Fatal(err)
		}
		return st
	}
	ps := []Passage{{ID: 0, Text: target, Metadata: map[string]any{"source": "chart.pdf"}}}

	t.Run("near-copies outrank the passage", func(t *testing.T) {
		st := setup(t, tokens(target))
		hits, err := st.Search(ctx, store.LexQuery{Text: target, Analyzer: an}, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			if h.ID == 1 {
				t.Fatal("the target is in its own top 10: the test does not exercise near-copies")
			}
		}
		if err := verifyMigration(ctx, st, an, ps, map[int64]int64{0: 0}, 17); err != nil {
			t.Fatalf("a correct copy was refused: %v", err)
		}
	})
	t.Run("mapped to another row", func(t *testing.T) {
		st := setup(t, tokens(target))
		err := verifyMigration(ctx, st, an, ps, map[int64]int64{0: 1}, 17)
		if err == nil || !strings.Contains(err.Error(), "not its own top-10 lexical hit") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("lexical row of another passage", func(t *testing.T) {
		// The text is right, but the lexical row holds the other passage's
		// tokens, which share "layer" with the target: one matching word
		// is not enough.
		st := setup(t, tokens(other))
		err := verifyMigration(ctx, st, an, ps, map[int64]int64{0: 0}, 17)
		if err == nil || !strings.Contains(err.Error(), "not its own top-10 lexical hit") {
			t.Fatalf("err = %v", err)
		}
	})
}

// verifyVectorFile names the step that fails.
func TestVerifyVectorFileFailures(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "v", "v")
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyVectorFile(ctx, dir, base, &IndexMeta{Backend: "no-such-backend"}); err == nil || !strings.Contains(err.Error(), "getting backend") {
		t.Errorf("unknown backend: %v", err)
	}
	initTestHNSW()
	err := verifyVectorFile(ctx, dir, base, &IndexMeta{Backend: "hnsw"})
	var pe *os.PathError
	if err == nil || !(errors.As(err, &pe) || strings.Contains(err.Error(), "verify vectors")) {
		t.Errorf("missing vector file: %v", err)
	}
}
