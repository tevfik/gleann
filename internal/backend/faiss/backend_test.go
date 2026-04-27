//go:build cgo && faiss

package faiss

import (
	"context"
	"math"
	"math/rand"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func defaultConfig() gleann.Config {
	c := gleann.DefaultConfig()
	c.Backend = "faiss"
	c.HNSWConfig.M = 16
	return c
}

func randomVectors(rng *rand.Rand, n, dim int) [][]float32 {
	vecs := make([][]float32, n)
	for i := range vecs {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32()
		}
		vecs[i] = v
	}
	return vecs
}

// ──────────────────── Factory ────────────────────

func TestFactoryName(t *testing.T) {
	f := &Factory{}
	if f.Name() != "faiss" {
		t.Errorf("expected 'faiss', got %q", f.Name())
	}
}

func TestBackendRegistered(t *testing.T) {
	_, err := gleann.GetBackend("faiss")
	if err != nil {
		t.Fatalf("FAISS backend not registered: %v", err)
	}
}

func TestFactoryCreatesBuilderAndSearcher(t *testing.T) {
	f := &Factory{}
	config := defaultConfig()

	b := f.NewBuilder(config)
	if b == nil {
		t.Fatal("NewBuilder returned nil")
	}

	s := f.NewSearcher(config)
	if s == nil {
		t.Fatal("NewSearcher returned nil")
	}
}

// ──────────────────── Builder ────────────────────

func TestBuilderBuild(t *testing.T) {
	config := defaultConfig()
	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 100, 32)

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
	builder := &Builder{config: defaultConfig()}
	_, err := builder.Build(context.Background(), nil)
	if err == nil {
		t.Error("expected error for empty embeddings")
	}
}

func TestBuilderBuildDefaultM(t *testing.T) {
	config := defaultConfig()
	config.HNSWConfig.M = 0 // should default to 32
	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 50, 16)

	data, err := builder.Build(context.Background(), embeddings)
	if err != nil {
		t.Fatalf("build error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty index data")
	}
}

func TestBuilderAddVectors(t *testing.T) {
	config := defaultConfig()
	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	ctx := context.Background()

	// Build initial index with 50 vectors.
	embeddings := randomVectors(rng, 50, 16)
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Add 20 more.
	newEmbeddings := randomVectors(rng, 20, 16)
	newData, err := builder.AddVectors(ctx, data, newEmbeddings, 50)
	if err != nil {
		t.Fatalf("add vectors: %v", err)
	}
	if len(newData) == 0 {
		t.Error("expected non-empty index data after adding")
	}

	// Verify the new index is larger.
	if len(newData) <= len(data) {
		t.Errorf("expected larger index after adding vectors: %d <= %d", len(newData), len(data))
	}
}

func TestBuilderRemoveVectorsErrors(t *testing.T) {
	config := defaultConfig()
	builder := &Builder{config: config}

	_, err := builder.RemoveVectors(context.Background(), nil, []int64{1, 2})
	if err == nil {
		t.Error("expected error: FAISS HNSW doesn't support removal")
	}
}

// ──────────────────── Searcher ────────────────────

func TestSearcherLoadAndSearch(t *testing.T) {
	config := defaultConfig()
	builder := &Builder{config: config}

	dims := 32
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 200, dims)

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	// Search with query.
	query := make([]float32, dims)
	for j := range query {
		query[j] = rng.Float32()
	}

	ids, distances, err := searcher.Search(ctx, query, 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(ids) == 0 {
		t.Error("expected results")
	}
	if len(ids) != len(distances) {
		t.Errorf("ids/distances length mismatch: %d vs %d", len(ids), len(distances))
	}

	// Distances should be non-negative (L2).
	for i, d := range distances {
		if d < 0 {
			t.Errorf("distance[%d] = %f, expected >= 0", i, d)
		}
	}

	// Distances should be sorted (ascending for L2).
	for i := 1; i < len(distances); i++ {
		if distances[i] < distances[i-1] {
			t.Errorf("distances not sorted: [%d]=%f < [%d]=%f", i, distances[i], i-1, distances[i-1])
		}
	}
}

func TestSearcherExactNeighbor(t *testing.T) {
	// Build an index with one known vector, search for it.
	config := defaultConfig()
	builder := &Builder{config: config}

	dims := 8
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 100, dims)

	// Insert a known vector at position 0.
	target := make([]float32, dims)
	for j := range target {
		target[j] = float32(j) * 0.1
	}
	embeddings[0] = target

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	// Search for the exact same vector.
	ids, distances, err := searcher.Search(ctx, target, 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(ids) != 1 {
		t.Fatalf("expected 1 result, got %d", len(ids))
	}
	if ids[0] != 0 {
		t.Errorf("expected ID 0, got %d", ids[0])
	}
	// Distance should be ~0 for exact match.
	if distances[0] > 1e-6 {
		t.Errorf("expected near-zero distance, got %f", distances[0])
	}
}

func TestSearcherNotLoaded(t *testing.T) {
	searcher := &Searcher{config: defaultConfig()}
	_, _, err := searcher.Search(context.Background(), []float32{1, 0, 0}, 5)
	if err == nil {
		t.Error("expected error when searching without loading")
	}
}

