package gleann

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"time"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// The incremental branch of a format-2 update (RFC 005 section 3): the
// passages of the changed sources get new slots from the high-water mark, only
// their texts are embedded, and the publication replaces the small delta file;
// the main vector file and the slots of every unchanged passage stay as they
// are. A source that changed or disappeared leaves dead slots behind.

// incrementalLimits bound the delta and the dead share before an update falls
// back to rebuilding the main vector file (RFC 005 section 3.4).
type incrementalLimits struct {
	deltaMin        int     // the delta may always hold this many entries
	deltaShare      float64 // ... or this share of the live passages, whichever is larger
	deadMin         int64   // below this many dead slots no compaction, whatever the share
	deadShare       float64 // dead share of the slot space that triggers a compaction
	deadSharePruned float64 // the same for a pruned index (dead nodes cost recall there)
}

func defaultIncrementalLimits() incrementalLimits {
	return incrementalLimits{deltaMin: 4096, deltaShare: 0.02, deadMin: 256, deadShare: 0.05, deadSharePruned: 0.05}
}

// incrementalLimitsFromEnv reads the overrides GLEANN_DELTA_MIN,
// GLEANN_DELTA_SHARE, GLEANN_DEAD_SHARE and GLEANN_DEAD_SHARE_PRUNED once, at
// builder creation. A value that does not parse is ignored.
func incrementalLimitsFromEnv() incrementalLimits {
	l := defaultIncrementalLimits()
	if v, err := strconv.Atoi(os.Getenv("GLEANN_DELTA_MIN")); err == nil && v >= 0 {
		l.deltaMin = v
	}
	for env, dst := range map[string]*float64{
		"GLEANN_DELTA_SHARE":       &l.deltaShare,
		"GLEANN_DEAD_SHARE":        &l.deadShare,
		"GLEANN_DEAD_SHARE_PRUNED": &l.deadSharePruned,
	} {
		if v, err := strconv.ParseFloat(os.Getenv(env), 64); err == nil && v >= 0 {
			*dst = v
		}
	}
	if v, err := strconv.ParseInt(os.Getenv("GLEANN_DEAD_MIN"), 10, 64); err == nil && v >= 0 {
		l.deadMin = v
	} else if os.Getenv("GLEANN_DEAD_SHARE") != "" || os.Getenv("GLEANN_DEAD_SHARE_PRUNED") != "" {
		l.deadMin = 0 // an explicit share is meant literally
	}
	return l
}

// limitsCrossed says whether the state after an update needs the main file
// rebuilt: the delta holds too many entries (merge) or too many slots are dead
// (compaction). live and dead are the counts after the update.
func (l incrementalLimits) crossed(deltaCount int, live, dead int64, pruned bool) (merge, compact bool) {
	merge = deltaCount > max(l.deltaMin, int(l.deltaShare*float64(live)))
	share := l.deadShare
	if pruned {
		share = l.deadSharePruned
	}
	if total := live + dead; total > 0 {
		compact = dead > l.deadMin && float64(dead)/float64(total) > share
	}
	return merge, compact
}

// vectorMetric is the distance metric of the configured backend.
func (c Config) vectorMetric() string {
	if c.Backend == "diskann" {
		return c.DiskANNConfig.DistanceMetric
	}
	return string(c.HNSWConfig.DistanceMetric)
}

// prunedIndex reports whether the configured backend drops stored embeddings.
func (c Config) prunedIndex() bool {
	return c.Backend == "hnsw" && c.HNSWConfig.PruneEmbeddings
}

// emptyDeltaFile is the delta a publication of a new main vector file stages
// next to it: the main file holds every vector, so no stale delta (whose slots
// were numbered for the old file) survives a rebuild.
func (b *LeannBuilder) emptyDeltaFile(dims int) stagedVectorFile {
	dtype := deltaDTypeF32
	if b.config.Backend == "hnsw" && b.config.HNSWConfig.VectorDType == deltaDTypeF16 {
		dtype = deltaDTypeF16
	}
	return stagedVectorFile{Suffix: suffixDelta, Data: newVectorDelta(dims, dtype, b.config.vectorMetric()).Marshal()}
}

