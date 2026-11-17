package gleann

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LeannBuilder builds and manages indexes.
// This mirrors Python LEANN's LeannBuilder.
type LeannBuilder struct {
	config      Config
	backend     BackendBuilder
	embedder    EmbeddingComputer
	chunker     Chunker
	progressCb  func(phase string, done, total int)
	lexicalMode string            // GLEANN_LEXICAL, read once at creation: "memory" (default) or "fts5"
	storeFormat int               // GLEANN_STORE_FORMAT, read once at creation: format for NEW indexes (1 bbolt, 2 SQLite)
	limits      incrementalLimits // delta and dead-slot limits of RFC 005, read once at creation
	sourceDir   string            // documents directory, for the files-table content hashes (SetSourceDir)
	// crashHook injects a failure at a named boundary of the format-2 vector
	// publication (tests only; see the crash* constants in builder_format2.go).
	// A struct field, not a package global: production builders never set it.
	crashHook func(point string) error

	// Results of the last format-2 passage build (buildPassagesFormat2):
	// the vector generation the rebuild published, and whether the database
	// had to be recreated (corrupt or from an incompatible build), in which
	// case the code graph it held is gone and the caller must rebuild it.
	passagesGeneration int
	dbRecreated        bool
}

// DatabaseRecreated reports whether the last BuildPassages/BuildLexicalOnly of
// a format-2 index had to delete and recreate <name>.db because it could not
// be opened (damaged, or written by an incompatible build). A normal rebuild
// replaces the passages inside the existing database and keeps the code graph;
// after a recreation the graph is gone, and the indexing pipeline rebuilds it.
func (b *LeannBuilder) DatabaseRecreated() bool { return b.dbRecreated }

// NewBuilder creates a new LeannBuilder.
func NewBuilder(config Config, embedder EmbeddingComputer) (*LeannBuilder, error) {
	factory, err := GetBackend(config.Backend)
	if err != nil {
		return nil, fmt.Errorf("get backend: %w", err)
	}
	dtype, err := hnswDTypeSwitch(config.HNSWConfig.VectorDType)
	if err != nil {
		return nil, err
	}
	config.HNSWConfig.VectorDType = dtype

	return &LeannBuilder{
		config:      config,
		backend:     factory.NewBuilder(config),
		embedder:    embedder,
		lexicalMode: lexicalLegMode(),
		storeFormat: storeFormatSwitch(),
		limits:      incrementalLimitsFromEnv(),
	}, nil
}

// maybeBuildSidecar rebuilds the persisted lexical sidecar after the passages
// have been written, but only when GLEANN_LEXICAL=fts5. A full rebuild after
// every build or sync is acceptable in this step (RFC 003 section 8 step 3);
// incremental lexical maintenance arrives with the format-2 store in step 4.
// Building it here means a normal search never pays the build cost.
//
// A failure never fails the build or sync: the sidecar records a stamp of the
// passage file, so a missed rebuild is seen as stale at the next load, which
// rebuilds it or falls back to in-memory BM25.
func (b *LeannBuilder) maybeBuildSidecar(ctx context.Context, name string) error {
	if b.lexicalMode != "fts5" {
		return nil
	}
	// A format-2 index keeps its lexical rows in <name>.db itself; the
	// format-1 sidecar must not be built for it.
	if StoreFormatOf(filepath.Join(b.config.IndexDir, name, name)) == 2 {
		return nil
	}
	if err := buildLexicalSidecar(ctx, b.config.IndexDir, name); err != nil {
		log.Printf("ℹ️  Persisted lexical index for %q not rebuilt (%v); the next search rebuilds it", name, err)
	}
	return nil
}

// SetProgressCallback registers a callback invoked as indexing phases make progress.
func (b *LeannBuilder) SetProgressCallback(cb func(phase string, done, total int)) {
	b.progressCb = cb
}

// SetChunker sets a custom chunker for text processing.
func (b *LeannBuilder) SetChunker(chunker Chunker) {
	b.chunker = chunker
}

