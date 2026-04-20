package gleann

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// LeannSearcher performs search on built indexes.
// This mirrors Python LEANN's LeannSearcher.
type LeannSearcher struct {
	config    Config
	backend   BackendSearcher
	passages  *PassageManager
	meta      IndexMeta
	embedder  EmbeddingComputer
	scorer    Scorer
	reranker  Reranker
	embServer EmbeddingServer
	graphDB   GraphDB

	loaded bool
}

// NewSearcher creates a new LeannSearcher.
func NewSearcher(config Config, embedder EmbeddingComputer) *LeannSearcher {
	return &LeannSearcher{
		config:   config,
		embedder: embedder,
	}
}

// SetScorer sets a BM25 scorer for hybrid search.
func (s *LeannSearcher) SetScorer(scorer Scorer) {
	s.scorer = scorer
}

// SetReranker sets a reranker for two-stage retrieval.
func (s *LeannSearcher) SetReranker(reranker Reranker) {
	s.reranker = reranker
}

// SetEmbeddingServer sets the embedding server for recomputation during search.
func (s *LeannSearcher) SetEmbeddingServer(server EmbeddingServer) {
	s.embServer = server
}

// Load loads an index for searching.
func (s *LeannSearcher) Load(ctx context.Context, name string) error {
	indexDir := filepath.Join(s.config.IndexDir, name)
	basePath := filepath.Join(indexDir, name)

	// Load metadata.
	metaPath := basePath + ".meta.json"
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}
	if err := json.Unmarshal(metaData, &s.meta); err != nil {
		return fmt.Errorf("unmarshal metadata: %w", err)
	}

	// Schema version check for backwards compatibility
	expectedVersion := "1.0.0"
	if s.meta.Version != "" && s.meta.Version != expectedVersion {
		log.Printf("⚠  WARNING: Index %q was built with version %q but current runtime expects %q. "+
			"The system will attempt to read it, but if errors occur, please rebuild with: gleann build %s --docs <dir>",
			name, s.meta.Version, expectedVersion, name)
	}

	// Warn if the current embedding model differs from what was used to build the index.
	if s.config.EmbeddingModel != "" && s.meta.EmbeddingModel != "" &&
		s.config.EmbeddingModel != s.meta.EmbeddingModel {
		log.Printf("⚠  WARNING: Index %q was built with embedding model %q (%d dims) "+
			"but current config uses %q — search results may be incorrect. "+
			"Rebuild with: gleann build %s --docs <dir>",
			name, s.meta.EmbeddingModel, s.meta.Dimensions, s.config.EmbeddingModel, name)
	}

	// Load passages.
	s.passages = NewPassageManager(basePath)
	if err := s.passages.Load(); err != nil {
		return fmt.Errorf("load passages: %w", err)
	}

	// Get backend.
	factory, err := GetBackend(s.meta.Backend)
	if err != nil {
		return fmt.Errorf("get backend: %w", err)
	}
	s.backend = factory.NewSearcher(s.config)

	// Attempt Zero-Copy Memory Mapping first, fallback to standard RAM loading
	indexPath := basePath + ".index"
	if mmapSearcher, ok := s.backend.(MmapBackendSearcher); ok {
		// Native zero-copy mmap
		if err := mmapSearcher.LoadFromFile(ctx, indexPath); err != nil {
			return fmt.Errorf("load backend mmap: %w", err)
		}
	} else {
		// Standard RAM load
		indexData, err := os.ReadFile(indexPath)
		if err != nil {
			return fmt.Errorf("read index: %w", err)
		}
		if err := s.backend.Load(ctx, indexData, s.meta); err != nil {
			return fmt.Errorf("load backend: %w", err)
		}
	}

	// Attempt to load Graph DB if it exists.
	// Primary path: {indexDir}/{name}_graph (written by buildGraphIndex).
	// Fallback: {basePath}/.kuzu (legacy location).
	graphDir := filepath.Join(s.config.IndexDir, name+"_graph")
	if _, err := os.Stat(graphDir); err != nil {
		// Try legacy path
		graphDir = filepath.Join(basePath, ".kuzu")
	}
	if _, err := os.Stat(graphDir); err == nil {
		if GraphDBOpener != nil {
			if db, openErr := GraphDBOpener(graphDir); openErr == nil {
				s.graphDB = db
			} else {
				log.Printf("⚠  WARNING: Found graph database at %s but failed to open: %v", graphDir, openErr)
			}
		} else {
			log.Printf("ℹ️  Graph database found at %s but gleann was not built with graph support (Cgo/treesitter disabled)", graphDir)
		}
	}

	// Build BM25 index if scorer is set.
	// For large corpora, use streaming to avoid loading all passages into RAM.
	if s.scorer != nil {
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

// Search performs a search and returns results.
func (s *LeannSearcher) Search(ctx context.Context, query string, opts ...SearchOption) ([]SearchResult, error) {
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
	retrieveK := topK * 2
	if s.reranker != nil && searchOpts.UseReranker {
		retrieveK = topK * 4
		if retrieveK < 50 {
			retrieveK = 50
		}
	}

	// Compute query embedding.
	queryEmb, err := s.embedder.ComputeSingle(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("compute query embedding: %w", err)
	}

	// Vector search.
	var ids []int64
	var distances []float32

	if s.embServer != nil && s.embServer.IsRunning() {
		// Use dedicated recomputation server if available
		ids, distances, err = s.backend.SearchWithRecompute(ctx, queryEmb, retrieveK, s.embServer.ComputeEmbeddings)
	} else if _, isMmap := s.backend.(MmapBackendSearcher); isMmap {
		// If using Mmap Searcher but no dedicated embServer, create an ad-hoc recomputer
		// so that the graph can traverse locally.
		adHocRecompute := func(ctx context.Context, targetIDs []int64) ([][]float32, error) {
			texts := make([]string, 0, len(targetIDs))
			for _, id := range targetIDs {
				passage, pErr := s.passages.Get(id)
				if pErr == nil {
					texts = append(texts, passage.Text)
				} else {
					texts = append(texts, "")
				}
			}
			return s.embedder.Compute(ctx, texts)
		}
		// HNSW/mmap passes context natively if we wrapper it or we just ignore the inner errors.
		ids, distances, err = s.backend.SearchWithRecompute(ctx, queryEmb, retrieveK, adHocRecompute)
	} else {
		// Standard RAM search with stored embeddings.
		ids, distances, err = s.backend.Search(ctx, queryEmb, retrieveK)
	}
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// Convert distances to cosine-like scores (higher = better).
	vectorScores := make(map[int64]float32, len(ids))
	maxDist := float32(0)
	for _, d := range distances {
		if d > maxDist {
			maxDist = d
		}
	}
	for i, id := range ids {
		if maxDist > 0 {
			vectorScores[id] = 1.0 - distances[i]/maxDist
		} else {
			vectorScores[id] = 1.0
		}
	}

	// Hybrid search with BM25.
	alpha := searchOpts.HybridAlpha
	finalScores := make(map[int64]float32)

	if s.scorer != nil && alpha < 1.0 {
		// Score only the FAISS candidates instead of the entire corpus.
		// BM25Adapter.Score maps by p.ID, so using a subset is correct and
		// reduces complexity from O(n_total) to O(retrieveK).
		candidatePassages := make([]Passage, 0, len(ids))
		for _, id := range ids {
			if p, err := s.passages.Get(id); err == nil {
				candidatePassages = append(candidatePassages, p)
			}
		}
		bm25Scores := s.scorer.Score(query, candidatePassages)

		// Normalize BM25 scores.
		maxBM25 := float32(0)
		for _, score := range bm25Scores {
			if score > maxBM25 {
				maxBM25 = score
			}
		}

		// Build score map indexed by passage ID.
		bm25ByID := make(map[int64]float32, len(candidatePassages))
		for i, p := range candidatePassages {
			if maxBM25 > 0 {
				bm25ByID[p.ID] = bm25Scores[i] / maxBM25
			}
		}

		// Merge vector scores and BM25 scores for all candidates.
		for _, id := range ids {
			vs := vectorScores[id]
			bs := bm25ByID[id] // 0 if not found
			finalScores[id] = alpha*vs + (1-alpha)*bs
		}
	} else {
		finalScores = vectorScores
	}

	// Sort by score.
	type scored struct {
		id    int64
		score float32
	}
	sortedResults := make([]scored, 0, len(finalScores))
	for id, score := range finalScores {
		if score >= searchOpts.MinScore {
			sortedResults = append(sortedResults, scored{id: id, score: score})
		}
	}
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].score > sortedResults[j].score
	})

	if len(sortedResults) > topK {
		sortedResults = sortedResults[:topK]
	}

	// Build results.
	results := make([]SearchResult, 0, len(sortedResults))
	for _, sr := range sortedResults {
		passage, err := s.passages.Get(sr.id)
		if err != nil {
			continue
		}
		results = append(results, SearchResult{
			ID:       sr.id,
			Text:     passage.Text,
			Score:    sr.score,
			Metadata: passage.Metadata,
		})
	}

	// Apply metadata filters if configured.
	if len(searchOpts.MetadataFilters) > 0 {
		engine := NewMetadataFilterEngine(searchOpts.MetadataFilters)
		if searchOpts.FilterLogic != "" {
			engine.Logic = searchOpts.FilterLogic
		}
		results = engine.FilterResults(results)
	}

	// Reranking stage: re-score results with cross-encoder if configured.
	if s.reranker != nil && searchOpts.UseReranker {
		reranked, err := s.reranker.Rerank(ctx, query, results, topK)
		if err != nil {
			// Log but don't fail — fall back to original ranking.
			fmt.Fprintf(os.Stderr, "reranker warning: %v (using original ranking)\n", err)
		} else {
			results = reranked
		}
	}

	// Graph-augmented search: enrich results with caller/callee context.
	if searchOpts.UseGraphContext && s.graphDB != nil {
		s.enrichWithGraphContext(results)
	}

	return results, nil
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

