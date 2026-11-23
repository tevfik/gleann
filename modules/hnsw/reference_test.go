package hnsw

import (
	"bytes"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// refGraph is the insert algorithm as it was before the build was optimised:
// nodes in a map, a fresh visited map and fresh heaps per level search, one
// distance at a time, every prune measuring its whole list, sort.Slice. It is
// kept here as the oracle: whatever Graph.Insert does to go faster, it has to
// build exactly the graph refGraph builds.
type refGraph struct {
	nodes          map[int64]*Node
	entryPoint     int64
	maxLevel       int
	m, mMax, mMax0 int
	efConstruction int
	ml             float64
	distFunc       DistanceFunc
	metric         string
	useHeuristic   bool
	rng            *rand.Rand
}

func newRefGraph(m, efConstruction int, distFunc DistanceFunc, useHeuristic bool) *refGraph {
	if distFunc == nil {
		distFunc = L2DistanceSquared
	}
	return &refGraph{
		nodes:          make(map[int64]*Node),
		entryPoint:     -1,
		maxLevel:       -1,
		m:              m,
		mMax:           m,
		mMax0:          m * 2,
		efConstruction: efConstruction,
		ml:             1.0 / math.Log(float64(m)),
		distFunc:       distFunc,
		metric:         detectMetric(distFunc),
		useHeuristic:   useHeuristic,
		rng:            rand.New(rand.NewSource(42)),
	}
}

// refFromGraph copies the nodes of g (a graph loaded from an index) into a
// reference graph that continues from the same state.
func refFromGraph(g *Graph, efConstruction int, distFunc DistanceFunc) *refGraph {
	r := newRefGraph(g.m, efConstruction, distFunc, true)
	r.entryPoint, r.maxLevel = g.entryPoint, g.maxLevel
	for id, n := range g.nodes.all() {
		c := &Node{ID: n.ID, Vector: n.Vector, VectorF16: n.VectorF16, Level: n.Level, Neighbors: make([][]int64, len(n.Neighbors))}
		for l, list := range n.Neighbors {
			c.Neighbors[l] = append([]int64(nil), list...)
		}
		r.nodes[id] = c
	}
	return r
}

func (g *refGraph) vectorNodeDist(vec []float32, node *Node) float32 {
	if len(node.VectorF16) > 0 {
		return GetDistanceFuncF16(g.metric)(vec, node.VectorF16)
	}
	return g.distFunc(vec, node.Vector)
}

func (g *refGraph) nodeNodeDist(a, b *Node) float32 {
	if len(a.VectorF16) > 0 && len(b.VectorF16) > 0 {
		return GetDistanceFuncF16F16(g.metric)(a.VectorF16, b.VectorF16)
	}
	if len(a.VectorF16) > 0 {
		return GetDistanceFuncF16(g.metric)(b.Vector, a.VectorF16)
	}
	if len(b.VectorF16) > 0 {
		return GetDistanceFuncF16(g.metric)(a.Vector, b.VectorF16)
	}
	return g.distFunc(a.Vector, b.Vector)
}

func (g *refGraph) insert(id int64, vector []float32) {
	level := int(math.Floor(-math.Log(g.rng.Float64()) * g.ml))
	node := &Node{ID: id, Vector: vector, Level: level, Neighbors: make([][]int64, level+1)}
	g.nodes[id] = node
	if g.entryPoint == -1 {
		g.entryPoint, g.maxLevel = id, level
		return
	}

	ep := g.entryPoint
	epNode := g.nodes[ep]
	for lc := g.maxLevel; lc > level; lc-- {
		if lc > epNode.Level {
			continue
		}
		for changed := true; changed; {
			changed = false
			if lc < len(epNode.Neighbors) {
				for _, nid := range epNode.Neighbors[lc] {
					n, ok := g.nodes[nid]
					if !ok {
						continue
					}
					if g.vectorNodeDist(vector, n) < g.vectorNodeDist(vector, epNode) {
						ep, epNode, changed = nid, n, true
					}
				}
			}
		}
	}

	for lc := min(level, g.maxLevel); lc >= 0; lc-- {
		candidates := g.searchLevel(vector, ep, g.efConstruction, lc)
		mMax := g.mMax
		if lc == 0 {
			mMax = g.mMax0
		}
		neighbors := g.selectNeighbors(candidates, g.m)
		node.Neighbors[lc] = make([]int64, len(neighbors))
		for i, c := range neighbors {
			node.Neighbors[lc][i] = c.ID
		}
		for _, c := range neighbors {
			n, ok := g.nodes[c.ID]
			if !ok {
				continue
			}
			for len(n.Neighbors) <= lc {
				n.Neighbors = append(n.Neighbors, nil)
			}
			n.Neighbors[lc] = append(n.Neighbors[lc], id)
			if len(n.Neighbors[lc]) > mMax {
				g.prune(n, lc, mMax)
			}
		}
		if len(candidates) > 0 {
			ep = candidates[0].ID
		}
	}
	if level > g.maxLevel {
		g.entryPoint, g.maxLevel = id, level
	}
}

func (g *refGraph) searchLevel(query []float32, entryID int64, ef, level int) []Candidate {
	visited := map[int64]bool{entryID: true}
	entryDist := g.vectorNodeDist(query, g.nodes[entryID])
	candidates := &candidateHeap{items: []Candidate{{ID: entryID, Distance: entryDist}}, isMin: true}
	results := &candidateHeap{items: []Candidate{{ID: entryID, Distance: entryDist}}}
	for candidates.Len() > 0 {
		current := candidates.Pop()
		if results.Len() >= ef && current.Distance > results.Peek().Distance {
			break
		}
		currentNode, ok := g.nodes[current.ID]
		if !ok || level >= len(currentNode.Neighbors) {
			continue
		}
		for _, nid := range currentNode.Neighbors[level] {
			if visited[nid] {
				continue
			}
			visited[nid] = true
			n, ok := g.nodes[nid]
			if !ok {
				continue
			}
			dist := g.vectorNodeDist(query, n)
			if results.Len() < ef || dist < results.Peek().Distance {
				candidates.Push(Candidate{ID: nid, Distance: dist})
				results.Push(Candidate{ID: nid, Distance: dist})
				if results.Len() > ef {
					results.Pop()
				}
			}
		}
	}
	sorted := results.items
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Distance < sorted[j].Distance })
	return sorted
}