// BuildPassages creates the index directory and stores passages into the passage database,
// immediately writing metadata marking lexical search as ready (Phase 1: Instant Lexical Index).
func (b *LeannBuilder) BuildPassages(name string, items []Item) ([]int64, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no items to index")
	}
	if err := checkPathName(name); err != nil {
		return nil, err
	}

	indexDir := filepath.Join(b.config.IndexDir, name)
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return nil, fmt.Errorf("create index directory: %w", err)
	}

	basePath := filepath.Join(indexDir, name)

	// Rebuilding must not forget what the user attached to the index.
	prevMeta, _ := GetIndexMeta(b.config.IndexDir, name)

	// The format of this build: an existing index keeps its format, a new one
	// follows the switch (GLEANN_STORE_FORMAT) read at builder creation.
	format := b.buildFormat(basePath)
	var ids []int64
	var err error
	if format == 2 {
		ids, err = b.buildPassagesFormat2(context.Background(), basePath, name, items, prevMeta)
	} else {
		// Clean up any old passages database to ensure we start from ID 0.
		os.Remove(basePath + ".passages.db")

		// Initialize passage manager and store passages.
		pm := NewPassageManager(basePath)
		ids, err = pm.Add(items)
		_ = pm.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("add passages: %w", err)
	}

	meta := IndexMeta{
		Name:           name,
		Backend:        b.config.Backend,
		EmbeddingModel: "",
		Dimensions:     0,
		NumPassages:    len(ids),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Version:        "1.0.0",
		GleannVersion:  GetGleannVersion(),
		PluginVersions: GetInstalledPluginVersions(),
		LexicalOnly:    true,
		VectorReady:    false,
		SignaturesOnly: b.config.ChunkConfig.SignaturesOnly,
	}
	if b.embedder != nil {
		meta.EmbeddingModel = b.embedder.ModelName()
		meta.Dimensions = b.embedder.Dimensions()
	}
	if prevMeta != nil {
		meta.SourceDir = prevMeta.SourceDir
		meta.AutoWatch = prevMeta.AutoWatch
		meta.Tags = prevMeta.Tags
		meta.Description = prevMeta.Description
		meta.MCPExposed = prevMeta.MCPExposed
		if !prevMeta.CreatedAt.IsZero() {
			meta.CreatedAt = prevMeta.CreatedAt
		}
	}

	if format == 2 {
		meta.StoreFormat = 2
		// Mirror the generation the rebuild published: the passages were
		// replaced (slotless), so a reader that holds the previous vector
		// file must see the change.
		meta.VectorGeneration = b.passagesGeneration
	}

	metaPath := basePath + ".meta.json"
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, metaData, 0o644); err != nil {
		return nil, fmt.Errorf("write metadata: %w", err)
	}

	// The passages are final; build the persisted lexical sidecar so the first
	// search does not have to (GLEANN_LEXICAL=fts5 only).
	if err := b.maybeBuildSidecar(context.Background(), name); err != nil {
		return nil, err
	}

	return ids, nil
}

