package hnsw

import (
	"context"
	"math/rand"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestBackendRegistered(t *testing.T) {
	_, err := gleann.GetBackend("hnsw")
	if err != nil {
		t.Fatalf("HNSW backend not registered: %v", err)
	}
}

func TestFactoryName(t *testing.T) {
	f := &Factory{}
	if f.Name() != "hnsw" {
		t.Errorf("expected name 'hnsw', got %q", f.Name())
	}
}

func TestBuilderBuild(t *testing.T) {
	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 4
	config.HNSWConfig.EfConstruction = 50
	config.HNSWConfig.PruneEmbeddings = true
	config.HNSWConfig.PruneKeepFraction = 0.0

	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	embeddings := make([][]float32, 20)
	for i := range embeddings {
		v := make([]float32, 16)
		for j := range v {
			v[j] = rng.Float32()
		}
		embeddings[i] = v
	}

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty index data")
	}
}

func TestBuilderBuildEmpty(t *testing.T) {
	config := gleann.DefaultConfig()
	builder := &Builder{config: config}
	_, err := builder.Build(context.Background(), nil)
	if err == nil {
		t.Error("expected error for empty embeddings")
	}
}

func TestSearcherLoadAndSearch(t *testing.T) {
	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 4
	config.HNSWConfig.EfConstruction = 50
	config.HNSWConfig.PruneEmbeddings = false

	builder := &Builder{config: config}

	dims := 16
	rng := rand.New(rand.NewSource(42))
	embeddings := make([][]float32, 50)
	for i := range embeddings {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		embeddings[i] = v
	}

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "hnsw"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Search.
	query := make([]float32, dims)
	for j := range query {
		query[j] = rng.Float32()
	}

	ids, distances, err := searcher.Search(ctx, query, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(ids) != 5 {
		t.Errorf("expected 5 results, got %d", len(ids))
	}
	if len(distances) != 5 {
		t.Errorf("expected 5 distances, got %d", len(distances))
	}

	// Distances should be sorted.
	for i := 1; i < len(distances); i++ {
		if distances[i] < distances[i-1] {
			t.Errorf("distances not sorted: %f < %f", distances[i], distances[i-1])
		}
	}

	// Close.
	if err := searcher.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestSearcherSearchWithRecompute(t *testing.T) {
	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 4
	config.HNSWConfig.EfConstruction = 50
	config.HNSWConfig.PruneEmbeddings = true
	config.HNSWConfig.PruneKeepFraction = 0.0

	builder := &Builder{config: config}

	dims := 8
	rng := rand.New(rand.NewSource(42))
	embeddings := make([][]float32, 20)
	for i := range embeddings {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		embeddings[i] = v
	}

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Create searcher with recomputation config.
	searchConfig := config
	searchConfig.HNSWConfig.EfSearch = 50
	searcher := &Searcher{config: searchConfig}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "hnsw"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}

	recompute := func(ctx context.Context, ids []int64) ([][]float32, error) {
		vecs := make([][]float32, len(ids))
		for i, id := range ids {
			if id >= 0 && int(id) < len(embeddings) {
				vecs[i] = embeddings[id]
			} else {
				vecs[i] = make([]float32, dims)
			}
		}
		return vecs, nil
	}

	query := make([]float32, dims)
	for j := range query {
		query[j] = rng.Float32()
	}

	ids, _, err := searcher.SearchWithRecompute(ctx, query, 5, recompute)
	if err != nil {
		t.Fatalf("search with recompute: %v", err)
	}

	if len(ids) == 0 {
		t.Error("expected results from recompute search")
	}
}

func TestSearcherNotLoaded(t *testing.T) {
	searcher := &Searcher{config: gleann.DefaultConfig()}
	_, _, err := searcher.Search(context.Background(), []float32{1, 0, 0}, 5)
	if err == nil {
		t.Error("expected error when searching without loading")
	}
}

func TestBuilderAddVectors(t *testing.T) {
	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 4
	config.HNSWConfig.EfConstruction = 50
	config.HNSWConfig.PruneEmbeddings = false

	builder := &Builder{config: config}
	dims := 8
	rng := rand.New(rand.NewSource(42))

	// Build initial index.
	embeddings := make([][]float32, 10)
	for i := range embeddings {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		embeddings[i] = v
	}

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Add more vectors.
	newEmbeddings := make([][]float32, 5)
	for i := range newEmbeddings {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		newEmbeddings[i] = v
	}

	newData, err := builder.AddVectors(ctx, data, newEmbeddings, 10)
	if err != nil {
		t.Fatalf("add vectors: %v", err)
	}
	if len(newData) == 0 {
		t.Error("expected non-empty updated index")
	}
}

func TestBuilderRemoveVectors(t *testing.T) {
	config := gleann.DefaultConfig()
	config.HNSWConfig.M = 4
	config.HNSWConfig.EfConstruction = 50
	config.HNSWConfig.PruneEmbeddings = false

	builder := &Builder{config: config}
	dims := 8
	rng := rand.New(rand.NewSource(42))

	embeddings := make([][]float32, 10)
	for i := range embeddings {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		embeddings[i] = v
	}

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Remove some vectors.
	newData, err := builder.RemoveVectors(ctx, data, []int64{2, 5, 7})
	if err != nil {
		t.Fatalf("remove vectors: %v", err)
	}
	if len(newData) == 0 {
		t.Error("expected non-empty result")
	}
}
