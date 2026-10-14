package hnsw

import (
	"context"
	"math/rand"
	"testing"
)

func randVecs(n, d int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, d)
		for j := range v {
			v[j] = rng.Float32()
		}
		out[i] = v
	}
	return out
}

// Editing a pruned graph used to silently drop every node without a stored
// vector (RemoveVectors) or insert against meaningless distances (AddVectors).
// It must now fail so the caller falls back to a full rebuild.
func TestPrunedIndexRejectsIncrementalEdits(t *testing.T) {
	config := DefaultConfig()
	config.HNSWConfig.M = 4
	config.HNSWConfig.PruneEmbeddings = true
	config.HNSWConfig.PruneKeepFraction = 0.0
	b := &Builder{config: config}
	ctx := context.Background()
	data, err := b.Build(ctx, randVecs(20, 16, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddVectors(ctx, data, randVecs(2, 16, 2), 20); err == nil {
		t.Error("AddVectors on pruned index must fail")
	}
	if _, err := b.RemoveVectors(ctx, data, []int64{3}); err == nil {
		t.Error("RemoveVectors on pruned index must fail")
	}
}

func TestUnprunedIndexAcceptsAdd(t *testing.T) {
	config := DefaultConfig()
	config.HNSWConfig.M = 4
	config.HNSWConfig.PruneEmbeddings = false
	b := &Builder{config: config}
	ctx := context.Background()
	data, err := b.Build(ctx, randVecs(20, 16, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddVectors(ctx, data, randVecs(2, 16, 2), 20); err != nil {
		t.Fatalf("AddVectors: %v", err)
	}
}
