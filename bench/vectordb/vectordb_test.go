package vectordb_test

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/bench/vectordb"
)

// TestGroundTruthMatchesBruteForce checks that ComputeGroundTruth equals
// direct brute-force computation on a small test set.
func TestGroundTruthMatchesBruteForce(t *testing.T) {
	ds := vectordb.GenerateDataset(100, 16, 5, 123)
	truth := vectordb.ComputeGroundTruth(ds, "cosine")

	if len(truth.ExactTop100) != 5 {
		t.Fatalf("expected 5 queries, got %d", len(truth.ExactTop100))
	}

	for qIdx, q := range ds.Queries {
		type pair struct {
			id   int64
			dist float32
		}
		var pairs []pair
		for i, v := range ds.Vectors {
			d := vectordb.ComputeDistance(q, v, "cosine")
			pairs = append(pairs, pair{id: ds.IDs[i], dist: d})
		}
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].dist == pairs[j].dist {
				return pairs[i].id < pairs[j].id
			}
			return pairs[i].dist < pairs[j].dist
		})

		k := min(100, len(pairs))
		for i := 0; i < k; i++ {
			if truth.ExactTop100[qIdx][i] != pairs[i].id {
				t.Fatalf("query %d rank %d mismatch: got %d, want %d",
					qIdx, i, truth.ExactTop100[qIdx][i], pairs[i].id)
			}
		}
	}
}

// TestRecallComputation checks RecallAtK and AverageRecallAtK on known inputs.
func TestRecallComputation(t *testing.T) {
	// Exact match
	r1 := vectordb.RecallAtK([]int64{1, 2, 3, 4}, []int64{1, 2, 3, 4}, 4)
	if r1 != 1.0 {
		t.Errorf("expected 1.0, got %f", r1)
	}

	// 50% match
	r2 := vectordb.RecallAtK([]int64{1, 2, 3, 4}, []int64{1, 2, 8, 9}, 4)
	if r2 != 0.5 {
		t.Errorf("expected 0.5, got %f", r2)
	}

	// Disjoint match
	r3 := vectordb.RecallAtK([]int64{1, 2, 3, 4}, []int64{5, 6, 7, 8}, 4)
	if r3 != 0.0 {
		t.Errorf("expected 0.0, got %f", r3)
	}

	// Partial truth with empty
	r4 := vectordb.RecallAtK(nil, nil, 5)
	if r4 != 1.0 {
		t.Errorf("expected 1.0 for empty sets, got %f", r4)
	}

	// Average recall across queries
	avg := vectordb.AverageRecallAtK(
		[][]int64{{1, 2}, {3, 4}},
		[][]int64{{1, 2}, {3, 9}},
		2,
	)
	if avg != 0.75 { // (1.0 + 0.5) / 2
		t.Errorf("expected 0.75, got %f", avg)
	}
}

// wrongStore returns pseudo-random IDs on search to verify that the benchmark
// harness detects low-accuracy stores.
type wrongStore struct{}

func (w *wrongStore) Name() string                                                  { return "wrong_store" }
func (w *wrongStore) Caveats() []string                                             { return []string{"intentionally inaccurate"} }
func (w *wrongStore) Open(ctx context.Context, dir string, p vectordb.Params) error { return nil }
func (w *wrongStore) Insert(ctx context.Context, ids []int64, vecs [][]float32, meta []vectordb.Meta) error {
	return nil
}
func (w *wrongStore) Upsert(ctx context.Context, ids []int64, vecs [][]float32, meta []vectordb.Meta) error {
	return nil
}
func (w *wrongStore) Delete(ctx context.Context, ids []int64) error { return nil }
func (w *wrongStore) Flush(ctx context.Context) error               { return nil }
func (w *wrongStore) Search(ctx context.Context, q []float32, k int, f *vectordb.Filter) ([]int64, error) {
	out := make([]int64, k)
	for i := 0; i < k; i++ {
		out[i] = int64(999000 + rand.Intn(1000))
	}
	return out, nil
}
func (w *wrongStore) Stats(ctx context.Context) (vectordb.Stats, error) {
	return vectordb.Stats{Count: 100}, nil
}
func (w *wrongStore) Close() error { return nil }

func init() {
	vectordb.Register("wrong_store", func() vectordb.Store { return &wrongStore{} })
}

// TestDeliberatelyWrongStoreFails verifies that a store returning random IDs
// is caught by the harness recall check.
func TestDeliberatelyWrongStoreFails(t *testing.T) {
	dir := t.TempDir()
	cfg := vectordb.Config{
		Sizes:   []int{100},
		Dims:    16,
		Queries: 10,
		K:       5,
		Stores:  []string{"wrong_store"},
		WorkDir: dir,
	}

	rep, err := vectordb.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error running benchmark: %v", err)
	}

	if !rep.Failed() {
		t.Fatal("expected rep.Failed() to be true for store returning random IDs")
	}

	// Verify reported recall is very low (< 0.10)
	sr := rep.Sizes[0].Results[0]
	for _, sp := range sr.Sweep {
		if sp.Recall > 0.10 {
			t.Errorf("expected recall near 0, got %f", sp.Recall)
		}
	}
}