func TestSearcherSearchWithRecompute(t *testing.T) {
	// SearchWithRecompute should fall back to Search.
	config := defaultConfig()
	builder := &Builder{config: config}

	dims := 16
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 50, dims)

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

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

func TestSearcherReload(t *testing.T) {
	// Load an index, then load a different one — old should be freed.
	config := defaultConfig()
	builder := &Builder{config: config}
	dims := 16
	rng := rand.New(rand.NewSource(42))

	ctx := context.Background()

	// Build two different indexes.
	data1, err := builder.Build(ctx, randomVectors(rng, 30, dims))
	if err != nil {
		t.Fatalf("build1: %v", err)
	}
	data2, err := builder.Build(ctx, randomVectors(rng, 60, dims))
	if err != nil {
		t.Fatalf("build2: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}

	// Load first.
	if err := searcher.Load(ctx, data1, meta); err != nil {
		t.Fatalf("load1: %v", err)
	}

	// Load second (should free first).
	if err := searcher.Load(ctx, data2, meta); err != nil {
		t.Fatalf("load2: %v", err)
	}

	// Search should work.
	query := make([]float32, dims)
	ids, _, err := searcher.Search(ctx, query, 5)
	if err != nil {
		t.Fatalf("search after reload: %v", err)
	}
	if len(ids) == 0 {
		t.Error("expected results after reload")
	}

	searcher.Close()
}

func TestSearcherClose(t *testing.T) {
	config := defaultConfig()
	builder := &Builder{config: config}
	dims := 8
	rng := rand.New(rand.NewSource(42))

	ctx := context.Background()
	data, err := builder.Build(ctx, randomVectors(rng, 20, dims))
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Close.
	if err := searcher.Close(); err != nil {
		t.Errorf("close error: %v", err)
	}

	// Search after close should fail.
	_, _, err = searcher.Search(ctx, make([]float32, dims), 5)
	if err == nil {
		t.Error("expected error after close")
	}

	// Double close should be safe.
	if err := searcher.Close(); err != nil {
		t.Errorf("double close should be safe: %v", err)
	}
}

// ──────────────────── Integration ────────────────────

func TestBuildSearchRoundtrip(t *testing.T) {
	// Full pipeline: build → serialize → load → search → verify recall.
	config := defaultConfig()
	config.HNSWConfig.M = 32
	dims := 64
	n := 500
	rng := rand.New(rand.NewSource(42))

	embeddings := randomVectors(rng, n, dims)

	ctx := context.Background()
	builder := &Builder{config: config}
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	t.Logf("Index size: %d bytes for %d vectors of dim %d", len(data), n, dims)

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	// Search for each of the first 10 vectors — they should be near the top.
	hits := 0
	for i := 0; i < 10; i++ {
		ids, _, err := searcher.Search(ctx, embeddings[i], 5)
		if err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
		for _, id := range ids {
			if id == int64(i) {
				hits++
				break
			}
		}
	}

	// With HNSW M=32 and 500 vectors, self-recall should be 100%.
	if hits < 10 {
		t.Errorf("self-recall: %d/10 (expected 10/10)", hits)
	}
}

func TestBuildAddSearchRoundtrip(t *testing.T) {
	// Build with initial vectors, add more, verify all are searchable.
	config := defaultConfig()
	dims := 16
	rng := rand.New(rand.NewSource(42))

	ctx := context.Background()
	builder := &Builder{config: config}

	// Build with 50 vectors.
	initial := randomVectors(rng, 50, dims)
	data, err := builder.Build(ctx, initial)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Add 50 more.
	additional := randomVectors(rng, 50, dims)
	data, err = builder.AddVectors(ctx, data, additional, 50)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Load and search.
	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	// Search for a vector from the additional batch.
	ids, _, err := searcher.Search(ctx, additional[0], 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) == 0 {
		t.Error("expected results")
	}
	// ID should be 50 (first of the added batch).
	if ids[0] != 50 {
		t.Logf("expected ID 50, got %d (may vary due to HNSW)", ids[0])
	}
}

// ──────────────────── BatchSearch ────────────────────

func TestBatchSearch(t *testing.T) {
	config := defaultConfig()
	builder := &Builder{config: config}

	dims := 16
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 100, dims)

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Load via loadFromBytes to get raw index for BatchSearch.
	index, cleanup, err := loadFromBytes(data)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer cleanup()

	// Batch of 5 queries.
	queries := randomVectors(rng, 5, dims)
	allIDs, allDists, err := BatchSearch(index, queries, 3)
	if err != nil {
		t.Fatalf("batch search: %v", err)
	}

	if len(allIDs) != 5 {
		t.Errorf("expected 5 result sets, got %d", len(allIDs))
	}
	for i, ids := range allIDs {
		if len(ids) == 0 {
			t.Errorf("query %d: expected results", i)
		}
		if len(ids) != len(allDists[i]) {
			t.Errorf("query %d: ids/dists length mismatch", i)
		}
	}
}

func TestBatchSearchEmpty(t *testing.T) {
	allIDs, allDists, err := BatchSearch(nil, nil, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allIDs != nil || allDists != nil {
		t.Error("expected nil for empty queries")
	}
}

// ──────────────────── Helpers ────────────────────

func TestFlattenVectors(t *testing.T) {
	vecs := [][]float32{
		{1, 2, 3},
		{4, 5, 6},
	}
	flat := flattenVectors(vecs)
	expected := []float32{1, 2, 3, 4, 5, 6}

	if len(flat) != len(expected) {
		t.Fatalf("length: got %d, want %d", len(flat), len(expected))
	}
	for i := range expected {
		if math.Abs(float64(flat[i]-expected[i])) > 1e-6 {
			t.Errorf("flat[%d] = %f, want %f", i, flat[i], expected[i])
		}
	}
}

func TestFlattenVectorsEmpty(t *testing.T) {
	flat := flattenVectors(nil)
	if flat != nil {
		t.Errorf("expected nil, got %v", flat)
	}
}

func TestSerializeDeserializeRoundtrip(t *testing.T) {
	config := defaultConfig()
	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 50, 16)

	data, err := builder.Build(context.Background(), embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Load from bytes.
	index, cleanup, err := loadFromBytes(data)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer cleanup()

	// Re-serialize.
	data2, err := serializeIndex(index)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	// Both should be non-empty and same length.
	if len(data2) == 0 {
		t.Error("re-serialized data is empty")
	}
	if len(data) != len(data2) {
		t.Errorf("re-serialized size mismatch: %d vs %d", len(data), len(data2))
	}
}

// ──────────────────── IVF Index Types ────────────────────

func ivfConfig(indexType string) gleann.Config {
	c := gleann.DefaultConfig()
	c.Backend = "faiss"
	c.FAISSConfig.IndexType = indexType
	c.FAISSConfig.NList = 4 // small for test data
	c.FAISSConfig.NProbe = 4
	return c
}

func TestIVFFlatBuildSearch(t *testing.T) {
	config := ivfConfig("ivf_flat")
	builder := &Builder{config: config}

	dims := 32
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 200, dims)

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty index data")
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	ids, dists, err := searcher.Search(ctx, embeddings[0], 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) == 0 {
		t.Error("expected results")
	}
	// Self-search should return the query vector itself.
	if ids[0] != 0 {
		t.Logf("IVF_Flat: nearest ID=%d (expected 0)", ids[0])
	}
	if dists[0] > 1e-4 {
		t.Errorf("IVF_Flat: self-distance=%f, expected near-zero", dists[0])
	}
}

func TestIVFPQBuildSearch(t *testing.T) {
	config := ivfConfig("ivf_pq")
	config.FAISSConfig.PQSubDim = 8
	builder := &Builder{config: config}

	dims := 32 // must be divisible by PQSubDim
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 500, dims) // PQ needs ≥256 training points

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	ids, _, err := searcher.Search(ctx, embeddings[0], 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) == 0 {
		t.Error("expected results from IVF+PQ search")
	}
}

