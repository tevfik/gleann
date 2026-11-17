package gleann

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// This file is the format-2 write path (RFC 003 section 8 step 4): one
// ApplySource transaction per source file for a fresh build, the files table
// as the manifest, and a crash-safe vector publication for updates: ALL
// database changes of one update commit in ONE transaction together with the
// pending-publication keys, then the staged vector file is renamed into
// place, then the generation is finished (recoverVectorPublication resolves
// a crash at any boundary).

// Meta keys of the incremental vector scheme (RFC 005): vector_slots is the
// high-water mark of the vec_slot space (a slot is never reused), and
// vector_main_generation changes only when the main vector file is replaced,
// where vector_generation changes with every publication.
const (
	metaVectorSlots          = "vector_slots"
	metaVectorMainGeneration = "vector_main_generation"
)

// SetSourceDir records the documents directory the builder is indexing, so the
// files-table rows can carry the same content hash as the format-1 manifest
// (computed from the file on disk). The indexing pipeline sets it before
// building; without it the rows still record size/mtime when the source path
// resolves, and an empty hash otherwise.
func (b *LeannBuilder) SetSourceDir(dir string) { b.sourceDir = dir }

// buildFormat decides the on-disk format for a build of the index at basePath.
// An existing index keeps its format: a format-2 index is always written as
// format 2, and a format-1 index stays format 1 (writes do not implicitly
// migrate it — RFC 003 open question 9.1). Only a NEW index follows the
// GLEANN_STORE_FORMAT switch read at builder creation.
func (b *LeannBuilder) buildFormat(basePath string) int {
	switch DetectStoreFormat(basePath) {
	case StoreV2:
		return 2
	case StoreV1:
		// A real (non-guard) bbolt passage store means an existing format-1
		// index; leftovers without one are rebuilt per the switch.
		if _, err := os.Stat(basePath + ".passages.db"); err == nil && !isGuardFile(basePath+".passages.db") {
			return 1
		}
	}
	if b.storeFormat == 2 {
		return 2
	}
	return 1
}

// fileRowFor builds the files-table manifest row for a source, hashing the
// file on disk exactly like the format-1 manifest (HashFileContent). A source
// that cannot be resolved (e.g. a synthetic memory item) still gets a row with
// an empty hash so the files table lists every indexed source.
func fileRowFor(source, sourceDir string) store.FileRow {
	f := store.FileRow{Path: source}
	if source == "" {
		return f
	}
	abs := source
	if sourceDir != "" && !filepath.IsAbs(source) {
		abs = filepath.Join(sourceDir, source)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return f
	}
	f.Size = fi.Size()
	f.MtimeNS = fi.ModTime().UnixNano()
	if h, err := HashFileContent(abs); err == nil {
		f.Hash = h
	}
	return f
}

// sourceDeltas groups items by source (first-appearance order) into
// ApplySource deltas, chunk_no being the position within the source. slots,
// when non-nil, is aligned to items and gives each passage its vec_slot; nil
// leaves every passage slotless (the lexical-only staging of RFC 003 section
// 3.6). It returns the deltas and, per delta, the item indices it carries, so
// callers can map per-source results back to item order.
func sourceDeltas(an analysis.Analyzer, items []Item, slots []int64, sourceDir string) ([]store.SourceDelta, [][]int) {
	order := []string{}
	groups := make(map[string][]int)
	for i, it := range items {
		src, _ := splitItemMeta(it.Metadata)
		if _, ok := groups[src]; !ok {
			order = append(order, src)
		}
		groups[src] = append(groups[src], i)
	}
	deltas := make([]store.SourceDelta, 0, len(order))
	idxs := make([][]int, 0, len(order))
	for _, src := range order {
		delta := store.SourceDelta{Source: src, File: fileRowFor(src, sourceDir)}
		kind := sidecarKind(src)
		for _, i := range groups[src] {
			it := items[i]
			_, metaJSON := splitItemMeta(it.Metadata)
			np := store.NewPassage{
				Text:   it.Text,
				Kind:   kind,
				Meta:   metaJSON,
				Tokens: strings.Join(an.Tokens(it.Text, analysis.Kind(kind)), " "),
			}
			if slots != nil {
				s := slots[i]
				np.VecSlot = &s
			}
			delta.Passages = append(delta.Passages, np)
		}
		deltas = append(deltas, delta)
		idxs = append(idxs, groups[src])
	}
	return deltas, idxs
}

// itemSlots assigns the dense slots startSlot..startSlot+len(items)-1 in item
// order, aligned to items.
func itemSlots(items []Item, startSlot int64) []int64 {
	slots := make([]int64, len(items))
	for i := range items {
		slots[i] = startSlot + int64(i)
	}
	return slots
}

