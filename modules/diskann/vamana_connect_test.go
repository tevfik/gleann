package diskann

import (
	"math/rand"
	"runtime"
	"testing"
)

// reachable counts the nodes a search from the medoid can ever visit.
func reachable(g *VamanaGraph) int {
	seen := map[int64]bool{g.medoid: true}
	queue := []int64{g.medoid}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, n := range g.neighbors[id] {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return len(seen)
}

// With several build workers the insertion order is racy; on a small index a
// node could end up with no path from the medoid, so a search could never
// return it however large topK was.
func TestBuildLeavesNoNodeUnreachable(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(2))
	rng := rand.New(rand.NewSource(7))
	for run := 0; run < 400; run++ {
		n := 4 + rng.Intn(8)
		emb := make([][]float32, n)
		for i := range emb {
			emb[i] = make([]float32, 16)
			for j := range emb[i] {
				emb[i][j] = rng.Float32()
			}
		}
		g := NewVamanaGraph(4, 8, 16, 1.2, L2DistanceSquared)
		g.Build(emb)
		if got := reachable(g); got != n {
			t.Fatalf("run %d: %d of %d nodes reachable from the medoid", run, got, n)
		}
	}
}
