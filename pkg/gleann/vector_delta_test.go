package gleann

import (
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func randDelta(t *testing.T, n, dims int, dtype, metric string, seed int64) *vectorDelta {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	d := newVectorDelta(dims, dtype, metric)
	for i := 0; i < n; i++ {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		// Non-contiguous slots: a delta holds whatever slots were allocated.
		if err := d.Append(int64(100+i*3), v); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestVectorDeltaRoundTrip(t *testing.T) {
	for _, dtype := range []string{deltaDTypeF32, deltaDTypeF16} {
		for _, metric := range []string{"l2", "cosine", "ip"} {
			d := randDelta(t, 50, 16, dtype, metric, 1)
			got, err := parseVectorDelta(d.Marshal())
			if err != nil {
				t.Fatalf("%s/%s: %v", dtype, metric, err)
			}
			if got.dtype != dtype || got.metric != metric || got.dims != 16 || got.Len() != 50 {
				t.Fatalf("%s/%s: header = %+v", dtype, metric, got)
			}
			for i := range d.slots {
				if got.slots[i] != d.slots[i] {
					t.Fatalf("slot %d: %d != %d", i, got.slots[i], d.slots[i])
				}
			}
			for i := range d.vecs {
				if got.vecs[i] != d.vecs[i] {
					t.Fatalf("%s: vector element %d: %v != %v (the in-memory value must equal the stored one)", dtype, i, got.vecs[i], d.vecs[i])
				}
			}
		}
	}
}

func TestVectorDeltaFileAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.delta")
	d, err := readVectorDelta(path)
	if d != nil || err != nil {
		t.Fatalf("a missing file is an empty delta, got %v, %v", d, err)
	}
	src := randDelta(t, 3, 4, deltaDTypeF32, "l2", 2)
	if err := os.WriteFile(path, src.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, err = readVectorDelta(path); err != nil || d.Len() != 3 {
		t.Fatalf("readVectorDelta = %v, %v", d, err)
	}
	b := src.Marshal()
	for _, bad := range [][]byte{nil, b[:10], b[:len(b)-1], append([]byte("XXXX"), b[4:]...)} {
		if _, err := parseVectorDelta(bad); err == nil {
			t.Errorf("a corrupt delta (%d bytes) must be refused", len(bad))
		}
	}
	// A hostile count must not overflow the size check.
	huge := append([]byte(nil), b...)
	for i := 16; i < 24; i++ {
		huge[i] = 0xFF
	}
	if _, err := parseVectorDelta(huge); err == nil {
		t.Error("an absurd count must be refused")
	}
}

// bruteTopK is the reference: every entry's distance, sorted.
func bruteTopK(d *vectorDelta, q []float32, k int) []int64 {
	dist := deltaDistance(d.metric)
	type hit struct {
		slot int64
		d    float32
	}
	hits := make([]hit, d.Len())
	for i := range hits {
		hits[i] = hit{d.slots[i], dist(q, d.Vector(i))}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].d != hits[j].d {
			return hits[i].d < hits[j].d
		}
		return hits[i].slot < hits[j].slot
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.slot
	}
	return out
}

func TestVectorDeltaSearchMatchesBruteForce(t *testing.T) {
	for _, dtype := range []string{deltaDTypeF32, deltaDTypeF16} {
		for _, metric := range []string{"l2", "cosine", "ip"} {
			d := randDelta(t, 300, 24, dtype, metric, 3)
			rng := rand.New(rand.NewSource(4))
			for trial := 0; trial < 20; trial++ {
				q := make([]float32, 24)
				for j := range q {
					q[j] = rng.Float32()*2 - 1
				}
				for _, k := range []int{1, 10, 300, 400} {
					ids, ds := d.Search(q, k)
					want := bruteTopK(d, q, k)
					if len(ids) != len(want) {
						t.Fatalf("%s/%s k=%d: %d hits, want %d", dtype, metric, k, len(ids), len(want))
					}
					for i := range ids {
						if ids[i] != want[i] {
							t.Fatalf("%s/%s k=%d: hit %d is slot %d, want %d", dtype, metric, k, i, ids[i], want[i])
						}
						if i > 0 && ds[i] < ds[i-1] {
							t.Fatalf("%s/%s: distances not ascending", dtype, metric)
						}
					}
				}
			}
		}
	}
}

func TestVectorDeltaWithout(t *testing.T) {
	d := randDelta(t, 10, 4, deltaDTypeF32, "l2", 5)
	dead := map[int64]bool{103: true, 112: true}
	w := d.Without(func(s int64) bool { return dead[s] })
	if w.Len() != 8 || w.Has(103) || w.Has(112) || !w.Has(100) {
		t.Fatalf("Without kept %v", w.slots)
	}
	if d.Len() != 10 {
		t.Fatal("Without must not modify the receiver")
	}
	// The vectors follow their slots.
	for i, s := range w.slots {
		j := 0
		for ; d.slots[j] != s; j++ {
		}
		for k := 0; k < 4; k++ {
			if w.Vector(i)[k] != d.Vector(j)[k] {
				t.Fatalf("vector of slot %d moved", s)
			}
		}
	}
}

// A slot present in both legs (a mixed main/delta pair) appears once, with
// the smaller distance.
func TestMergeVectorHitsDedup(t *testing.T) {
	ids, ds := mergeVectorHits(
		[]int64{1, 2, 3}, []float32{0.1, 0.5, 0.9},
		[]int64{2, 4}, []float32{0.3, 0.6}, 10)
	wantIDs := []int64{1, 2, 4, 3}
	wantDs := []float32{0.1, 0.3, 0.6, 0.9}
	if len(ids) != 4 {
		t.Fatalf("ids = %v", ids)
	}
	for i := range ids {
		if ids[i] != wantIDs[i] || ds[i] != wantDs[i] {
			t.Fatalf("merged = %v %v, want %v %v", ids, ds, wantIDs, wantDs)
		}
	}
	ids, _ = mergeVectorHits([]int64{1, 2, 3}, []float32{0.1, 0.5, 0.9}, nil, nil, 2)
	if len(ids) != 2 {
		t.Fatalf("k cut: %v", ids)
	}
}
