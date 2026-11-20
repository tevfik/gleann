package gleann

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tevfik/gleann/internal/graphstore"
)

// LeannSearcher performs search on built indexes.
// This mirrors Python LEANN's LeannSearcher.
type LeannSearcher struct {
	config   Config
	backend  BackendSearcher
	passages PassageStore
	warnings []string
	meta     IndexMeta
	embedder EmbeddingComputer
	scorer   Scorer
	// customScorer is set by SetScorer: the caller's scorer (or nil, which
	// disables the lexical leg) replaces the index's own FTS5 lexical index.
	customScorer     bool
	reranker         Reranker
	embServer        EmbeddingServer
	graphDB          GraphDB
	embedderResolver EmbedderResolver
	expander         QueryExpander

	langOnce sync.Once
	language string // dominant source language, for query expansion

	loaded          bool
	vectorAvailable bool
	storeFormat     int // 2 once Load opened the index (format 2 is the only format, RFC 004)
	name            string
	estimatedSize   int64

	// fresh is the published state of the format-2 database recorded at Load
	// (freshKnown false when none was recorded); see freshness.go.
	fresh      indexFreshness
	freshKnown bool
	freshMu    sync.Mutex

	// dstate is the delta file and slot counts of the loaded index (RFC 005);
	// nil for a format-1 index or an index without vectors.
	dstate atomic.Pointer[deltaState]
}

// VectorAvailable reports whether a vector index is loaded and available for search.
func (s *LeannSearcher) VectorAvailable() bool {
	return s.vectorAvailable
}

// EstimatedSize returns the estimated resident memory size of the searcher in bytes.
// If an explicit size was set via SetEstimatedSize, that value is returned.
// Otherwise, it computes the sum of the file sizes of the index artifacts on disk.
func (s *LeannSearcher) EstimatedSize() int64 {
	if s == nil {
		return 0
	}
	if s.estimatedSize > 0 {
		return s.estimatedSize
	}
	name := s.name
	if name == "" {
		name = s.meta.Name
	}
	if name == "" {
		return 0
	}
	indexDir := filepath.Join(s.config.IndexDir, name)
	basePath := filepath.Join(indexDir, name)
	var total int64
	exts := []string{".index", ".delta", ".db", ".meta.json", ".db-wal", ".db-shm"}
	for _, ext := range exts {
		if fi, err := os.Stat(basePath + ext); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// SetEstimatedSize overrides the estimated size for testing.
func (s *LeannSearcher) SetEstimatedSize(size int64) {
	if s != nil {
		s.estimatedSize = size
	}
}

// NewSearcher creates a new LeannSearcher.
func NewSearcher(config Config, embedder EmbeddingComputer) *LeannSearcher {
	config = withIndexDefaults(config)
	return &LeannSearcher{
		config:           config,
		embedder:         embedder,
		embedderResolver: config.EmbedderResolver,
	}
}

// SetScorer replaces the index's own FTS5 lexical leg with a custom scorer
// for hybrid search; nil disables the lexical leg. A scorer that is not
// persisted is fed every passage at Load.
func (s *LeannSearcher) SetScorer(scorer Scorer) {
	s.scorer = scorer
	s.customScorer = true
}

// SetReranker sets a reranker for two-stage retrieval.
func (s *LeannSearcher) SetReranker(reranker Reranker) {
	s.reranker = reranker
}

// SetEmbeddingServer sets the embedding server for recomputation during search.
func (s *LeannSearcher) SetEmbeddingServer(server EmbeddingServer) {
	s.embServer = server
}

// SetEmbedderResolver sets the dynamic embedder resolver for per-index models.
func (s *LeannSearcher) SetEmbedderResolver(resolver EmbedderResolver) {
	s.embedderResolver = resolver
}

// Load loads an index for searching.
func (s *LeannSearcher) Load(ctx context.Context, name string) (retErr error) {
	if err := checkPathName(name); err != nil {
		return err
	}
	s.name = name
	// A previous Load (or a Close that already ran) leaves s.graphDB bound to
	// the store handle this Load is about to replace. Reset it up front so the
	// format-2 attachment below always re-opens, and no adapter outlives its
	// store (a stale adapter would query a closed database). Close on the
	// SQLite backend is a no-op because the graph store is owned by the
	// passage store.
	if s.graphDB != nil {
		s.graphDB.Close()
		s.graphDB = nil
	}
	// Likewise the FTS scorer of a previous Load holds its own handle.
	if fts, ok := s.scorer.(*FTSScorer); ok && !s.customScorer {
		_ = fts.Close()
		s.scorer = nil
	}
	// And the passage store of a previous Load: replacing it without a close
	// leaked the database handle (on Windows the index files then cannot be
	// removed or replaced until the process exits).
	if s.passages != nil {
		_ = s.passages.Close()
		s.passages = nil
	}
	indexDir := filepath.Join(s.config.IndexDir, name)
	basePath := filepath.Join(indexDir, name)

	// An old-format index is refused with the migration hint before anything
	// else is read, also when its meta.json is missing.
	if err := CheckStoreFormat(s.config.IndexDir, name); err != nil {
		return err
	}

	// Load metadata.
	metaPath := basePath + ".meta.json"
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}
	// A fresh value, not the previous index's: omitempty fields absent from
	// this JSON would otherwise keep the values of an earlier Load.
	var meta IndexMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return fmt.Errorf("unmarshal metadata: %w", err)
	}
	s.meta = meta

	// Schema version check for backwards compatibility
	expectedVersion := "1.0.0"
	if s.meta.Version != "" && s.meta.Version != expectedVersion {
		log.Printf("⚠  WARNING: Index %q was built with version %q but current runtime expects %q. "+
			"The system will attempt to read it, but if errors occur, please rebuild with: gleann build %s --docs <dir>",
			name, s.meta.Version, expectedVersion, name)
	}

	// Dynamically resolve dedicated embedder if index specifies an embedding model
	if s.embedderResolver != nil && s.meta.EmbeddingModel != "" {
		needsResolve := s.embedder == nil ||
			s.embedder.ModelName() != s.meta.EmbeddingModel ||
			(s.meta.Dimensions > 0 && s.embedder.Dimensions() > 0 && s.embedder.Dimensions() != s.meta.Dimensions)
		if needsResolve {
			if resolved, err := s.embedderResolver.ResolveEmbedder(ctx, s.meta.EmbeddingModel, s.meta.Dimensions); err == nil && resolved != nil {
				s.embedder = resolved
			}
		}
	}

	// Warn if the current embedding model differs from what was used to build the index.
	if s.embedder != nil && s.meta.EmbeddingModel != "" && s.embedder.ModelName() != "" {
		if !SameEmbeddingModel(s.embedder.ModelName(), s.meta.EmbeddingModel) {
			log.Printf("⚠  WARNING: Index %q was built with embedding model %q (%d dims) "+
				"but current config uses %q — search results will be incorrect! "+
				"To migrate to the new model, run: gleann index rebuild %s --docs <dir>",
				name, s.meta.EmbeddingModel, s.meta.Dimensions, s.embedder.ModelName(), name)
		}
	}

	// Strictly validate embedding dimensions to prevent backend crashes or corruption.
	if !s.meta.LexicalOnly && s.embedder != nil && s.meta.Dimensions > 0 && s.embedder.Dimensions() > 0 && s.embedder.Dimensions() != s.meta.Dimensions {
		return fmt.Errorf("embedding dimension mismatch: index %q expects %d dims (%s), but embedder provides %d dims (%s). Rebuild index: gleann index rebuild %s --docs <dir>",
			name, s.meta.Dimensions, s.meta.EmbeddingModel, s.embedder.Dimensions(), s.embedder.ModelName(), name)
	}

	// Load passages in read-only mode from the SQLite record store. An index
	// in the old store format is refused (*OldFormatError).
	ps, err := OpenPassageStore(basePath, true)
	if err != nil {
		if errors.Is(err, ErrOldStoreFormat) {
			return err
		}
		return fmt.Errorf("load passages: %w", err)
	}
	s.passages = ps
	s.storeFormat = 2
	// Every later failure of this Load releases the store again.
	defer func() {
		if retErr != nil {
			if s.passages != nil {
				_ = s.passages.Close()
				s.passages = nil
			}
			// The format-2 graph adapter shares the passage store's handle,
			// which the close above just released; clear it so a retry
			// reattaches instead of querying the closed store.
			if s.graphDB != nil {
				s.graphDB.Close()
				s.graphDB = nil
			}
		}
	}()
	// The format-2 opener may have rolled an interrupted vector publication
	// forward and synced meta.json (recoverVectorPublication). Re-read the
	// metadata so this Load sees the published state — VectorReady, the
	// generation — instead of the pre-recovery copy read above.
	if s.storeFormat == 2 {
		if metaData, err := os.ReadFile(metaPath); err == nil {
			// Decode into a fresh value: omitempty fields such as
			// lexical_only are absent from the JSON when false, and
			// unmarshalling over the old struct would keep their old value.
			var fresh IndexMeta
			if err := json.Unmarshal(metaData, &fresh); err != nil {
				return fmt.Errorf("unmarshal metadata after recovery: %w", err)
			}
			s.meta = fresh
		}
	}
	// Record the published state before anything is read from it, so a
	// change that lands during this Load is seen as stale afterwards.
	if err := s.recordFreshness(ctx); err != nil {
		return err
	}
	if err := s.passages.Load(); err != nil {
		return fmt.Errorf("load passages: %w", err)
	}

	s.warnings = nil
	s.checkHealth(name)

	// Check if vector index exists and is ready
	indexPath := basePath + ".index"
	s.vectorAvailable = false
	s.dstate.Store(nil)
	if s.backend != nil {
		_ = s.backend.Close()
		s.backend = nil
	}
	if _, err := os.Stat(indexPath); err == nil && !s.meta.LexicalOnly {
		if err := s.loadVectorFiles(ctx, basePath); err != nil {
			return err
		}
		s.vectorAvailable = true
	}

	// The code graph lives in the index's own SQLite database
	// (symbols/edges/doc_nodes tables, RFC 003 sections 3.4 and 3.7), so a
	// format-2 index with graph data gets graph queries in every build
	// including CGO_ENABLED=0, sharing the passage store's connection pools
	// (RFC 003 section 8 step 7). A format-1 index has no code graph; an old
	// <name>_graph Kùzu directory next to it is never read (RFC 004 A3).
	if s.storeFormat == 2 {
		if sp, ok := s.passages.(*storePassages); ok && sp.st != nil {
			gs := graphstore.New(sp.st)
			has, err := gs.HasData()
			if err != nil {
				log.Printf("⚠  WARNING: Checking graph data for index %q failed: %v", name, err)
			} else if has {
				s.graphDB = &sqliteGraphDB{g: gs}
			}
		}
	}

	// The lexical leg is the index's own FTS5 table: the scorer reads
	// <name>.db directly. A custom scorer (or nil, set to disable lexical
	// search) is left untouched.
	if !s.customScorer {
		fts, err := s.openFormat2Scorer(ctx)
		if err != nil {
			return fmt.Errorf("opening the lexical index: %w", err)
		}
		s.scorer = fts
		defer func() {
			// A later step of Load failed: release the database handle, so a
			// retry starts clean.
			if retErr != nil {
				_ = fts.Close()
				s.scorer = nil
			}
		}()
	}

	// A custom scorer that is not persisted is fed every passage.
	// For large corpora, use streaming to avoid loading all passages into RAM.
	if s.scorer != nil {
		if ps, ok := s.scorer.(PersistedScorer); ok && ps.Persisted() {
			// A persisted scorer's index already lives on disk: skip the whole
			// passage scan / AddDocuments build, and MaxBM25Passages does not
			// apply to it (RFC 003 section 3.1). This is what removes the cold
			// open's tokenising pass (IDX-14).
			s.loaded = true
			return nil
		}
		numPassages := s.passages.Count()
		maxBM25 := s.config.SearchConfig.MaxBM25Passages
		if maxBM25 > 0 && numPassages > maxBM25 {
			log.Printf("ℹ️  Corpus has %d passages, BM25 limited to %d (MaxBM25Passages). Using streaming index.",
				numPassages, maxBM25)
		}

		if numPassages > 100_000 {
			// Streaming: build BM25 index without loading all passages into RAM cache.
			log.Printf("ℹ️  Large corpus (%d passages), building BM25 index via streaming...", numPassages)
			indexed := 0
			if err := s.passages.ForEachPassage(func(p Passage) error {
				if maxBM25 > 0 && indexed >= maxBM25 {
					return nil
				}
				s.scorer.AddDocuments([]Passage{p})
				indexed++
				return nil
			}); err != nil {
				return fmt.Errorf("stream passages for BM25: %w", err)
			}
			log.Printf("ℹ️  BM25 index built: %d passages indexed", indexed)
		} else {
			// Small corpus: load all into cache (fast path).
			limit := 0
			if maxBM25 > 0 {
				limit = maxBM25
			}
			if err := s.passages.LoadAllWithLimit(limit); err != nil {
				return fmt.Errorf("load all passages for BM25: %w", err)
			}
			s.scorer.AddDocuments(s.passages.All())
		}
	}

	s.loaded = true
	return nil
}

