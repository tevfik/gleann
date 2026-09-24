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
	"strings"
	"time"
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
		scorer:   NewBM25Adapter(),
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
			"but current config uses %q — search results will be incorrect! "+
			"To migrate to the new model, run: gleann index rebuild %s --docs <dir>",
			name, s.meta.EmbeddingModel, s.meta.Dimensions, s.config.EmbeddingModel, name)
	}

	// Strictly validate embedding dimensions to prevent backend crashes or corruption.
	if s.embedder != nil && s.meta.Dimensions > 0 && s.embedder.Dimensions() > 0 && s.embedder.Dimensions() != s.meta.Dimensions {
		return fmt.Errorf("embedding dimension mismatch: index %q expects %d dims (%s), but embedder provides %d dims (%s). Rebuild index: gleann index rebuild %s --docs <dir>",
			name, s.meta.Dimensions, s.meta.EmbeddingModel, s.embedder.Dimensions(), s.embedder.ModelName(), name)
	}

	// Load passages in read-only mode (shared flock)
	s.passages = NewReadOnlyPassageManager(basePath)
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
	}
	if len(searchOpts.MetadataFilters) > 0 {
		if retrieveK < topK*4 {
			retrieveK = topK * 4
		}
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

	// Hybrid search with BM25 (RRF: Reciprocal Rank Fusion)
	alpha := searchOpts.HybridAlpha
	finalScores := make(map[int64]float32)

	// If BM25 scorer is configured and alpha < 1.0, perform full-corpus lexical retrieval
	var bm25IDs []int64
	if s.scorer != nil && alpha < 1.0 {
		if topKScorer, ok := s.scorer.(TopKScorer); ok {
			bm25IDs, _ = topKScorer.TopK(query, retrieveK)
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

	// Build results and apply boosts/penalties:
	// 1. Symbol / FQN exact match boost (+0.35 to +0.5)
	// 2. Kind filtering ("code" vs "docs")
	// 3. Test / vendor demotion (unless searchOpts.IncludeTests is true or query mentions test)
	qClean := strings.TrimSpace(query)
	qLower := strings.ToLower(qClean)
	queryMentionsTest := strings.Contains(qLower, "test") || strings.Contains(qLower, "benchmark")

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
			} else if strings.Contains(qLower, strings.ToLower(name)) && len(name) >= 3 {
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

		if score >= searchOpts.MinScore {
			results = append(results, SearchResult{
				ID:       id,
				Text:     passage.Text,
				Score:    score,
				Metadata: passage.Metadata,
			})
		}
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(finalScores) > 0 && len(results) == 0 && searchOpts.MinScore == 0 && searchOpts.Kind == "" {
		log.Printf("⚠  WARNING: Index %q backend returned candidate vectors, but none were found in the passages database! The index is desynchronized. Run: gleann index rebuild %s", s.meta.Name, s.meta.Name)
	}

	// Apply metadata filters if configured (BEFORE topK truncation).
	if len(searchOpts.MetadataFilters) > 0 {
		engine := NewMetadataFilterEngine(searchOpts.MetadataFilters)
		if searchOpts.FilterLogic != "" {
			engine.Logic = searchOpts.FilterLogic
		}
		results = engine.FilterResults(results)
	}

	// Truncate to topK if not reranking (reranker handles topK internally).
	if !(s.reranker != nil && searchOpts.UseReranker) {
		if len(results) > topK {
			results = results[:topK]
		}
	}

	// Reranking stage: re-score results with cross-encoder if configured.
	if activeReranker != nil && searchOpts.UseReranker {
		reranked, err := activeReranker.Rerank(ctx, query, results, topK)
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

// SetMeta updates the in-memory metadata.
func (s *LeannSearcher) SetMeta(meta IndexMeta) {
	s.meta = meta
}


// GraphDB returns the underlying Graph DB connection, or nil if none exists.
func (s *LeannSearcher) GraphDB() GraphDB {
	return s.graphDB
}

// PassageManager returns the internal passage manager containing all indexed chunks.
func (s *LeannSearcher) PassageManager() *PassageManager {
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
		results = append(results, SearchResult{
			ID:       id,
			Score:    scores[i],
			Text:     p.Text,
			Metadata: p.Metadata,
		})
	}
	return results, nil
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


