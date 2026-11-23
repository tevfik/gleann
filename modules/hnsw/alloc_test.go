package hnsw

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// allocGraph builds a graph of n random vectors for the allocation tests.
func allocGraph(n, dims int) (*Graph, *rand.Rand) {
	r := rand.New(rand.NewSource(21))
	g := NewGraph(16, 100, dims)
	for i := 0; i < n; i++ {
		v := make([]float32, dims)
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		g.Insert(int64(i), v)
	}
	return g, r
}

// An insert allocates the node and its neighbour lists and nothing per step
// of the search: the visited set, the heaps and the buffers of the neighbour
// selection are the graph's own. It used to make a visited map, two heaps and
// a sort closure per level, and new lists for every pruned neighbour: 130
// allocations per insert on this graph.
func TestInsertAllocatesOnlyTheNode(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	const dims = 32
	g, r := allocGraph(3000, dims)
	vecs := make([][]float32, 400)
	for i := range vecs {
		vecs[i] = make([]float32, dims)
		for j := range vecs[i] {
			vecs[i][j] = float32(r.NormFloat64())
		}
	}
	next := 0
	allocs := testing.AllocsPerRun(len(vecs)-1, func() {
		g.Insert(int64(3000+next), vecs[next])
		next++
	})
	// The node, its list of lists and the list of kept distances, then a
	// neighbour list and its distances per level (most nodes have one level).
	t.Logf("%.0f allocations per insert", allocs)
	if allocs > 10 {
		t.Fatalf("%.0f allocations per insert, want at most 10", allocs)
	}
}

// A search allocates its result and reuses everything else. It used to make a
// visited map, two heaps and the neighbour buffers: 48 allocations per search
// on this graph.
func TestSearchAllocatesOnlyTheResult(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	const dims = 32
	g, r := allocGraph(3000, dims)
	q := make([]float32, dims)
	for j := range q {
		q[j] = float32(r.NormFloat64())
	}
	g.Search(q, 10, 64) // warm the scratch
	allocs := testing.AllocsPerRun(200, func() {
		if got := g.Search(q, 10, 64); len(got) != 10 {
			t.Fatalf("%d results", len(got))
		}
	})
	t.Logf("%.0f allocations per search", allocs)
	if allocs > 2 {
		t.Fatalf("%.0f allocations per search, want at most 2", allocs)
	}
}

// Searches that run at the same time each use their own scratch: every one
// returns what a search on its own returns.
func TestConcurrentSearchesDoNotShareScratch(t *testing.T) {
	const dims = 16
	g, r := allocGraph(1500, dims)
	queries := make([][]float32, 64)
	want := make([][]Candidate, len(queries))
	for i := range queries {
		queries[i] = make([]float32, dims)
		for j := range queries[i] {
			queries[i][j] = float32(r.NormFloat64())
		}
		want[i] = g.Search(queries[i], 10, 50)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for round := 0; round < 20; round++ {
				for i := (w * 5) % len(queries); i < len(queries); i++ {
					got := g.Search(queries[i], 10, 50)
					if len(got) != len(want[i]) {
						errs <- "a concurrent search returned another number of results"
						return
					}
					for k := range got {
						if got[k] != want[i][k] {
							errs <- "a concurrent search returned other results"
							return
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// TestConcurrentSearchesWithWriter verifies that pooled search scratches
// resize safely and remain race-free when concurrent searches run while
// a writer inserts new vectors into the graph.
func TestConcurrentSearchesWithWriter(t *testing.T) {
	const dims = 16
	g, r := allocGraph(200, dims)
	queries := make([][]float32, 32)
	for i := range queries {
		queries[i] = make([]float32, dims)
		for j := range queries[i] {
			queries[i][j] = float32(r.NormFloat64())
		}
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, 8)

	// Start 4 concurrent readers
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			idx := worker
			for {
				select {
				case <-done:
					return
				default:
					q := queries[idx%len(queries)]
					idx++
					res := g.Search(q, 10, 50)
					if len(res) == 0 {
						select {
						case errCh <- fmt.Errorf("search returned 0 results"):
						default:
						}
						return
					}
				}
			}
		}(w)
	}

	// Writer inserts 300 additional nodes
	for i := 200; i < 500; i++ {
		v := make([]float32, dims)
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		g.Insert(int64(i), v)
	}
	close(done)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	if g.Size() != 500 {
		t.Fatalf("expected 500 nodes, got %d", g.Size())
	}
}

// BenchmarkGraphSearch measures the in-memory search (Graph.Search) on 5,000
// vectors of 128 dimensions at ef 128.
func BenchmarkGraphSearch(b *testing.B) {
	g, r := allocGraph(5000, 128)
	queries := make([][]float32, 256)
	for i := range queries {
		queries[i] = make([]float32, 128)
		for j := range queries[i] {
			queries[i][j] = float32(r.NormFloat64())
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Search(queries[i%len(queries)], 10, 128)
	}
}
