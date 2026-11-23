package diskann

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// vectorIDScorer is the capability gleann.VectorIDScorer asks of a backend
// (modules must not import pkg/gleann). The assertion keeps this file
// compilable on a searcher that does not implement it yet: the test then
// fails at runtime instead of at compile time.
type vectorIDScorer interface {
	VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error)
}

// TestSearcherVectorDistances: the DiskANN searcher must score explicit node
// IDs with EXACT distances from the raw stored vectors (not PQ), on the RAM
// and the mmap load path, for l2 and cosine. Filtered search scores a
// SQL-pre-selected set through this capability (gleann.VectorIDScorer);
// without it a selective filter on the default backend stays approximate.
func TestSearcherVectorDistances(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(42))
	const n, dims = 40, 16
	vecs := make([][]float32, n)
	for i := range vecs {
		vecs[i] = make([]float32, dims)
		for d := range vecs[i] {
			vecs[i][d] = rng.Float32()
		}
	}

	for _, metric := range []DistanceMetric{DistanceL2, DistanceCosine} {
		cfg := DiskANNConfig{
			R:              16,
			L:              30,
			Alpha:          1.2,
			PQDim:          4,
			PQCentroids:    16,
			SearchL:        30,
			DistanceMetric: metric,
		}
		idx, err := BuildDiskIndex(vecs, cfg)
		if err != nil {
			t.Fatalf("BuildDiskIndex(%s): %v", metric, err)
		}
		var buf bytes.Buffer
		if _, err := idx.WriteTo(&buf); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
		data := buf.Bytes()
		path := filepath.Join(t.TempDir(), "test.index")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		for _, mode := range []string{"ram", "mmap"} {
			t.Run(string(metric)+"/"+mode, func(t *testing.T) {
				s := &Searcher{config: Config{Backend: "diskann", DiskANNConfig: cfg}}
				defer s.Close()
				switch mode {
				case "ram":
					err = s.Load(ctx, data, IndexMeta{Dimensions: dims, Backend: "diskann"})
				case "mmap":
					err = s.LoadFromFile(ctx, path)
				}
				if err != nil {
					t.Fatalf("load (%s): %v", mode, err)
				}
				var any interface{} = s
				vs, ok := any.(vectorIDScorer)
				if !ok {
					t.Fatalf("diskann Searcher (%s) does not implement VectorDistances: filtered search has no exact path", mode)
				}

				// Exact distances from the raw vectors, in the given order.
				// PQ approximations would differ; equality proves the raw path.
				query := vecs[3]
				ids := []int64{3, 7, 25, 0}
				dist := GetDistanceFunc(string(metric))
				got, err := vs.VectorDistances(ctx, query, ids)
				if err != nil {
					t.Fatalf("VectorDistances: %v", err)
				}
				if len(got) != len(ids) {
					t.Fatalf("VectorDistances returned %d distances for %d ids", len(got), len(ids))
				}
				for i, id := range ids {
					want := dist(query, vecs[id])
					if got[i] != want {
						t.Errorf("distance to node %d = %v, want exact %v (raw vectors, not PQ)", id, got[i], want)
					}
				}
				if got[0] != 0 {
					t.Errorf("distance to the query's own vector = %v, want 0", got[0])
				}

				// Empty id list: empty result, no error.
				if d, err := vs.VectorDistances(ctx, query, nil); err != nil || len(d) != 0 {
					t.Errorf("VectorDistances(nil ids) = %v, %v", d, err)
				}
			})
		}
	}
}

// TestSearcherVectorDistancesErrors: scoring explicit IDs fails cleanly when
// no index is loaded, the query dimension is wrong, or a node has no stored
// vector, so the caller can fall back instead of scoring garbage.
func TestSearcherVectorDistancesErrors(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig()
	vecs := [][]float32{{1, 0}, {0, 1}, {1, 1}}
	data, err := (&Builder{config: cfg}).Build(ctx, vecs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	t.Run("no index loaded", func(t *testing.T) {
		s := &Searcher{config: cfg}
		var any interface{} = s
		vs, ok := any.(vectorIDScorer)
		if !ok {
			t.Fatal("diskann Searcher does not implement VectorDistances")
		}
		if _, err := vs.VectorDistances(ctx, []float32{1, 0}, []int64{0}); err == nil {
			t.Error("expected an error without a loaded index")
		}
	})

	s := &Searcher{config: cfg}
	defer s.Close()
	if err := s.Load(ctx, data, IndexMeta{Dimensions: 2, Backend: "diskann"}); err != nil {
		t.Fatalf("load: %v", err)
	}
	var any interface{} = s
	vs, ok := any.(vectorIDScorer)
	if !ok {
		t.Fatal("diskann Searcher does not implement VectorDistances")
	}
	for _, tc := range []struct {
		name  string
		query []float32
		ids   []int64
	}{
		{"wrong query dimension", []float32{1, 0, 0}, []int64{0}},
		{"negative id", []float32{1, 0}, []int64{-1}},
		{"id beyond the index", []float32{1, 0}, []int64{int64(len(vecs))}},
		{"one bad id spoils the set", []float32{1, 0}, []int64{0, 99}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := vs.VectorDistances(ctx, tc.query, tc.ids); err == nil {
				t.Errorf("VectorDistances(%v, %v): expected an error", tc.query, tc.ids)
			} else {
				t.Logf("error (expected): %v", err)
			}
		})
	}
}