// BuildVectors computes vector embeddings for items and builds the vector index backend,
// updating metadata to mark vectors as ready (Phase 2: Semantic Vector Index).
func (b *LeannBuilder) BuildVectors(ctx context.Context, name string, items []Item, ids []int64) error {
	if b.embedder == nil {
		return fmt.Errorf("no embedder configured for vector build")
	}
	if err := checkPathName(name); err != nil {
		return err
	}

	indexDir := filepath.Join(b.config.IndexDir, name)
	basePath := filepath.Join(indexDir, name)

	// Extract texts for embedding computation, with the model's passage prefix (recorded in the meta below).
	passagePrefix, queryPrefix := EmbeddingPrefixes(b.config, b.embedder.ModelName())
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = passagePrefix + item.Text
	}

	// Wire progress reporting from embedder if supported
	if reporter, ok := b.embedder.(interface{ SetProgressCallback(func(int, int)) }); ok {
		reporter.SetProgressCallback(func(done, total int) {
			if b.progressCb != nil {
				b.progressCb("vector_index", done, total)
			}
		})
	}

	// Compute embeddings.
	embeddings, err := b.embedder.Compute(ctx, texts)
	if err != nil {
		return fmt.Errorf("compute embeddings: %w", err)
	}

	if len(embeddings) == 0 {
		return fmt.Errorf("embedder returned 0 vectors for %d texts", len(texts))
	}
	if len(embeddings[0]) == 0 {
		return fmt.Errorf("embedder returned zero-dimensional vectors (model=%q); check provider configuration", b.embedder.ModelName())
	}
	expectedDim := len(embeddings[0])
	for i, vec := range embeddings {
		if len(vec) != expectedDim {
			return fmt.Errorf("embedding row %d has %d dims, expected %d (text len=%d)", i, len(vec), expectedDim, len(texts[i]))
		}
	}

	// Build index.
	indexData, err := b.backend.Build(ctx, embeddings)
	if err != nil {
		return fmt.Errorf("build index: %w", err)
	}

	// Write index file. A format-2 index publishes crash-safely (RFC 003
	// section 3.4): the staged passages are slotless until this step, and
	// their slots are assigned in the SAME transaction as the pending
	// publication keys, before the staged vector file is renamed into place.
	// The ids BuildPassages returned are the internal rowids, aligned to
	// items, so slot i belongs to item i.
	indexPath := basePath + ".index"
	if StoreFormatOf(basePath) == 2 {
		st, err := openFormat2Store(basePath)
		if err != nil {
			return err
		}
		defer st.Close()
		if err := recoverVectorPublication(ctx, st, basePath); err != nil {
			return fmt.Errorf("recovering interrupted vector publication: %w", err)
		}
		idToSlot := make(map[int64]int64, len(ids))
		for i, id := range ids {
			idToSlot[id] = int64(i)
		}
		nextGen := currentVectorGeneration(ctx, st) + 1
		if err := b.publishVectorFormat2(ctx, st, basePath, []stagedVectorFile{{Suffix: suffixMain, Data: indexData}, b.emptyDeltaFile(expectedDim)}, nextGen, func(tx *sql.Tx) error {
			if err := st.AssignSlotsTx(ctx, tx, idToSlot); err != nil {
				return err
			}
			return st.SetMetaTx(ctx, tx, metaVectorSlots, strconv.Itoa(len(ids)))
		}); err != nil {
			return err
		}
		return UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
			m.EmbeddingModel = b.embedder.ModelName()
			m.Dimensions = expectedDim
			m.PassagePrefix, m.QueryPrefix = passagePrefix, queryPrefix
			m.VectorReady = true
			m.LexicalOnly = false
			m.SignaturesOnly = b.config.ChunkConfig.SignaturesOnly
			m.VectorGeneration = nextGen
			m.VectorMainGeneration = nextGen
			m.VectorSlots = int64(len(ids))
			if b.config.Backend == "hnsw" && b.config.HNSWConfig.VectorDType != "" {
				m.VectorDType = b.config.HNSWConfig.VectorDType
			}
			m.UpdatedAt = time.Now()
		})
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}

	// Update metadata with vector completion
	_ = UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.EmbeddingModel = b.embedder.ModelName()
		m.Dimensions = expectedDim
		m.PassagePrefix, m.QueryPrefix = passagePrefix, queryPrefix
		m.VectorReady = true
		m.LexicalOnly = false
		m.SignaturesOnly = b.config.ChunkConfig.SignaturesOnly
		if b.config.Backend == "hnsw" && b.config.HNSWConfig.VectorDType != "" {
			m.VectorDType = b.config.HNSWConfig.VectorDType
		}
		m.UpdatedAt = time.Now()
	})

	return nil
}

// BuildLexicalOnly builds only Phase 1 instant lexical index without computing vector embeddings.
func (b *LeannBuilder) BuildLexicalOnly(name string, items []Item) error {
	_, err := b.BuildPassages(name, items)
	return err
}