// TestReportJSONRoundTrip checks serialization and deserialization of Report.
func TestReportJSONRoundTrip(t *testing.T) {
	rep := &vectordb.Report{
		Time:    "2026-11-19 12:00:00",
		Go:      "go1.25.9",
		Platf:   "linux/amd64",
		CPUs:    8,
		Dims:    384,
		K:       10,
		Queries: 100,
		Sizes: []vectordb.SizeRun{
			{
				Size:    1000,
				Dims:    384,
				Queries: 100,
				K:       10,
				Results: []*vectordb.StoreResult{
					{
						Store:        "hnsw",
						LoadMS:       123.4,
						ColdStartMS:  5.6,
						FileUpdateMS: 1.2,
						DiskBytes:    10240,
						Caveats:      []string{"test caveat"},
						Sweep: []vectordb.SweepPoint{
							{EfSearch: 32, Recall: 0.96, P50us: 1500, P95us: 2000, QPSC1: 500, QPSC8: 3000},
						},
						RecallMatched: map[string]vectordb.RecallMatchedRow{
							"0.95": {TargetRecall: 0.95, AchievedRecall: 0.96, EfSearch: 32, P50us: 1500, Reached: true},
						},
						Filtered: map[string]vectordb.FilteredRow{
							"10%": {Selectivity: "10%", Recall: 0.94, P50us: 1200, P95us: 1800},
						},
					},
				},
			},
		},
	}

	data, err := rep.JSON()
	if err != nil {
		t.Fatalf("marshaling report: %v", err)
	}

	var parsed vectordb.Report
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshaling report: %v", err)
	}

	if parsed.Time != rep.Time || parsed.Dims != rep.Dims || len(parsed.Sizes) != 1 {
		t.Fatalf("parsed report mismatch: %+v", parsed)
	}

	md := rep.Markdown()
	if !strings.Contains(md, "Vector database benchmark") || !strings.Contains(md, "hnsw") {
		t.Fatalf("markdown output missing expected contents:\n%s", md)
	}

	dir := t.TempDir()
	jPath, mPath, err := rep.Write(dir)
	if err != nil {
		t.Fatalf("writing report: %v", err)
	}
	if _, err := os.Stat(jPath); err != nil {
		t.Errorf("expected json file %s: %v", jPath, err)
	}
	if _, err := os.Stat(mPath); err != nil {
		t.Errorf("expected md file %s: %v", mPath, err)
	}
}

// TestFvecsIvecsRoundTrip verifies loading and saving .fvecs and .ivecs files.
func TestFvecsIvecsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// 1. Fvecs
	fvecsPath := filepath.Join(dir, "test.fvecs")
	vecs := [][]float32{
		{0.1, 0.2, 0.3},
		{0.4, 0.5, 0.6},
	}
	if err := vectordb.SaveFvecs(fvecsPath, vecs); err != nil {
		t.Fatalf("SaveFvecs: %v", err)
	}
	loadedVecs, err := vectordb.LoadFvecs(fvecsPath)
	if err != nil {
		t.Fatalf("LoadFvecs: %v", err)
	}
	if len(loadedVecs) != len(vecs) || len(loadedVecs[0]) != 3 {
		t.Fatalf("fvecs dimension or count mismatch: %+v", loadedVecs)
	}
	for i := range vecs {
		for d := range vecs[i] {
			if loadedVecs[i][d] != vecs[i][d] {
				t.Errorf("fvecs[%d][%d] mismatch: got %f, want %f", i, d, loadedVecs[i][d], vecs[i][d])
			}
		}
	}

	// 2. Ivecs
	ivecsPath := filepath.Join(dir, "test.ivecs")
	entries := [][]int32{
		{10, 20, 30},
		{40, 50, 60},
	}
	if err := vectordb.SaveIvecs(ivecsPath, entries); err != nil {
		t.Fatalf("SaveIvecs: %v", err)
	}
	loadedEntries, err := vectordb.LoadIvecs(ivecsPath)
	if err != nil {
		t.Fatalf("LoadIvecs: %v", err)
	}
	if len(loadedEntries) != len(entries) || len(loadedEntries[0]) != 3 {
		t.Fatalf("ivecs dimension or count mismatch: %+v", loadedEntries)
	}
	for i := range entries {
		for d := range entries[i] {
			if loadedEntries[i][d] != entries[i][d] {
				t.Errorf("ivecs[%d][%d] mismatch: got %d, want %d", i, d, loadedEntries[i][d], entries[i][d])
			}
		}
	}
}