// Search performs a search and returns results. When another process
// published a new vector generation while the query ran, the result is
// discarded with ErrIndexChanged instead of returning passages resolved
// through renumbered slots (see freshness.go).
func (s *LeannSearcher) Search(ctx context.Context, query string, opts ...SearchOption) ([]SearchResult, error) {
	results, err := s.search(ctx, query, opts...)
	if err != nil {
		return nil, err
	}
	if stale, serr := s.mainChanged(ctx); stale {
		if serr != nil {
			return nil, fmt.Errorf("%w: %v", ErrIndexChanged, serr)
		}
		return nil, ErrIndexChanged
	}
	return results, nil
}

func (s *LeannSearcher) search(ctx context.Context, query string, opts ...SearchOption) ([]SearchResult, error) {
	if !s.loaded {
		return nil, fmt.Errorf("no index loaded; call Load() first")
	}

	// Apply options.
	searchOpts := s.config.SearchConfig
	for _, opt := range opts {
		opt(&searchOpts)
	}

	topK := searchOpts.TopK
	if topK <= 0 {
		topK = 10
	}

	// When reranking is enabled, fetch more candidates from stage-1
	// so the reranker has a richer pool to work with.
	activeReranker := searchOpts.CustomReranker
	if activeReranker == nil {
		activeReranker = s.reranker
	}

	retrieveK := topK * 2
	if activeReranker != nil && searchOpts.UseReranker {
		retrieveK = topK * 4
		if retrieveK < 50 {
			retrieveK = 50
		}
		// RerankPool only limits how many candidates reach the reranker;
		// the first stage keeps its wide lists. Narrowing the dense and
		// BM25 lists to the pool size lowers the quality of the pool
		// itself (gleann benchmark: MiniLM rerank of the top 20 gave MRR
		// 0.535 with 20-wide lists vs 0.58 with 50-wide lists).
		if searchOpts.RerankPool > 0 {
			retrieveK = max(retrieveK, searchOpts.RerankPool, topK)
		}
	}
	if searchOpts.OnePerFile && retrieveK < topK*4 {
		retrieveK = topK * 4
	}
	if len(searchOpts.MetadataFilters) > 0 {
		if retrieveK < topK*4 {
			retrieveK = topK * 4
		}
		if retrieveK < 50 {
			retrieveK = 50
		}
	}

	// Text used for embedding and BM25 (query + HyDE expansion when
	// enabled). Symbol boosts and the reranker keep the original query.
	rq := s.retrievalQuery(ctx, query, searchOpts)

	// Metadata filters (WP-2.5): the widen-until-k loop below runs first for
	// both store formats — it is cheap and answers the common case. Only when
	// it stops at filterPoolCap with fewer than k results does a format-2
	// index fall back to an exact SQL pre-selection (see searchPreselected).
	var filterEngine *MetadataFilterEngine
	if len(searchOpts.MetadataFilters) > 0 {
		filterEngine = NewMetadataFilterEngine(searchOpts.MetadataFilters)
		if searchOpts.FilterLogic != "" {
			filterEngine.Logic = searchOpts.FilterLogic
		}
	}

	// Fallback to pure lexical search if vector backend is not available or lexical-only search is requested
	if !s.vectorAvailable || s.backend == nil || searchOpts.HybridAlpha == 0.0 {
		if filterEngine != nil {
			return s.searchLexicalFiltered(ctx, query, rq, searchOpts, topK, retrieveK, activeReranker, filterEngine)
		}
		results, err := s.SearchBM25(ctx, rq, retrieveK)
		if err != nil {
			return nil, err
		}
		if searchOpts.OnePerFile {
			results = onePerFile(results)
		}
		if activeReranker != nil && searchOpts.UseReranker && len(results) > 0 {
			return rerankStage(ctx, activeReranker, query, results, topK, searchOpts), nil
		}
		if len(results) > topK {
			results = results[:topK]
		}
		return results, nil
	}

	// Compute query embedding.
	if s.embedder == nil {
		if filterEngine != nil {
			return s.searchLexicalFiltered(ctx, query, rq, searchOpts, topK, retrieveK, activeReranker, filterEngine)
		}
		return s.SearchBM25(ctx, rq, topK)
	}
	queryEmb, err := s.embedder.ComputeSingle(ctx, s.meta.QueryPrefix+rq) // the prefix the index was built for
	if err != nil {
		if s.scorer != nil {
			log.Printf("⚠  Embedder error (%v); falling back to pure BM25 retrieval", err)
			if filterEngine != nil {
				return s.searchLexicalFiltered(ctx, query, rq, searchOpts, topK, retrieveK, activeReranker, filterEngine)
			}
			return s.SearchBM25(ctx, rq, topK)
		}
		return nil, fmt.Errorf("compute query embedding: %w", err)
	}

	// A query vector of the wrong size makes DiskANN panic and HNSW return
	// meaningless results (C-07).
	if s.meta.Dimensions > 0 && len(queryEmb) != s.meta.Dimensions {
		return nil, fmt.Errorf("query embedding has %d dimensions but index was built with %d (model %q); use the index's embedding model or rebuild the index",
			len(queryEmb), s.meta.Dimensions, s.meta.EmbeddingModel)
	}

	// Widen-until-k (WP-2.5): with metadata filters a single retrieveK
	// retrieval leaves fewer than k results when the filter is selective, so
	// the candidate pool grows (nextFilterPool) until k results pass the filter, every
	// pre-selected match has been examined, both retrieval legs are
	// exhausted, or the pool reaches filterPoolCap. Without filters the loop
	// runs exactly once with the original retrieveK: results, scores and
	// order are unchanged.
	pool := retrieveK
	var results []SearchResult
	var candidateCount, preFilterCount int
	hitCap := false
	// Vector score normalizer, fixed at the first round (pool == retrieveK):
	// the max distance among the first retrieveK candidates — exactly the
	// normalizer the unfiltered search uses. Widening the pool must not
	// change a passage's score, so every later round (and the pre-selection
	// fallback) reuses it instead of the wider pool's max distance.
	var normDist float32
	normSet := false
	dst := s.dstate.Load() // one state for the whole query, whatever a refresh does meanwhile
	for {
		// Vector search. With dead slots the backend is asked for more hits,
		// so that about pool remain once the dead ones are dropped.
		var ids []int64
		var distances []float32
		fetch := overfetch(pool, dst)
		for {
			ids, distances, err = s.vectorSearch(ctx, queryEmb, fetch)
			if err != nil {
				return nil, fmt.Errorf("vector search: %w", err)
			}
			returned := len(ids)
			if ids, distances, err = s.mergeDeltaHits(ctx, dst, queryEmb, ids, distances, pool, fetch); err != nil {
				return nil, fmt.Errorf("vector search: %w", err)
			}
			// The over-fetch assumes the dead slots are spread evenly. When
			// they were not and fewer than pool hits survived although the
			// backend still had more, ask for more.
			capFetch := max(pool, filterPoolCap)
			if dst == nil || dst.dead == 0 || len(ids) >= pool || returned < fetch || fetch >= capFetch {
				break
			}
			fetch = min(fetch*2, capFetch)
		}

		// Raw distances, kept on the results for ranking several indexes together (FuseAcrossIndexes).
		rawDistances := make(map[int64]float32, len(ids))
		for i, id := range ids {
			rawDistances[id] = distances[i]
		}

		// Convert distances to cosine-like scores (higher = better).
		vectorScores := make(map[int64]float32, len(ids))
		maxDist := normDist
		if !normSet {
			for _, d := range distances {
				if d > maxDist {
					maxDist = d
				}
			}
			// Fixed at the first round that returned vector candidates; an
			// empty first round must not pin the normalizer to 0.
			if len(distances) > 0 {
				normDist, normSet = maxDist, true
			}
		}
		for i, id := range ids {
			if maxDist > 0 {
				vectorScores[id] = 1.0 - distances[i]/maxDist
			} else {
				vectorScores[id] = 1.0
			}
		}

		// Hybrid search with BM25 (RRF: Reciprocal Rank Fusion)
		alpha := searchOpts.HybridAlpha
		finalScores := make(map[int64]float32)

		// If BM25 scorer is configured and alpha < 1.0, perform full-corpus lexical retrieval
		var bm25IDs []int64
		bm25Raw := map[int64]float32{}
		if s.scorer != nil && alpha < 1.0 {
			if topKScorer, ok := s.scorer.(TopKScorer); ok {
				var bm25Scores []float32
				bm25IDs, bm25Scores = topKScorer.TopK(rq, pool)
				for i, id := range bm25IDs {
					if i < len(bm25Scores) {
						bm25Raw[id] = bm25Scores[i]
					}
				}
			}
		}

		if len(bm25IDs) > 0 && alpha < 1.0 {
			// Reciprocal Rank Fusion (RRF with k = 60)
			const kRRF = 60.0
			rrfScores := make(map[int64]float32)

			for i, id := range ids {
				rank := float32(i + 1)
				rrfScores[id] += alpha * (1.0 / (kRRF + rank))
			}
			for i, id := range bm25IDs {
				rank := float32(i + 1)
				rrfScores[id] += (1.0 - alpha) * (1.0 / (kRRF + rank))
			}

			// Normalize RRF scores relative to theoretical maximum rank 1 in both
			maxRRFPossible := alpha*(1.0/(kRRF+1.0)) + (1.0-alpha)*(1.0/(kRRF+1.0))
			if maxRRFPossible <= 0 {
				maxRRFPossible = 1.0 / (kRRF + 1.0)
			}
			for id, rrf := range rrfScores {
				normScore := rrf / maxRRFPossible
				if normScore > 1.0 {
					normScore = 1.0
				}
				finalScores[id] = normScore
			}
		} else {
			finalScores = vectorScores
		}

		results = s.buildBoostedResults(query, rq, searchOpts, finalScores, rawDistances, bm25Raw)

		candidateCount = len(finalScores)
		preFilterCount = len(results)

		// Apply metadata filters if configured (BEFORE topK truncation).
		if filterEngine != nil {
			results = filterEngine.FilterResults(results)
		}

		if searchOpts.OnePerFile {
			results = onePerFile(results)
		}

		if filterEngine == nil {
			break // unfiltered: one retrieval, exactly the previous behaviour
		}
		if len(results) >= topK {
			break // k results pass the filter
		}
		if len(ids) < pool && len(bm25IDs) < pool {
			break // both legs returned fewer than requested: the index is exhausted
		}
		next := nextFilterPool(pool, len(results), preFilterCount, topK)
		if next <= pool {
			hitCap = true
			break // the cap bounds the work
		}
		pool = next
	}

	// Exact pre-selection fallback (WP-2.5): the loop hit the pool cap with
	// fewer than k results — a highly selective filter whose matches rank deep.
	// On a format-2 index, pre-select the matching IDs in SQL and score only
	// those; otherwise the loop's result stands.
	if hitCap && len(results) < topK && filterEngine != nil && s.storeFormat == 2 {
		if pre, exact, ok := s.searchPreselected(ctx, query, rq, queryEmb, searchOpts, topK, filterEngine, normDist); ok {
			if exact {
				results = pre
			} else {
				results = mergeLexicalFill(results, pre, topK, searchOpts.MinScore)
			}
		}
	}

	if candidateCount > 0 && preFilterCount == 0 && searchOpts.MinScore == 0 && searchOpts.Kind == "" {
		log.Printf("⚠  WARNING: Index %q backend returned candidate vectors, but none were found in the passages database! The index is desynchronized. Run: gleann index rebuild %s", s.meta.Name, s.meta.Name)
	}

	// Truncate to topK if not reranking (reranker handles topK internally).
	if activeReranker != nil && searchOpts.UseReranker {
		results = rerankStage(ctx, activeReranker, query, results, topK, searchOpts)
	} else if len(results) > topK {
		results = results[:topK]
	}

	// Graph-augmented search: enrich results with caller/callee context.
	if searchOpts.UseGraphContext && s.graphDB != nil {
		s.enrichWithGraphContext(results)
	}

	return results, nil
}

