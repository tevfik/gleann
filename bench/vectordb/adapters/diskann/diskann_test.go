package diskann_test

import (
	"context"
	"testing"

	"github.com/tevfik/gleann/bench/vectordb"
	_ "github.com/tevfik/gleann/bench/vectordb/adapters/diskann"
)

func TestDiskANNAdapter_InsertSearchUpsertDeleteFilter(t *testing.T) {
	ctx := context.Background()
	const (
		numVectors = 1000
		dims       = 32
		numQueries = 20
		k          = 10
	)

	ds := vectordb.GenerateDataset(numVectors, dims, numQueries, 42)
	truth := vectordb.ComputeGroundTruth(ds, "cosine")

	store, err := vectordb.New("diskann")
	if err != nil {
		t.Fatalf("vectordb.New(diskann): %v", err)
	}

	dir := t.TempDir()
	params := vectordb.Params{
		Dims:           dims,
		Metric:         "cosine",
		M:              64,
		EfConstruction: 100,
		EfSearch:       100,
	}

	if err := store.Open(ctx, dir, params); err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer store.Close()

	// 1. Insert 1K
	if err := store.Insert(ctx, ds.IDs, ds.Vectors, ds.Metas); err != nil {
		t.Fatalf("store.Insert: %v", err)
	}
	if err := store.Flush(ctx); err != nil {
		t.Fatalf("store.Flush: %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil || stats.Count != int64(numVectors) {
		t.Fatalf("expected count %d, got %d (err: %v)", numVectors, stats.Count, err)
	}

	// 2. Search & compare with exact search
	results := make([][]int64, numQueries)
	for qIdx, q := range ds.Queries {
		ids, err := store.Search(ctx, q, k, nil)
		if err != nil {
			t.Fatalf("store.Search query %d: %v", qIdx, err)
		}
		if len(ids) != k {
			t.Fatalf("expected %d results, got %d", k, len(ids))
		}
		results[qIdx] = ids
	}
	recall := vectordb.AverageRecallAtK(truth.ExactTop100, results, k)
	// The adapter searches like gleann's DiskANN searcher: PQ beam search,
	// then an exact rerank of all L candidates. A broken adapter returns
	// ~0.01; the old 2*k rerank reached only about 0.78 on this set.
	if recall < 0.90 {
		t.Fatalf("expected recall@10 >= 0.90, got %f", recall)
	}

	// 3. Upsert
	upsertIDs := []int64{10, 11, 12, 13, 14}
	upsertVecs := make([][]float32, len(upsertIDs))
	upsertMetas := make([]vectordb.Meta, len(upsertIDs))
	for i := range upsertIDs {
		vec := make([]float32, dims)
		vec[i%dims] = 1.0 // distinct orthogonal direction
		upsertVecs[i] = vec
		upsertMetas[i] = vectordb.Meta{
			Source: "upserted_file.go",
			Kind:   "code",
			Tag:    5,
		}
	}
	if err := store.Upsert(ctx, upsertIDs, upsertVecs, upsertMetas); err != nil {
		t.Fatalf("store.Upsert: %v", err)
	}
	if err := store.Flush(ctx); err != nil {
		t.Fatalf("store.Flush after upsert: %v", err)
	}

	topHits, err := store.Search(ctx, upsertVecs[0], 1, nil)
	if err != nil || len(topHits) == 0 || topHits[0] != upsertIDs[0] {
		t.Fatalf("expected upserted ID %d as top hit, got %v (err: %v)", upsertIDs[0], topHits, err)
	}

	// 4. Delete
	deleteIDs := []int64{20, 21, 22, 23, 24}
	if err := store.Delete(ctx, deleteIDs); err != nil {
		t.Fatalf("store.Delete: %v", err)
	}
	if err := store.Flush(ctx); err != nil {
		t.Fatalf("store.Flush after delete: %v", err)
	}

	deletedSet := make(map[int64]bool)
	for _, id := range deleteIDs {
		deletedSet[id] = true
	}
	for qIdx, q := range ds.Queries {
		ids, err := store.Search(ctx, q, k, nil)
		if err != nil {
			t.Fatalf("store.Search after delete: %v", err)
		}
		for _, id := range ids {
			if deletedSet[id] {
				t.Fatalf("query %d returned deleted ID %d", qIdx, id)
			}
		}
	}

	// 5. Filter
	filter := &vectordb.Filter{
		Kind:   "code",
		TagMax: 10,
	}
	for qIdx, q := range ds.Queries {
		ids, err := store.Search(ctx, q, k, filter)
		if err != nil {
			t.Fatalf("store.Search with filter: %v", err)
		}
		for _, id := range ids {
			var m vectordb.Meta
			if id < int64(len(ds.Metas)) {
				m = ds.Metas[id]
			}
			for uIdx, uID := range upsertIDs {
				if uID == id {
					m = upsertMetas[uIdx]
				}
			}
			if !filter.Matches(m) {
				t.Fatalf("query %d result ID %d meta %+v does not match filter %+v", qIdx, id, m, filter)
			}
		}
	}

	// Check caveats
	if len(store.Caveats()) == 0 {
		t.Errorf("expected store to document caveats")
	}
}
