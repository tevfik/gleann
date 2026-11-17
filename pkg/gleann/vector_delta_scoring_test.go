package gleann

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestVectorDeltaDistancePerMetric(t *testing.T) {
	q := []float32{1, 0}
	v := []float32{0.6, 0.8}
	for _, tc := range []struct {
		metric string
		want   float32
	}{
		{"l2", (1-0.6)*(1-0.6) + 0.8*0.8},
		{"cosine", 1 - 0.6},
		{"ip", -0.6},
	} {
		d := newVectorDelta(2, deltaDTypeF32, tc.metric)
		if err := d.Append(7, v); err != nil {
			t.Fatal(err)
		}
		got, ok := d.Distance(q, 7)
		if !ok || math.Abs(float64(got-tc.want)) > 1e-5 {
			t.Errorf("%s distance = %v, %v; want %v", tc.metric, got, ok, tc.want)
		}
		// The metric survives a round trip through the file encoding.
		back, err := parseVectorDelta(d.Marshal())
		if err != nil {
			t.Fatal(err)
		}
		if again, _ := back.Distance(q, 7); math.Abs(float64(again-got)) > 1e-6 {
			t.Errorf("%s distance after round trip = %v, want %v", tc.metric, again, got)
		}
		if _, ok := d.Distance(q, 8); ok {
			t.Errorf("%s: an absent slot was scored", tc.metric)
		}
		if _, ok := d.Distance([]float32{1}, 7); ok {
			t.Errorf("%s: a query of other dims was scored", tc.metric)
		}
	}
	// A zero vector has no direction: cosine distance 1, not NaN.
	d := newVectorDelta(2, deltaDTypeF32, "cosine")
	_ = d.Append(1, []float32{0, 0})
	if got, _ := d.Distance(q, 1); got != 1 {
		t.Fatalf("cosine distance to a zero vector = %v, want 1", got)
	}

	var nilDelta *vectorDelta
	if _, ok := nilDelta.Distance(q, 1); ok || nilDelta.Has(1) {
		t.Fatal("a nil delta scored a slot")
	}
	if ids, _ := nilDelta.Search(q, 3); ids != nil {
		t.Fatal("a nil delta returned hits")
	}
	if err := d.Append(2, []float32{1, 2, 3}); err == nil {
		t.Fatal("an entry of other dims was appended")
	}
}

// Ties on distance are broken by slot, both when the heap evicts and in the
// final order, so results are deterministic.
func TestVectorDeltaSearchTieBreak(t *testing.T) {
	d := newVectorDelta(1, deltaDTypeF32, "l2")
	for _, s := range []int64{9, 3, 7, 1, 5} {
		_ = d.Append(s, []float32{2})
	}
	ids, dists := d.Search([]float32{0}, 3)
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 3 || ids[2] != 5 || dists[0] != 4 {
		t.Fatalf("Search = %v %v, want slots 1 3 5", ids, dists)
	}
	if ids, _ := d.Search([]float32{0}, 0); ids != nil {
		t.Fatal("k = 0 returned hits")
	}
}

func TestParseVectorDeltaRejectsDamage(t *testing.T) {
	d := newVectorDelta(2, deltaDTypeF16, "l2")
	_ = d.Append(1, []float32{1, 2})
	good := d.Marshal()
	for _, tc := range []struct {
		name    string
		corrupt func([]byte) []byte
		want    string
	}{
		{"version", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[4:], 9); return b }, "unsupported version 9"},
		{"zero dims with entries", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[8:], 0); return b }, "zero dimensions"},
		{"huge count", func(b []byte) []byte { binary.LittleEndian.PutUint64(b[16:], 1<<62); return b }, "does not match"},
		{"truncated", func(b []byte) []byte { return b[:len(b)-1] }, "does not match"},
		{"magic", func(b []byte) []byte { b[0] = 'X'; return b }, "bad header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseVectorDelta(tc.corrupt(append([]byte(nil), good...)))
			if !errors.Is(err, errDeltaFormat) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// fakeMainScorer scores slot s as float32(s) and records what it was asked.
type fakeMainScorer struct {
	asked []int64
	short bool
	err   error
}

func (f *fakeMainScorer) VectorDistances(_ context.Context, _ []float32, ids []int64) ([]float32, error) {
	f.asked = append(f.asked, ids...)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]float32, len(ids))
	for i, id := range ids {
		out[i] = float32(id)
	}
	if f.short {
		out = out[:len(out)-1]
	}
	return out, nil
}

func TestDeltaScorerSplitsSlotsBetweenLegs(t *testing.T) {
	delta := newVectorDelta(1, deltaDTypeF32, "l2")
	_ = delta.Append(100, []float32{3})
	main := &fakeMainScorer{}
	got, err := deltaScorer{main: main, delta: delta}.VectorDistances(context.Background(), []float32{1}, []int64{5, 100, 7})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 5 || got[1] != 4 || got[2] != 7 {
		t.Fatalf("distances = %v, want [5 4 7] (delta slot 100 scored by the delta)", got)
	}
	if len(main.asked) != 2 || main.asked[0] != 5 || main.asked[1] != 7 {
		t.Fatalf("main leg asked for %v, want [5 7]", main.asked)
	}

	// Only delta slots: the main leg is not called.
	main.asked = nil
	if _, err := (deltaScorer{main: main, delta: delta}).VectorDistances(context.Background(), []float32{1}, []int64{100}); err != nil || main.asked != nil {
		t.Fatalf("delta-only scoring called the main leg: %v %v", main.asked, err)
	}

	boom := errors.New("backend down")
	if _, err := (deltaScorer{main: &fakeMainScorer{err: boom}, delta: delta}).VectorDistances(context.Background(), []float32{1}, []int64{5}); !errors.Is(err, boom) {
		t.Fatalf("main error = %v", err)
	}
	if _, err := (deltaScorer{main: &fakeMainScorer{short: true}, delta: delta}).VectorDistances(context.Background(), []float32{1}, []int64{5, 6}); err == nil || !strings.Contains(err.Error(), "scored 1 of 2") {
		t.Fatalf("short main answer = %v", err)
	}
}
