package backends_test

import (
	"context"
	"math/rand"
	"testing"

	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

func randomVectors(rng *rand.Rand, n, dims int) [][]float32 {
	vecs := make([][]float32, n)
	for i := range vecs {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		vecs[i] = v
	}
	return vecs
}

func TestDiskANNRegistered(t *testing.T) {
	_, err := gleann.GetBackend("diskann")
	if err != nil {
		t.Fatalf("diskann backend not registered: %v", err)
	}
}

func TestDiskANNInListBackends(t *testing.T) {
	backends := gleann.ListBackends()
	found := false
	for _, b := range backends {
		if b == "diskann" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("diskann not in ListBackends: %v", backends)
	}
}

func TestDiskANNFullPipeline(t *testing.T) {
	factory, err := gleann.GetBackend("diskann")
	if err != nil {
		t.Fatalf("get backend: %v", err)
	}

	config := gleann.DefaultConfig()
	config.Backend = "diskann"
	config.DiskANNConfig.R = 32
	config.DiskANNConfig.L = 50
	config.DiskANNConfig.Alpha = 1.2

	dims := 32
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 300, dims)

	ctx := context.Background()

	// Build.
	builder := factory.NewBuilder(config)
	data, err := builder.Build(ctx, vecs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty index data")
	}
	t.Logf("Index size: %d bytes for %d vectors of dim %d", len(data), 300, dims)

	// Load + Search.
	searcher := factory.NewSearcher(config)
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "diskann"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	// Search.
	ids, dists, err := searcher.Search(ctx, vecs[0], 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("no results")
	}
	if len(ids) != len(dists) {
		t.Fatalf("ids/dists length mismatch: %d vs %d", len(ids), len(dists))
	}

	// Self-search: top result should be query itself.
	if ids[0] != 0 {
		t.Logf("expected ID 0 as top result, got %d", ids[0])
	}
	if dists[0] > 1e-4 {
		t.Errorf("self-search distance %f, expected near-zero", dists[0])
	}

	// Distances should be sorted ascending.
	for i := 1; i < len(dists); i++ {
		if dists[i] < dists[i-1] {
			t.Errorf("distances not sorted: [%d]=%f < [%d]=%f", i, dists[i], i-1, dists[i-1])
		}
	}
}

func TestDiskANNSearchWithRecompute(t *testing.T) {
	factory, err := gleann.GetBackend("diskann")
	if err != nil {
		t.Fatalf("get backend: %v", err)
	}

	config := gleann.DefaultConfig()
	config.Backend = "diskann"
	config.DiskANNConfig.R = 32
	config.DiskANNConfig.L = 50

	dims := 32
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 200, dims)

	ctx := context.Background()
	builder := factory.NewBuilder(config)
	data, err := builder.Build(ctx, vecs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := factory.NewSearcher(config)
	meta := gleann.IndexMeta{Dimensions: dims, Backend: "diskann"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	recompute := func(ctx context.Context, ids []int64) ([][]float32, error) {
		result := make([][]float32, len(ids))
		for i, id := range ids {
			if id >= 0 && id < int64(len(vecs)) {
				result[i] = vecs[id]
			}
		}
		return result, nil
	}

	ids, dists, err := searcher.SearchWithRecompute(ctx, vecs[0], 5, recompute)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("no results")
	}
	if dists[0] > 1e-4 {
		t.Errorf("self-search distance %f, expected near-zero", dists[0])
	}
}

func TestDiskANNAddRemoveVectors(t *testing.T) {
	factory, err := gleann.GetBackend("diskann")
	if err != nil {
		t.Fatalf("get backend: %v", err)
	}

	config := gleann.DefaultConfig()
	config.Backend = "diskann"
	config.DiskANNConfig.R = 16
	config.DiskANNConfig.L = 30

	dims := 16
	rng := rand.New(rand.NewSource(42))
	initial := randomVectors(rng, 100, dims)
	additional := randomVectors(rng, 50, dims)

	ctx := context.Background()
	builder := factory.NewBuilder(config)

	data, err := builder.Build(ctx, initial)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Add vectors.
	data2, err := builder.AddVectors(ctx, data, additional, 100)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(data2) <= len(data) {
		t.Errorf("expected larger index after adding: %d <= %d", len(data2), len(data))
	}

	// Remove vectors.
	data3, err := builder.RemoveVectors(ctx, data, []int64{0, 1, 2})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(data3) >= len(data) {
		t.Errorf("expected smaller index after removing: %d >= %d", len(data3), len(data))
	}
}