// Build creates a new index from the given items executing staged build:
// Phase 1 (Instant Lexical) followed by Phase 2 (Vector Index).
func (b *LeannBuilder) Build(ctx context.Context, name string, items []Item) error {
	ids, err := b.BuildPassages(name, items)
	if err != nil {
		return err
	}
	return b.BuildVectors(ctx, name, items, ids)
}

// UpdateIndex updates an existing index: it drops the passages of removeSources
// and adds newItems.
//
// Appending is applied in place. The vector backends address a vector by its
// position/ID, which must equal the passage ID, and passage IDs come from an
// ever-increasing sequence, so removing passages in place leaves permanent gaps
// (positional DiskANN renumbers, a pruned HNSW loses vectors): vectors silently
// end up attached to the wrong passage and searches return the wrong text. A
// change that removes passages — or an index whose IDs already have gaps, or one
// whose backend cannot take an in-place add — therefore compacts instead: the
// index is rebuilt from the remaining passages plus newItems, which renumbers
// them densely. Embeddings are computed before any file is touched, so a failure
// there leaves the index as it was.
func (b *LeannBuilder) UpdateIndex(ctx context.Context, name string, newItems []Item, removeSources []string) error {
	_, err := b.UpdateIndexWithStats(ctx, name, newItems, removeSources)
	return err
}

// UpdateMode says how an index update was applied.
type UpdateMode string

const (
	// UpdateNoop: there was nothing to remove or add.
	UpdateNoop UpdateMode = "noop"
	// UpdateInPlace: new passages were appended to the existing vector index.
	UpdateInPlace UpdateMode = "in-place"
	// UpdateMerge: the main vector file was rebuilt from the vectors already
	// stored, plus the embeddings of the new texts only (a delta merge or a
	// compaction of dead slots; the slots are dense again).
	UpdateMerge UpdateMode = "merge"
	// UpdateCompact: the index was rebuilt from its remaining passages.
	UpdateCompact UpdateMode = "compact"
	// UpdateEmptied: the last passages were removed; the vector index is gone.
	UpdateEmptied UpdateMode = "emptied"
)

// UpdateStats describes what an index update did and what it cost. The mode is
// the number to watch: a compaction re-embeds every remaining passage (cheap
// when the embedding cache holds them, expensive when it does not), an in-place
// append embeds only the new ones.
type UpdateStats struct {
	Mode UpdateMode
	// Added and Removed count passages: Removed is only known when the index
	// was compacted or emptied.
	Added   int
	Removed int
	// Passages is how many passages the index holds afterwards.
	Passages int
	// Embedded is how many texts were sent to the embedder, EmbedTime how long
	// that took (cache hits included).
	Embedded  int
	EmbedTime time.Duration
	// Elapsed is the whole update.
	Elapsed time.Duration
}

// UpdateIndexWithStats is UpdateIndex that also reports what it did and what it
// cost. The stats are filled in even when the update fails part-way.
func (b *LeannBuilder) UpdateIndexWithStats(ctx context.Context, name string, newItems []Item, removeSources []string) (UpdateStats, error) {
	start := time.Now()
	st := UpdateStats{Mode: UpdateNoop}
	err := b.updateIndex(ctx, name, newItems, removeSources, &st)
	st.Elapsed = time.Since(start)
	// updateIndex closed the passage store; rebuild the sidecar to match the
	// new passage set (a noop changed nothing, so skip it).
	if err == nil && st.Mode != UpdateNoop {
		err = b.maybeBuildSidecar(ctx, name)
	}
	return st, err
}

