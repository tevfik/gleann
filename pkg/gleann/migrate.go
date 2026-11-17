package gleann

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// This file implements `gleann index migrate` (RFC 003 section 6.4): converting
// a format-1 bbolt index to the format-2 SQLite record store without touching
// the vector file, with verification, guard files and a byte-identical
// rollback.

// MigrateOptions controls a migration. Language overrides the analyzer
// language; empty means guess it from the corpus (RFC 003 section 6.4 step 2).
type MigrateOptions struct {
	Language string
	// crashHook injects a failure at a named step of the switch (tests only;
	// see the migrateAfter* constants). It runs after each rename and guard
	// write of step 4; a non-nil error aborts the migration there, leaving the
	// crash state that a re-run or --rollback must resolve. Unexported: only
	// in-package tests can set it, so it is not part of the public API.
	crashHook func(point string) error
}

// Crash-injection points of the migration switch (step 4), for tests only.
// They fire after each rename / guard write, in the switch's execution order.
const (
	migrateAfterPassagesRename = "after_passages_rename"
	migrateAfterManifestRename = "after_manifest_rename"
	migrateAfterPassagesGuard  = "after_passages_guard"
	migrateAfterManifestGuard  = "after_manifest_guard"
	migrateAfterDBRename       = "after_db_rename"
)

