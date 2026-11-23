package backends

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// capDiskANNVD adds VectorDistances to bareDiskANN and records its calls.
type capDiskANNVD struct {
	bareDiskANN
	distIDs []int64
}

func (c *capDiskANNVD) VectorDistances(_ context.Context, _ []float32, ids []int64) ([]float32, error) {
	c.distIDs = ids
	return []float32{9}, nil
}

// TestDiskANNAdapterVectorDistancesForwarding: both DiskANN adapters (RAM and
// mmap) must expose gleann.VectorIDScorer, forwarding to a capable inner
// searcher and erroring cleanly on a bare one, exactly like the hnsw adapter.
// The interface assertions keep the test compilable before the adapters
// implement the capability: it then fails at runtime.
func TestDiskANNAdapterVectorDistancesForwarding(t *testing.T) {
	ctx := context.Background()

	bare := &diskannMmapSearcherAdapter{diskannSearcherAdapter{inner: &bareDiskANN{}}}
	vs, ok := interface{}(bare).(gleann.VectorIDScorer)
	if !ok {
		t.Fatal("diskann mmap adapter is no VectorIDScorer: filtered search has no exact path on the default backend")
	}
	if _, err := vs.VectorDistances(ctx, nil, []int64{1}); err == nil || !strings.Contains(err.Error(), "VectorDistances") {
		t.Errorf("bare inner: VectorDistances err = %v, want a VectorDistances error", err)
	}

	inner := &capDiskANNVD{}
	capable := &diskannSearcherAdapter{inner: inner}
	vs2, ok := interface{}(capable).(gleann.VectorIDScorer)
	if !ok {
		t.Fatal("diskann RAM adapter is no VectorIDScorer")
	}
	d, err := vs2.VectorDistances(ctx, []float32{1}, []int64{4, 5})
	if err != nil || len(d) != 1 || d[0] != 9 || len(inner.distIDs) != 2 {
		t.Errorf("VectorDistances = %v, %v (ids %v)", d, err, inner.distIDs)
	}
}

// TestDiskANNRealSearcherVectorDistances mirrors TestHNSWRealSearcherVectorDistances:
// the real DiskANN searcher scores explicit IDs exactly from its stored raw
// vectors, on the RAM and the mmap load path. Before the fix the searcher has
// no VectorDistances at all, so a selective filter on the default backend
// cannot be scored exactly.
func TestDiskANNRealSearcherVectorDistances(t *testing.T) {
	ctx := context.Background()
	vecs := make([][]float32, 30)
	for i := range vecs {
		vecs[i] = []float32{float32(i), 1, float32(i % 5), 2}
	}
	for _, mmap := range []bool{false, true} {
		name := "ram"
		if mmap {
			name = "mmap"
		}
		t.Run(name, func(t *testing.T) {
			cfg := gleann.DefaultConfig()
			cfg.Backend = "diskann"
			cfg.IndexDir = t.TempDir()
			cfg.UseMmap = &mmap
			f, err := gleann.GetBackend("diskann")
			if err != nil {
				t.Fatal(err)
			}
			data, err := f.NewBuilder(cfg).Build(ctx, vecs)
			if err != nil {
				t.Fatal(err)
			}
			s := f.NewSearcher(cfg)
			defer s.Close()
			if mmap {
				path := filepath.Join(cfg.IndexDir, "x.index")
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
				ms, ok := s.(gleann.MmapBackendSearcher)
				if !ok {
					t.Fatal("diskann mmap config returned no MmapBackendSearcher")
				}
				err = ms.LoadFromFile(ctx, path)
			} else {
				err = s.Load(ctx, data, gleann.IndexMeta{Name: "x", Backend: "diskann", Dimensions: 4, NumPassages: 30})
			}
			if err != nil {
				t.Fatal(err)
			}
			vs, ok := s.(gleann.VectorIDScorer)
			if !ok {
				t.Fatalf("diskann searcher (%s) is no VectorIDScorer: filtered search has no exact path", name)
			}
			d, err := vs.VectorDistances(ctx, vecs[2], []int64{2, 25})
			if err != nil || len(d) != 2 {
				t.Fatalf("VectorDistances = %v, %v", d, err)
			}
			// Exact squared L2: vecs[2]={2,1,2,2} to itself is 0; to
			// vecs[25]={25,1,0,2} it is 23^2 + 2^2 = 533. A PQ distance
			// would not hit these values.
			if d[0] != 0 || d[1] != 533 {
				t.Errorf("VectorDistances = %v, want [0 533] (exact raw-vector distances)", d)
			}
			ids, _, err := s.Search(ctx, vecs[2], 3)
			if err != nil || len(ids) == 0 {
				t.Fatalf("Search = %v, %v", ids, err)
			}
			if ids[0] != 2 {
				t.Errorf("nearest to vecs[2] = %d, want 2", ids[0])
			}
		})
	}
}