func (b *LeannBuilder) updateIndex(ctx context.Context, name string, newItems []Item, removeSources []string, st *UpdateStats) error {
	if err := checkPathName(name); err != nil {
		return err
	}
	if b.embedder == nil {
		return fmt.Errorf("no embedder configured for index update")
	}
	basePath := filepath.Join(b.config.IndexDir, name, name)
	switch DetectStoreFormat(basePath) {
	case StoreV2:
		return b.updateIndexFormat2(ctx, name, basePath, newItems, removeSources, st, false)
	case StoreNone:
		if len(newItems) == 0 {
			// An index that does not exist has nothing to remove.
			return nil
		}
		return fmt.Errorf("updating index %q: %w", name, ErrIndexNotFound)
	}
	indexPath := basePath + ".index"
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && len(newItems) == 0 {
			// An index that does not exist has nothing to remove.
			return nil
		}
		// An index emptied by removing its last passage has no vector file (the
		// backends cannot represent an empty index); adding to it starts over.
		meta, metaErr := GetIndexMeta(b.config.IndexDir, name)
		if !errors.Is(err, fs.ErrNotExist) || metaErr != nil || meta.NumPassages != 0 {
			return fmt.Errorf("read existing index: %w", err)
		}
		indexData = nil
	}

	pm := NewPassageManager(basePath)
	if err := pm.Load(); err != nil {
		return fmt.Errorf("load passages: %w", err)
	}
	defer pm.Close()

	removes := false
	if len(removeSources) > 0 {
		if removes, err = pm.HasSources(removeSources); err != nil {
			return fmt.Errorf("scan passages: %w", err)
		}
	}
	if !removes {
		if len(newItems) == 0 {
			st.Passages = pm.Count()
			return nil // nothing to remove, nothing to add
		}
		dense, err := pm.IsDense()
		if err != nil {
			return fmt.Errorf("scan passages: %w", err)
		}
		if dense {
			done, err := b.appendInPlace(ctx, name, pm, indexPath, indexData, newItems, st)
			if err != nil || done {
				return err
			}
			// The backend cannot take the add in place; fall through.
		}
	}
	return b.compact(ctx, name, pm, newItems, removeSources, st)
}

// Compact rebuilds the index's vector index from its passage store, renumbering
// the passages 0..n-1. It is what repairs an index whose passage IDs have gaps
// or whose vector file is missing or out of step (see CheckIndexHealth); the
// passages themselves are kept as they are. The stats say what it cost.
func (b *LeannBuilder) Compact(ctx context.Context, name string) (UpdateStats, error) {
	start := time.Now()
	st := UpdateStats{Mode: UpdateCompact}
	err := b.compactStored(ctx, name, &st)
	st.Elapsed = time.Since(start)
	// compactStored closed the passage store; rebuild the sidecar to match.
	if err == nil {
		err = b.maybeBuildSidecar(ctx, name)
	}
	return st, err
}

// ErrDimensionMismatch is returned when an embedder produces vectors of another
// size than the index holds, i.e. the index was built with a different
// embedding model. Mixing them would corrupt the index (or crash the backend),
// so the index has to be rebuilt to change models.
var ErrDimensionMismatch = errors.New("embedding dimension mismatch")

// checkDimensions fails when got differs from the vector size recorded for the
// index. An index without a recorded size is not checked.
func (b *LeannBuilder) checkDimensions(name string, got int) error {
	meta, err := GetIndexMeta(b.config.IndexDir, name)
	if err != nil || meta.Dimensions == 0 || meta.Dimensions == got {
		return nil
	}
	return fmt.Errorf("%w: index %q holds %d-dimensional vectors (model %q) but the embedder %q produces %d; "+
		"changing the embedding model needs a rebuild: gleann index rebuild %s --docs <dir> --model %s",
		ErrDimensionMismatch, name, meta.Dimensions, meta.EmbeddingModel, b.embedder.ModelName(), got, name, b.embedder.ModelName())
}

func (b *LeannBuilder) compactStored(ctx context.Context, name string, st *UpdateStats) error {
	if err := checkPathName(name); err != nil {
		return err
	}
	if b.embedder == nil {
		return fmt.Errorf("no embedder configured for index repair")
	}
	basePath := filepath.Join(b.config.IndexDir, name, name)
	if StoreFormatOf(basePath) == 2 {
		st2, err := openFormat2Store(basePath)
		if err != nil {
			return err
		}
		cnt, err := st2.Count(ctx)
		_ = st2.Close()
		if err != nil {
			return fmt.Errorf("counting passages: %w", err)
		}
		if cnt == 0 {
			return fmt.Errorf("index %q has no passages to rebuild from", name)
		}
		return b.updateIndexFormat2(ctx, name, basePath, nil, nil, st, true)
	}
	pm := NewPassageManager(basePath)
	if err := pm.Load(); err != nil {
		return fmt.Errorf("load passages: %w", err)
	}
	defer pm.Close()
	if pm.Count() == 0 {
		return fmt.Errorf("index %q has no passages to rebuild from", name)
	}
	return b.compact(ctx, name, pm, nil, nil, st)
}