// updateIncremental applies the update through the delta when it can. It
// returns done=false, with nothing changed, when the update needs the full
// path: a forced rebuild, a missing main file, a passage without a slot, an
// update that would empty the index, or one that crosses a limit.
func (b *LeannBuilder) updateIncremental(ctx context.Context, name, basePath string, st *UpdateStats, d incrementalUpdate) (done bool, err error) {
	storeDB := d.store
	meta, err := GetIndexMeta(b.config.IndexDir, name)
	if err != nil || meta.Dimensions == 0 || !meta.VectorReady || meta.LexicalOnly {
		return false, nil
	}
	if _, statErr := os.Stat(basePath + suffixMain); statErr != nil {
		return false, nil
	}
	count, _, slotless, err := storeDB.SlotSpan(ctx)
	if err != nil {
		return false, err
	}
	if slotless != 0 || count == 0 {
		return false, nil
	}

	drop := make(map[string]bool, len(d.removeSources))
	for _, s := range d.removeSources {
		drop[s] = true
	}
	var changed []string
	seenChanged := map[string]bool{}
	for _, it := range d.newItems {
		src, _ := splitItemMeta(it.Metadata)
		if !drop[src] && !seenChanged[src] {
			seenChanged[src] = true
			changed = append(changed, src)
		}
	}
	dropList := make([]string, 0, len(drop))
	for s := range drop {
		dropList = append(dropList, s)
	}
	dropSlots, dropPassages, err := storeDB.SourceSlots(ctx, dropList)
	if err != nil {
		return false, err
	}
	changedSlots, changedPassages, err := storeDB.SourceSlots(ctx, changed)
	if err != nil {
		return false, err
	}
	removed := make(map[int64]bool, len(dropSlots)+len(changedSlots))
	for _, s := range dropSlots {
		removed[s] = true
	}
	for _, s := range changedSlots {
		removed[s] = true
	}
	// A source that held passages without a slot is damage this path does not
	// repair.
	if dropPassages+changedPassages != len(removed) {
		return false, nil
	}
	st.Removed = dropPassages
	newN := len(d.newItems)
	if newN == 0 && dropPassages == 0 {
		st.Mode, st.Passages = UpdateNoop, count
		return true, nil
	}
	live := int64(count) - int64(len(removed)) + int64(newN)
	if live <= 0 {
		return false, nil // emptied: the full path handles it
	}

	vslots, err := currentVectorSlots(ctx, storeDB)
	if err != nil {
		return false, err
	}
	oldDelta, err := readVectorDelta(basePath + suffixDelta)
	if err != nil {
		return false, nil // an unreadable delta is rebuilt by the full path
	}
	dtype := deltaDTypeF32
	if meta.VectorDType == deltaDTypeF16 {
		dtype = deltaDTypeF16
	}
	nd := newVectorDelta(meta.Dimensions, dtype, b.config.vectorMetric())
	if oldDelta != nil {
		if oldDelta.dims != meta.Dimensions {
			return false, nil
		}
		for i, slot := range oldDelta.slots {
			if !removed[slot] {
				if err := nd.Append(slot, oldDelta.Vector(i)); err != nil {
					return false, err
				}
			}
		}
	}
	// vector_slots - live is the dead count of the final state.
	dead := vslots + int64(newN) - live
	merge, compact := b.limits.crossed(nd.Len()+newN, live, dead, b.config.prunedIndex())
	if merge || compact {
		// The main file is rebuilt from the stored vectors, embedding only the
		// new texts. A backend that cannot hand its vectors back (pruned
		// embeddings, mmap) takes the full path instead.
		return b.mergeVectors(ctx, name, basePath, st, d, mergePlan{
			meta: meta, removed: removed, drop: drop, changed: changed, oldDelta: oldDelta,
			live: live, vslots: vslots,
		})
	}

	// Embed only the changed chunks, before any file changes.
	embeddings, err := b.embedNewItems(ctx, name, meta, d.newItems, st)
	if err != nil {
		return false, err
	}
	for i, v := range embeddings {
		if err := nd.Append(vslots+int64(i), v); err != nil {
			return false, err
		}
	}

	sourceDir := b.sourceDir
	if sourceDir == "" {
		sourceDir = meta.SourceDir
	}
	nextGen := currentVectorGeneration(ctx, storeDB) + 1
	analyzer := d.analyzer
	err = b.publishVectorFormat2(ctx, storeDB, basePath, []stagedVectorFile{{Suffix: suffixDelta, Data: nd.Marshal()}}, nextGen, func(tx *sql.Tx) error {
		return b.applyIncrementalTx(ctx, tx, d, analyzer, drop, changed, vslots, sourceDir)
	})
	if err != nil {
		return false, err
	}

	st.Mode, st.Added, st.Passages = UpdateInPlace, newN, int(live)
	return true, UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.NumPassages = int(live)
		m.StoreFormat = 2
		m.VectorGeneration = nextGen
		m.VectorSlots = vslots + int64(newN)
		m.UpdatedAt = time.Now()
	})
}