// vectorSearch runs the vector leg against the main file: through the
// recomputation server when one runs, through the memory-mapped graph with an
// ad-hoc recomputer otherwise, or against stored embeddings.
func (s *LeannSearcher) vectorSearch(ctx context.Context, queryEmb []float32, fetch int) ([]int64, []float32, error) {
	if s.embServer != nil && s.embServer.IsRunning() {
		// Use dedicated recomputation server if available
		return s.backend.SearchWithRecompute(ctx, queryEmb, fetch, s.embServer.ComputeEmbeddings)
	}
	if _, isMmap := s.backend.(MmapBackendSearcher); isMmap {
		// If using Mmap Searcher but no dedicated embServer, create an ad-hoc recomputer
		// so that the graph can traverse locally.
		adHocRecompute := func(ctx context.Context, targetIDs []int64) ([][]float32, error) {
			// A dead slot (its passage was removed or replaced) has no
			// text to embed: it gets a nil vector, which the graph
			// traversal skips, instead of the embedding of "".
			out := make([][]float32, len(targetIDs))
			texts := make([]string, 0, len(targetIDs))
			at := make([]int, 0, len(targetIDs))
			for i, id := range targetIDs {
				if passage, pErr := s.passages.Get(id); pErr == nil {
					texts = append(texts, passage.Text)
					at = append(at, i)
				}
			}
			if len(texts) == 0 {
				return out, nil
			}
			vecs, cErr := s.embedder.Compute(ctx, withPrefix(s.meta.PassagePrefix, texts)) // as the index was built
			if cErr != nil {
				return nil, cErr
			}
			if len(vecs) != len(texts) {
				return nil, fmt.Errorf("embedder returned %d vectors for %d texts", len(vecs), len(texts))
			}
			for j, i := range at {
				out[i] = vecs[j]
			}
			return out, nil
		}
		// HNSW/mmap passes context natively if we wrapper it or we just ignore the inner errors.
		return s.backend.SearchWithRecompute(ctx, queryEmb, fetch, adHocRecompute)
	}
	// Standard RAM search with stored embeddings.
	return s.backend.Search(ctx, queryEmb, fetch)
}

