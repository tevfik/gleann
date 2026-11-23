package hnsw

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func randVec(r *rand.Rand, d int) []float32 {
	v := make([]float32, d)
	for i := range v {
		v[i] = float32(r.NormFloat64())
	}
	return v
}

// l2Dist4 must return exactly what four L2DistanceSquared calls return.
func TestL2Dist4BitIdentical(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, d := range []int{1, 3, 8, 384, 1024} {
		q := randVec(r, d)
		a, b, c, e := randVec(r, d), randVec(r, d), randVec(r, d), randVec(r, d)
		s0, s1, s2, s3 := l2Dist4(q, a, b, c, e)
		for i, p := range []struct{ got, want float32 }{
			{s0, L2DistanceSquared(q, a)}, {s1, L2DistanceSquared(q, b)},
			{s2, L2DistanceSquared(q, c)}, {s3, L2DistanceSquared(q, e)},
		} {
			if math.Float32bits(p.got) != math.Float32bits(p.want) {
				t.Fatalf("dims %d lane %d: %v != %v", d, i, p.got, p.want)
			}
		}
	}
}

func TestIsL2Func(t *testing.T) {
	if !isL2Func(L2DistanceSquared) || isL2Func(CosineDistance) || isL2Func(nil) {
		t.Fatal("isL2Func misclassifies the distance functions")
	}
	wrapped := DistanceFunc(func(a, b []float32) float32 { return L2DistanceSquared(a, b) })
	if isL2Func(wrapped) {
		t.Fatal("a wrapper must not enable batching")
	}
}

// l2Dist2 and l2Dist3 must return exactly what L2DistanceSquared returns.
func TestL2Dist2And3BitIdentical(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for _, d := range []int{1, 2, 3, 7, 100, 384, 768} {
		q, a, b, c := randVec(r, d), randVec(r, d), randVec(r, d), randVec(r, d)
		s0, s1 := l2Dist2(q, a, b)
		t0, t1, t2 := l2Dist3(q, a, b, c)
		got := []float32{s0, s1, t0, t1, t2}
		for i, v := range [][]float32{a, b, a, b, c} {
			if want := L2DistanceSquared(q, v); math.Float32bits(got[i]) != math.Float32bits(want) {
				t.Fatalf("dims %d result %d: %v != %v", d, i, got[i], want)
			}
		}
	}
}

// The built-in metrics give f(a, b) and f(b, a) the same bits, which is what
// lets a link's distance be kept for the node at its other end (nbrDistOK).
func TestBuiltinDistancesAreSymmetricToTheBit(t *testing.T) {
	metrics := map[string]DistanceFunc{"l2": L2DistanceSquared, "cosine": CosineDistance, "ip": InnerProductDistance}
	r := rand.New(rand.NewSource(12))
	for name, f := range metrics {
		if !isBuiltinDist(f) {
			t.Fatalf("%s is not recognised as a built-in metric", name)
		}
		for round := 0; round < 2000; round++ {
			d := 1 + r.Intn(70)
			a, b := randVec(r, d), randVec(r, d)
			switch round % 5 {
			case 1: // far apart in magnitude: the sums round at every step
				for i := range a {
					a[i] *= float32(math.Pow(10, float64(r.Intn(12)-6)))
				}
			case 2: // tiny values: denormal squares
				for i := range a {
					a[i] *= 1e-22
					b[i] *= 1e-23
				}
			case 3: // a zero vector: cosine has no angle
				clear(b)
			case 4: // different sizes: no distance at all
				b = b[:d-1]
			}
			if x, y := f(a, b), f(b, a); math.Float32bits(x) != math.Float32bits(y) {
				t.Fatalf("%s round %d: f(a, b) = %v, f(b, a) = %v", name, round, x, y)
			}
		}
	}
	if isBuiltinDist(nil) || isBuiltinDist(func(a, b []float32) float32 { return L2DistanceSquared(a, b) }) {
		t.Fatal("a nil or wrapped distance function counts as built-in")
	}
}

// distBatch handles nodes that are removed (nil vector), of another size or
// float16, and group sizes that are not a multiple of four.
func TestDistBatchMatchesScalar(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	g := NewGraphWithOptions(8, 16, 16, nil, true)
	if !g.batchL2 {
		t.Fatal("default L2 graph should batch")
	}
	q := randVec(r, 16)
	var nodes []*Node
	for i := 0; i < 11; i++ {
		nodes = append(nodes, &Node{ID: int64(i), Vector: randVec(r, 16)})
	}
	nodes[5].Vector = nil
	nodes[8].Vector = randVec(r, 7)
	nodes[9].VectorF16, nodes[9].Vector = Float32SliceToFloat16(randVec(r, 16)), nil
	for n := 0; n <= len(nodes); n++ {
		out := make([]float32, n)
		g.distBatch(q, nodes[:n], out)
		for i := range out {
			want := g.vectorNodeDist(q, nodes[i])
			if math.Float32bits(out[i]) != math.Float32bits(want) {
				t.Fatalf("n=%d node %d: %v != %v", n, i, out[i], want)
			}
		}
	}
}

// A graph built with batching is the same graph as one built without it.
func TestBuildWithBatchingIsIdentical(t *testing.T) {
	build := func(batch bool) *Graph {
		g := NewGraphWithOptions(8, 40, 24, nil, true)
		g.batchL2 = batch
		rr := rand.New(rand.NewSource(6))
		for i := 0; i < 600; i++ {
			g.Insert(int64(i), randVec(rr, 24))
		}
		return g
	}
	a, b := build(true), build(false)
	if a.entryPoint != b.entryPoint || a.maxLevel != b.maxLevel || a.nodes.len() != b.nodes.len() {
		t.Fatal("graph shape differs")
	}
	for id, n := range a.nodes.all() {
		if !reflect.DeepEqual(n.Neighbors, b.nodes.get(id).Neighbors) {
			t.Fatalf("neighbours of %d differ", id)
		}
	}
}
