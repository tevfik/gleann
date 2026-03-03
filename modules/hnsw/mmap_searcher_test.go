package hnsw

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestMmapSearcher(t *testing.T) {
	// 1. Setup a temp directory
	tmpDir, err := os.MkdirTemp("", "mmap_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// 2. Generate random vectors and build a standard CSR graph
	rng := rand.New(rand.NewSource(42))
	dims := 16
	numVecs := 100
	embeddings := make([][]float32, numVecs)

	for i := 0; i < numVecs; i++ {
		embeddings[i] = make([]float32, dims)
		for j := 0; j < dims; j++ {
			embeddings[i][j] = rng.Float32()
		}
	}

	config := DefaultConfig()
	config.HNSWConfig.PruneEmbeddings = true
	config.HNSWConfig.PruneKeepFraction = 0.0 // Keep only the entry point

	builder := &Builder{config: config}
	ctx := context.Background()
	indexData, err := builder.Build(ctx, embeddings)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Write index to a file so we can mmap it
	indexPath := filepath.Join(tmpDir, "index.csr")
	if err := os.WriteFile(indexPath, indexData, 0644); err != nil {
		t.Fatal(err)
	}

	// 4. Test MmapSearcher
	mmapSearcher := &MmapSearcher{config: config}
	if err := mmapSearcher.LoadFromFile(ctx, indexPath); err != nil {
		t.Fatal(err)
	}
	defer mmapSearcher.Close()

	// 5. Build standard searcher to compare results
	stdSearcher := &Searcher{config: config}
	if err := stdSearcher.Load(ctx, indexData, IndexMeta{}); err != nil {
		t.Fatal(err)
	}
	defer stdSearcher.Close()

	// Recomputer function for tests (mocks computing back from original array)
	// Notice that the MmapSearcher returns true external IDs, so our IDs match the index in 'embeddings' directly
	recomputeFunc := func(ids []int64) [][]float32 {
		res := make([][]float32, len(ids))
		for i, id := range ids {
			res[i] = embeddings[id]
		}
		return res
	}

	queries := 5
	for q := 0; q < queries; q++ {
		query := make([]float32, dims)
		for j := 0; j < dims; j++ {
			query[j] = rng.Float32()
		}

		topK := 5

		stdIDs, stdDist, err := stdSearcher.SearchWithRecompute(ctx, query, topK, func(c context.Context, ids []int64) ([][]float32, error) {
			return recomputeFunc(ids), nil
		})
		if err != nil {
			t.Fatal(err)
		}

		mmapIDs, mmapDist, err := mmapSearcher.SearchWithRecompute(ctx, query, topK, func(ctx context.Context, ids []int64) ([][]float32, error) {
			return recomputeFunc(ids), nil
		})
		if err != nil {
			t.Fatal(err)
		}

		// Verify same number of results
		if len(stdIDs) != len(mmapIDs) {
			t.Fatalf("Query %d: Length mismatch std=%d, mmap=%d", q, len(stdIDs), len(mmapIDs))
		}

		// Verify result parity
		for i := 0; i < len(stdIDs); i++ {
			if stdIDs[i] != mmapIDs[i] {
				t.Errorf("Query %d Rank %d: ID mismatch std=%d mmap=%d", q, i, stdIDs[i], mmapIDs[i])
			}
			// Comparing floats exactly might randomly fall to precision issues due to arithmetic order,
			// but distances should be very close.
			diff := stdDist[i] - mmapDist[i]
			if diff < 0 {
				diff = -diff
			}
			if diff > 1e-4 {
				t.Errorf("Query %d Rank %d: Dist mismatch std=%f mmap=%f", q, i, stdDist[i], mmapDist[i])
			}
		}
	}
}