// GraphDB returns the underlying Graph DB connection, or nil if none exists.
func (s *LeannSearcher) GraphDB() GraphDB {
	return s.graphDB
}

// PassageManager returns the internal passage manager containing all indexed chunks.
func (s *LeannSearcher) PassageManager() *PassageManager {
	return s.passages
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

// WithGraphContext enables graph-augmented search.
// Each result is enriched with symbols from the same source file
// and their caller/callee relationships from the code graph.
func WithGraphContext(enabled bool) SearchOption {
	return func(c *SearchConfig) {
		c.UseGraphContext = enabled
	}
}

// ListIndexes returns all available indexes in the configured directory.
func ListIndexes(indexDir string) ([]IndexMeta, error) {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		if os.IsNotExist(err) {
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

// RemoveIndex removes an index and all its associated files (vector index, graph database, sync state).
func RemoveIndex(indexDir, name string) error {
	// 1. Remove the main index directory (contains .index, .passages.jsonl, .meta.json, etc.)
	indexPath := filepath.Join(indexDir, name)
	if err := os.RemoveAll(indexPath); err != nil {
		return fmt.Errorf("remove index directory: %w", err)
	}

	// 2. Remove the graph database associated with the index (Cgo/KuzuDB path)
	graphPath := filepath.Join(indexDir, name+"_graph")
	if err := os.RemoveAll(graphPath); err != nil {
		return fmt.Errorf("remove graph database: %w", err)
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