// AddNearDuplicates replaces the requested share with near-copies of a few
// base vectors, keeps them unit length, and changes the ground-truth cache
// key so a plain dataset's cached truth is not reused.
func TestAddNearDuplicates(t *testing.T) {
	ds := vectordb.GenerateDataset(1000, 16, 5, 42)
	plain := vectordb.GenerateDataset(1000, 16, 5, 42)
	vectordb.AddNearDuplicates(ds, 0.3, 20, 7)
	if ds.DupFraction != 0.3 {
		t.Fatalf("DupFraction = %v", ds.DupFraction)
	}
	bases := map[int]bool{}
	for i := 0; i < 300; i++ {
		var norm float64
		for _, x := range ds.Vectors[i] {
			norm += float64(x) * float64(x)
		}
		if norm < 0.999 || norm > 1.001 {
			t.Fatalf("vector %d has norm² %v", i, norm)
		}
		// each near-copy is within a tiny distance of one of the last 20 vectors
		found := false
		for b := 0; b < 20; b++ {
			base := ds.Vectors[999-b]
			var d float64
			for j := range base {
				diff := float64(base[j] - ds.Vectors[i][j])
				d += diff * diff
			}
			if d < 1e-5 {
				found, bases[b] = true, true
				break
			}
		}
		if !found {
			t.Fatalf("vector %d is not a near-copy of a base vector", i)
		}
	}
	if len(bases) < 10 {
		t.Errorf("near-copies use only %d of 20 base vectors", len(bases))
	}
	if &ds.Vectors[300][0] == &plain.Vectors[300][0] || ds.Vectors[300][0] != plain.Vectors[300][0] {
		t.Errorf("vectors after the replaced share must be unchanged")
	}
	dir := t.TempDir()
	if _, err := vectordb.LoadOrComputeGroundTruth(dir, plain, "cosine"); err != nil {
		t.Fatal(err)
	}
	if _, err := vectordb.LoadOrComputeGroundTruth(dir, ds, "cosine"); err != nil {
		t.Fatal(err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "truth_*")); len(files) != 2 {
		t.Errorf("want separate truth caches for the plain and near-copy datasets, got %v", files)
	}
}

// TestDatasetFromVectors: real vectors become a dataset with held-out queries,
// deterministic for a seed, no vector both a query and a corpus entry.
func TestDatasetFromVectors(t *testing.T) {
	const n, dims, q = 500, 8, 50
	rng := rand.New(rand.NewSource(3))
	vecs := make([][]float32, n)
	for i := range vecs {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		v[0] = float32(i) // unique marker
		vecs[i] = v
	}
	ds, err := vectordb.DatasetFromVectors(vecs, q, 42, "unit.fvecs")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Queries) != q || len(ds.Vectors) != n-q || len(ds.IDs) != n-q || len(ds.Metas) != n-q || ds.Dims != dims || ds.Source != "unit.fvecs" {
		t.Fatalf("shape: %d queries, %d vectors, %d ids, %d metas, dims %d, source %q", len(ds.Queries), len(ds.Vectors), len(ds.IDs), len(ds.Metas), ds.Dims, ds.Source)
	}
	seen := make(map[float32]bool, n)
	for _, v := range append(append([][]float32{}, ds.Queries...), ds.Vectors...) {
		if seen[v[0]] {
			t.Fatalf("vector %v appears twice (as a query and in the corpus)", v[0])
		}
		seen[v[0]] = true
	}
	if len(seen) != n {
		t.Fatalf("%d distinct vectors in the dataset, want all %d", len(seen), n)
	}
	for i, id := range ds.IDs {
		if id != int64(i) {
			t.Fatalf("IDs must be dense 0..n-1, got %d at %d", id, i)
		}
	}
	// Metadata keeps the selectivities the filtered phase relies on.
	if ds.Metas[0].Tag != 0 || ds.Metas[99].Tag != 99 || ds.Metas[100].Tag != 0 || ds.Metas[24].Source == ds.Metas[23].Source {
		t.Errorf("synthetic metadata layout changed: %+v %+v", ds.Metas[0], ds.Metas[24])
	}
	again, err := vectordb.DatasetFromVectors(vecs, q, 42, "unit.fvecs")
	if err != nil {
		t.Fatal(err)
	}
	for i := range ds.Queries {
		if ds.Queries[i][0] != again.Queries[i][0] {
			t.Fatal("the held-out queries differ between two runs with the same seed")
		}
	}

	if _, err := vectordb.DatasetFromVectors(vecs[:60], q, 42, ""); err == nil {
		t.Error("too few vectors for the held-out queries: want an error")
	}
	bad := append([][]float32{}, vecs...)
	bad[7] = bad[7][:dims-1]
	if _, err := vectordb.DatasetFromVectors(bad, q, 42, ""); err == nil {
		t.Error("mixed dimensions: want an error")
	}
}
