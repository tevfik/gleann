package backends

import (
	"context"

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
			UseMmap:           config.HNSWConfig.UseMmap,
			MaxLevel:          config.HNSWConfig.MaxLevel,
			DistanceMetric:    hnsw.DistanceMetric(config.HNSWConfig.DistanceMetric),
			UseHeuristic:      config.HNSWConfig.UseHeuristic,
			PruneEmbeddings:   config.HNSWConfig.PruneEmbeddings,
			PruneKeepFraction: config.HNSWConfig.PruneKeepFraction,
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

func (s *searcherAdapter) Close() error {
	return s.inner.Close()
}