// embedNewItems embeds the texts of the new items (nothing else) and checks
// the vectors against the index's dimensions.
func (b *LeannBuilder) embedNewItems(ctx context.Context, name string, meta *IndexMeta, items []Item, st *UpdateStats) ([][]float32, error) {
	if len(items) == 0 {
		return nil, nil
	}
	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = it.Text
	}
	if reporter, ok := b.embedder.(interface{ SetProgressCallback(func(int, int)) }); ok {
		reporter.SetProgressCallback(func(done, total int) {
			if b.progressCb != nil {
				b.progressCb("vector_index", done, total)
			}
		})
	}
	embedStart := time.Now()
	embeddings, err := b.embedder.Compute(ctx, withPrefix(meta.PassagePrefix, texts))
	st.Embedded, st.EmbedTime = len(texts), time.Since(embedStart)
	if err != nil {
		return nil, fmt.Errorf("compute embeddings: %w", err)
	}
	if len(embeddings) != len(items) {
		return nil, fmt.Errorf("embedder returned %d vectors for %d texts", len(embeddings), len(items))
	}
	if err := b.checkDimensions(name, len(embeddings[0])); err != nil {
		return nil, err
	}
	for i, v := range embeddings {
		if len(v) != meta.Dimensions {
			return nil, fmt.Errorf("embedding row %d has %d dims, expected %d", i, len(v), meta.Dimensions)
		}
	}
	return embeddings, nil
}

// mergePlan is what updateIncremental worked out before it found a limit
// crossed.
type mergePlan struct {
	meta     *IndexMeta
	removed  map[int64]bool // slots the update kills
	drop     map[string]bool
	changed  []string
	oldDelta *vectorDelta
	live     int64 // passages after the update
	vslots   int64
}