// appendInPlace adds newItems to the passage store and the vector index. It
// reports done=false, with the passage store restored, when the change cannot
// be applied in place and the caller should compact instead.
func (b *LeannBuilder) appendInPlace(ctx context.Context, name string, pm *PassageManager, indexPath string, indexData []byte, newItems []Item, st *UpdateStats) (done bool, err error) {
	texts := make([]string, len(newItems))
	for i, item := range newItems {
		texts[i] = item.Text
	}
	// Compute embeddings before touching the passage store (with the prefix the index was built with).
	embedStart := time.Now()
	embeddings, err := b.embedder.Compute(ctx, withPrefix(recordedPassagePrefix(b.config.IndexDir, name), texts))
	st.Embedded, st.EmbedTime = len(texts), time.Since(embedStart)
	if err != nil {
		return false, fmt.Errorf("compute embeddings: %w", err)
	}
	if len(embeddings) != len(newItems) {
		return false, fmt.Errorf("embedder returned %d vectors for %d texts", len(embeddings), len(newItems))
	}
	if len(embeddings) > 0 {
		// Checked before the passage store is touched, so a mismatch leaves
		// the index exactly as it was. Every row is checked: a torn cache
		// entry can be shorter than the rest (C-08).
		if err := b.checkDimensions(name, len(embeddings[0])); err != nil {
			return false, err
		}
		for i, e := range embeddings {
			if len(e) != len(embeddings[0]) {
				return false, fmt.Errorf("embedding row %d has %d dims, expected %d", i, len(e), len(embeddings[0]))
			}
		}
	}

	before := int64(pm.Count())
	ids, err := pm.Add(newItems)
	if err != nil {
		return false, fmt.Errorf("add passages: %w", err)
	}
	if ids[0] != before {
		_ = pm.RollbackAdd(ids)
		return false, nil
	}
	newData, err := b.addVectorsSafe(ctx, indexData, embeddings, ids[0])
	if err != nil {
		_ = pm.RollbackAdd(ids)
		return false, nil // e.g. a pruned HNSW graph cannot be edited
	}
	if err := writeFileAtomic(indexPath, newData); err != nil {
		_ = pm.RollbackAdd(ids)
		return false, fmt.Errorf("write updated index: %w", err)
	}
	count := pm.Count()
	_ = UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.NumPassages = count
		m.UpdatedAt = time.Now()
	})
	st.Mode, st.Added, st.Passages = UpdateInPlace, len(newItems), count
	return true, nil
}