// applySourceGroupsTx writes items to the store, one ApplySource delta per
// source file, assigning dense vector slots startSlot..startSlot+len(items)-1
// in item order, and returns the slot of each item, aligned to items. It runs
// inside a caller-owned transaction: every source of the update commits together with the slot renumbering and
// the pending-publication keys, so a crash never leaves slots pointing into
// the wrong vector file (RFC 003 section 3.4).
func applySourceGroupsTx(ctx context.Context, tx *sql.Tx, st *store.Store, an analysis.Analyzer, items []Item, startSlot int64, sourceDir string) ([]int64, error) {
	slots := itemSlots(items, startSlot)
	deltas, _ := sourceDeltas(an, items, slots, sourceDir)
	for _, d := range deltas {
		if _, err := st.ApplySourceTx(ctx, tx, d); err != nil {
			return nil, fmt.Errorf("applying source %q: %w", d.Source, err)
		}
	}
	return slots, nil
}

// applyStagedSources writes the lexical-only staging of a build (BuildPassages
// / BuildLexicalOnly): one ApplySource transaction per source, every passage
// slotless (vec_slot NULL) because no vector file exists yet. It returns the
// INTERNAL IDs of the passages aligned to items, which BuildVectors uses to
// assign slot i to item i when it publishes the first vector file.
func applyStagedSources(ctx context.Context, st *store.Store, an analysis.Analyzer, items []Item, sourceDir string) ([]int64, error) {
	deltas, idxs := sourceDeltas(an, items, nil, sourceDir)
	ids := make([]int64, len(items))
	for di, d := range deltas {
		got, err := st.ApplySource(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("applying source %q: %w", d.Source, err)
		}
		for j, id := range got {
			ids[idxs[di][j]] = id
		}
	}
	return ids, nil
}

// buildPassagesFormat2 writes all items into the format-2 record store of the
// index, one transaction per source for a new index, ONE transaction for the
// whole replacement of an existing one. The passages are stored slotless
// (vec_slot NULL): this is the lexical-only staging, and no vector file exists
// yet — BuildVectors assigns the slots when it publishes the first vector
// file (RFC 003 section 3.6). It returns the internal passage IDs aligned to
// items.
//
// An existing <name>.db is NOT deleted: it also holds the code graph (symbols,
// edges, document nodes and the graph columns of the files table), which a
// lexical-only sync or an `index build` without --graph must not lose. The
// passages, the lexical rows and the manifest are replaced inside the
// database (see replacePassagesInPlace); a database that cannot be opened is
// recreated from scratch instead, and the graph is rebuilt by the caller
// (DatabaseRecreated). No file is deleted while a reader may hold it open,
// which Windows would refuse.
func (b *LeannBuilder) buildPassagesFormat2(ctx context.Context, basePath, name string, items []Item, prevMeta *IndexMeta) ([]int64, error) {
	b.dbRecreated = false
	sourceDir := b.sourceDir
	if sourceDir == "" && prevMeta != nil {
		sourceDir = prevMeta.SourceDir
	}
	analyzer := analysis.New("")

	if _, err := os.Stat(basePath + ".db"); err == nil {
		ids, err := b.replacePassagesInPlace(ctx, basePath, name, items, sourceDir, analyzer)
		if err == nil {
			return ids, nil
		}
		if !store.IsCorrupt(err) && !errors.Is(err, store.ErrUnsupportedFormat) {
			return nil, err
		}
		// The database is damaged or comes from an incompatible build: keeping
		// it is wrong. The passages are rebuilt from the sources anyway; the
		// graph it held is lost with it, so say so and let the caller rebuild it.
		log.Printf("⚠  The database of index %q cannot be reused (%v); recreating it. The code graph is rebuilt from the sources.", name, err)
		b.dbRecreated = true
		if err := removeDBFiles(basePath + ".db"); err != nil {
			return nil, fmt.Errorf("removing the old format-2 store (is another gleann process using index %q?): %w", name, err)
		}
	}

	st, err := store.Open(basePath+".db", store.Options{})
	if err != nil {
		return nil, fmt.Errorf("opening format-2 store: %w", err)
	}
	defer st.Close()

	ids, err := applyStagedSources(ctx, st, analyzer, items, sourceDir)
	if err != nil {
		return nil, err
	}
	for _, kv := range [][2]string{
		{"analyzer", analysis.ID},
		{"language", analyzer.Language},
	} {
		if err := st.SetMeta(ctx, kv[0], kv[1]); err != nil {
			return nil, fmt.Errorf("setting store meta %q: %w", kv[0], err)
		}
	}
	b.passagesGeneration = currentVectorGeneration(ctx, st)
	return ids, nil
}