func TestDiskANNRecallComparison(t *testing.T) {
	// Compare DiskANN recall@10 vs HNSW on the same dataset.
	dims := 64
	n := 1000
	queries := 100
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, n, dims)

	ctx := context.Background()

	// Build HNSW index.
	hnswFactory, err := gleann.GetBackend("hnsw")
	if err != nil {
		t.Fatalf("get hnsw: %v", err)
	}
	hnswConfig := gleann.DefaultConfig()
	hnswConfig.Backend = "hnsw"
	hnswConfig.HNSWConfig.M = 32
	hnswConfig.HNSWConfig.EfConstruction = 200
	hnswConfig.HNSWConfig.EfSearch = 128
	hnswConfig.HNSWConfig.PruneEmbeddings = false
	hnswConfig.HNSWConfig.UseMmap = false // load from bytes, not mmap

	hnswData, err := hnswFactory.NewBuilder(hnswConfig).Build(ctx, vecs)
	if err != nil {
		t.Fatalf("hnsw build: %v", err)
	}

	hnswSearcher := hnswFactory.NewSearcher(hnswConfig)
	if err := hnswSearcher.Load(ctx, hnswData, gleann.IndexMeta{Dimensions: dims, Backend: "hnsw"}); err != nil {
		t.Fatalf("hnsw load: %v", err)
	}
	defer hnswSearcher.Close()

	// Build DiskANN index.
	daFactory, err := gleann.GetBackend("diskann")
	if err != nil {
		t.Fatalf("get diskann: %v", err)
	}
	daConfig := gleann.DefaultConfig()
	daConfig.Backend = "diskann"
	daConfig.DiskANNConfig.R = 64
	daConfig.DiskANNConfig.L = 100
	daConfig.DiskANNConfig.Alpha = 1.2
	daConfig.DiskANNConfig.SearchL = 128

	daData, err := daFactory.NewBuilder(daConfig).Build(ctx, vecs)
	if err != nil {
		t.Fatalf("diskann build: %v", err)
	}

	daSearcher := daFactory.NewSearcher(daConfig)
	if err := daSearcher.Load(ctx, daData, gleann.IndexMeta{Dimensions: dims, Backend: "diskann"}); err != nil {
		t.Fatalf("diskann load: %v", err)
	}
	defer daSearcher.Close()

	// Compute brute-force ground truth for comparison.
	hnswRecall := 0
	daRecall := 0
	for q := 0; q < queries; q++ {
		query := vecs[q]

		// HNSW search.
		hnswIDs, _, err := hnswSearcher.Search(ctx, query, 10)
		if err != nil {
			t.Fatalf("hnsw search: %v", err)
		}
		for _, id := range hnswIDs {
			if id == int64(q) {
				hnswRecall++
				break
			}
		}

		// DiskANN search.
		daIDs, _, err := daSearcher.Search(ctx, query, 10)
		if err != nil {
			t.Fatalf("diskann search: %v", err)
		}
		for _, id := range daIDs {
			if id == int64(q) {
				daRecall++
				break
			}
		}
	}

	hnswRate := float64(hnswRecall) / float64(queries) * 100
	daRate := float64(daRecall) / float64(queries) * 100

	t.Logf("╭──────────────────────────────────────────╮")
	t.Logf("│  Recall@10 Comparison (%d vectors, %dd)  │", n, dims)
	t.Logf("├──────────────┬──────────────┬────────────┤")
	t.Logf("│              │ HNSW         │ DiskANN    │")
	t.Logf("├──────────────┼──────────────┼────────────┤")
	t.Logf("│ Self-recall  │  %5.1f%%      │  %5.1f%%   │", hnswRate, daRate)
	t.Logf("│ Index size   │  %6dKB    │  %6dKB  │", len(hnswData)/1024, len(daData)/1024)
	t.Logf("╰──────────────┴──────────────┴────────────╯")

	// DiskANN should have ≥80% recall (relaxed threshold for PQ approximation).
	if daRate < 80 {
		t.Errorf("DiskANN recall %.1f%% too low (expected ≥80%%)", daRate)
	}
	if hnswRate < 90 {
		t.Errorf("HNSW recall %.1f%% too low (expected ≥90%%)", hnswRate)
	}
}
