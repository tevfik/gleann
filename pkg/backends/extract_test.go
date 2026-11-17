package backends

import (
	"context"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// A merge rebuilds the main vector file from the vectors a loaded searcher
// still holds: they must come back exactly.
func TestExtractVectorsRoundTrip(t *testing.T) {
	for _, backend := range []string{"hnsw", "diskann"} {
		t.Run(backend, func(t *testing.T) {
			cfg := gleann.DefaultConfig()
			cfg.Backend = backend
			cfg.IndexDir = t.TempDir()
			noMmap := false
			cfg.UseMmap = &noMmap
			f, err := gleann.GetBackend(backend)
			if err != nil {
				t.Fatal(err)
			}
			vecs := make([][]float32, 40)
			for i := range vecs {
				vecs[i] = []float32{float32(i), float32(i % 7), 1, float32(i * i % 11)}
			}
			data, err := f.NewBuilder(cfg).Build(context.Background(), vecs)
			if err != nil {
				t.Fatal(err)
			}
			s := f.NewSearcher(cfg)
			defer s.Close()
			if err := s.Load(context.Background(), data, gleann.IndexMeta{Name: "x", Backend: backend, Dimensions: 4, NumPassages: 40}); err != nil {
				t.Fatal(err)
			}
			ve, ok := s.(gleann.VectorExtractor)
			if !ok {
				t.Fatal("searcher is no VectorExtractor")
			}
			ids := []int64{3, 17, 39}
			got, err := ve.ExtractVectors(context.Background(), ids)
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range ids {
				for d := range got[i] {
					if got[i][d] != vecs[id][d] {
						t.Fatalf("vector %d differs: %v vs %v", id, got[i], vecs[id])
					}
				}
			}
		})
	}
}
