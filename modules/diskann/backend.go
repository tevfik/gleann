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

// Searcher loads and queries a DiskANN index.
type Searcher struct {
	config Config
	mu     sync.RWMutex
	idx    *DiskIndex
}

func (s *Searcher) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx != nil {
		_ = s.idx.Close()
	}
	idx, err := ReadDiskIndex(bytes.NewReader(indexData))
	if err != nil {
		return err
	}
	s.idx = idx
	return nil
}

// LoadFromFile memory-maps the DiskANN index from disk without reading the full file into RAM.
func (s *Searcher) LoadFromFile(ctx context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx != nil {
		_ = s.idx.Close()
	}
	idx, err := OpenDiskIndexMmap(path)
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

	// Reranking fetches embeddings via in-memory/mmap first, then recompute callback (=disk read).
	var recomputeErr error
	getVector := func(id int64) []float32 {
		// Try in-memory or mmap first.
		if vec := s.idx.GetVector(id); vec != nil {
			return vec
		}
		// Fall back to recompute callback.
		vecs, err := recompute(ctx, []int64{id})
		if err != nil || len(vecs) == 0 {
			if recomputeErr == nil {
				recomputeErr = fmt.Errorf("recompute embedding for node %d: %v", id, err)
			}
			return nil
		}
		return vecs[0]
	}

	candidates := SearchDiskIndex(s.idx, query, topK, searchL, rerankK, distFunc, getVector)
	if recomputeErr != nil {
		return nil, nil, recomputeErr
	}

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
	if s.idx != nil {
		err := s.idx.Close()
		s.idx = nil
		return err
	}
	return nil
}
