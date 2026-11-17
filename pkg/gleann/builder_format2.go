package gleann

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// This file is the format-2 write path (RFC 003 section 8 step 4): one
// ApplySource transaction per source file for a fresh build, the files table
// as the manifest, and a crash-safe vector publication for updates: ALL
// database changes of one update commit in ONE transaction together with the
// pending-publication keys, then the staged vector file is renamed into
// place, then the generation is finished (recoverVectorPublication resolves
// a crash at any boundary).

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
	if StoreFormatOf(basePath) == 2 {
		return 2
	}
	// A real (non-guard) bbolt passage store means an existing format-1 index.
	if _, err := os.Stat(basePath + ".passages.db"); err == nil && !isGuardFile(basePath+".passages.db") {
		return 1
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

// buildPassagesFormat2 creates a fresh format-2 record store and writes all
// items into it, one transaction per source. The passages are stored slotless
// (vec_slot NULL): this is the lexical-only staging, and no vector file exists
// yet — BuildVectors assigns the slots when it publishes the first vector
// file (RFC 003 section 3.6). The old database (and any WAL companions) is
// removed first so the build starts from a clean schema. It returns the
// internal passage IDs aligned to items.
func (b *LeannBuilder) buildPassagesFormat2(ctx context.Context, basePath, name string, items []Item, prevMeta *IndexMeta) ([]int64, error) {
	if err := removeDBFiles(basePath + ".db"); err != nil {
		return nil, fmt.Errorf("removing the old format-2 store (is another gleann process using index %q?): %w", name, err)
	}
	st, err := store.Open(basePath+".db", store.Options{})
	if err != nil {
		return nil, fmt.Errorf("opening format-2 store: %w", err)
	}
	defer st.Close()

	sourceDir := b.sourceDir
	if sourceDir == "" && prevMeta != nil {
		sourceDir = prevMeta.SourceDir
	}
	analyzer := analysis.New("")
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
	return ids, nil
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

// publishVectorFormat2 publishes indexData as the new vector file of the
// format-2 index at basePath, in the crash-safe order of RFC 003 section 3.4:
//
//  1. stage the bytes under <name>.index.tmp;
//  2. run dbChanges — EVERY database change of this update — together with
//     the pending_vector_file / pending_vector_generation meta keys in ONE
//     transaction, and commit. Before the commit the database still matches
//     the old vector file; after it, the pending keys mark the publication;
//  3. rename the staging file over <name>.index;
//  4. finish: set vector_generation to the pending value and delete both
//     pending keys in a second small transaction.
//
// A crash between 2 and 3, or between 3 and 4, leaves the pending keys, which
// recoverVectorPublication rolls forward on the next open. A crash before the
// commit leaves only a stale staging file, which recovery deletes. The caller
// writes meta.json (including VectorGeneration) after this returns.
func (b *LeannBuilder) publishVectorFormat2(ctx context.Context, st *store.Store, basePath string, indexData []byte, nextGen int, dbChanges func(tx *sql.Tx) error) error {
	tmpIndex := basePath + ".index.tmp"
	if err := os.WriteFile(tmpIndex, indexData, 0o644); err != nil {
		return fmt.Errorf("staging vector index: %w", err)
	}

	tx, err := st.BeginWrite(ctx)
	if err != nil {
		return fmt.Errorf("beginning vector publication: %w", err)
	}
	defer tx.Rollback()
	if err := dbChanges(tx); err != nil {
		return err
	}
	if err := st.SetMetaTx(ctx, tx, "pending_vector_file", filepath.Base(tmpIndex)); err != nil {
		return fmt.Errorf("recording pending vector file: %w", err)
	}
	if err := st.SetMetaTx(ctx, tx, "pending_vector_generation", strconv.Itoa(nextGen)); err != nil {
		return fmt.Errorf("recording pending vector generation: %w", err)
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
	if err := os.Rename(tmpIndex, basePath+".index"); err != nil {
		// Keep the staging file: the pending keys are committed, so recovery
		// needs it to roll the publication forward.
		return fmt.Errorf("publishing vector index: %w", err)
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

// recoverVectorPublication resolves an interrupted vector publication (RFC
// 003 section 3.4). It runs when a format-2 index is opened for search or
// write, before any reader trusts the slots:
//
//   - pending_vector_file set and the staging file present: the crash landed
//     after the database commit but before the rename — roll forward (rename
//     the file into place, then finish);
//   - pending_vector_file set and the staging file absent: the rename already
//     happened — finish;
//   - no pending key but a stale .index.tmp: the crash landed before the
//     commit, so the database still matches the old vector file — delete the
//     staging file;
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
		if _, err := os.Stat(basePath + ".index.tmp"); err == nil {
			if err := os.Remove(basePath + ".index.tmp"); err != nil {
				return fmt.Errorf("removing stale vector staging file: %w", err)
			}
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
	tmpPath := filepath.Join(filepath.Dir(basePath), pendingFile)
	if _, err := os.Stat(tmpPath); err == nil {
		if err := os.Rename(tmpPath, basePath+".index"); err != nil {
			return fmt.Errorf("rolling forward vector publication: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking staged vector file: %w", err)
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
	_, statErr := os.Stat(basePath + ".index")
	vectorReady := statErr == nil && count > 0
	if err := UpdateIndexMeta(indexDir, name, func(m *IndexMeta) {
		m.NumPassages = count
		m.VectorGeneration = gen
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
	err = b.publishVectorFormat2(ctx, storeDB, basePath, indexData, nextGen, func(tx *sql.Tx) error {
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
		return nil
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
	if err := os.Remove(basePath + ".index"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove vector index: %w", err)
	}
	return UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.NumPassages = 0
		m.VectorReady = false
		m.LexicalOnly = true
		m.UpdatedAt = time.Now()
	})
}