func (g *refGraph) selectNeighbors(candidates []Candidate, m int) []Candidate {
	if len(candidates) <= m {
		return candidates
	}
	if !g.useHeuristic {
		return candidates[:m]
	}
	selected := make([]Candidate, 0, m)
	for _, best := range candidates {
		if len(selected) >= m {
			break
		}
		good := true
		if bestNode, ok := g.nodes[best.ID]; ok {
			for _, s := range selected {
				sNode, ok := g.nodes[s.ID]
				if !ok {
					continue
				}
				if g.nodeNodeDist(bestNode, sNode) < best.Distance {
					good = false
					break
				}
			}
		}
		if good {
			selected = append(selected, best)
		}
	}
	for _, c := range candidates {
		if len(selected) >= m {
			break
		}
		found := false
		for _, s := range selected {
			if s.ID == c.ID {
				found = true
				break
			}
		}
		if !found {
			selected = append(selected, c)
		}
	}
	return selected
}

func (g *refGraph) prune(node *Node, level, mMax int) {
	type scoredID struct {
		id   int64
		dist float32
	}
	var scored []scoredID
	for _, nid := range node.Neighbors[level] {
		if n, ok := g.nodes[nid]; ok {
			scored = append(scored, scoredID{nid, g.nodeNodeDist(node, n)})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].dist < scored[j].dist })
	if len(scored) > mMax {
		scored = scored[:mMax]
	}
	node.Neighbors[level] = make([]int64, len(scored))
	for i, s := range scored {
		node.Neighbors[level][i] = s.id
	}
}

