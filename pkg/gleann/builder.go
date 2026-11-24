package gleann

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LeannBuilder builds and manages indexes.
// This mirrors Python LEANN's LeannBuilder.
type LeannBuilder struct {
	config     Config
	backend    BackendBuilder
	embedder   EmbeddingComputer
	chunker    Chunker
	progressCb func(phase string, done, total int)
	limits     incrementalLimits // delta and dead-slot limits of RFC 005, read once at creation
	metaHook   func(*IndexMeta)  // applied to the metadata of every passage build before it is written (SetMetaHook)
	sourceDir  string            // documents directory, for the files-table content hashes (SetSourceDir)
	// crashHook injects a failure at a named boundary of the format-2 vector
	// publication (tests only; see the crash* constants in builder_format2.go).
	// A struct field, not a package global: production builders never set it.
	crashHook func(point string) error

	// fallbackMeta is the metadata whose user fields a build carries over when
	// the index has no readable meta.json yet (SetPreviousMeta).
	fallbackMeta *IndexMeta

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
	config = withIndexDefaults(config)
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
		config:   config,
		backend:  factory.NewBuilder(config),
		embedder: embedder,
		limits:   incrementalLimitsFromEnv(),
	}, nil
}

// SetProgressCallback registers a callback invoked as indexing phases make progress.
func (b *LeannBuilder) SetProgressCallback(cb func(phase string, done, total int)) {
	b.progressCb = cb
}

// SetPreviousMeta names the metadata of the index before a rebuild that starts
// from an empty directory (a sync rebuild moves the old index aside first): the
// first meta.json the build writes then already carries its user fields (tags,
// description, mcp_exposed, auto_watch, source_dir, created_at) instead of
// reading as untagged and exposed until the end of the rebuild. An index that
// has a readable meta.json uses that file instead.
func (b *LeannBuilder) SetPreviousMeta(m *IndexMeta) { b.fallbackMeta = m }

// SetMetaHook registers a function applied to the index metadata just before
// BuildPassages writes it, after the previous metadata is carried over. It lets
// a caller attach governance fields (tags, description, exposure) in the very
// write that creates the index, instead of a second update that can fail
// and leave an index without them.
func (b *LeannBuilder) SetMetaHook(f func(*IndexMeta)) {
	b.metaHook = f
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
	if prevMeta == nil {
		prevMeta = b.fallbackMeta
	}

	// Every index is format 2 (RFC 004 B1). An index in the old format is
	// refused: `gleann index migrate` converts it, `gleann index rebuild`
	// removes it and builds it again.
	if DetectStoreFormat(basePath) == StoreOld {
		return nil, &OldFormatError{Name: name}
	}
	ids, err := b.buildPassagesFormat2(context.Background(), basePath, name, items, prevMeta)
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
		MergeUserMeta(&meta, prevMeta, prevMeta, prevMeta)
		if !prevMeta.CreatedAt.IsZero() {
			meta.CreatedAt = prevMeta.CreatedAt
		}
	}

	if b.metaHook != nil {
		b.metaHook(&meta)
	}

	meta.StoreFormat = 2
	// Mirror the generation the rebuild published: the passages were
	// replaced (slotless), so a reader that holds the previous vector file
	// must see the change.
	meta.VectorGeneration = b.passagesGeneration

	if err := b.crashPoint(crashBeforePassagesMeta); err != nil {
		return nil, err
	}
	// Same per-index lock and atomic replace as UpdateIndexMeta: the file
	// cannot tear, and an edit of a user field (PATCH) made since prevMeta was
	// read is kept instead of overwritten (MergeUserMeta, under the lock).
	// A rebuild in another process is not covered: it does not hold this lock.
	err = replaceIndexMeta(b.config.IndexDir, name, func(current *IndexMeta) (*IndexMeta, error) {
		if prevMeta != nil {
			MergeUserMeta(&meta, prevMeta, prevMeta, current)
		}
		return &meta, nil
	})
	if err != nil {
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
	switch DetectStoreFormat(basePath) {
	case StoreOld:
		return &OldFormatError{Name: name}
	case StoreNone:
		return fmt.Errorf("building vectors of index %q: %w", name, ErrIndexNotFound)
	}
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
// and adds newItems. Only the new passages are embedded: they go to the delta
// vector file, removed passages leave dead slots, and a merge or compaction
// folds both into the main file past a limit (RFC 005). Embeddings are
// computed before any file is touched, so a failure there leaves the index as
// it was.
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
	case StoreOld:
		// Only `gleann index migrate` converts an old store; an update can at
		// best start over from the sources.
		return requiresRebuild(&OldFormatError{Name: name})
	}
	if len(newItems) == 0 {
		// An index that does not exist has nothing to remove.
		return nil
	}
	return requiresRebuild(fmt.Errorf("updating index %q: %w", name, ErrIndexNotFound))
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
	return st, err
}

// ErrDimensionMismatch is returned when an embedder produces vectors of another
// size than the index holds, i.e. the index was built with a different
// embedding model. Mixing them would corrupt the index (or crash the backend),
// so the index has to be rebuilt to change models.
var ErrDimensionMismatch = errors.New("embedding dimension mismatch")

// ErrRebuildRequired marks an index update failure that no retry and no delta
// can repair: the passage store is missing, unreadable or damaged, or the index
// is in a store format/state the update cannot edit in place. The caller has to
// rebuild the index from its sources (`gleann index rebuild`, or Sync's
// full-rebuild fallback), which recreates the store.
//
// A transient failure (embedder unreachable, source or plugin read error) is
// NOT wrapped in it: rebuilding would repeat it or re-embed the whole corpus.
var ErrRebuildRequired = errors.New("index update requires a rebuild from its sources")

// requiresRebuild wraps a store-level update failure with ErrRebuildRequired,
// keeping the original error (and any sentinel it carries) reachable through
// errors.Is/As.
func requiresRebuild(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrRebuildRequired, err)
}

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
	if err := CheckStoreFormat(b.config.IndexDir, name); err != nil {
		return err
	}
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

// hnswDTypeSwitch resolves the storage type of the HNSW vectors for NEW
// writes, once where a builder is created:
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