// mergeVectors rebuilds the main vector file from the vectors already stored
// (the old main file and the delta), plus the embeddings of the new texts, and
// renumbers the slots densely (RFC 005 section 3.4). Because slots change, the
// publication replaces the main file and bumps its generation, which readers
// detect and answer with a full reload. It returns done=false, with nothing
// changed, when the backend cannot return its stored vectors.
func (b *LeannBuilder) mergeVectors(ctx context.Context, name, basePath string, st *UpdateStats, d incrementalUpdate, p mergePlan) (bool, error) {
	storeDB := d.store
	factory, err := GetBackend(b.config.Backend)
	if err != nil {
		return false, nil
	}
	mainData, err := os.ReadFile(basePath + suffixMain)
	if err != nil {
		return false, nil
	}
	// The mmap searchers do not hand vectors back: load into memory.
	noMmap := false
	scfg := b.config
	scfg.UseMmap = &noMmap
	searcher := factory.NewSearcher(scfg)
	defer searcher.Close()
	extractor, ok := searcher.(VectorExtractor)
	if !ok {
		return false, nil
	}
	if err := searcher.Load(ctx, mainData, *p.meta); err != nil {
		return false, nil
	}

	slots, err := storeDB.AllSlots(ctx)
	if err != nil {
		return false, err
	}
	kept := slots[:0:0]
	for _, s := range slots {
		if !p.removed[s] {
			kept = append(kept, s)
		}
	}
	// Vectors of the unchanged passages: the delta holds the newest ones, the
	// main file the rest.
	vectors := make([][]float32, len(kept), len(kept)+len(d.newItems))
	var mainIdx []int
	var mainSlots []int64
	for i, s := range kept {
		if p.oldDelta != nil {
			if v, ok := p.oldDelta.Vector32(s); ok {
				vectors[i] = v
				continue
			}
		}
		mainIdx = append(mainIdx, i)
		mainSlots = append(mainSlots, s)
	}
	if len(mainSlots) > 0 {
		vs, err := extractor.ExtractVectors(ctx, mainSlots)
		if err != nil || len(vs) != len(mainSlots) {
			return false, nil // pruned or unreadable: the full path re-embeds
		}
		for j, i := range mainIdx {
			vectors[i] = vs[j]
		}
	}

	embeddings, err := b.embedNewItems(ctx, name, p.meta, d.newItems, st)
	if err != nil {
		return false, err
	}
	vectors = append(vectors, embeddings...)
	n := int64(len(vectors))
	nextGen := currentVectorGeneration(ctx, storeDB) + 1
	// The graph is built in a shuffled insertion order: HNSW assumes it, and
	// the kept passages followed by the newest topics would cluster the
	// inserts and cost recall against a build in source order. The slots
	// follow the order, so a vector's slot stays its position in the file.
	pos := rand.New(rand.NewSource(int64(nextGen))).Perm(int(n))
	ordered := make([][]float32, n)
	for i, v := range vectors {
		ordered[pos[i]] = v
	}
	indexData, err := b.backend.Build(ctx, ordered)
	if err != nil {
		return false, fmt.Errorf("build index: %w", err)
	}

	reassign := make(map[int64]int64, len(kept))
	for i, s := range kept {
		reassign[s] = int64(pos[i])
	}
	newSlots := make([]int64, len(d.newItems))
	for j := range newSlots {
		newSlots[j] = int64(pos[len(kept)+j])
	}
	sourceDir := b.sourceDir
	if sourceDir == "" {
		sourceDir = p.meta.SourceDir
	}
	err = b.publishVectorFormat2(ctx, storeDB, basePath, []stagedVectorFile{{Suffix: suffixMain, Data: indexData}, b.emptyDeltaFile(p.meta.Dimensions)}, nextGen, func(tx *sql.Tx) error {
		if cur, err := currentVectorSlots(ctx, storeDB); err != nil {
			return err
		} else if cur != p.vslots {
			return fmt.Errorf("the index changed while it was being updated (vector slots %d, expected %d); run the update again", cur, p.vslots)
		}
		for src := range p.drop {
			if _, err := storeDB.RemoveSourceTx(ctx, tx, src); err != nil {
				return fmt.Errorf("removing source %q: %w", src, err)
			}
		}
		for _, src := range p.changed {
			if _, err := storeDB.RemoveSourceTx(ctx, tx, src); err != nil {
				return fmt.Errorf("removing source %q: %w", src, err)
			}
		}
		if err := storeDB.ReassignSlotsTx(ctx, tx, reassign); err != nil {
			return fmt.Errorf("reassigning slots: %w", err)
		}
		deltas, _ := sourceDeltas(d.analyzer, d.newItems, newSlots, sourceDir)
		for _, sd := range deltas {
			if _, err := storeDB.ApplySourceTx(ctx, tx, sd); err != nil {
				return fmt.Errorf("applying source %q: %w", sd.Source, err)
			}
		}
		return storeDB.SetMetaTx(ctx, tx, metaVectorSlots, strconv.FormatInt(n, 10))
	})
	if err != nil {
		return false, err
	}

	st.Mode, st.Added, st.Passages = UpdateMerge, len(d.newItems), int(n)
	return true, UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.NumPassages = int(n)
		m.StoreFormat = 2
		m.VectorGeneration = nextGen
		m.VectorSlots = n
		m.VectorMainGeneration = nextGen
		m.UpdatedAt = time.Now()
	})
}

// incrementalUpdate is the input of updateIncremental.
type incrementalUpdate struct {
	store         *store.Store
	analyzer      analysis.Analyzer
	newItems      []Item
	removeSources []string
}

// applyIncrementalTx is the database change of an incremental publication,
// inside its transaction: removed and changed sources lose their passages (the
// slots die), the new passages take slots from vslots upward, and the
// high-water mark advances. No unchanged passage is touched.
func (b *LeannBuilder) applyIncrementalTx(ctx context.Context, tx *sql.Tx, d incrementalUpdate, an analysis.Analyzer, drop map[string]bool, changed []string, vslots int64, sourceDir string) error {
	// The slots were allocated from the value read before the embeddings were
	// computed; another writer in between would hand out the same slots.
	if cur, err := currentVectorSlots(ctx, d.store); err != nil {
		return err
	} else if cur != vslots {
		return fmt.Errorf("the index changed while it was being updated (vector slots %d, expected %d); run the update again", cur, vslots)
	}
	for src := range drop {
		if _, err := d.store.RemoveSourceTx(ctx, tx, src); err != nil {
			return fmt.Errorf("removing source %q: %w", src, err)
		}
	}
	for _, src := range changed {
		if _, err := d.store.RemoveSourceTx(ctx, tx, src); err != nil {
			return fmt.Errorf("removing source %q: %w", src, err)
		}
	}
	if _, err := applySourceGroupsTx(ctx, tx, d.store, an, d.newItems, vslots, sourceDir); err != nil {
		return err
	}
	return d.store.SetMetaTx(ctx, tx, metaVectorSlots, strconv.FormatInt(vslots+int64(len(d.newItems)), 10))
}
