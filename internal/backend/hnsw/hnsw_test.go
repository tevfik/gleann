package hnsw

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestNewGraph(t *testing.T) {
	g := NewGraph(16, 100, 128)
	if g.m != 16 {
		t.Errorf("expected M=16, got %d", g.m)
	}
	if g.efConstruction != 100 {
		t.Errorf("expected efConstruction=100, got %d", g.efConstruction)
	}
	if g.dimensions != 128 {
		t.Errorf("expected dimensions=128, got %d", g.dimensions)
	}
	if g.Size() != 0 {
		t.Errorf("expected initial size 0, got %d", g.Size())
	}
	if g.GetEntryPoint() != -1 {
		t.Errorf("expected initial entry point -1, got %d", g.GetEntryPoint())
	}
}

func TestNewGraphDefaults(t *testing.T) {
	g := NewGraph(0, 0, 64)
	if g.m != 32 {
		t.Errorf("expected default M=32, got %d", g.m)
	}
	if g.efConstruction != 200 {
		t.Errorf("expected default efConstruction=200, got %d", g.efConstruction)
	}
}

func TestInsertSingle(t *testing.T) {
	g := NewGraph(4, 50, 3)
	vec := []float32{1.0, 2.0, 3.0}
	g.Insert(0, vec)

	if g.Size() != 1 {
		t.Errorf("expected size 1, got %d", g.Size())
	}
	if g.GetEntryPoint() != 0 {
		t.Errorf("expected entry point 0, got %d", g.GetEntryPoint())
	}

	node, ok := g.GetNode(0)
	if !ok {
		t.Fatal("node 0 not found")
	}
	if len(node.Vector) != 3 {
		t.Errorf("expected vector len 3, got %d", len(node.Vector))
	}
}

func TestInsertMultiple(t *testing.T) {
	g := NewGraph(4, 50, 3)
	vectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
		{1.0, 1.0, 0.0},
		{0.0, 1.0, 1.0},
	}

	for i, v := range vectors {
		g.Insert(int64(i), v)
	}

	if g.Size() != 5 {
		t.Errorf("expected size 5, got %d", g.Size())
	}
}

func TestSearchExact(t *testing.T) {
	g := NewGraph(4, 50, 3)
	vectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
		{0.5, 0.5, 0.0},
		{0.0, 0.5, 0.5},
	}

	for i, v := range vectors {
		g.Insert(int64(i), v)
	}

	// Search for nearest to {1, 0, 0} — should return ID 0.
	results := g.Search([]float32{1.0, 0.0, 0.0}, 1, 50)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].ID != 0 {
		t.Errorf("expected ID 0, got %d", results[0].ID)
	}
	if results[0].Distance > 1e-6 {
		t.Errorf("expected distance ~0, got %f", results[0].Distance)
	}
}

func TestSearchTopK(t *testing.T) {
	g := NewGraph(8, 100, 3)
	rng := rand.New(rand.NewSource(42))

	n := 100
	for i := 0; i < n; i++ {
		v := []float32{rng.Float32(), rng.Float32(), rng.Float32()}
		g.Insert(int64(i), v)
	}

	query := []float32{0.5, 0.5, 0.5}
	results := g.Search(query, 5, 50)

	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}

	// Verify sorted by distance.
	for i := 1; i < len(results); i++ {
		if results[i].Distance < results[i-1].Distance {
			t.Errorf("results not sorted: %f < %f at position %d", results[i].Distance, results[i-1].Distance, i)
		}
	}
}

func TestSearchRecall(t *testing.T) {
	dims := 32
	n := 500
	g := NewGraph(16, 200, dims)
	rng := rand.New(rand.NewSource(42))

	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		vectors[i] = v
		g.Insert(int64(i), v)
	}

	// Brute-force ground truth.
	query := make([]float32, dims)
	for j := range query {
		query[j] = rng.Float32()
	}

	type candidate struct {
		id   int64
		dist float32
	}
	brute := make([]candidate, n)
	for i := 0; i < n; i++ {
		brute[i] = candidate{id: int64(i), dist: L2DistanceSquared(query, vectors[i])}
	}
	sort.Slice(brute, func(i, j int) bool {
		return brute[i].dist < brute[j].dist
	})

	topK := 10
	results := g.Search(query, topK, 64)

	// Check recall@10.
	groundTruth := make(map[int64]bool)
	for i := 0; i < topK; i++ {
		groundTruth[brute[i].id] = true
	}

	hits := 0
	for _, r := range results {
		if groundTruth[r.ID] {
			hits++
		}
	}

	recall := float64(hits) / float64(topK)
	if recall < 0.5 {
		t.Errorf("recall@%d = %.2f (expected >= 0.5)", topK, recall)
	}
	t.Logf("recall@%d = %.2f (%d/%d hits)", topK, recall, hits, topK)
}