// enrichWithGraphContext enriches search results with graph-derived context.
// For each result that has a "source" metadata field, it looks up symbols
// declared in that file and fetches their callers/callees from the graph.
// It also fetches the hierarchical document summary and folder name.
func (s *LeannSearcher) enrichWithGraphContext(results []SearchResult) {
	if s.graphDB == nil {
		return
	}

	// Cache lookups to avoid duplicate queries.
	fileSymbolCache := make(map[string][]Callee)
	docContextCache := make(map[string]*DocumentContextData)

	for i := range results {
		source, _ := results[i].Metadata["source"].(string)
		if source == "" {
			continue
		}

		graphCtx := &GraphContextInfo{}
		hasContext := false

		// 1. Fetch Document Hierarchy Context
		docCtx, ok := docContextCache[source]
		if !ok {
			var err error
			docCtx, err = s.graphDB.DocumentContext(source)
			if err != nil {
				docCtx = nil // graceful degradation
			}
			docContextCache[source] = docCtx
		}

		if docCtx != nil {
			graphCtx.DocumentContext = docCtx
			hasContext = true
		}

		// 2. Fetch Symbol/Code Context
		symbols, ok := fileSymbolCache[source]
		if !ok {
			var err error
			symbols, err = s.graphDB.SymbolsInFile(source)
			if err != nil {
				symbols = nil // graceful degradation
			}
			fileSymbolCache[source] = symbols
		}

		if len(symbols) > 0 {
			maxSymbols := 5
			if len(symbols) < maxSymbols {
				maxSymbols = len(symbols)
			}

			graphCtx.Symbols = make([]SymbolNeighbors, 0, maxSymbols)

			for _, sym := range symbols[:maxSymbols] {
				sn := SymbolNeighbors{
					FQN:  sym.FQN,
					Kind: sym.Kind,
				}

				if callees, err := s.graphDB.Callees(sym.FQN); err == nil {
					for _, c := range callees {
						sn.Callees = append(sn.Callees, c.FQN)
					}
				}

				if callers, err := s.graphDB.Callers(sym.FQN); err == nil {
					for _, c := range callers {
						sn.Callers = append(sn.Callers, c.FQN)
					}
				}

				if len(sn.Callers) > 0 || len(sn.Callees) > 0 {
					graphCtx.Symbols = append(graphCtx.Symbols, sn)
				}
			}

			if len(graphCtx.Symbols) > 0 {
				hasContext = true
			}
		}

		if hasContext {
			results[i].GraphContext = graphCtx
		}
	}
}

