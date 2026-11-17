package backends

import (
	"context"
	"fmt"

	"github.com/tevfik/gleann/modules/hnsw"
	"github.com/tevfik/gleann/pkg/gleann"
)

func init() {
	gleann.RegisterBackend(&hnswFactoryAdapter{})
}

type hnswFactoryAdapter struct{}

func (f *hnswFactoryAdapter) Name() string { return "hnsw" }

func toHNSWConfig(config gleann.Config) hnsw.Config {
	return hnsw.Config{
		IndexDir: config.IndexDir,
		Backend:  config.Backend,
		HNSWConfig: hnsw.HNSWConfig{
			M:                 config.HNSWConfig.M,
			EfConstruction:    config.HNSWConfig.EfConstruction,
			EfSearch:          config.HNSWConfig.EfSearch,
			UseMmap:           config.MmapEnabled(),
			MaxLevel:          config.HNSWConfig.MaxLevel,
			DistanceMetric:    hnsw.DistanceMetric(config.HNSWConfig.DistanceMetric),
			UseHeuristic:      config.HNSWConfig.UseHeuristic,
			PruneEmbeddings:   config.HNSWConfig.PruneEmbeddings,
			PruneKeepFraction: config.HNSWConfig.PruneKeepFraction,
			VectorDType:       config.HNSWConfig.VectorDType,
		},
	}
}

func (f *hnswFactoryAdapter) NewBuilder(config gleann.Config) gleann.BackendBuilder {
	factory := &hnsw.Factory{}
	return factory.NewBuilder(toHNSWConfig(config))
}

func (f *hnswFactoryAdapter) NewSearcher(config gleann.Config) gleann.BackendSearcher {
	factory := &hnsw.Factory{}
	inner := factory.NewSearcher(toHNSWConfig(config))
	if config.MmapEnabled() {
		// Return the mmap-capable adapter so MmapBackendSearcher type assertion succeeds.
		return &mmapSearcherAdapter{searcherAdapter{inner: inner}}
	}
	// Return a plain adapter that does NOT implement LoadFromFile,
	// so searcher.go falls back to the standard RAM load path.
	return &searcherAdapter{inner: inner}
}

type searcherAdapter struct {
	inner hnsw.BackendSearcher
}

func (s *searcherAdapter) Load(ctx context.Context, indexData []byte, meta gleann.IndexMeta) error {
	hnswMeta := hnsw.IndexMeta{
		Name:           meta.Name,
		Backend:        meta.Backend,
		EmbeddingModel: meta.EmbeddingModel,
		Dimensions:     meta.Dimensions,
		NumPassages:    meta.NumPassages,
		CreatedAt:      meta.CreatedAt,
		UpdatedAt:      meta.UpdatedAt,
		Version:        meta.Version,
		VectorDType:    meta.VectorDType,
	}
	return s.inner.Load(ctx, indexData, hnswMeta)
}

func (s *searcherAdapter) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	return s.inner.Search(ctx, query, topK)
}

func (s *searcherAdapter) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute gleann.EmbeddingRecomputer) ([]int64, []float32, error) {
	hnswRecompute := func(ctx context.Context, ids []int64) ([][]float32, error) {
		return recompute(ctx, ids)
	}
	return s.inner.SearchWithRecompute(ctx, query, topK, hnswRecompute)
}

// VectorDistances forwards to the inner searcher when it supports scoring
// explicit IDs (the RAM HNSW searcher does; the mmap searcher does not). The
// optional gleann.VectorIDScorer interface lets filtered search score a
// pre-selected set directly; backends without it fall back to lexical scoring.
func (s *searcherAdapter) VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error) {
	if vd, ok := s.inner.(interface {
		VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error)
	}); ok {
		return vd.VectorDistances(ctx, query, ids)
	}
	return nil, fmt.Errorf("hnsw backend does not support VectorDistances")
}

// ExtractVectors forwards to the inner searcher when it can return its stored
// vectors (the RAM searcher does), so a merge can rebuild without embedding.
func (s *searcherAdapter) ExtractVectors(ctx context.Context, ids []int64) ([][]float32, error) {
	if ve, ok := s.inner.(interface {
		ExtractVectors(ctx context.Context, ids []int64) ([][]float32, error)
	}); ok {
		return ve.ExtractVectors(ctx, ids)
	}
	return nil, fmt.Errorf("hnsw backend does not support ExtractVectors")
}

func (s *searcherAdapter) Close() error {
	return s.inner.Close()
}

// mmapInnerSearcher is satisfied by HNSW searchers that support memory-mapped loading.
type mmapInnerSearcher interface {
	LoadFromFile(ctx context.Context, path string) error
}

// mmapSearcherAdapter wraps searcherAdapter and adds LoadFromFile support for
// mmap-capable inner searchers. Only returned when UseMmap=true.
type mmapSearcherAdapter struct {
	searcherAdapter
}

func (s *mmapSearcherAdapter) LoadFromFile(ctx context.Context, path string) error {
	if mmap, ok := s.inner.(mmapInnerSearcher); ok {
		return mmap.LoadFromFile(ctx, path)
	}
	return fmt.Errorf("inner hnsw searcher does not support LoadFromFile")
}