func TestSearchWithRecompute(t *testing.T) {
	g := NewGraph(4, 50, 3)
	originalVectors := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
		{0.5, 0.5, 0.0},
		{0.0, 0.5, 0.5},
	}

	for i, v := range originalVectors {
		g.Insert(int64(i), v)
	}

	// Remove stored vectors to simulate pruned HNSW.
	savedVectors := make(map[int64][]float32)
	for i, v := range originalVectors {
		savedVectors[int64(i)] = v
		g.RemoveVector(int64(i))
	}
	// Keep entry point vector.
	ep := g.GetEntryPoint()
	g.nodes[ep].Vector = savedVectors[ep]

	recompute := func(ids []int64) [][]float32 {
		vecs := make([][]float32, len(ids))
		for i, id := range ids {
			vecs[i] = savedVectors[id]
		}
		return vecs
	}

	results := g.SearchWithRecompute([]float32{1.0, 0.0, 0.0}, 1, 50, recompute)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].ID != 0 {
		t.Errorf("expected ID 0, got %d", results[0].ID)
	}
}

func TestRemoveVector(t *testing.T) {
	g := NewGraph(4, 50, 3)
	g.Insert(0, []float32{1.0, 0.0, 0.0})
	g.Insert(1, []float32{0.0, 1.0, 0.0})

	g.RemoveVector(1)

	node, ok := g.GetNode(1)
	if !ok {
		t.Fatal("node 1 should still exist")
	}
	if node.Vector != nil {
		t.Error("expected nil vector after removal")
	}
}

func TestL2DistanceSquared(t *testing.T) {
	tests := []struct {
		a, b     []float32
		expected float32
	}{
		{[]float32{0, 0}, []float32{0, 0}, 0},
		{[]float32{1, 0}, []float32{0, 0}, 1},
		{[]float32{3, 4}, []float32{0, 0}, 25},
		{[]float32{1, 2, 3}, []float32{4, 5, 6}, 27},
	}

	for _, tt := range tests {
		got := L2DistanceSquared(tt.a, tt.b)
		if math.Abs(float64(got-tt.expected)) > 1e-6 {
			t.Errorf("l2(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.expected)
		}
	}
}

func TestCandidateHeap(t *testing.T) {
	// Min heap
	h := &candidateHeap{isMin: true}
	h.Push(Candidate{ID: 1, Distance: 5})
	h.Push(Candidate{ID: 2, Distance: 3})
	h.Push(Candidate{ID: 3, Distance: 7})
	h.Push(Candidate{ID: 4, Distance: 1})

	if h.Peek().Distance != 1 {
		t.Errorf("min heap peek: expected 1, got %f", h.Peek().Distance)
	}

	got := h.Pop()
	if got.Distance != 1 {
		t.Errorf("min heap pop: expected 1, got %f", got.Distance)
	}
	got = h.Pop()
	if got.Distance != 3 {
		t.Errorf("min heap pop: expected 3, got %f", got.Distance)
	}

	// Max heap.
	h2 := &candidateHeap{isMin: false}
	h2.Push(Candidate{ID: 1, Distance: 5})
	h2.Push(Candidate{ID: 2, Distance: 3})
	h2.Push(Candidate{ID: 3, Distance: 7})

	got = h2.Pop()
	if got.Distance != 7 {
		t.Errorf("max heap pop: expected 7, got %f", got.Distance)
	}
}

func TestAllNodeIDs(t *testing.T) {
	g := NewGraph(4, 50, 3)
	g.Insert(5, []float32{1, 0, 0})
	g.Insert(10, []float32{0, 1, 0})
	g.Insert(15, []float32{0, 0, 1})

	ids := g.AllNodeIDs()
	if len(ids) != 3 {
		t.Errorf("expected 3 IDs, got %d", len(ids))
	}
}

func TestSearchEmptyGraph(t *testing.T) {
	g := NewGraph(4, 50, 3)
	results := g.Search([]float32{1, 0, 0}, 5, 50)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func BenchmarkInsert(b *testing.B) {
	dims := 128
	g := NewGraph(32, 200, dims)
	rng := rand.New(rand.NewSource(42))

	vectors := make([][]float32, b.N)
	for i := range vectors {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		vectors[i] = v
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Insert(int64(i), vectors[i])
	}
}

func BenchmarkSearch(b *testing.B) {
	dims := 128
	n := 10000
	g := NewGraph(32, 200, dims)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < n; i++ {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		g.Insert(int64(i), v)
	}

	query := make([]float32, dims)
	for j := range query {
		query[j] = rng.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Search(query, 10, 128)
	}
}