// sameGraph fails the test unless g is exactly the graph ref built: the same
// entry point, levels and neighbour lists, in the same order.
func sameGraph(t *testing.T, g *Graph, ref *refGraph) {
	t.Helper()
	if g.entryPoint != ref.entryPoint || g.maxLevel != ref.maxLevel || g.nodes.len() != len(ref.nodes) {
		t.Fatalf("entry point %d, max level %d, %d nodes; reference %d, %d, %d",
			g.entryPoint, g.maxLevel, g.nodes.len(), ref.entryPoint, ref.maxLevel, len(ref.nodes))
	}
	for id, want := range ref.nodes {
		got := g.nodes.get(id)
		if got == nil {
			t.Fatalf("node %d is missing", id)
		}
		if got.Level != want.Level || len(got.Neighbors) != len(want.Neighbors) {
			t.Fatalf("node %d: level %d with %d lists, reference level %d with %d", id, got.Level, len(got.Neighbors), want.Level, len(want.Neighbors))
		}
		for l := range want.Neighbors {
			if len(got.Neighbors[l]) != len(want.Neighbors[l]) {
				t.Fatalf("node %d level %d: %v, reference %v", id, l, got.Neighbors[l], want.Neighbors[l])
			}
			for i, n := range want.Neighbors[l] {
				if got.Neighbors[l][i] != n {
					t.Fatalf("node %d level %d: %v, reference %v", id, l, got.Neighbors[l], want.Neighbors[l])
				}
			}
		}
	}
}

// keptDistancesHold fails the test unless every distance kept next to a
// neighbour list is, bit for bit, the one a prune of that list computes.
func keptDistancesHold(t *testing.T, g *Graph) {
	t.Helper()
	if !g.nbrDistOK {
		return
	}
	inStep := 0
	for id, n := range g.nodes.all() {
		for l := range n.nbrDist {
			if l >= len(n.Neighbors) || len(n.nbrDist[l]) != len(n.Neighbors[l]) {
				continue
			}
			for i, nid := range n.Neighbors[l] {
				other := g.nodes.get(nid)
				if other == nil {
					t.Fatalf("node %d level %d: kept distance for %d, which has no node", id, l, nid)
				}
				want := g.nodeNodeDist(n, other)
				if len(n.VectorF16) == 0 {
					want = g.vectorNodeDist(n.Vector, other)
				}
				if math.Float32bits(n.nbrDist[l][i]) != math.Float32bits(want) {
					t.Fatalf("node %d level %d neighbour %d: kept %v, computed %v", id, l, nid, n.nbrDist[l][i], want)
				}
				inStep++
			}
		}
	}
	if inStep == 0 {
		t.Fatal("no kept distance to check: the graph does not keep any")
	}
}

// gridVec returns a point on a small integer grid: many points coincide and
// many distances tie, which is where a different sort or a different order of
// comparisons would show.
func gridVec(r *rand.Rand, d int) []float32 {
	v := make([]float32, d)
	for i := range v {
		v[i] = float32(r.Intn(3))
	}
	return v
}

func wrappedL2(a, b []float32) float32 { return L2DistanceSquared(a, b) }

// Graph.Insert builds the graph the reference algorithm builds: for every
// metric, with and without the neighbour heuristic, with tied distances, and
// whatever the IDs are (from zero, from an offset, far apart, negative, out of
// order).
func TestInsertBuildsTheReferenceGraph(t *testing.T) {
	metrics := map[string]DistanceFunc{
		"l2":      L2DistanceSquared,
		"cosine":  CosineDistance,
		"ip":      InnerProductDistance,
		"wrapped": wrappedL2, // not a built-in: no batching, no kept distances
	}
	ids := map[string]func(i int) int64{
		"from zero":    func(i int) int64 { return int64(i) },
		"from 700":     func(i int) int64 { return int64(700 + i) },
		"far apart":    func(i int) int64 { return 1<<40 + int64(i)*1_000_003 },
		"negative":     func(i int) int64 { return int64(i) - 250 },
		"out of order": func(i int) int64 { return int64((i * 7919) % 1009) },
	}
	data := map[string]func(*rand.Rand) []float32{
		"gaussian 24d": func(r *rand.Rand) []float32 { return randVec(r, 24) },
		"grid 3d":      func(r *rand.Rand) []float32 { return gridVec(r, 3) },
		"gaussian 5d":  func(r *rand.Rand) []float32 { return randVec(r, 5) }, // odd size: 2- and 3-wide batch ends
	}
	for mName, dist := range metrics {
		for idName, idOf := range ids {
			for dName, vec := range data {
				for _, heuristic := range []bool{true, false} {
					if !heuristic && (mName != "l2" || idName != "from zero") {
						continue
					}
					name := mName + "/" + idName + "/" + dName
					if !heuristic {
						name += "/closest"
					}
					t.Run(name, func(t *testing.T) {
						r := rand.New(rand.NewSource(11))
						g := NewGraphWithOptions(6, 40, 0, dist, heuristic)
						ref := newRefGraph(6, 40, dist, heuristic)
						for i := 0; i < 500; i++ {
							v := vec(r)
							g.Insert(idOf(i), v)
							ref.insert(idOf(i), v)
						}
						sameGraph(t, g, ref)
						keptDistancesHold(t, g)
					})
				}
			}
		}
	}
}