// Close releases resources.
func (s *LeannSearcher) Close() error {
	var errs []error
	if s.backend != nil {
		if err := s.backend.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.passages != nil {
		if err := s.passages.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.graphDB != nil {
		s.graphDB.Close()
		s.graphDB = nil
	}
	// The index's FTS scorer was put in place by Load; release it so a later
	// Load on this searcher opens a fresh one.
	if fts, ok := s.scorer.(*FTSScorer); ok && !s.customScorer {
		if err := fts.Close(); err != nil {
			errs = append(errs, err)
		}
		s.scorer = nil
	}
	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}

// Meta returns the index metadata.
func (s *LeannSearcher) Meta() IndexMeta {
	return s.meta
}

// SetMeta updates the in-memory metadata.
func (s *LeannSearcher) SetMeta(meta IndexMeta) {
	s.meta = meta
}

// GraphDB returns the underlying Graph DB connection, or nil if none exists.
func (s *LeannSearcher) GraphDB() GraphDB {
	return s.graphDB
}

// PassageManager returns the internal passage store containing all indexed
// chunks (the SQLite record store).
func (s *LeannSearcher) PassageManager() PassageStore {
	return s.passages
}

func (s *LeannSearcher) Backend() BackendSearcher {
	return s.backend
}

// SearchOption modifies search parameters.
type SearchOption func(*SearchConfig)

// WithTopK sets the number of results to return.
func WithTopK(k int) SearchOption {
	return func(c *SearchConfig) {
		c.TopK = k
	}
}

// WithHybridAlpha sets the vector vs BM25 weight.
func WithHybridAlpha(alpha float32) SearchOption {
	return func(c *SearchConfig) {
		c.HybridAlpha = alpha
	}
}

// WithMinScore sets the minimum score threshold.
func WithMinScore(score float32) SearchOption {
	return func(c *SearchConfig) {
		c.MinScore = score
	}
}

// WithReranker enables reranking for this search query.
func WithReranker(enabled bool) SearchOption {
	return func(c *SearchConfig) {
		c.UseReranker = enabled
	}
}

// WithCustomReranker sets a custom reranker instance for this search without modifying the searcher.
func WithCustomReranker(r Reranker) SearchOption {
	return func(c *SearchConfig) {
		c.UseReranker = true
		c.CustomReranker = r
	}
}

// WithGraphContext enables graph-augmented search.
// Each result is enriched with symbols from the same source file
// and their caller/callee relationships from the code graph.
func WithGraphContext(enabled bool) SearchOption {
	return func(c *SearchConfig) {
		c.UseGraphContext = enabled
	}
}

// WithMetadataFilters sets metadata filter conditions for the search.
func WithMetadataFilters(filters []MetadataFilter, logic ...string) SearchOption {
	return func(c *SearchConfig) {
		c.MetadataFilters = filters
		if len(logic) > 0 {
			c.FilterLogic = logic[0]
		}
	}
}

// WithRerankSkipIdentifiers keeps identifier-like queries in first-stage order.
func WithRerankSkipIdentifiers(skip bool) SearchOption {
	return func(c *SearchConfig) {
		c.RerankSkipIdentifiers = skip
	}
}

// WithRerankPool sets how many stage-1 candidates the reranker scores. The
// default is the reranker's preferred pool (20 for the built-in engine), else
// everything the first stage retrieved (max(4*top-k, 50)).
func WithRerankPool(n int) SearchOption {
	return func(c *SearchConfig) {
		c.RerankPool = n
	}
}

// WithOnePerFile keeps only the best chunk of each file in the results.
func WithOnePerFile(enabled bool) SearchOption {
	return func(c *SearchConfig) {
		c.OnePerFile = enabled
	}
}

// onePerFile keeps the first (best-ranked) result of every file, preserving
// order. Results without file metadata are kept as they are.
func onePerFile(results []SearchResult) []SearchResult {
	seen := make(map[string]bool, len(results))
	out := results[:0:0]
	for _, r := range results {
		f, _ := r.Metadata["file"].(string)
		if f == "" {
			f, _ = r.Metadata["source"].(string)
		}
		if f != "" {
			if seen[f] {
				continue
			}
			seen[f] = true
		}
		out = append(out, r)
	}
	return out
}

// WithIncludeTests enables or disables test inclusion without score demotion.
func WithIncludeTests(include bool) SearchOption {
	return func(c *SearchConfig) {
		c.IncludeTests = include
	}
}

// WithKind sets the artifact kind filter ("code", "docs", or "all").
func WithKind(kind string) SearchOption {
	return func(c *SearchConfig) {
		c.Kind = kind
	}
}

// ListIndexes returns all available indexes in the configured directory.
func ListIndexes(indexDir string) ([]IndexMeta, error) {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Windows reports reading a regular file as a directory as "path
			// not found", which IsNotExist accepts: tell that apart from an
			// index directory that does not exist yet.
			if fi, statErr := os.Stat(indexDir); statErr == nil && !fi.IsDir() {
				return nil, fmt.Errorf("read index directory: %s is not a directory", indexDir)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("read index directory: %w", err)
	}

	var indexes []IndexMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(indexDir, entry.Name(), entry.Name()+".meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta IndexMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		indexes = append(indexes, meta)
	}

	return indexes, nil
}

// GetIndexMeta reads the metadata for a specific index.
func GetIndexMeta(indexDir, name string) (*IndexMeta, error) {
	if err := checkPathName(name); err != nil {
		return nil, err
	}
	metaPath := filepath.Join(indexDir, name, name+".meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read index metadata: %w", err)
	}
	var meta IndexMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal index metadata: %w", err)
	}
	return &meta, nil
}

// UpdateIndexMeta atomically updates the metadata file of an existing index.
func UpdateIndexMeta(indexDir, name string, updateFn func(*IndexMeta)) error {
	if err := checkPathName(name); err != nil {
		return err
	}
	meta, err := GetIndexMeta(indexDir, name)
	if err != nil {
		return err
	}
	updateFn(meta)
	meta.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index metadata: %w", err)
	}

	metaPath := filepath.Join(indexDir, name, name+".meta.json")
	tmpPath := metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp metadata: %w", err)
	}
	return os.Rename(tmpPath, metaPath)
}

// ListIndexesByTag returns all indexes containing the given tag.
func ListIndexesByTag(indexDir, tag string) ([]IndexMeta, error) {
	all, err := ListIndexes(indexDir)
	if err != nil {
		return nil, err
	}
	var matched []IndexMeta
	for _, idx := range all {
		if idx.HasTag(tag) {
			matched = append(matched, idx)
		}
	}
	return matched, nil
}

// RemoveIndex removes an index and all its associated files (vector index, graph database, sync state).
func RemoveIndex(indexDir, name string) error {
	// Never let a crafted name (e.g. "..") escape indexDir: RemoveAll below
	// would otherwise delete arbitrary directories.
	if err := checkPathName(name); err != nil {
		return err
	}
	// 1. Remove the main index directory (contains .index, .passages.jsonl, .meta.json, etc.)
	indexPath := filepath.Join(indexDir, name)
	if err := os.RemoveAll(indexPath); err != nil {
		return fmt.Errorf("remove index directory: %w", err)
	}

	// 2. Remove the old Kùzu graph directory of a v1.9 index, if any: index
	// remove deletes everything of an index, including leftovers.
	graphPath := filepath.Join(indexDir, name+"_graph")
	if err := os.RemoveAll(graphPath); err != nil {
		return fmt.Errorf("remove graph database: %w", err)
	}

	// Its companions: the Kùzu write-ahead log and shadow file and the v1.9
	// per-file content hash store.
	for _, suffix := range []string{"_graph.wal", "_graph.shadow", "_graph_file_hashes.db"} {
		if err := os.Remove(filepath.Join(indexDir, name+suffix)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", name+suffix, err)
		}
	}

	// A rollback copy left by an interrupted sync and the cross-process rewrite
	// lock would otherwise make a later index of this name look half-synced.
	if err := os.RemoveAll(indexPath + ".sync-bak"); err != nil {
		return fmt.Errorf("remove rollback copy: %w", err)
	}
	if err := os.Remove(filepath.Join(indexDir, name+".rewrite.lock")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove rewrite lock: %w", err)
	}
	// The format-2 build/migrate lock lives beside the index directory (RFC 003
	// section 3.6). The SQLite store, its WAL companions and the .v1 rollback
	// copies are inside <name>/ and go with the directory removed above.
	if err := os.Remove(filepath.Join(indexDir, name+".lock")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove index lock: %w", err)
	}
	// The v1.9 lexical sidecar lived inside <name>/; its rebuild lock beside it.
	if err := os.Remove(filepath.Join(indexDir, name+".lex.lock")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove lexical lock: %w", err)
	}

	// 3. Remove the sync state file if it exists
	syncPath := filepath.Join(indexDir, name+".sync.json")
	if err := os.Remove(syncPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove sync state: %w", err)
	}

	return nil
}

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float32 {
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denominator := float32(math.Sqrt(float64(normA)) * math.Sqrt(float64(normB)))
	if denominator == 0 {
		return 0
	}
	return dot / denominator
}

