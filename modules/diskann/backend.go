package diskann

import (
	"bytes"
	"context"
	"fmt"
	"sync"
)

// Factory creates DiskANN builders and searchers.
type Factory struct{}

func (f *Factory) Name() string { return "diskann" }

func (f *Factory) NewBuilder(config Config) BackendBuilder {
	return &Builder{config: config}
}

func (f *Factory) NewSearcher(config Config) BackendSearcher {
	return &Searcher{config: config}
}

// Builder builds DiskANN indexes.
type Builder struct {
	config Config
}

func (b *Builder) Build(ctx context.Context, embeddings [][]float32) ([]byte, error) {
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings to build index from")
	}

	cfg := b.config.DiskANNConfig
	cfg.Defaults(len(embeddings[0]))

	idx, err := BuildDiskIndex(embeddings, cfg)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if _, err := idx.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("serialize: %w", err)
	}

	return buf.Bytes(), nil
}

func (b *Builder) AddVectors(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) ([]byte, error) {
	// Load existing index, append embeddings, rebuild.
	existing, err := ReadDiskIndex(bytes.NewReader(indexData))
	if err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}

	// Merge embeddings.
	allEmbeddings := make([][]float32, existing.NumNodes+int64(len(embeddings)))
	copy(allEmbeddings, existing.Embeddings)
	copy(allEmbeddings[existing.NumNodes:], embeddings)

	cfg := b.config.DiskANNConfig
	cfg.Defaults(existing.Dims)

	idx, err := BuildDiskIndex(allEmbeddings, cfg)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if _, err := idx.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("serialize: %w", err)
	}
	return buf.Bytes(), nil
}

func (b *Builder) RemoveVectors(ctx context.Context, indexData []byte, ids []int64) ([]byte, error) {
	existing, err := ReadDiskIndex(bytes.NewReader(indexData))
	if err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}

	// Filter out removed IDs and rebuild.
	removeSet := make(map[int64]bool, len(ids))
	for _, id := range ids {
		removeSet[id] = true
	}

	var kept [][]float32
	for i := int64(0); i < existing.NumNodes; i++ {
		if !removeSet[i] {
			kept = append(kept, existing.Embeddings[i])
		}
	}

	if len(kept) == 0 {
		return nil, fmt.Errorf("all vectors removed")
	}

	cfg := b.config.DiskANNConfig
	cfg.Defaults(existing.Dims)

	idx, err := BuildDiskIndex(kept, cfg)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if _, err := idx.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("serialize: %w", err)
	}
	return buf.Bytes(), nil
}

// Searcher loads and queries a DiskANN index.
type Searcher struct {
	config Config
	mu     sync.RWMutex
	idx    *DiskIndex
}

func (s *Searcher) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := ReadDiskIndex(bytes.NewReader(indexData))
	if err != nil {
		return err
	}
	s.idx = idx
	return nil
}

func (s *Searcher) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.idx == nil {
		return nil, nil, fmt.Errorf("index not loaded")
	}

	cfg := s.config.DiskANNConfig
	distFunc := GetDistanceFunc(string(cfg.DistanceMetric))
	searchL := cfg.SearchL
	if searchL <= 0 {
		searchL = 100
	}

	// Use the PQ-prefiltered search with in-memory embeddings for reranking.
	candidates := SearchExact(s.idx, query, topK, searchL, distFunc)

	ids := make([]int64, len(candidates))
	distances := make([]float32, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
		distances[i] = c.Distance
	}
	return ids, distances, nil
}

func (s *Searcher) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.idx == nil {
		return nil, nil, fmt.Errorf("index not loaded")
	}

	cfg := s.config.DiskANNConfig
	distFunc := GetDistanceFunc(string(cfg.DistanceMetric))
	searchL := cfg.SearchL
	if searchL <= 0 {
		searchL = 100
	}
	rerankK := cfg.SearchPQRerank
	if rerankK <= 0 {
		rerankK = topK * 2
	}

	// Reranking fetches embeddings via the recompute callback (=disk read).
	getVector := func(id int64) []float32 {
		// Try in-memory first (if embeddings are loaded).
		if s.idx.Embeddings != nil && id >= 0 && id < s.idx.NumNodes && s.idx.Embeddings[id] != nil {
			return s.idx.Embeddings[id]
		}
		// Fall back to recompute callback.
		vecs, err := recompute(ctx, []int64{id})
		if err != nil || len(vecs) == 0 {
			return nil
		}
		return vecs[0]
	}

	candidates := SearchDiskIndex(s.idx, query, topK, searchL, rerankK, distFunc, getVector)

	ids := make([]int64, len(candidates))
	distances := make([]float32, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
		distances[i] = c.Distance
	}
	return ids, distances, nil
}

func (s *Searcher) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idx = nil
	return nil
}