// compact rebuilds the index from the passages that remain after removing
// removeSources, plus newItems. Passage IDs come out dense again, which also
// repairs an index that earlier in-place removals left with gaps.
func (b *LeannBuilder) compact(ctx context.Context, name string, pm *PassageManager, newItems []Item, removeSources []string, st *UpdateStats) error {
	basePath := filepath.Join(b.config.IndexDir, name, name)

	drop := make(map[string]bool, len(removeSources))
	for _, s := range removeSources {
		drop[s] = true
	}
	var items []Item
	stored := 0
	for _, p := range pm.All() {
		stored++
		if src, _ := p.Metadata["source"].(string); drop[src] {
			continue
		}
		items = append(items, Item{Text: p.Text, Metadata: p.Metadata})
	}
	st.Removed = stored - len(items)
	items = append(items, newItems...)
	if len(items) == 0 {
		return b.empty(name, pm, st)
	}

	// Everything that can fail on external input happens before a file changes.
	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = it.Text
	}
	// Compacting re-embeds everything that remains, which can take as long as the
	// original embedding pass: report progress like a build does.
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
	if len(embeddings) != len(items) {
		return fmt.Errorf("embedder returned %d vectors for %d texts", len(embeddings), len(items))
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

	// Stage the new passage store next to the old one, then swap both files.
	tmpBase := basePath + ".compact"
	tmpDB := tmpBase + ".passages.db"
	_ = os.Remove(tmpDB)
	tmpPM := NewPassageManager(tmpBase)
	ids, err := tmpPM.Add(items)
	_ = tmpPM.Close()
	if err != nil {
		_ = os.Remove(tmpDB)
		return fmt.Errorf("stage passages: %w", err)
	}
	_ = pm.Close() // release the old passage DB so it can be replaced
	if err := os.Rename(tmpDB, basePath+".passages.db"); err != nil {
		_ = os.Remove(tmpDB)
		return fmt.Errorf("replace passages: %w", err)
	}
	if err := writeFileAtomic(basePath+".index", indexData); err != nil {
		return fmt.Errorf("write compacted index: %w", err)
	}
	st.Mode, st.Added, st.Passages = UpdateCompact, len(newItems), len(ids)
	return UpdateIndexMeta(b.config.IndexDir, name, func(m *IndexMeta) {
		m.NumPassages = len(ids)
		m.Dimensions = dim
		m.VectorReady = true
		m.LexicalOnly = false
		m.UpdatedAt = time.Now()
	})
}

// empty leaves the index with no passages after its last one was removed. The
// vector backends cannot represent an empty index, so the passage store is
// emptied and the vector file removed; the metadata says so, and a later add
// builds the index afresh. The removed content is no longer searchable.
func (b *LeannBuilder) empty(name string, pm *PassageManager, st *UpdateStats) error {
	basePath := filepath.Join(b.config.IndexDir, name, name)
	st.Mode, st.Passages = UpdateEmptied, 0
	_ = pm.Close()
	if err := os.Remove(basePath + ".passages.db"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("empty passages: %w", err)
	}
	// Readers still expect a passage store to open; leave an empty one.
	fresh := NewPassageManager(basePath)
	err := fresh.Load()
	_ = fresh.Close()
	if err != nil {
		return fmt.Errorf("create empty passage store: %w", err)
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

// writeFileAtomic writes data to path via a temp file and a rename, so a crash
// cannot leave a torn file.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// BuildFromTexts is a convenience method that creates Items from plain texts.
func (b *LeannBuilder) BuildFromTexts(ctx context.Context, name string, texts []string) error {
	items := make([]Item, len(texts))
	for i, text := range texts {
		items[i] = Item{Text: text}
	}
	return b.Build(ctx, name, items)
}

// AddToIndex adds new items to an existing index.
func (b *LeannBuilder) AddToIndex(ctx context.Context, name string, items []Item) error {
	return b.UpdateIndex(ctx, name, items, nil)
}

// addVectorsSafe calls backend.AddVectors and turns a backend panic into an
// error, so the caller can roll the passage store back (C-08).
func (b *LeannBuilder) addVectorsSafe(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("vector backend panic: %v", r)
		}
	}()
	return b.backend.AddVectors(ctx, indexData, embeddings, startID)
}

// hnswDTypeSwitch resolves the storage type of the HNSW vectors for NEW
// writes, once where a builder is created (as storeFormatSwitch does):
// GLEANN_HNSW_DTYPE=f16|f32 overrides the configured value; an unknown
// environment value is ignored so a typo cannot change the on-disk format,
// an unknown configured value is an error. "" keeps the default (float32,
// or the dtype an existing file already has on an incremental add).
func hnswDTypeSwitch(configured string) (string, error) {
	switch env := strings.ToLower(strings.TrimSpace(os.Getenv("GLEANN_HNSW_DTYPE"))); env {
	case "f16", "f32":
		return env, nil
	}
	switch v := strings.ToLower(strings.TrimSpace(configured)); v {
	case "", "f16", "f32":
		return v, nil
	default:
		return "", fmt.Errorf("hnsw_config.vector_dtype %q: want f32 or f16", configured)
	}
}