// SearchBM25 performs pure lexical retrieval using the configured BM25 scorer across the entire corpus.
// It directly retrieves the top-K matching passages without requiring vector embeddings.
func (s *LeannSearcher) SearchBM25(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	if s.scorer == nil {
		return nil, fmt.Errorf("BM25 scorer is not configured")
	}

	topKScorer, ok := s.scorer.(TopKScorer)
	if !ok {
		return nil, fmt.Errorf("configured scorer does not support direct TopK corpus retrieval")
	}

	ids, scores := topKScorer.TopK(query, topK)
	results := make([]SearchResult, 0, len(ids))
	for i, id := range ids {
		p, err := s.passages.Get(id)
		if err != nil {
			continue
		}
		bm25 := scores[i]
		results = append(results, SearchResult{
			ID:        id,
			Score:     scores[i],
			Text:      p.Text,
			Metadata:  p.Metadata,
			BM25Score: &bm25,
			pid:       p.PID,
		})
	}
	if s.storeFormat == 2 {
		// RFC 003 section 3.4 (IDX-06): order by score, then stable pid.
		// Format 1 keeps the scorer's order unchanged.
		sort.Slice(results, func(i, j int) bool {
			if results[i].Score != results[j].Score {
				return results[i].Score > results[j].Score
			}
			return results[i].pid < results[j].pid
		})
	}
	return results, nil
}

// searchLexicalFiltered is the widen-until-k loop of the pure lexical leg
// (WP-2.5): no vector backend, HybridAlpha 0, or an embedder that is absent or
// failed. It grows the candidate pool geometrically until k results pass the
// metadata filter, every pre-selected match has been examined, the scorer
// returns fewer than requested (corpus exhausted) or the pool reaches
// filterPoolCap, then applies the same onePerFile/rerank/topK tail as the
// unfiltered lexical path.
func (s *LeannSearcher) searchLexicalFiltered(ctx context.Context, query, rq string, searchOpts SearchConfig, topK, retrieveK int, activeReranker Reranker, engine *MetadataFilterEngine) ([]SearchResult, error) {
	pool := retrieveK
	var results []SearchResult
	hitCap := false
	for {
		raw, err := s.SearchBM25(ctx, rq, pool)
		if err != nil {
			return nil, err
		}
		results = engine.FilterResults(raw)
		if searchOpts.OnePerFile {
			results = onePerFile(results)
		}
		if len(results) >= topK {
			break
		}
		if len(raw) < pool {
			break // the scorer returned fewer than requested: corpus exhausted
		}
		next := nextFilterPool(pool, len(results), len(raw), topK)
		if next <= pool {
			hitCap = true
			break
		}
		pool = next
	}
	// Exact pre-selection fallback (WP-2.5): the loop hit the cap with fewer
	// than k results on a format-2 index. The lexical leg has no query vector,
	// so the pre-selected matches are scored lexically.
	if hitCap && len(results) < topK && s.storeFormat == 2 {
		if pre, _, ok := s.searchPreselected(ctx, query, rq, nil, searchOpts, topK, engine, 0); ok {
			results = pre
		}
	}
	if activeReranker != nil && searchOpts.UseReranker && len(results) > 0 {
		return rerankStage(ctx, activeReranker, query, results, topK, searchOpts), nil
	}
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

// buildBoostedResults turns a score map into ranked results, applying the
// symbol/FQN boosts, kind filter, test/vendor demotion and MinScore gate, then
// sorting by score (stable pid tiebreak). It is shared by the widen-until-k
// loop and the exact pre-selection fallback so both rank identically.
func (s *LeannSearcher) buildBoostedResults(query, rq string, searchOpts SearchConfig, finalScores, rawDistances, bm25Raw map[int64]float32) []SearchResult {
	// Build results and apply boosts/penalties:
	// 1. Symbol / FQN exact match boost (+0.35 to +0.5)
	// 2. Kind filtering ("code" vs "docs")
	// 3. Test / vendor demotion (unless searchOpts.IncludeTests is true or query mentions test)
	qClean := strings.TrimSpace(query)
	qLower := strings.ToLower(qClean)
	queryMentionsTest := strings.Contains(qLower, "test") || strings.Contains(qLower, "benchmark")
	// The "contains symbol name" boost also sees the expansion: symbols
	// named in the hypothetical code are the bridge HyDE provides (without
	// it the offline gain of MRR +0.19 vanished end to end).
	containsLower := qLower
	if rq != query {
		containsLower = strings.ToLower(rq)
	}

	results := make([]SearchResult, 0, len(finalScores))
	for id, score := range finalScores {
		passage, err := s.passages.Get(id)
		if err != nil {
			continue
		}

		// Kind filter: "code" vs "docs"
		if searchOpts.Kind != "" && searchOpts.Kind != "all" {
			isDoc := false
			if k, ok := passage.Metadata["kind"].(string); ok && (k == "doc" || k == "documentation") {
				isDoc = true
			} else if ext, ok := passage.Metadata["ext"].(string); ok && (ext == ".md" || ext == ".txt" || ext == ".rst" || ext == ".markdown") {
				isDoc = true
			} else if src, ok := passage.Metadata["source"].(string); ok && (strings.HasSuffix(src, ".md") || strings.HasSuffix(src, ".txt") || strings.HasSuffix(src, ".rst") || strings.HasSuffix(src, ".markdown")) {
				isDoc = true
			} else if file, ok := passage.Metadata["file"].(string); ok && (strings.HasSuffix(file, ".md") || strings.HasSuffix(file, ".txt") || strings.HasSuffix(file, ".rst") || strings.HasSuffix(file, ".markdown")) {
				isDoc = true
			}

			if searchOpts.Kind == "code" && isDoc {
				continue
			}
			if searchOpts.Kind == "docs" && !isDoc {
				continue
			}
		}

		// Symbol and FQN boost
		if name, ok := passage.Metadata["name"].(string); ok && name != "" {
			if strings.EqualFold(qClean, name) {
				score += 0.5 // Exact symbol match
			} else if strings.Contains(containsLower, strings.ToLower(name)) && len(name) >= 3 {
				score += 0.2 // Query contains symbol name
			}
		}
		if fqn, ok := passage.Metadata["fqn"].(string); ok && fqn != "" {
			if strings.EqualFold(qClean, fqn) || strings.HasSuffix(strings.ToLower(fqn), "."+qLower) {
				score += 0.5 // Exact FQN match
			}
			// Graph-assisted ranking (T13): boost symbols with verified AST callers/centrality
			if s.graphDB != nil {
				if callers, err := s.graphDB.Callers(fqn); err == nil && len(callers) > 0 {
					boost := float32(math.Log(1.0+float64(len(callers)))) * 0.05
					if boost > 0.15 {
						boost = 0.15
					}
					score += boost
				}
			}
		}

		// Test demotion
		isTest := false
		if it, ok := passage.Metadata["is_test"].(bool); ok && it {
			isTest = true
		} else {
			for _, key := range []string{"source", "file"} {
				if path, ok := passage.Metadata[key].(string); ok && path != "" {
					base := filepath.Base(path)
					if strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") ||
						strings.HasSuffix(base, ".spec.ts") || strings.Contains(path, "/test/") ||
						strings.Contains(path, "/tests/") {
						isTest = true
						break
					}
				}
			}
		}
		if isTest && !searchOpts.IncludeTests && !queryMentionsTest {
			score *= 0.5
		}

		// Vendor demotion
		isVendor := false
		if iv, ok := passage.Metadata["is_vendor"].(bool); ok && iv {
			isVendor = true
		} else {
			for _, key := range []string{"source", "file"} {
				if path, ok := passage.Metadata[key].(string); ok && path != "" {
					if strings.Contains(path, "vendor/") || strings.Contains(path, "third_party/") || strings.Contains(path, "node_modules/") {
						isVendor = true
						break
					}
				}
			}
		}
		if isVendor {
			score *= 0.3
		}

		// MinScore gate. With no threshold (MinScore <= 0) every candidate
		// is kept, including negative scores: under the fixed normalizer a
		// candidate deeper than the first retrieveK can score below 0, and
		// an unfiltered search of a larger topK would keep it (its wider
		// pool normalizes it to a small positive score). Dropping it here
		// would silently lose filtered matches.
		if score >= searchOpts.MinScore || searchOpts.MinScore <= 0 {
			r := SearchResult{
				ID:       id,
				Text:     passage.Text,
				Score:    score,
				Metadata: passage.Metadata,
				pid:      passage.PID,
			}
			if d, ok := rawDistances[id]; ok {
				r.VectorDistance = &d
			}
			if b, ok := bm25Raw[id]; ok {
				r.BM25Score = &b
			}
			results = append(results, r)
		}
	}

	// Sort by score descending; equal scores are ordered by the stable
	// passage ID (pid) so results are deterministic across rebuilds (RFC 003
	// section 3.4, IDX-06). Format-1 passages have no pid and keep the
	// previous order.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].pid != "" && results[j].pid != "" {
			return results[i].pid < results[j].pid
		}
		return false
	})
	return results
}

