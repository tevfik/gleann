// Package gate is the recall gate of gleann's vector search: it fails when a
// change lowers recall@10 below thresholds measured on a 10,000 x 384 dataset.
// Unit tests did not catch the recall bugs of 2026-11 (DiskANN rerank width,
// HNSW neighbour top-up, filtered search with few matches, a zero Config
// turning the neighbour heuristic off); each row below would have.
//
// Thresholds sit a little under the measured value (in the comment of each
// row), so that noise does not fail the gate and a real regression does. A
// 10K index does not show every scale problem: run `make bench-vectordb` at
// 100K before a release.
//
//	cd bench/vectordb && go test -count=1 -v ./gate/
package gate

import (
	"context"
	"testing"

	"github.com/tevfik/gleann/bench/vectordb"
	_ "github.com/tevfik/gleann/bench/vectordb/adapters/diskann"
	_ "github.com/tevfik/gleann/bench/vectordb/adapters/hnsw"
	"github.com/tevfik/gleann/bench/vectordb/gleannsearch"
	"github.com/tevfik/gleann/pkg/gleann"
)

const (
	gateSize    = 10000
	gateDims    = 384
	gateQueries = 300
	gateK       = 10
)

// storeRecall loads ds into the named backend adapter with gleann's default
// build parameters and returns recall@10 at the given search width.
func storeRecall(t *testing.T, name string, ds *vectordb.Dataset, truth *vectordb.GroundTruth, efSearch int) float64 {
	t.Helper()
	ctx := context.Background()
	st, err := vectordb.New(name)
	if err != nil {
		t.Fatalf("store %s: %v", name, err)
	}
	p := vectordb.Params{Dims: ds.Dims, Metric: "cosine", M: 32, EfConstruction: 200, EfSearch: efSearch}
	if err := st.Open(ctx, t.TempDir(), p); err != nil {
		t.Fatalf("%s open: %v", name, err)
	}
	defer st.Close()
	if err := st.Insert(ctx, ds.IDs, ds.Vectors, ds.Metas); err != nil {
		t.Fatalf("%s insert: %v", name, err)
	}
	if err := st.Flush(ctx); err != nil {
		t.Fatalf("%s flush: %v", name, err)
	}
	got := make([][]int64, len(ds.Queries))
	for i, q := range ds.Queries {
		if got[i], err = st.Search(ctx, q, gateK, nil); err != nil {
			t.Fatalf("%s search %d: %v", name, i, err)
		}
	}
	return vectordb.AverageRecallAtK(truth.ExactTop100, got, gateK)
}

func check(t *testing.T, what string, got, min float64) {
	t.Helper()
	t.Logf("%-46s recall@%d = %.4f (gate %.3f)", what, gateK, got, min)
	if got < min {
		t.Errorf("%s: recall@%d = %.4f, below the gate %.3f", what, gateK, got, min)
	}
}

func TestRecallGate(t *testing.T) {
	if testing.Short() {
		t.Skip("recall gate builds 10K-vector indexes; skipped with -short")
	}
	ds := vectordb.GenerateDataset(gateSize, gateDims, gateQueries, 42)
	truth := vectordb.ComputeGroundTruth(ds, "cosine")

	t.Run("hnsw", func(t *testing.T) {
		check(t, "hnsw, ef 128 (default)", storeRecall(t, "hnsw", ds, truth, 128), gateHNSW)
	})
	t.Run("diskann", func(t *testing.T) {
		check(t, "diskann, search_l 100 (default)", storeRecall(t, "diskann", ds, truth, 100), gateDiskANN)
	})
	t.Run("hnsw-near-copies", func(t *testing.T) {
		dup := vectordb.GenerateDataset(gateSize, gateDims, gateQueries, 42)
		vectordb.AddNearDuplicates(dup, 0.3, 20, 7)
		dupTruth := vectordb.ComputeGroundTruth(dup, "cosine")
		check(t, "hnsw, 30% near-copies, ef 16", storeRecall(t, "hnsw", dup, dupTruth, 16), gateHNSWDup)
	})
	t.Run("gleann-search", func(t *testing.T) {
		ctx := context.Background()
		s, _, err := gleannsearch.Open(ctx, t.TempDir(), ds, gleannsearch.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		runSearchRows(t, s, ds, []searchRow{
			{"gleann search, no filter", "", truth.ExactTop100, gateSearchAll},
			{"gleann search, filter 1%", "t1", truth.Filtered1Pct, gateSearch1},
			{"gleann search, filter 10%", "t10", truth.Filtered10Pct, gateSearch10},
			{"gleann search, filter 50%", "t50", truth.Filtered50Pct, gateSearch50},
		})
	})
	t.Run("gleann-search-diskann", func(t *testing.T) {
		ctx := context.Background()
		s, _, err := gleannsearch.Open(ctx, t.TempDir(), ds, gleannsearch.Options{Backend: "diskann"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		runSearchRows(t, s, ds, []searchRow{
			{"gleann search diskann, no filter", "", truth.ExactTop100, gateDiskSearchAll},
			{"gleann search diskann, filter 1%", "t1", truth.Filtered1Pct, gateDiskSearch1},
			{"gleann search diskann, filter 10%", "t10", truth.Filtered10Pct, gateDiskSearch10},
			{"gleann search diskann, filter 50%", "t50", truth.Filtered50Pct, gateDiskSearch50},
		})
	})
}

// searchRow is one filtered-search gate row: a tag bucket of known
// selectivity, its exact ground truth and the recall gate.
type searchRow struct {
	what, field string
	truth       [][]int64
	min         float64
}

// runSearchRows queries every dataset query through the real gleann search
// path for each row and checks recall@10 against the row's gate.
func runSearchRows(t *testing.T, s *gleann.LeannSearcher, ds *vectordb.Dataset, rows []searchRow) {
	t.Helper()
	ctx := context.Background()
	for _, row := range rows {
		got := make([][]int64, len(ds.Queries))
		var err error
		for i := range ds.Queries {
			if got[i], err = gleannsearch.Query(ctx, s, i, gateK, 1.0, row.field); err != nil {
				t.Fatal(err)
			}
			if len(got[i]) < gateK {
				t.Errorf("%s: query %d returned %d results, want %d", row.what, i, len(got[i]), gateK)
			}
		}
		check(t, row.what, vectordb.AverageRecallAtK(row.truth, got, gateK), row.min)
	}
}
