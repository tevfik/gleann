package diskann

import "testing"

// TestGreedySearchExpandsClosestCandidate: the query search must expand the
// closest candidate it has not expanded yet. Here the medoid's last neighbour
// E is the only way to the true nearest node T. The search used until v1.20.1
// (the build's walk, buildCandidates) goes through its candidate list in
// insertion order and re-sorts it when it grows past 2L; E then sorts in
// front of the position it has reached and is never expanded, so T is not
// found.
func TestGreedySearchExpandsClosestCandidate(t *testing.T) {
	const (
		m, a, b, c, d, e, target = 0, 1, 2, 3, 4, 5, 6
	)
	g := NewVamanaGraph(8, 2, 1, 1.2, L2DistanceSquared)
	g.nodes = [][]float32{m: {0}, a: {-10}, b: {-11}, c: {-12}, d: {-13}, e: {9}, target: {10}}
	g.neighbors = [][]int64{
		m:      {a, b, c, d, e},
		a:      {m},
		b:      {m},
		c:      {m},
		d:      {m},
		e:      {target},
		target: {e},
	}
	g.medoid = m

	got := g.greedySearch([]float32{10}, 2)
	if len(got) != 2 || got[0].ID != target || got[1].ID != e {
		t.Fatalf("greedySearch = %+v, want the true nearest [%d %d]", got, target, e)
	}
	if got[0].Distance != 0 || got[1].Distance != 1 {
		t.Errorf("distances = %v, %v, want 0, 1 (sorted ascending)", got[0].Distance, got[1].Distance)
	}
}

// TestPruneToRBoundsDegree: reverse edges may take a node up to slackDegree
// during the build; afterwards every node is back to at most R.
func TestPruneToRBoundsDegree(t *testing.T) {
	const n, r = 40, 4
	g := NewVamanaGraph(r, 8, 1, 1.2, L2DistanceSquared)
	g.nodes = make([][]float32, n)
	g.neighbors = make([][]int64, n)
	for i := range g.nodes {
		g.nodes[i] = []float32{float32(i)}
	}
	if g.slackDegree() <= r {
		t.Fatalf("slackDegree() = %d, want more than R = %d", g.slackDegree(), r)
	}
	// Node 0 holds 20 neighbours, node 1 exactly R, the rest none.
	for i := int64(2); i < 22; i++ {
		g.neighbors[0] = append(g.neighbors[0], i)
	}
	g.neighbors[1] = []int64{2, 3, 4, 5}
	g.pruneToR(3)
	if len(g.neighbors[0]) == 0 || len(g.neighbors[0]) > r {
		t.Errorf("node 0 has %d neighbours after pruneToR, want 1..%d", len(g.neighbors[0]), r)
	}
	if g.neighbors[0][0] != 2 {
		t.Errorf("node 0 lost its nearest neighbour: %v", g.neighbors[0])
	}
	if len(g.neighbors[1]) != r {
		t.Errorf("node 1 (already at R) changed: %v", g.neighbors[1])
	}
}