// searchPreselected is the exact format-2 fallback (WP-2.5) for a highly
// selective filter whose matches rank too deep for the widen-until-k loop: it
// pre-selects the matching IDs in SQL (preselectFilterIDs, capped at
// filterPreselectCap, never reading passage text) and scores ONLY those. It
// returns ok=false when pre-selection cannot run (format 1, an inexpressible
// filter, or a non-selective filter over the cap), leaving the loop's result.
//
// Scoring tiers: vector distance for the allowed slots when the backend
// implements VectorIDScorer and a query vector is available (hybrid-fused with
// lexical scores when alpha < 1); else lexical BM25 over the allowed rowids
// (FTSScorer.ScoreRowIDs); else ok=false. queryEmb is nil on the lexical leg.
// normDist is the widen loop's fixed score normalizer (the max distance among
// the first retrieveK candidates), so a pre-selected passage scores exactly as
// it would in the loop; it is 0 on the lexical leg, which never uses tier 1.
// Both tiers rank through buildBoostedResults, so MinScore, Kind, the
// symbol/graph boosts, test/vendor demotion and the pid tiebreak apply
// identically on every path.
//
// exact reports whether the result scores the pre-selected set the way the
// caller's leg scores passages: tier 1, or tier 2 on the lexical leg. Tier 2
// on a vector or hybrid leg (a backend without VectorIDScorer) is a lexical
// stand-in: the caller keeps its own results and only fills up with it
// (mergeLexicalFill), never replacing vector-scored results by BM25 ones.
func (s *LeannSearcher) searchPreselected(ctx context.Context, query, rq string, queryEmb []float32, searchOpts SearchConfig, topK int, engine *MetadataFilterEngine, normDist float32) (results []SearchResult, exact, ok bool) {
	allowed, ran := s.preselectFilterIDs(ctx, engine)
	if !ran {
		return nil, false, false
	}
	if len(allowed) == 0 {
		return []SearchResult{}, true, true // no passage matches the filter
	}

	// Tier 1: vector distance for the allowed slots, when the backend can score
	// explicit IDs and a query vector is available.
	if queryEmb != nil && searchOpts.HybridAlpha > 0 {
		if vd, ok := s.backend.(VectorIDScorer); ok {
			if dst := s.dstate.Load(); dst != nil && dst.delta.Len() > 0 {
				vd = deltaScorer{main: vd, delta: dst.delta}
			}
			slots := make([]int64, 0, len(allowed))
			for id := range allowed {
				if id >= 0 {
					slots = append(slots, id)
				}
			}
			sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
			if len(slots) > 0 {
				dists, err := vd.VectorDistances(ctx, queryEmb, slots)
				if err == nil && len(dists) == len(slots) {
					rawDistances := make(map[int64]float32, len(slots))
					vectorScores := make(map[int64]float32, len(slots))
					for i, id := range slots {
						rawDistances[id] = dists[i]
						if normDist > 0 {
							vectorScores[id] = 1.0 - dists[i]/normDist
						} else {
							vectorScores[id] = 1.0
						}
					}
					finalScores := vectorScores
					bm25Raw := map[int64]float32{}
					alpha := searchOpts.HybridAlpha
					if alpha < 1.0 {
						if fts, ok := s.scorer.(*FTSScorer); ok {
							// As in the main loop, fusion runs only when the
							// lexical leg matched something; otherwise the
							// vector scores stand (fusing with an empty
							// lexical ranking would cap every score at alpha).
							rowids := make([]int64, 0, len(allowed))
							for _, rid := range allowed {
								rowids = append(rowids, rid)
							}
							rowScores := fts.ScoreRowIDs(rq, rowids)
							for id, rid := range allowed {
								if sc, ok := rowScores[rid]; ok {
									bm25Raw[id] = sc
								}
							}
						}
						if len(bm25Raw) > 0 {
							// RRF fuse: vector ranks by distance asc, lexical by
							// score desc (k=60), normalized as in the main loop.
							vOrder := append([]int64(nil), slots...)
							sort.Slice(vOrder, func(i, j int) bool {
								if rawDistances[vOrder[i]] != rawDistances[vOrder[j]] {
									return rawDistances[vOrder[i]] < rawDistances[vOrder[j]]
								}
								return vOrder[i] < vOrder[j]
							})
							bOrder := make([]int64, 0, len(bm25Raw))
							for id := range bm25Raw {
								bOrder = append(bOrder, id)
							}
							sort.Slice(bOrder, func(i, j int) bool {
								if bm25Raw[bOrder[i]] != bm25Raw[bOrder[j]] {
									return bm25Raw[bOrder[i]] > bm25Raw[bOrder[j]]
								}
								return bOrder[i] < bOrder[j]
							})
							const kRRF = 60.0
							rrf := make(map[int64]float32)
							for i, id := range vOrder {
								rrf[id] += alpha * (1.0 / (kRRF + float32(i+1)))
							}
							for i, id := range bOrder {
								rrf[id] += (1.0 - alpha) * (1.0 / (kRRF + float32(i+1)))
							}
							maxRRF := alpha*(1.0/(kRRF+1.0)) + (1.0-alpha)*(1.0/(kRRF+1.0))
							if maxRRF <= 0 {
								maxRRF = 1.0 / (kRRF + 1.0)
							}
							finalScores = make(map[int64]float32, len(rrf))
							for id, r := range rrf {
								ns := r / maxRRF
								if ns > 1.0 {
									ns = 1.0
								}
								finalScores[id] = ns
							}
						}
					}
					results := s.buildBoostedResults(query, rq, searchOpts, finalScores, rawDistances, bm25Raw)
					if searchOpts.OnePerFile {
						results = onePerFile(results)
					}
					return results, true, true
				}
			}
		}
	}

	// Tier 2: lexical BM25 over the allowed rowids (no vector path): only
	// passages the query matches, raw BM25 kept. The scores go through the
	// same buildBoostedResults tail as every other path, so MinScore, Kind,
	// the symbol/graph boosts, test/vendor demotion and the pid tiebreak
	// apply here too.
	if fts, ok := s.scorer.(*FTSScorer); ok {
		rowids := make([]int64, 0, len(allowed))
		for _, rid := range allowed {
			rowids = append(rowids, rid)
		}
		rowScores := fts.ScoreRowIDs(rq, rowids)
		finalScores := make(map[int64]float32, len(rowScores))
		bm25Raw := make(map[int64]float32, len(rowScores))
		for id, rid := range allowed {
			if sc, ok := rowScores[rid]; ok {
				finalScores[id] = sc
				bm25Raw[id] = sc
			}
		}
		results := s.buildBoostedResults(query, rq, searchOpts, finalScores, map[int64]float32{}, bm25Raw)
		if searchOpts.OnePerFile {
			results = onePerFile(results)
		}
		return results, queryEmb == nil || searchOpts.HybridAlpha <= 0, true
	}

	// Tier 3: no way to score the pre-selected set; keep the loop's result.
	return nil, false, false
}