// replacePassagesInPlace replaces the passages, the lexical rows and the
// manifest of an existing format-2 database in one transaction, keeping the
// graph (store.ReplacePassagesTx). A source file that disappeared since the
// last build loses its passages, its manifest row and its graph rows in the
// same commit; a source whose content changed and that has graph rows is
// marked graph_dirty for the next graph pass. The same transaction advances
// vector_generation: the slots all become NULL, so a searcher that still holds
// the previous vector file must reload (it sees the new generation).
//
// Crash safety: an interrupted vector publication of an earlier run is
// resolved first (recoverVectorPublication), so no pending key survives into
// the new state; the replacement itself is one commit, so a crash leaves the
// old index or the new one.
func (b *LeannBuilder) replacePassagesInPlace(ctx context.Context, basePath, name string, items []Item, sourceDir string, analyzer analysis.Analyzer) ([]int64, error) {
	st, err := store.Open(basePath+".db", store.Options{})
	if err != nil {
		if store.IsCorrupt(err) || errors.Is(err, store.ErrUnsupportedFormat) {
			return nil, err
		}
		return nil, fmt.Errorf("opening format-2 store (is another gleann process using index %q?): %w", name, err)
	}
	defer st.Close()
	if err := recoverVectorPublication(ctx, st, basePath); err != nil {
		return nil, fmt.Errorf("recovering interrupted vector publication: %w", err)
	}

	deltas, idxs := sourceDeltas(analyzer, items, nil, sourceDir)
	g := graphstore.New(st)
	hooks := store.RebuildHooks{
		Fate: func(src string) store.SourceFate { return sourceFate(src, sourceDir) },
		Retract: func(ctx context.Context, tx *sql.Tx, src string) error {
			if src == "" {
				return nil
			}
			return g.RetractSourceTx(ctx, tx, src)
		},
	}
	nextGen := currentVectorGeneration(ctx, st) + 1

	tx, err := st.BeginWrite(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning passage replacement: %w", err)
	}
	defer tx.Rollback()
	got, err := st.ReplacePassagesTx(ctx, tx, deltas, hooks)
	if err != nil {
		return nil, err
	}
	for _, kv := range [][2]string{
		{"analyzer", analysis.ID},
		{"language", analyzer.Language},
		{"vector_generation", strconv.Itoa(nextGen)},
		// Every slot is NULL now; the next vector publication assigns 0..n-1.
		{metaVectorSlots, "0"},
	} {
		if err := st.SetMetaTx(ctx, tx, kv[0], kv[1]); err != nil {
			return nil, fmt.Errorf("setting store meta %q: %w", kv[0], err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing passage replacement: %w", err)
	}
	b.passagesGeneration = nextGen

	ids := make([]int64, len(items))
	for di, idx := range idxs {
		for j, id := range got[di] {
			ids[idx[j]] = id
		}
	}
	return ids, nil
}

// sourceFate says whether a source that no longer has passages was deleted
// from the source tree (see store.SourceFate).
func sourceFate(source, sourceDir string) store.SourceFate {
	if source == "" {
		return store.SourceGone
	}
	abs := source
	if !filepath.IsAbs(abs) {
		if sourceDir == "" {
			return store.SourceUnknown
		}
		abs = filepath.Join(sourceDir, source)
	}
	_, err := os.Stat(abs)
	switch {
	case err == nil:
		return store.SourceKept
	case errors.Is(err, fs.ErrNotExist):
		return store.SourceGone
	default:
		return store.SourceUnknown
	}
}

// Crash-injection points of the vector publication protocol, for tests only
// (LeannBuilder.crashHook). A crash at each point must leave a state that
// recoverVectorPublication resolves on the next open, without vector hits
// ever addressing the wrong passage.
const (
	crashBeforeCommit = "before_commit" // staging file written, transaction not committed
	crashAfterCommit  = "after_commit"  // transaction committed, rename not done
	crashAfterRename  = "after_rename"  // rename done, generation not finished
	crashAfterFinish  = "after_finish"  // generation finished, meta.json not written

	// Points of a publication that stages more than one file (RFC 005 section 4).
	crashAfterRenameMain  = "after_rename_main"  // main file renamed, the other staged files not yet
	crashAfterMergeCommit = "after_merge_commit" // a publication that replaces the main file committed, nothing renamed
)

// crashPoint runs the builder's test crash hook, if set. It is a struct field,
// not a package global: production builders never carry one.
func (b *LeannBuilder) crashPoint(point string) error {
	if b.crashHook != nil {
		return b.crashHook(point)
	}
	return nil
}

// currentVectorGeneration reads the published generation from the store's
// meta table; an absent or unreadable key counts as 0 (never published).
func currentVectorGeneration(ctx context.Context, st *store.Store) int {
	if v, err := st.Meta(ctx, "vector_generation"); err == nil && v != "" {
		if g, e := strconv.Atoi(v); e == nil {
			return g
		}
	}
	return 0
}

// stagedVectorFile is one file of a vector publication: Suffix is the final
// name's extension after the index name (".index" for the main vector file,
// ".delta" for the delta file of RFC 005); the bytes are staged under the
// final name plus ".tmp".
type stagedVectorFile struct {
	Suffix string
	Data   []byte
}

// Names of the files a vector publication can stage.
const (
	suffixMain  = ".index"
	suffixDelta = ".delta"
)

// pendingSeparator joins the staged names of a publication in the
// pending_vector_file meta value. A single name (every publication before
// RFC 005) is the one-element list.
const pendingSeparator = "\n"

func joinPendingFiles(names []string) string { return strings.Join(names, pendingSeparator) }

func splitPendingFiles(v string) []string {
	var out []string
	for _, n := range strings.Split(v, pendingSeparator) {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// currentVectorSlots reads the high-water mark of the vec_slot space. An index
// without the key (built before RFC 005) derives it as the highest occupied
// slot plus one, which is what the key would have held.
func currentVectorSlots(ctx context.Context, st *store.Store) (int64, error) {
	v, err := st.Meta(ctx, metaVectorSlots)
	if err != nil {
		return 0, fmt.Errorf("reading vector slots: %w", err)
	}
	if v != "" {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil && n >= 0 {
			return n, nil
		}
	}
	_, maxSlot, _, err := st.SlotSpan(ctx)
	if err != nil {
		return 0, err
	}
	return maxSlot + 1, nil
}

// currentVectorMainGeneration reads the generation of the main vector file;
// an index without the key counts as its published generation.
func currentVectorMainGeneration(ctx context.Context, st *store.Store) int {
	if v, err := st.Meta(ctx, metaVectorMainGeneration); err == nil && v != "" {
		if g, e := strconv.Atoi(v); e == nil {
			return g
		}
	}
	return currentVectorGeneration(ctx, st)
}

// publishVectorFormat2 publishes files as the new vector file(s) of the
// format-2 index at basePath, in the crash-safe order of RFC 003 section 3.4
// (generalised to several files by RFC 005 section 4):
//
//  1. stage each file under its final name plus ".tmp";
//  2. run dbChanges — EVERY database change of this update — together with
//     the pending_vector_file (a list) / pending_vector_generation meta keys
//     in ONE transaction, and commit. Before the commit the database still
//     matches the old vector files; after it, the pending keys mark the
//     publication;
//  3. rename each staged file to its final name, main file first;
//  4. finish: set vector_generation to the pending value and delete both
//     pending keys in a second small transaction.
//
// A crash between 2 and 4 leaves the pending keys, which
// recoverVectorPublication rolls forward on the next open (it renames every
// staged file still present). A crash before the commit leaves only stale
// staging files, which recovery deletes. The caller writes meta.json
// (including VectorGeneration) after this returns.
//
// A publication that stages the main file also records vector_main_generation
// in the same transaction: readers re-map the main file only when it changes.
func (b *LeannBuilder) publishVectorFormat2(ctx context.Context, st *store.Store, basePath string, files []stagedVectorFile, nextGen int, dbChanges func(tx *sql.Tx) error) error {
	if len(files) == 0 {
		return fmt.Errorf("publishing vector files: nothing staged")
	}
	// The main file is renamed first: a mixed pair (new main, old delta) is
	// still correct for HNSW (stable slots) and is resolved by recovery.
	sort.SliceStable(files, func(i, j int) bool { return files[i].Suffix == suffixMain && files[j].Suffix != suffixMain })
	names := make([]string, len(files))
	hasMain := false
	for i, f := range files {
		tmp := basePath + f.Suffix + ".tmp"
		if err := os.WriteFile(tmp, f.Data, 0o644); err != nil {
			return fmt.Errorf("staging vector file %s: %w", filepath.Base(tmp), err)
		}
		names[i] = filepath.Base(tmp)
		hasMain = hasMain || f.Suffix == suffixMain
	}

	tx, err := st.BeginWrite(ctx)
	if err != nil {
		return fmt.Errorf("beginning vector publication: %w", err)
	}
	defer tx.Rollback()
	if err := dbChanges(tx); err != nil {
		return err
	}
	if err := st.SetMetaTx(ctx, tx, "pending_vector_file", joinPendingFiles(names)); err != nil {
		return fmt.Errorf("recording pending vector file: %w", err)
	}
	if err := st.SetMetaTx(ctx, tx, "pending_vector_generation", strconv.Itoa(nextGen)); err != nil {
		return fmt.Errorf("recording pending vector generation: %w", err)
	}
	if hasMain {
		if err := st.SetMetaTx(ctx, tx, metaVectorMainGeneration, strconv.Itoa(nextGen)); err != nil {
			return fmt.Errorf("recording main vector generation: %w", err)
		}
	}
	if err := b.crashPoint(crashBeforeCommit); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing vector publication: %w", err)
	}

	if err := b.crashPoint(crashAfterCommit); err != nil {
		return err
	}
	if hasMain {
		if err := b.crashPoint(crashAfterMergeCommit); err != nil {
			return err
		}
	}
	for i, f := range files {
		final := basePath + f.Suffix
		if err := os.Rename(final+".tmp", final); err != nil {
			// A reader that opened the index between the commit and this rename
			// runs recoverVectorPublication, which rolls the publication forward
			// itself: the staging file is gone because it was renamed already.
			// The finish step below is idempotent, so carry on.
			if !errors.Is(err, fs.ErrNotExist) {
				// Keep the staging files: the pending keys are committed, so
				// recovery needs them to roll the publication forward.
				return fmt.Errorf("publishing vector file %s: %w", filepath.Base(final), err)
			}
			if _, statErr := os.Stat(final); statErr != nil {
				return fmt.Errorf("publishing vector file %s: %w", filepath.Base(final), err)
			}
		}
		if i == 0 && len(files) > 1 && f.Suffix == suffixMain {
			if err := b.crashPoint(crashAfterRenameMain); err != nil {
				return err
			}
		}
	}

	if err := b.crashPoint(crashAfterRename); err != nil {
		return err
	}
	if err := finishVectorPublication(ctx, st, nextGen); err != nil {
		return err
	}
	return b.crashPoint(crashAfterFinish)
}

// finishVectorPublication is step 4 of the publication: record the new
// generation and delete the pending keys in one transaction, so the database
// never shows a finished generation alongside pending keys. Recovery calls
// the same function to complete an interrupted publication.
func finishVectorPublication(ctx context.Context, st *store.Store, gen int) error {
	tx, err := st.BeginWrite(ctx)
	if err != nil {
		return fmt.Errorf("beginning vector generation write: %w", err)
	}
	defer tx.Rollback()
	if err := st.SetMetaTx(ctx, tx, "vector_generation", strconv.Itoa(gen)); err != nil {
		return fmt.Errorf("setting vector generation: %w", err)
	}
	if err := st.DeleteMetaTx(ctx, tx, "pending_vector_file"); err != nil {
		return fmt.Errorf("clearing pending vector file: %w", err)
	}
	if err := st.DeleteMetaTx(ctx, tx, "pending_vector_generation"); err != nil {
		return fmt.Errorf("clearing pending vector generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing vector generation: %w", err)
	}
	return nil
}

// removeStaleVectorStaging deletes the staging files of a publication that
// never committed (.index.tmp, .delta.tmp).
func removeStaleVectorStaging(basePath string) error {
	for _, suffix := range []string{suffixMain, suffixDelta} {
		tmp := basePath + suffix + ".tmp"
		if _, err := os.Stat(tmp); err == nil {
			if err := os.Remove(tmp); err != nil {
				return fmt.Errorf("removing stale vector staging file: %w", err)
			}
		}
	}
	return nil
}

// recoverVectorPublication resolves an interrupted vector publication (RFC
// 003 section 3.4, RFC 005 section 4). It runs when a format-2 index is opened
// for search or write, before any reader trusts the slots:
//
//   - pending_vector_file set (a list of staged names): every staged file
//     still present is renamed to its name without ".tmp" — the crash landed
//     after the database commit — and the generation is finished; a staged
//     file already gone was renamed before the crash;
//   - no pending key but stale staging files: the crash landed before the
//     commit, so the database still matches the old vector files — delete them;
//   - no pending key but meta.json behind the database's vector_generation:
//     the crash landed after the finish step but before the builder wrote
//     meta.json — sync meta.json, else a fresh build would stay marked
//     lexical-only and Load would drop the vector leg.
//
// Finishing also syncs the meta.json fields the builder writes after a
// publication (all derivable from the committed database), so index doctor
// reports the index healthy after recovery. A read-only open may write here:
// it only completes a publication the builder already committed, never new
// data.
func recoverVectorPublication(ctx context.Context, st *store.Store, basePath string) error {
	pendingFile, err := st.Meta(ctx, "pending_vector_file")
	if err != nil {
		return fmt.Errorf("reading pending vector file: %w", err)
	}
	if pendingFile == "" {
		if err := removeStaleVectorStaging(basePath); err != nil {
			return err
		}
		if gen := currentVectorGeneration(ctx, st); gen > 0 {
			name := filepath.Base(basePath)
			indexDir := filepath.Dir(filepath.Dir(basePath))
			if m, err := GetIndexMeta(indexDir, name); err == nil && m.VectorGeneration < gen {
				return syncPublishedMeta(ctx, st, basePath, gen)
			}
		}
		return nil
	}
	pendingGen, err := st.Meta(ctx, "pending_vector_generation")
	if err != nil {
		return fmt.Errorf("reading pending vector generation: %w", err)
	}
	gen, err := strconv.Atoi(pendingGen)
	if err != nil {
		return fmt.Errorf("interrupted vector publication records generation %q: %w; repair the index: gleann index repair %s",
			pendingGen, err, filepath.Base(basePath))
	}
	// Staged names are file names next to the database, never paths: a
	// tampered value must not rename anything outside the index directory.
	dir := filepath.Dir(basePath)
	for _, n := range splitPendingFiles(pendingFile) {
		if n != filepath.Base(n) || !strings.HasSuffix(n, ".tmp") {
			return fmt.Errorf("interrupted vector publication records staged file %q; repair the index: gleann index repair %s",
				n, filepath.Base(basePath))
		}
		tmpPath := filepath.Join(dir, n)
		if _, err := os.Stat(tmpPath); err == nil {
			if err := os.Rename(tmpPath, strings.TrimSuffix(tmpPath, ".tmp")); err != nil {
				return fmt.Errorf("rolling forward vector publication: %w", err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("checking staged vector file: %w", err)
		}
	}
	if err := finishVectorPublication(ctx, st, gen); err != nil {
		return err
	}
	return syncPublishedMeta(ctx, st, basePath, gen)
}

// syncPublishedMeta writes the meta.json fields a publication's builder would
// have written after the finish step, deriving them from the committed
// database. It is what makes a recovered index pass index doctor without a
// repair; a missing meta.json (a half-removed index) is left alone.
func syncPublishedMeta(ctx context.Context, st *store.Store, basePath string, gen int) error {
	name := filepath.Base(basePath)
	indexDir := filepath.Dir(filepath.Dir(basePath))
	if _, err := GetIndexMeta(indexDir, name); err != nil {
		return nil
	}
	count, err := st.Count(ctx)
	if err != nil {
		return fmt.Errorf("counting passages after recovery: %w", err)
	}
	// A passage without a slot (a rebuild replaced the passages and no vector
	// file was published for them yet) keeps the vector leg off.
	_, statErr := os.Stat(basePath + ".index")
	_, _, slotless, err := st.SlotSpan(ctx)
	if err != nil {
		return fmt.Errorf("reading slot span after recovery: %w", err)
	}
	vectorReady := statErr == nil && count > 0 && slotless == 0
	slots, err := currentVectorSlots(ctx, st)
	if err != nil {
		return fmt.Errorf("reading vector slots after recovery: %w", err)
	}
	mainGen := currentVectorMainGeneration(ctx, st)
	if err := UpdateIndexMeta(indexDir, name, func(m *IndexMeta) {
		m.NumPassages = count
		m.VectorGeneration = gen
		m.VectorSlots = slots
		m.VectorMainGeneration = mainGen
		m.StoreFormat = 2
		m.VectorReady = vectorReady
		m.LexicalOnly = !vectorReady
	}); err != nil {
		return fmt.Errorf("syncing meta.json after recovery: %w", err)
	}
	return nil
}

// updateIndexFormat2 applies an update to a format-2 index. There is no
// in-place append: the vector file is rebuilt from the remaining passages plus
// newItems and republished in the RFC 3.4 crash-safe order (one transaction
// for every database change plus the pending keys, then the rename, then the
// generation finish). Passages of unchanged sources keep their internal ID
// and stable PID (only their vec_slot is renumbered); changed sources are
// rewritten with one ApplySourceTx each inside the publication transaction.
// force skips the noop short-circuit so Compact/repair always renumbers
// densely.
func (b *LeannBuilder) updateIndexFormat2(ctx context.Context, name, basePath string, newItems []Item, removeSources []string, st *UpdateStats, force bool) error {
	storeDB, err := openFormat2Store(basePath)
	if err != nil {
		return err
	}
	defer storeDB.Close()
	// Resolve an interrupted publication of a previous run before reading the
	// passages, so this update starts from a published, consistent state.
	if err := recoverVectorPublication(ctx, storeDB, basePath); err != nil {
		return fmt.Errorf("recovering interrupted vector publication: %w", err)
	}

	analyzer := analysis.New("")
	if lang, err := storeDB.Meta(ctx, "language"); err == nil {
		analyzer = analysis.New(lang)
	}

	// The incremental branch (RFC 005): embed only the changed chunks and
	// publish the delta, leaving the main vector file and every unchanged
	// slot alone. A forced rebuild (repair, compaction), a missing main file or
	// a crossed limit takes the full path below.
	if !force {
		done, err := b.updateIncremental(ctx, name, basePath, st, incrementalUpdate{
			store: storeDB, analyzer: analyzer, newItems: newItems, removeSources: removeSources,
		})
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}

	var all []store.Passage
	if err := storeDB.ForEach(ctx, func(p store.Passage) error {
		all = append(all, p)
		return nil
	}); err != nil {
		return fmt.Errorf("reading passages: %w", err)
	}
	// Order by current vector slot so unchanged passages keep their relative
	// order (nil slots sort first).
	slotOf := func(p store.Passage) int64 {
		if p.VecSlot != nil {
			return *p.VecSlot
		}
		return -1
	}
	sort.Slice(all, func(i, j int) bool { return slotOf(all[i]) < slotOf(all[j]) })

	drop := make(map[string]bool, len(removeSources))
	for _, s := range removeSources {
		drop[s] = true
	}
	newBySource := make(map[string]bool)
	for _, it := range newItems {
		src, _ := splitItemMeta(it.Metadata)
		newBySource[src] = true
	}

	var unchanged []store.Passage
	changedSources := make(map[string]bool)
	removedCount := 0
	for _, p := range all {
		switch {
		case drop[p.Source]:
			removedCount++
		case newBySource[p.Source]:
			changedSources[p.Source] = true
		default:
			unchanged = append(unchanged, p)
		}
	}
	st.Removed = removedCount
	k := int64(len(unchanged))
	n := k + int64(len(newItems))

	if n == 0 {
		return b.emptyFormat2(ctx, name, basePath, storeDB, st)
	}
	if !force && len(newItems) == 0 && removedCount == 0 {
		st.Mode, st.Passages = UpdateNoop, len(all)
		return nil
	}

	// Final slot order: unchanged passages (old-slot order) then newItems.
	texts := make([]string, 0, n)
	for _, p := range unchanged {
		texts = append(texts, p.Text)
	}
	for _, it := range newItems {
		texts = append(texts, it.Text)
	}

	// Everything that can fail on external input happens before a file changes.
	if reporter, ok := b.embedder.(interface{ SetProgressCallback(func(int, int)) }); ok {
		reporter.SetProgressCallback(func(done, total int) {
			if b.progressCb != nil {
				b.progressCb("vector_index", done, total)
			}
		})
	}
	embedStart := time.Now()
	embeddings, err := b.embedder.Compute(ctx, withPrefix(recordedPassagePrefix(b.config.IndexDir, name), texts))
	st.Embedded, st.EmbedTime = len(texts), time.Since(embedStart)
	if err != nil {
		return fmt.Errorf("compute embeddings: %w", err)
	}
	if len(embeddings) != len(texts) {
		return fmt.Errorf("embedder returned %d vectors for %d texts", len(embeddings), len(texts))
	}
	dim := len(embeddings[0])
	if dim == 0 {
		return fmt.Errorf("embedder returned zero-dimensional vectors (model=%q)", b.embedder.ModelName())
	}
	if err := b.checkDimensions(name, dim); err != nil {
		return err
	}
	for i, v := range embeddings {
		if len(v) != dim {
			return fmt.Errorf("embedding row %d has %d dims, expected %d", i, len(v), dim)
		}
	}
	indexData, err := b.backend.Build(ctx, embeddings)
	if err != nil {
		return fmt.Errorf("build index: %w", err)
	}

	// RFC 3.4 publication: every database change of this update commits in
	// ONE transaction together with the pending-publication keys, then the
	// staged vector file is renamed into place, then the generation is
	// finished. A crash at any boundary is resolved by recoverVectorPublication
	// on the next open; slots never point into the wrong vector file.
	reassign := make(map[int64]int64, len(unchanged))
	slotlessSources := make(map[string]bool)
	for i, p := range unchanged {
		if p.VecSlot != nil {
			reassign[*p.VecSlot] = int64(i)
		} else {
			slotlessSources[p.Source] = true
		}
	}
	sourceDir := b.sourceDir
	if sourceDir == "" {
		if m, err := GetIndexMeta(b.config.IndexDir, name); err == nil {
			sourceDir = m.SourceDir
		}
	}
	nextGen := currentVectorGeneration(ctx, storeDB) + 1
	err = b.publishVectorFormat2(ctx, storeDB, basePath, []stagedVectorFile{{Suffix: suffixMain, Data: indexData}, b.emptyDeltaFile(dim)}, nextGen, func(tx *sql.Tx) error {
		// Remove deleted and changed sources first so the renumbering targets
		// are free, reassign the unchanged passages to 0..k-1, then write the
		// changed/new sources at k..n-1.
		for src := range drop {
			if _, err := storeDB.RemoveSourceTx(ctx, tx, src); err != nil {
				return fmt.Errorf("removing source %q: %w", src, err)
			}
		}
		for src := range changedSources {
			if _, err := storeDB.RemoveSourceTx(ctx, tx, src); err != nil {
				return fmt.Errorf("removing source %q: %w", src, err)
			}
		}
		if err := storeDB.ReassignSlotsTx(ctx, tx, reassign); err != nil {
			return fmt.Errorf("reassigning slots: %w", err)
		}
		// Passages without a slot are damage in a vector-ready index, and the
		// rebuilt vector file carries a vector for every stored passage: give
		// them their dense position too, or the repair leaves the index unhealthy.
		if len(slotlessSources) > 0 {
			if err := assignSlotlessSourcesTx(ctx, tx, storeDB, analyzer, unchanged, slotlessSources); err != nil {
				return err
			}
		}
		if _, err := applySourceGroupsTx(ctx, tx, storeDB, analyzer, newItems, k, sourceDir); err != nil {
			return err
		}
		// The renumbering is dense: the high-water mark restarts at n.
		return storeDB.SetMetaTx(ctx, tx, metaVectorSlots, strconv.FormatInt(n, 10))
	})
	if err != nil {
		return err
	}

	st.Mode, st.Added, st.Passages = UpdateCompact, len(newItems), int(n)
	return UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.NumPassages = int(n)
		m.Dimensions = dim
		m.VectorReady = true
		m.LexicalOnly = false
		// Keep the format field in step with the files, so a repair also
		// restores a meta.json that lost it (doctor cross-checks the two).
		m.StoreFormat = 2
		// Mirror the published generation; doctor cross-checks it against the
		// store's vector_generation (a crash between the finish and this write
		// leaves a mismatch that a repair resolves).
		m.VectorGeneration = nextGen
		m.VectorSlots = n
		m.VectorMainGeneration = nextGen
		m.UpdatedAt = time.Now()
	})
}

// assignSlotlessSourcesTx gives the passages without a vector slot their dense
// position in the rebuilt vector file, inside the publication transaction.
// ReassignSlotsTx is keyed by old slot, so it cannot address them; their
// sources are rewritten instead, one ApplySourceTx each, with every passage of
// the source in chunk order at its final slot and the files row preserved. The
// stable PID is a function of source, text and chunk order, so it survives the
// rewrite; the internal ID does not, which is acceptable for a source the
// damage already touched.
func assignSlotlessSourcesTx(ctx context.Context, tx *sql.Tx, st *store.Store, an analysis.Analyzer, unchanged []store.Passage, sources map[string]bool) error {
	// The files rows of these (unchanged) sources are not touched by the
	// publication transaction, so the reader's committed view is current.
	files, err := st.Files(ctx)
	if err != nil {
		return fmt.Errorf("listing files: %w", err)
	}
	newSlot := make(map[int64]int64, len(unchanged))
	for i, p := range unchanged {
		newSlot[p.ID] = int64(i)
	}
	for src := range sources {
		var ps []store.Passage
		for _, p := range unchanged {
			if p.Source == src {
				ps = append(ps, p)
			}
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i].ChunkNo < ps[j].ChunkNo })
		delta := store.SourceDelta{Source: src, File: files[src]}
		for _, p := range ps {
			s := newSlot[p.ID]
			delta.Passages = append(delta.Passages, store.NewPassage{
				Text:    p.Text,
				Kind:    p.Kind,
				Meta:    p.Meta,
				VecSlot: &s,
				Tokens:  strings.Join(an.Tokens(p.Text, analysis.Kind(p.Kind)), " "),
			})
		}
		if _, err := st.ApplySourceTx(ctx, tx, delta); err != nil {
			return fmt.Errorf("assigning slots to source %q: %w", src, err)
		}
	}
	return nil
}

// emptyFormat2 leaves a format-2 index with no passages: every source is
// removed (the empty database stays so readers can open it) and the vector
// file is deleted, since the backends cannot represent an empty index.
func (b *LeannBuilder) emptyFormat2(ctx context.Context, name, basePath string, storeDB *store.Store, st *UpdateStats) error {
	st.Mode, st.Passages = UpdateEmptied, 0
	files, err := storeDB.Files(ctx)
	if err != nil {
		return fmt.Errorf("listing files: %w", err)
	}
	for src := range files {
		if _, err := storeDB.RemoveSource(ctx, src); err != nil {
			return fmt.Errorf("removing source %q: %w", src, err)
		}
	}
	// Sourceless passages (empty source bucket) are not in the files table.
	if _, err := storeDB.RemoveSource(ctx, ""); err != nil {
		return fmt.Errorf("removing sourceless passages: %w", err)
	}
	for _, suffix := range []string{suffixMain, suffixDelta} {
		if err := os.Remove(basePath + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove vector file %s: %w", suffix, err)
		}
	}
	// Bump the generation so a searcher that still holds the removed vector
	// file sees the change on its next freshness check.
	nextGen := currentVectorGeneration(ctx, storeDB) + 1
	for _, kv := range [][2]string{
		{"vector_generation", strconv.Itoa(nextGen)},
		{metaVectorMainGeneration, strconv.Itoa(nextGen)},
		{metaVectorSlots, "0"},
	} {
		if err := storeDB.SetMeta(ctx, kv[0], kv[1]); err != nil {
			return fmt.Errorf("setting %s: %w", kv[0], err)
		}
	}
	return UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.VectorGeneration = nextGen
		m.VectorMainGeneration = nextGen
		m.VectorSlots = 0
		m.NumPassages = 0
		m.VectorReady = false
		m.LexicalOnly = true
		m.UpdatedAt = time.Now()
	})
}