// MigrateIndex converts the format-1 index at indexDir/name to format 2. It
// takes the exclusive index lock, copies every passage into <name>.db.tmp with
// id = vec_slot = the old passage ID (so the vector file stays valid), verifies
// the copy, then switches with all handles closed: the old files are renamed
// to .v1 and guarded FIRST, and the database rename plus the meta flag are the
// last actions, so every interrupted state is resolved by a re-run or
// --rollback. The vector file is never modified.
func MigrateIndex(indexDir, name string, opts MigrateOptions) error {
	if err := checkPathName(name); err != nil {
		return err
	}
	basePath := filepath.Join(indexDir, name, name)

	// Step 1: exclusive lock; fail fast if another process holds it.
	lock, err := store.AcquireLock(filepath.Join(indexDir, name+".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()

	// A live format-2 database means a previous attempt already switched, or
	// crashed between the database rename and the meta flag (the LAST two
	// actions of the switch): refuse the former, complete the latter, so a
	// re-run of migrate resolves every leftover state.
	if _, err := os.Stat(basePath + ".db"); err == nil {
		m, err := GetIndexMeta(indexDir, name)
		if err != nil {
			return fmt.Errorf("reading index meta: %w", err)
		}
		if m.StoreFormat == 2 {
			return fmt.Errorf("index %q is already store format 2", name)
		}
		if err := UpdateIndexMeta(indexDir, name, func(m *IndexMeta) { m.StoreFormat = 2 }); err != nil {
			return fmt.Errorf("recording store format: %w", err)
		}
		return nil
	}
	if StoreFormatOf(basePath) == 2 {
		// meta.json says format 2 but the database is gone: a crash state
		// (an interrupted rollback), not an index to migrate. Never create an
		// empty store over it.
		return fmt.Errorf("index %q is store format 2 but %s.db is missing (a crash may have interrupted a rollback); "+
			"restore the format-1 files with: gleann index migrate --rollback %s, or repair the index: gleann index repair %s",
			name, name, name, name)
	}
	// Undo the leftovers of an interrupted switch (partial .v1 renames, guard
	// files, a stale staging database) so this run starts from a clean
	// format-1 index.
	if err := undoMigrationSwitch(basePath); err != nil {
		return err
	}
	if _, err := os.Stat(basePath + ".passages.db"); err != nil {
		return fmt.Errorf("index %q has no format-1 passage store to migrate", name)
	}
	meta, err := GetIndexMeta(indexDir, name)
	if err != nil {
		return fmt.Errorf("reading index meta: %w", err)
	}

	// Read the old store read-only and collect its passages in ID order.
	pm := NewReadOnlyPassageManager(basePath)
	if err := pm.Load(); err != nil {
		return fmt.Errorf("opening format-1 store: %w", err)
	}
	var passages []Passage
	err = pm.ForEachPassage(func(p Passage) error {
		passages = append(passages, p)
		return nil
	})
	pm.Close()
	if err != nil {
		return fmt.Errorf("reading format-1 passages: %w", err)
	}
	n := len(passages)
	if n == 0 {
		return fmt.Errorf("index %q has no passages to migrate", name)
	}
	// A format-1 index with non-dense IDs cannot keep its vector file valid
	// (vec_slot = old ID would leave gaps); refuse and point at repair.
	for i, p := range passages {
		if p.ID != int64(i) {
			return fmt.Errorf("index %q has non-contiguous passage IDs (ID %d at position %d); "+
				"repair it before migrating: gleann index repair %s", name, p.ID, i, name)
		}
	}
	if meta.NumPassages != n {
		return fmt.Errorf("index %q holds %d passages but meta records %d; repair it before migrating", name, n, meta.NumPassages)
	}

	language := opts.Language
	if language == "" {
		language = guessLanguage(passages)
	}
	analyzer := analysis.New(language)

	// Group by source, ordering each source's passages by chunk_index so
	// chunk_no reproduces the original chunk order.
	groups := make(map[string][]Passage)
	order := []string{}
	for _, p := range passages {
		src, _ := p.Metadata["source"].(string)
		if _, ok := groups[src]; !ok {
			order = append(order, src)
		}
		groups[src] = append(groups[src], p)
	}
	for _, src := range order {
		sort.SliceStable(groups[src], func(i, j int) bool {
			return chunkIndexOf(groups[src][i]) < chunkIndexOf(groups[src][j])
		})
	}

	oldManifest, _ := LoadManifest(basePath + ".manifest.json")

	// Step 2: copy into <name>.db.tmp, one transaction per source, with
	// id = vec_slot = old ID and the files rows from the old manifest.
	ctx := context.Background()
	tmpPath := basePath + ".db.tmp"
	removeLexicalFiles(tmpPath)
	st, err := store.Open(tmpPath, store.Options{})
	if err != nil {
		return fmt.Errorf("creating format-2 store: %w", err)
	}
	fail := func(err error) error {
		st.Close()
		removeLexicalFiles(tmpPath)
		return err
	}
	for _, src := range order {
		delta := store.SourceDelta{Source: src, File: migrateFileRow(src, oldManifest, meta.SourceDir)}
		for _, p := range groups[src] {
			id := p.ID
			kind := sidecarKind(src)
			_, metaJSON := splitItemMeta(p.Metadata)
			delta.Passages = append(delta.Passages, store.NewPassage{
				ID:      &id, // internal id = old passage ID
				VecSlot: &id, // vec_slot = old ID: the vector file stays valid
				Text:    p.Text,
				Kind:    kind,
				Meta:    metaJSON,
				Tokens:  strings.Join(analyzer.Tokens(p.Text, analysis.Kind(kind)), " "),
			})
		}
		if _, err := st.ApplySource(ctx, delta); err != nil {
			return fail(fmt.Errorf("migrating source %q: %w", src, err))
		}
	}
	// Manifest entries without passages still get a files row.
	if oldManifest != nil {
		for path, e := range oldManifest.Entries {
			if _, ok := groups[path]; ok {
				continue
			}
			fr := store.FileRow{Path: e.Path, Size: e.Size, MtimeNS: e.ModTime, Hash: e.Hash}
			if _, err := st.ApplySource(ctx, store.SourceDelta{Source: path, File: fr}); err != nil {
				return fail(fmt.Errorf("migrating file row %q: %w", path, err))
			}
		}
	}
	for _, kv := range [][2]string{{"analyzer", analysis.ID}, {"language", analyzer.Language}} {
		if err := st.SetMeta(ctx, kv[0], kv[1]); err != nil {
			return fail(fmt.Errorf("setting store meta %q: %w", kv[0], err))
		}
	}

	// Step 3: verify the copy before switching. No embedding happens.
	if err := verifyMigration(ctx, st, analyzer, passages, meta, indexDir, basePath, n); err != nil {
		return fail(err)
	}
	// Close every handle so the renames below are safe on Windows.
	if err := st.Close(); err != nil {
		removeLexicalFiles(tmpPath)
		return fmt.Errorf("closing format-2 store: %w", err)
	}

	// Step 4: switch, with every database handle closed (Windows cannot rename
	// an open file). The database rename and the meta flag are the LAST
	// actions: every earlier crash state still has the format-1 originals (or
	// their .v1 copies) and no live format-2 database, so a migrate re-run or
	// --rollback resolves it. The guards go in before the switch so an old
	// binary's bbolt.Open fails loudly at every point after the old files are
	// renamed aside. On error, the completed steps are undone in reverse
	// order, restoring the byte-identical format-1 files.
	var undo []func()
	failSwitch := func(err error) error {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		// The staging database is a derived intermediate: drop it too, so a
		// failed switch leaves a pristine format-1 index (a panic skips this
		// cleanup; a re-run or rollback discards the stale tmp instead).
		removeLexicalFiles(tmpPath)
		return err
	}
	hook := func(point string) error {
		if opts.crashHook == nil {
			return nil
		}
		return opts.crashHook(point)
	}
	if err := os.Rename(basePath+".passages.db", basePath+".passages.db.v1"); err != nil {
		return fmt.Errorf("preserving old passage store: %w", err)
	}
	undo = append(undo, func() { _ = os.Rename(basePath+".passages.db.v1", basePath+".passages.db") })
	if err := hook(migrateAfterPassagesRename); err != nil {
		return failSwitch(err)
	}
	if _, err := os.Stat(basePath + ".manifest.json"); err == nil {
		if err := os.Rename(basePath+".manifest.json", basePath+".manifest.json.v1"); err != nil {
			return failSwitch(fmt.Errorf("preserving old manifest: %w", err))
		}
		undo = append(undo, func() { _ = os.Rename(basePath+".manifest.json.v1", basePath+".manifest.json") })
		if err := hook(migrateAfterManifestRename); err != nil {
			return failSwitch(err)
		}
	}
	guard := []byte(storeGuardText())
	if err := os.WriteFile(basePath+".passages.db", guard, 0o644); err != nil {
		return failSwitch(fmt.Errorf("writing passage guard: %w", err))
	}
	undo = append(undo, func() { _ = os.Remove(basePath + ".passages.db") })
	if err := hook(migrateAfterPassagesGuard); err != nil {
		return failSwitch(err)
	}
	if err := os.WriteFile(basePath+".manifest.json", guard, 0o644); err != nil {
		return failSwitch(fmt.Errorf("writing manifest guard: %w", err))
	}
	undo = append(undo, func() { _ = os.Remove(basePath + ".manifest.json") })
	if err := hook(migrateAfterManifestGuard); err != nil {
		return failSwitch(err)
	}
	if err := os.Rename(tmpPath, basePath+".db"); err != nil {
		return failSwitch(fmt.Errorf("switching to format 2: %w", err))
	}
	undo = append(undo, func() {
		_ = os.Rename(basePath+".db", tmpPath)
		removeLexicalFiles(basePath + ".db") // WAL companions, if any remain
	})
	if err := hook(migrateAfterDBRename); err != nil {
		return failSwitch(err)
	}
	if err := UpdateIndexMeta(indexDir, name, func(m *IndexMeta) { m.StoreFormat = 2 }); err != nil {
		return failSwitch(fmt.Errorf("recording store format: %w", err))
	}
	// Step 5: the vector file (.index) is untouched.
	return nil
}

// undoMigrationSwitch restores the format-1 files after an interrupted
// switch: it removes the guard files (never a real store), renames the
// preserved .v1 originals back and deletes a stale staging database. It is
// the reverse of step 4's order, shared by a migrate re-run and rollback.
func undoMigrationSwitch(basePath string) error {
	for _, suffix := range []string{".passages.db", ".manifest.json"} {
		if isGuardFile(basePath + suffix) {
			if err := os.Remove(basePath + suffix); err != nil {
				return fmt.Errorf("removing guard %s: %w", suffix, err)
			}
		}
	}
	if _, err := os.Stat(basePath + ".passages.db.v1"); err == nil {
		if err := os.Rename(basePath+".passages.db.v1", basePath+".passages.db"); err != nil {
			return fmt.Errorf("restoring old passage store: %w", err)
		}
	}
	if _, err := os.Stat(basePath + ".manifest.json.v1"); err == nil {
		if err := os.Rename(basePath+".manifest.json.v1", basePath+".manifest.json"); err != nil {
			return fmt.Errorf("restoring old manifest: %w", err)
		}
	}
	removeLexicalFiles(basePath + ".db.tmp")
	return nil
}

// migrateFileRow builds the files row for a source from the old manifest when
// it has an entry, else from the file on disk (same hash as the manifest).
func migrateFileRow(source string, m *Manifest, sourceDir string) store.FileRow {
	if m != nil {
		if e, ok := m.Entries[source]; ok {
			return store.FileRow{Path: e.Path, Size: e.Size, MtimeNS: e.ModTime, Hash: e.Hash}
		}
	}
	return fileRowFor(source, sourceDir)
}

// chunkIndexOf reads a passage's chunk_index metadata (JSON numbers arrive as
// float64); missing defaults to 0, and a stable sort then keeps ID order.
func chunkIndexOf(p Passage) int {
	switch v := p.Metadata["chunk_index"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// verifyMigration checks the copied store: the passage count matches, every
// vec_slot is within the vector file, and sampled passages are their own
// lexical top-10 hit (RFC 003 section 6.4 step 3).
func verifyMigration(ctx context.Context, st *store.Store, analyzer analysis.Analyzer, passages []Passage, meta *IndexMeta, indexDir, basePath string, n int) error {
	cnt, err := st.Count(ctx)
	if err != nil {
		return fmt.Errorf("verify count: %w", err)
	}
	if cnt != n {
		return fmt.Errorf("verify: copied %d passages, expected %d", cnt, n)
	}
	// vec_slot range: the old IDs are dense 0..n-1 and a dense format-1 index
	// stores exactly n vectors (passage ID == vector position), so the slots
	// are in range once the vector file loads. A lexical-only index has none.
	if err := verifyVectorFile(ctx, indexDir, basePath, meta); err != nil {
		return err
	}
	// 20 sampled passages must retrieve themselves in the top 10.
	sample := samplePassages(passages, 20)
	for _, p := range sample {
		hits, err := st.Search(ctx, store.LexQuery{Text: p.Text, Analyzer: analyzer}, 10)
		if err != nil {
			return fmt.Errorf("verify lexical search: %w", err)
		}
		found := false
		for _, h := range hits {
			if h.ID == p.ID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("verify: passage %d is not its own top-10 lexical hit", p.ID)
		}
	}
	return nil
}

// verifyVectorFile loads the vector backend to confirm the file the migrated
// vec_slots address is present and well-formed. A lexical-only index (no
// .index) has nothing to check.
func verifyVectorFile(ctx context.Context, indexDir, basePath string, meta *IndexMeta) error {
	indexPath := basePath + ".index"
	if _, err := os.Stat(indexPath); err != nil {
		return nil // no vectors
	}
	factory, err := GetBackend(meta.Backend)
	if err != nil {
		return fmt.Errorf("verify vectors: getting backend: %w", err)
	}
	bs := factory.NewSearcher(Config{IndexDir: indexDir, Backend: meta.Backend})
	defer bs.Close()
	if mmap, ok := bs.(MmapBackendSearcher); ok {
		if err := mmap.LoadFromFile(ctx, indexPath); err != nil {
			return fmt.Errorf("verify vectors: loading %s: %w", indexPath, err)
		}
		return nil
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("verify vectors: reading %s: %w", indexPath, err)
	}
	if err := bs.Load(ctx, data, *meta); err != nil {
		return fmt.Errorf("verify vectors: loading %s: %w", indexPath, err)
	}
	return nil
}

// samplePassages picks up to k passages spread evenly across the corpus,
// skipping ones that tokenize to nothing (they cannot be a lexical self-hit).
func samplePassages(passages []Passage, k int) []Passage {
	var usable []Passage
	an := analysis.New("")
	for _, p := range passages {
		src, _ := p.Metadata["source"].(string)
		if len(an.Tokens(p.Text, analysis.Kind(sidecarKind(src)))) > 0 {
			usable = append(usable, p)
		}
	}
	if len(usable) <= k {
		return usable
	}
	out := make([]Passage, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, usable[i*len(usable)/k])
	}
	return out
}

// guessLanguage infers the analyzer language from the corpus: a substantial
// share of prose passages containing Turkish-specific letters means Turkish,
// otherwise the default (RFC 003 section 6.4 step 2).
func guessLanguage(passages []Passage) string {
	prose, tr := 0, 0
	for _, p := range passages {
		src, _ := p.Metadata["source"].(string)
		if sidecarKind(src) != "prose" {
			continue
		}
		prose++
		if strings.ContainsAny(p.Text, "ıİşğŞĞ") {
			tr++
		}
	}
	if prose > 0 && tr*5 >= prose { // >= 20% of prose passages
		return "tr"
	}
	return ""
}

// RollbackMigration restores a migrated index to format 1: the format flag in
// meta.json is cleared FIRST, then the format-2 store is deleted, then the
// guard files are replaced by the preserved .v1 originals (byte-identical).
// That order matters: deleting the database before clearing the flag left a
// crash window where meta.json said format 2 with no database, and the next
// open created an EMPTY format-2 store. With the flag cleared first, every
// later crash state is re-runnable: the remaining steps are idempotent, and
// an interrupted switch's leftovers (.v1 files, guards, a stale .db.tmp) are
// resolved too. Changes synced after the switch are lost (RFC 003 section
// 6.4). It takes the same exclusive lock.
func RollbackMigration(indexDir, name string) error {
	if err := checkPathName(name); err != nil {
		return err
	}
	basePath := filepath.Join(indexDir, name, name)
	lock, err := store.AcquireLock(filepath.Join(indexDir, name+".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()

	_, v1PassagesErr := os.Stat(basePath + ".passages.db.v1")
	_, v1ManifestErr := os.Stat(basePath + ".manifest.json.v1")
	guarded := isGuardFile(basePath+".passages.db") || isGuardFile(basePath+".manifest.json")
	if StoreFormatOf(basePath) != 2 && v1PassagesErr != nil && v1ManifestErr != nil && !guarded {
		return fmt.Errorf("index %q is not store format 2; nothing to roll back", name)
	}
	if v1PassagesErr != nil {
		// Nothing preserved to restore. A real format-1 store already in place
		// means a previous rollback restored it and only the flag, the
		// database or the manifest guard may remain; otherwise refuse.
		if _, err := os.Stat(basePath + ".passages.db"); err != nil || isGuardFile(basePath+".passages.db") {
			return fmt.Errorf("index %q has no preserved format-1 store to roll back to", name)
		}
	}
	// Clear the format flag first: a crash after this point leaves meta.json
	// saying format 1, which the opener reads as format 1 (or refuses loudly
	// on a guard), never as an empty format-2 store.
	if err := UpdateIndexMeta(indexDir, name, func(m *IndexMeta) { m.StoreFormat = 0 }); err != nil {
		return fmt.Errorf("clearing store format: %w", err)
	}
	removeLexicalFiles(basePath + ".db")
	// Replace the guards with the originals. Rename over the guard works on
	// unix and Windows (Go uses MOVEFILE_REPLACE_EXISTING).
	return undoMigrationSwitch(basePath)
}

// CleanupMigration removes the preserved .v1 files of a migrated index, making
// the switch permanent (rollback is no longer possible). It takes the lock.
func CleanupMigration(indexDir, name string) error {
	if err := checkPathName(name); err != nil {
		return err
	}
	basePath := filepath.Join(indexDir, name, name)
	lock, err := store.AcquireLock(filepath.Join(indexDir, name+".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	if StoreFormatOf(basePath) != 2 {
		return fmt.Errorf("index %q is not store format 2; nothing to clean up", name)
	}
	for _, suffix := range []string{".passages.db.v1", ".manifest.json.v1"} {
		if err := os.Remove(basePath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", suffix, err)
		}
	}
	return nil
}