// The kept neighbour distances are given up, and the graph still matches the
// reference, when something changes what a distance would be: a node that
// loses its vector, a node that is replaced, a node that leaves the table.
func TestInsertMatchesReferenceAfterGraphEdits(t *testing.T) {
	edits := map[string]func(g *Graph, ref *refGraph, r *rand.Rand){
		"vector removed": func(g *Graph, ref *refGraph, r *rand.Rand) {
			for id := int64(0); id < 300; id += 7 {
				g.RemoveVector(id)
				ref.nodes[id].Vector = nil
			}
		},
		"node replaced": func(g *Graph, ref *refGraph, r *rand.Rand) {
			for id := int64(3); id < 300; id += 41 {
				v := randVec(r, 12)
				g.Insert(id, v)
				ref.insert(id, v)
			}
		},
		"node removed": func(g *Graph, ref *refGraph, r *rand.Rand) {
			for id := int64(1); id < 300; id += 9 {
				if id != g.entryPoint {
					g.removeNode(id)
					delete(ref.nodes, id)
				}
			}
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			r := rand.New(rand.NewSource(5))
			g := NewGraphWithOptions(5, 30, 12, nil, true)
			ref := newRefGraph(5, 30, nil, true)
			for i := 0; i < 300; i++ {
				v := randVec(r, 12)
				g.Insert(int64(i), v)
				ref.insert(int64(i), v)
			}
			if !g.nbrDistOK {
				t.Fatal("test setup: the graph keeps no neighbour distances")
			}
			edit(g, ref, r)
			if g.nbrDistOK {
				t.Fatal("the kept neighbour distances are still trusted after the edit")
			}
			for i := 300; i < 600; i++ {
				v := randVec(r, 12)
				g.Insert(int64(i), v)
				ref.insert(int64(i), v)
			}
			// A replaced node counts twice in neither graph's table.
			sameGraph(t, g, ref)
		})
	}
}

// Vectors added to an index that was loaded from its serialized form
// (Builder.AddVectors) join the graph as the reference algorithm joins them:
// the loaded lists have no kept distances until they are first pruned, and a
// float16 index mixes float16 nodes with the new float32 ones.
func TestInsertIntoLoadedGraphMatchesReference(t *testing.T) {
	for _, dtype := range []string{VectorDTypeF32, VectorDTypeF16} {
		for mName, dist := range map[string]DistanceFunc{"l2": L2DistanceSquared, "cosine": CosineDistance, "ip": InnerProductDistance} {
			t.Run(dtype+"/"+mName, func(t *testing.T) {
				r := rand.New(rand.NewSource(8))
				built := NewGraphWithOptions(5, 30, 10, dist, true)
				for i := 0; i < 300; i++ {
					built.Insert(int64(i), randVec(r, 10))
				}
				csr, err := ReadCSR(bytes.NewReader(serialize(t, built, dtype, 1)))
				if err != nil {
					t.Fatal(err)
				}
				g := csr.ToGraphWithDistance(dist)
				g.efConstruction = 30
				ref := refFromGraph(g, 30, dist)
				for i := 300; i < 600; i++ {
					v := randVec(r, 10)
					g.Insert(int64(i), v)
					ref.insert(int64(i), v)
				}
				sameGraph(t, g, ref)
				keptDistancesHold(t, g)
			})
		}
	}
}