func TestIVFSQ8BuildSearch(t *testing.T) {
	config := ivfConfig("ivf_sq8")
	builder := &Builder{config: config}

	dims := 32
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 200, dims)

	ctx := context.Background()
	data, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "faiss"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	ids, _, err := searcher.Search(ctx, embeddings[0], 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) == 0 {
		t.Error("expected results from IVF+SQ8 search")
	}
}

func TestIVFAutoNList(t *testing.T) {
	// When NList is 0, buildFAISSIndex should auto-calculate it.
	config := gleann.DefaultConfig()
	config.Backend = "faiss"
	config.FAISSConfig.IndexType = "ivf_flat"
	config.FAISSConfig.NList = 0 // auto
	config.FAISSConfig.NProbe = 4

	builder := &Builder{config: config}

	dims := 16
	rng := rand.New(rand.NewSource(42))
	embeddings := randomVectors(rng, 100, dims)

	data, err := builder.Build(context.Background(), embeddings)
	if err != nil {
		t.Fatalf("build with auto-nlist: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty index data")
	}
}

func TestIVFNeedsTrainAndIsIVF(t *testing.T) {
	tests := []struct {
		indexType string
		isIVF     bool
		needTrain bool
	}{
		{"hnsw", false, false},
		{"hnsw_pq", false, true},
		{"hnsw_sq8", false, true},
		{"ivf_flat", true, true},
		{"ivf_pq", true, true},
		{"ivf_sq8", true, true},
	}
	for _, tt := range tests {
		fc := gleann.FAISSConfig{IndexType: tt.indexType}
		if fc.IsIVF() != tt.isIVF {
			t.Errorf("%s: IsIVF()=%v, want %v", tt.indexType, fc.IsIVF(), tt.isIVF)
		}
		if fc.NeedsTrain() != tt.needTrain {
			t.Errorf("%s: NeedsTrain()=%v, want %v", tt.indexType, fc.NeedsTrain(), tt.needTrain)
		}
	}
}
