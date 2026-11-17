package hnsw

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// refDistance is a float64 reference implementation of the three metrics.
func refDistance(metric string, a, b []float32) float64 {
	var dot, na, nb, l2 float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
		l2 += (x - y) * (x - y)
	}
	switch metric {
	case "cosine":
		return 1 - dot/(math.Sqrt(na)*math.Sqrt(nb))
	case "ip", "inner_product":
		return -dot
	}
	return l2
}

var metrics = []string{"l2", "cosine", "ip", "inner_product"}

func TestDistanceFunctionsMatchReference(t *testing.T) {
	vecs := randVecs(20, 16, 11)
	for _, m := range metrics {
		f := GetDistanceFunc(m)
		f16 := GetDistanceFuncF16(m)
		f16f16 := GetDistanceFuncF16F16(m)
		for i := 0; i+1 < len(vecs); i++ {
			a, b := vecs[i], vecs[i+1]
			if got, want := float64(f(a, b)), refDistance(m, a, b); math.Abs(got-want) > 1e-4 {
				t.Fatalf("%s(%d,%d) = %v, want %v", m, i, i+1, got, want)
			}
			// The float16 variants compute exactly what the float32 function
			// computes on the widened float16 values.
			a16, b16 := Float32SliceToFloat16(a), Float32SliceToFloat16(b)
			bw := Float16SliceToFloat32(b16)
			if got, want := f16(a, b16), f(a, bw); got != want {
				t.Fatalf("%s F16 = %v, want %v", m, got, want)
			}
			if got, want := f16f16(a16, b16), f(Float16SliceToFloat32(a16), bw); got != want {
				t.Fatalf("%s F16F16 = %v, want %v", m, got, want)
			}
		}
	}
}

func TestDistanceProperties(t *testing.T) {
	a := []float32{1, 2, 3}
	if d := CosineDistance(a, a); math.Abs(float64(d)) > 1e-6 {
		t.Fatalf("cosine of a vector with itself = %v", d)
	}
	if d := CosineDistance([]float32{1, 0}, []float32{0, 1}); math.Abs(float64(d)-1) > 1e-6 {
		t.Fatalf("cosine of orthogonal vectors = %v, want 1", d)
	}
	if d := CosineDistance([]float32{1, 0}, []float32{-1, 0}); math.Abs(float64(d)-2) > 1e-6 {
		t.Fatalf("cosine of opposite vectors = %v, want 2", d)
	}
	// A larger dot product is a smaller (better) inner-product distance.
	if InnerProductDistance(a, []float32{2, 4, 6}) >= InnerProductDistance(a, []float32{1, 1, 1}) {
		t.Fatal("inner-product distance does not rank the larger dot product first")
	}

	maxF := float32(math.MaxFloat32)
	zero16 := Float32SliceToFloat16([]float32{0, 0})
	one16 := Float32SliceToFloat16([]float32{1, 0})
	degenerate := map[string]float32{
		"L2 empty":            L2DistanceSquared(nil, a),
		"L2 length mismatch":  L2DistanceSquared(a, a[:2]),
		"cosine empty":        CosineDistance(nil, nil),
		"cosine mismatch":     CosineDistance(a, a[:1]),
		"cosine zero vector":  CosineDistance([]float32{0, 0}, []float32{1, 0}),
		"ip mismatch":         InnerProductDistance(a, a[:2]),
		"L2 F16 mismatch":     L2DistanceSquaredF16(a, one16),
		"cosine F16 mismatch": CosineDistanceF16(a, one16),
		"cosine F16 zero":     CosineDistanceF16([]float32{1, 0}, zero16),
		"ip F16 mismatch":     InnerProductDistanceF16(a, one16),
		"L2 F16F16 mismatch":  L2DistanceSquaredF16F16(one16, one16[:1]),
		"cosine F16F16 empty": CosineDistanceF16F16(nil, one16),
		"cosine F16F16 zero":  CosineDistanceF16F16(zero16, one16),
		"ip F16F16 mismatch":  InnerProductDistanceF16F16(one16, nil),
	}
	for name, d := range degenerate {
		if d != maxF {
			t.Errorf("%s = %v, want MaxFloat32 (never the nearest)", name, d)
		}
	}
	if Float32SliceToFloat16(nil) != nil || Float16SliceToFloat32(nil) != nil {
		t.Fatal("nil slices must convert to nil")
	}
}