// mergeLexicalFill keeps the widen loop's results (vector or hybrid scored)
// and fills up to topK with lexical pre-selection hits it does not already
// hold, in their BM25 order. The fill is ranked strictly below the weakest
// loop result and carries no vector distance; its raw BM25 scores are not
// comparable with the loop's, so they are mapped onto evenly spaced scores
// under that floor. With a MinScore the mapped scores are held to it too.
func mergeLexicalFill(results, lexical []SearchResult, topK int, minScore float32) []SearchResult {
	if len(results) >= topK || len(lexical) == 0 {
		return results
	}
	seen := make(map[int64]bool, len(results))
	for _, r := range results {
		seen[r.ID] = true
	}
	floor := float32(1)
	if len(results) > 0 {
		floor = results[len(results)-1].Score
	}
	var fill []SearchResult
	for _, r := range lexical {
		if len(results)+len(fill) >= topK {
			break
		}
		if seen[r.ID] {
			continue
		}
		fill = append(fill, r)
	}
	out := results
	for i, r := range fill {
		var sc float32
		if floor > 0 {
			sc = floor * float32(len(fill)-i) / float32(len(fill)+1)
		} else {
			sc = floor - float32(i+1)*1e-3
		}
		if minScore > 0 && sc < minScore {
			break
		}
		r.Score = sc
		r.VectorDistance = nil
		out = append(out, r)
	}
	return out
}

// SearchGraphRAG performs graph-augmented hybrid retrieval. If a knowledge graph is available,
// it enriches seed retrieval candidates with caller/callee neighborhood context.
func (s *LeannSearcher) SearchGraphRAG(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	// First retrieve hybrid candidates
	baseResults, err := s.Search(ctx, query, WithTopK(topK), WithHybridAlpha(0.5))
	if err != nil {
		return nil, err
	}

	if s.graphDB == nil {
		return baseResults, nil
	}

	// Extract candidate file paths and symbol names to probe graph neighbors
	seenPaths := make(map[string]bool)
	for _, r := range baseResults {
		if path, ok := r.Metadata["file"].(string); ok && path != "" {
			seenPaths[path] = true
		} else if path, ok := r.Metadata["source"].(string); ok && path != "" {
			seenPaths[path] = true
		}
	}

	// Look up graph callers and callees for terms in query
	words := strings.Fields(query)
	for _, w := range words {
		w = strings.Trim(w, `",':;()[]{}*`)
		if len(w) < 3 {
			continue
		}
		// Probe callees / callers for potential symbol matches
		callees, err := s.graphDB.Callees(w)
		if err == nil {
			for _, c := range callees {
				if c.File != "" && !seenPaths[c.File] {
					seenPaths[c.File] = true
					// Inject graph-discovered file context
					baseResults = append(baseResults, SearchResult{
						ID:    -1,
						Score: 0.85,
						Text:  fmt.Sprintf("// Graph relation: %s calls %s (line %d in %s)", w, c.Name, c.Line, c.File),
						Metadata: map[string]any{
							"file":   c.File,
							"source": c.File,
							"graph":  true,
						},
					})
				}
			}
		}
		callers, err := s.graphDB.Callers(w)
		if err == nil {
			for _, c := range callers {
				if c.File != "" && !seenPaths[c.File] {
					seenPaths[c.File] = true
					baseResults = append(baseResults, SearchResult{
						ID:    -1,
						Score: 0.80,
						Text:  fmt.Sprintf("// Graph relation: %s called by %s (line %d in %s)", w, c.Name, c.Line, c.File),
						Metadata: map[string]any{
							"file":   c.File,
							"source": c.File,
							"graph":  true,
						},
					})
				}
			}
		}
	}

	if len(baseResults) > topK {
		baseResults = baseResults[:topK]
	}
	return baseResults, nil
}

// Warnings returns what Load found wrong with the index that searching will
// not fix: results from it may be wrong or incomplete. They are also logged.
func (s *LeannSearcher) Warnings() []string {
	return append([]string(nil), s.warnings...)
}

// checkHealth records a warning for an index whose passage IDs have gaps.
// Older versions left such indexes behind after updating a changed or deleted
// file in place: vectors are addressed by position, so after a gap they point
// at the wrong passages and a search returns the wrong text or too few hits,
// without any error. The check reads only the passage store's key count and last
// key, so it costs nothing per load.
func (s *LeannSearcher) checkHealth(name string) {
	if s.meta.LexicalOnly || !s.meta.VectorReady {
		return // no vectors, nothing is addressed by position
	}
	count, maxID, err := s.passages.idSpan()
	if err != nil || count == 0 {
		return
	}
	dense := int64(count) == maxID+1
	if s.storeFormat == 2 {
		// The slot span alone can look contiguous while passages without a
		// slot exist (their count fills the gap in the span); the store's
		// dense check counts them too, so ask it instead of trusting the span.
		if d, derr := s.passages.IsDense(); derr == nil {
			dense = d
		}
	}
	if dense {
		return
	}
	msg := fmt.Sprintf("index %q is damaged: its passage IDs are not contiguous (%d passages, highest ID %d), "+
		"so vectors may be attached to the wrong passages and results may be wrong or incomplete. "+
		"Repair it with: gleann index repair %s", name, count, maxID, name)
	s.warnings = append(s.warnings, msg)
	log.Printf("⚠  WARNING: %s", msg)
}

// SameEmbeddingModel compares embedding model names loosely: an index records
// the name it was built with, while the configured model may be given as a
// path or with a .gguf suffix.
func SameEmbeddingModel(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToLower(filepath.Base(strings.TrimSpace(s)))
		return strings.TrimSuffix(s, ".gguf")
	}
	return norm(a) == norm(b)
}

// Embedder returns the embedding model resolved for the loaded index
// (nil for lexical-only searchers).
func (s *LeannSearcher) Embedder() EmbeddingComputer {
	return s.embedder
}

// IndexPath returns the directory that holds the loaded index's files.
func (s *LeannSearcher) IndexPath() string {
	return filepath.Join(s.config.IndexDir, s.meta.Name)
}
