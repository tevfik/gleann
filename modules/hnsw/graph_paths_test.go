package hnsw

import (
	"context"
	"math"
	"testing"
)

func TestDetectMetricIdentifiesDistanceFuncs(t *testing.T) {
	cases := []struct {
		name string
		f    DistanceFunc
		want string
	}{
		{"nil falls back to l2", nil, "l2"},
		{"l2", L2DistanceSquared, "l2"},
		{"cosine", CosineDistance, "cosine"},
		{"inner product", InnerProductDistance, "ip"},
	}
	for _, tc := range cases {
		if got := detectMetric(tc.f); got != tc.want {
			t.Errorf("%s: detectMetric = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Distances between nodes use the float16 kernel whenever either side is
// stored as float16, and agree with the float32 distance up to float16
// rounding.
func TestNodeNodeDistMixedStorage(t *testing.T) {
	g := NewGraph(4, 20, 3)
	a32, b32 := []float32{1, 2, 3}, []float32{3, 2, 1}
	a16, b16 := Float32SliceToFloat16(a32), Float32SliceToFloat16(b32)
	want := L2DistanceSquared(a32, b32) // 8

	cases := map[string][2]*Node{
		"f32 f32": {{Vector: a32}, {Vector: b32}},
		"f16 f16": {{VectorF16: a16}, {VectorF16: b16}},
		"f16 f32": {{VectorF16: a16}, {Vector: b32}},
		"f32 f16": {{Vector: a32}, {VectorF16: b16}},
	}
	for name, n := range cases {
		if got := g.nodeNodeDist(n[0], n[1]); math.Abs(float64(got-want)) > 1e-2 {
			t.Errorf("%s: distance = %v, want %v", name, got, want)
		}
	}
	// vectorNodeDist picks the kernel by the node's storage too.
	if got := g.vectorNodeDist(a32, &Node{VectorF16: b16}); math.Abs(float64(got-want)) > 1e-2 {
		t.Errorf("vectorNodeDist over a float16 node = %v, want %v", got, want)
	}
}

func TestCandidateHeapEmptyAccessors(t *testing.T) {
	h := &candidateHeap{}
	if c := h.Pop(); c != (Candidate{}) {
		t.Fatalf("Pop on an empty heap = %+v, want the zero Candidate", c)
	}
	if c := h.Peek(); c != (Candidate{}) {
		t.Fatalf("Peek on an empty heap = %+v, want the zero Candidate", c)
	}
	if h.Len() != 0 {
		t.Fatalf("Len = %d", h.Len())
	}
}

// With fewer connections than the cap nothing is pruned, and a connection to a
// node that is no longer in the graph is dropped when pruning.
func TestPruneConnections(t *testing.T) {
	g := NewGraph(2, 10, 2)
	for i := int64(0); i < 6; i++ {
		g.Insert(i, []float32{float32(i), 0})
	}
	n := &Node{ID: 100, Vector: []float32{0, 0}, Neighbors: [][]int64{{1, 2}}}
	g.pruneConnections(n, 0, 2)
	if len(n.Neighbors[0]) != 2 {
		t.Fatalf("at the cap nothing may be pruned, got %v", n.Neighbors[0])
	}

	n.Neighbors[0] = []int64{5, 777, 3, 1, 4}
	g.pruneConnections(n, 0, 2)
	if len(n.Neighbors[0]) != 2 || n.Neighbors[0][0] != 1 || n.Neighbors[0][1] != 3 {
		t.Fatalf("kept %v, want the two nearest existing nodes [1 3] and no phantom 777", n.Neighbors[0])
	}
}

// Without the diversity heuristic the closest M are taken as they come; with
// it, candidates that are nearer to an already chosen neighbour than to the
// query are skipped, and the remaining slots are filled from the closest
// leftovers so a node is never starved of connections.
func TestSelectNeighbors(t *testing.T) {
	build := func(heuristic bool) *Graph {
		g := NewGraphWithOptions(2, 10, 2, L2DistanceSquared, heuristic)
		// Two tight clusters: 1, 2, 3 near x=1 and 10 near x=10.
		g.nodes[1] = &Node{ID: 1, Vector: []float32{1, 0}}
		g.nodes[2] = &Node{ID: 2, Vector: []float32{1.1, 0}}
		g.nodes[3] = &Node{ID: 3, Vector: []float32{1.2, 0}}
		g.nodes[10] = &Node{ID: 10, Vector: []float32{10, 0}}
		return g
	}
	cands := []Candidate{{ID: 1, Distance: 1}, {ID: 2, Distance: 1.21}, {ID: 3, Distance: 1.44}, {ID: 10, Distance: 81}}

	plain := build(false).selectNeighbors(cands, 2)
	if len(plain) != 2 || plain[0].ID != 1 || plain[1].ID != 2 {
		t.Fatalf("without the heuristic: %v, want the two closest [1 2]", plain)
	}
	if got := build(false).selectNeighbors(cands[:2], 2); len(got) != 2 {
		t.Fatalf("at most M candidates are all kept, got %v", got)
	}

	div := build(true).selectNeighbors(cands, 2)
	if len(div) != 2 || div[0].ID != 1 {
		t.Fatalf("with the heuristic: %v", div)
	}

	// A candidate that is no longer in the graph is still selectable: the
	// diversity check just has nothing to compare it with.
	g := build(true)
	delete(g.nodes, 2)
	if got := g.selectNeighbors(cands, 3); len(got) != 3 {
		t.Fatalf("with a vanished node: %v", got)
	}
}

// Searching asks for at least topK candidates even with a smaller ef.
func TestSearchRaisesEfToTopK(t *testing.T) {
	g := NewGraph(4, 20, 2)
	for i := int64(0); i < 20; i++ {
		g.Insert(i, []float32{float32(i), float32(i % 3)})
	}
	if got := g.Search([]float32{3, 0}, 8, 1); len(got) != 8 {
		t.Fatalf("Search(topK=8, ef=1) returned %d, want 8", len(got))
	}
}

// Dangling neighbour ids (a node dropped from the graph's table) are skipped
// by every traversal instead of panicking on a nil node.
func TestTraversalSkipsVanishedNodes(t *testing.T) {
	g := NewGraph(3, 20, 2)
	for i := int64(0); i < 25; i++ {
		g.Insert(i, []float32{float32(i), float32(i * 2 % 7)})
	}
	// Drop some level-0 nodes from the table while keeping the edges that
	// point at them.
	dropped := 0
	for i := int64(0); i < 25; i += 2 {
		if n := g.nodes[i]; n.Level == 0 && i != g.GetEntryPoint() {
			delete(g.nodes, i)
			dropped++
		}
	}
	if dropped == 0 {
		t.Fatal("test setup: no level-0 node to drop")
	}
	q := []float32{5, 3}
	if got := g.Search(q, 5, 10); len(got) == 0 {
		t.Fatal("Search found nothing")
	}
	recompute := func(ids []int64) [][]float32 {
		out := make([][]float32, len(ids))
		for i, id := range ids {
			out[i] = []float32{float32(id), float32(id * 2 % 7)}
		}
		return out
	}
	if got := g.SearchWithRecompute(q, 5, 10, recompute); len(got) == 0 {
		t.Fatal("SearchWithRecompute found nothing")
	}
	// Inserting still works around the holes.
	g.Insert(100, []float32{5, 3})
	if _, ok := g.GetNode(100); !ok {
		t.Fatal("insert around vanished nodes failed")
	}
}

// When the recompute callback has no vector for the entry point or for some
// neighbours, those nodes are skipped; with none at all the search is empty.
func TestSearchWithRecomputeMissingVectors(t *testing.T) {
	g := NewGraph(3, 20, 2)
	for i := int64(0); i < 20; i++ {
		g.Insert(i, []float32{float32(i), 0})
		g.RemoveVector(i) // pruned: every vector must be recomputed
	}
	q := []float32{4, 0}

	if got := g.SearchWithRecompute(q, 3, 10, func([]int64) [][]float32 { return nil }); len(got) != 0 {
		t.Fatalf("with no vectors at all: %v, want nothing", got)
	}

	// Odd ids have no vector; even ids do.
	partial := func(ids []int64) [][]float32 {
		if ids[0]%2 == 1 {
			return nil
		}
		return [][]float32{{float32(ids[0]), 0}}
	}
	got := g.SearchWithRecompute(q, 3, 10, partial)
	for _, c := range got {
		if c.ID%2 == 1 {
			t.Fatalf("returned node %d which has no vector", c.ID)
		}
	}
}

// A heap smaller than ef evicts its worst entry once full: asking for ef = 1
// keeps only the nearest recomputed candidate.
func TestSearchWithRecomputeSmallEf(t *testing.T) {
	g := NewGraph(4, 20, 2)
	vecs := map[int64][]float32{}
	for i := int64(0); i < 30; i++ {
		vecs[i] = []float32{float32(i), 0}
		g.Insert(i, vecs[i])
		g.RemoveVector(i)
	}
	got := g.SearchWithRecompute([]float32{7, 0}, 1, 1, func(ids []int64) [][]float32 {
		out := make([][]float32, len(ids))
		for i, id := range ids {
			out[i] = vecs[id]
		}
		return out
	})
	if len(got) != 1 {
		t.Fatalf("got %v, want one candidate", got)
	}
}

// Growing a float16 index with float32 vectors mixes storage in one graph; the
// result must still be searchable and keep every vector.
func TestAddVectorsToFloat16IndexMixesStorage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 4
	cfg.HNSWConfig.EfConstruction = 30
	cfg.HNSWConfig.VectorDType = VectorDTypeF16
	data, emb := buildSmall(t, cfg, 24, 6)
	more := smallVectors(24, 6, 99)

	out, err := (&Builder{config: cfg}).AddVectors(context.Background(), data, more, 24)
	if err != nil {
		t.Fatal(err)
	}
	s := loadSearcher(t, cfg, out)
	for _, probe := range []struct {
		id  int64
		vec []float32
	}{{3, emb[3]}, {30, more[6]}} {
		ids, _, err := s.Search(context.Background(), probe.vec, 3)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, id := range ids {
			found = found || id == probe.id
		}
		if !found {
			t.Fatalf("vector %d is not among its own 3 nearest neighbours %v", probe.id, ids)
		}
	}
}
