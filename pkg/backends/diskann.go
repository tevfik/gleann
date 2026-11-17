package backends

import (
	"context"
	"fmt"

	"github.com/tevfik/gleann/modules/diskann"
	"github.com/tevfik/gleann/pkg/gleann"
)

func init() {
	gleann.RegisterBackend(&diskannFactoryAdapter{})
}

type diskannFactoryAdapter struct{}

func (f *diskannFactoryAdapter) Name() string { return "diskann" }

func toDiskANNConfig(config gleann.Config) diskann.Config {
	return diskann.Config{
		IndexDir: config.IndexDir,
		Backend:  config.Backend,
		DiskANNConfig: diskann.DiskANNConfig{
			R:              config.DiskANNConfig.R,
			L:              config.DiskANNConfig.L,
			Alpha:          config.DiskANNConfig.Alpha,
			PQDim:          config.DiskANNConfig.PQDim,
			PQCentroids:    config.DiskANNConfig.PQCentroids,
			SearchL:        config.DiskANNConfig.SearchL,
			SearchPQRerank: config.DiskANNConfig.SearchPQRerank,
			DistanceMetric: diskann.DistanceMetric(config.DiskANNConfig.DistanceMetric),
			UseMmap:        config.MmapEnabled(),
		},
	}
}

func (f *diskannFactoryAdapter) NewBuilder(config gleann.Config) gleann.BackendBuilder {
	factory := &diskann.Factory{}
	return &diskannBuilderAdapter{inner: factory.NewBuilder(toDiskANNConfig(config))}
}

func (f *diskannFactoryAdapter) NewSearcher(config gleann.Config) gleann.BackendSearcher {
	factory := &diskann.Factory{}
	inner := factory.NewSearcher(toDiskANNConfig(config))
	if config.MmapEnabled() {
		return &diskannMmapSearcherAdapter{diskannSearcherAdapter{inner: inner}}
	}
	return &diskannSearcherAdapter{inner: inner}
}

// ── Builder adapter ──

type diskannBuilderAdapter struct {
	inner diskann.BackendBuilder
}

func (b *diskannBuilderAdapter) Build(ctx context.Context, embeddings [][]float32) ([]byte, error) {
	return b.inner.Build(ctx, embeddings)
}

func (b *diskannBuilderAdapter) AddVectors(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) ([]byte, error) {
	return b.inner.AddVectors(ctx, indexData, embeddings, startID)
}

// ── Searcher adapter ──

type diskannSearcherAdapter struct {
	inner diskann.BackendSearcher
}

func (s *diskannSearcherAdapter) Load(ctx context.Context, indexData []byte, meta gleann.IndexMeta) error {
	dMeta := diskann.IndexMeta{
		Name:           meta.Name,
		Backend:        meta.Backend,
		EmbeddingModel: meta.EmbeddingModel,
		Dimensions:     meta.Dimensions,
		NumPassages:    meta.NumPassages,
		CreatedAt:      meta.CreatedAt,
		UpdatedAt:      meta.UpdatedAt,
		Version:        meta.Version,
	}
	return s.inner.Load(ctx, indexData, dMeta)
}

func (s *diskannSearcherAdapter) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	return s.inner.Search(ctx, query, topK)
}

func (s *diskannSearcherAdapter) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute gleann.EmbeddingRecomputer) ([]int64, []float32, error) {
	daRecompute := func(ctx context.Context, ids []int64) ([][]float32, error) {
		return recompute(ctx, ids)
	}
	return s.inner.SearchWithRecompute(ctx, query, topK, daRecompute)
}

func (s *diskannSearcherAdapter) ExtractVectors(ctx context.Context, ids []int64) ([][]float32, error) {
	if ve, ok := s.inner.(interface {
		ExtractVectors(ctx context.Context, ids []int64) ([][]float32, error)
	}); ok {
		return ve.ExtractVectors(ctx, ids)
	}
	return nil, fmt.Errorf("diskann backend does not support ExtractVectors")
}

func (s *diskannSearcherAdapter) Close() error {
	return s.inner.Close()
}

// diskannMmapSearcherAdapter wraps diskannSearcherAdapter and adds LoadFromFile support.
type diskannMmapSearcherAdapter struct {
	diskannSearcherAdapter
}

func (s *diskannMmapSearcherAdapter) LoadFromFile(ctx context.Context, path string) error {
	if mmap, ok := s.inner.(mmapInnerSearcher); ok {
		return mmap.LoadFromFile(ctx, path)
	}
	return fmt.Errorf("backend searcher does not support LoadFromFile")
}

var _ gleann.BackendBuilder = (*diskannBuilderAdapter)(nil)
var _ gleann.BackendSearcher = (*diskannSearcherAdapter)(nil)
var _ gleann.BackendFactory = (*diskannFactoryAdapter)(nil)
var _ gleann.MmapBackendSearcher = (*diskannMmapSearcherAdapter)(nil)