// The mmap reader scores a query against a stored embedding straight from
// the file; the result must equal the in-memory distance on the same vector,
// for both storage types and every metric.
func TestMmapDistanceToStoredEmbeddingMatchesInMemory(t *testing.T) {
	vecs := randVecs(16, 8, 21)
	query := randVecs(1, 8, 22)[0]
	for _, dtype := range []string{VectorDTypeF32, VectorDTypeF16} {
		mg, err := OpenMmapGraphFromData(serialize(t, buildGraph(vecs, 100, nil), dtype, 1))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range metrics {
			f := GetDistanceFunc(m)
			for i := range vecs {
				id := int64(100 + i)
				stored := mg.GetStoredEmbedding(id)
				got, ok := mg.DistanceToStoredEmbedding(query, id, DistanceMetric(m))
				if !ok || stored == nil {
					t.Fatalf("%s/%s: embedding %d not found", dtype, m, id)
				}
				if want := f(query, stored); math.Abs(float64(got-want)) > 1e-5 {
					t.Fatalf("%s/%s id %d: mmap distance %v, in-memory %v", dtype, m, id, got, want)
				}
			}
			if _, ok := mg.DistanceToStoredEmbedding(query, 9999, DistanceMetric(m)); ok {
				t.Fatalf("%s/%s: distance to an unknown id", dtype, m)
			}
			if _, ok := mg.DistanceToStoredEmbedding(query[:3], 100, DistanceMetric(m)); ok {
				t.Fatalf("%s/%s: distance with a query of the wrong dimension", dtype, m)
			}
		}
		// A zero query has no cosine direction: never the nearest.
		if d, ok := mg.DistanceToStoredEmbedding(make([]float32, 8), 100, "cosine"); !ok || d != math.MaxFloat32 {
			t.Fatalf("%s: cosine of a zero query = %v, %v", dtype, d, ok)
		}
	}
}

func TestFactoryPicksSearcherByConfig(t *testing.T) {
	f := &Factory{}
	if f.Name() != "hnsw" {
		t.Fatalf("Name = %q", f.Name())
	}
	cfg := DefaultConfig()
	cfg.HNSWConfig.UseMmap = true
	if _, ok := f.NewSearcher(cfg).(*MmapSearcher); !ok {
		t.Fatal("UseMmap did not select the mmap searcher")
	}
	cfg.HNSWConfig.UseMmap = false
	if _, ok := f.NewSearcher(cfg).(*Searcher); !ok {
		t.Fatal("without UseMmap the in-memory searcher must be used")
	}
	if _, ok := f.NewBuilder(cfg).(*Builder); !ok {
		t.Fatal("NewBuilder did not return a Builder")
	}
}

// ExtractVectors returns each stored vector (widened from float16 when the
// index stores halves) and refuses absent or pruned ones: a merge cannot
// rebuild from what is not stored.
func TestSearcherExtractVectors(t *testing.T) {
	ctx := context.Background()
	vecs := randVecs(10, 4, 31)
	s := &Searcher{config: DefaultConfig()}
	if _, err := s.ExtractVectors(ctx, []int64{0}); err == nil {
		t.Fatal("ExtractVectors without an index")
	}
	for _, dtype := range []string{VectorDTypeF32, VectorDTypeF16} {
		if err := s.Load(ctx, serialize(t, buildGraph(vecs, 0, nil), dtype, 1), IndexMeta{}); err != nil {
			t.Fatal(err)
		}
		got, err := s.ExtractVectors(ctx, []int64{3, 0})
		if err != nil {
			t.Fatal(err)
		}
		for i, id := range []int64{3, 0} {
			for j := range got[i] {
				if math.Abs(float64(got[i][j]-vecs[id][j])) > 1e-3 {
					t.Fatalf("%s: vector %d = %v, want %v", dtype, id, got[i], vecs[id])
				}
			}
		}
		// The result is a copy: changing it does not change the index.
		got[0][0] = 42
		again, _ := s.ExtractVectors(ctx, []int64{3})
		if again[0][0] == 42 {
			t.Fatalf("%s: ExtractVectors returned the stored slice", dtype)
		}
		if _, err := s.ExtractVectors(ctx, []int64{99}); err == nil || !strings.Contains(err.Error(), "not in graph") {
			t.Fatalf("%s: absent id: %v", dtype, err)
		}
	}
	if err := s.Load(ctx, serialize(t, buildGraph(vecs, 0, nil), VectorDTypeF32, 0), IndexMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExtractVectors(ctx, []int64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}); err == nil || !strings.Contains(err.Error(), "pruned") {
		t.Fatalf("pruned vectors: %v", err)
	}
}

func TestIndexMetaJSONUsesRFC3339(t *testing.T) {
	at := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	b, err := json.Marshal(IndexMeta{Name: "idx", Dimensions: 4, CreatedAt: at, UpdatedAt: at.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["created_at"] != "2026-11-17T09:30:00Z" || m["updated_at"] != "2026-11-17T10:30:00Z" || m["name"] != "idx" {
		t.Fatalf("IndexMeta JSON = %s", b)
	}
}
