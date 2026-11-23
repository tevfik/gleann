// Package hnsw implements a pure Go HNSW (Hierarchical Navigable Small World)
// graph for approximate nearest neighbor search.
//
// This is a from-scratch implementation that avoids CGo/FAISS dependency,
// following the algorithm from "Efficient and robust approximate nearest
// neighbor search using Hierarchical Navigable Small World graphs"
// by Malkov & Yashunin (2018).
package hnsw

import (
	"math"
	"math/rand"
	"slices"
	"sort"
	"sync"
)

// DistanceFunc is a function that computes distance between two vectors.
// Lower values indicate more similar vectors.
type DistanceFunc func(a, b []float32) float32

// Graph is a multi-level HNSW graph for approximate nearest neighbor search.
type Graph struct {
	mu sync.RWMutex

	// nodes stores all nodes in the graph, indexed by their ID.
	nodes nodeStore

	// entryPoint is the entry point node at the highest level.
	entryPoint int64

	// maxLevel is the current maximum level in the graph.
	maxLevel int

	// Parameters
	m              int     // number of connections per node
	mMax           int     // max connections per node at non-zero levels
	mMax0          int     // max connections per node at level 0
	efConstruction int     // candidate list size during construction
	ml             float64 // level generation factor: 1/ln(M)

	// distFunc is the distance function to use.
	distFunc DistanceFunc

	// useHeuristic enables diversity-aware neighbor selection (Algorithm 4).
	useHeuristic bool

	// rng is used for level generation.
	rng *rand.Rand

	// dimensions of vectors.
	dimensions int

	// vectorDType indicates whether stored vectors are float32 ("f32") or float16 ("f16").
	vectorDType string

	// distMetricName stores the metric name ("l2", "cosine", "ip").
	distMetricName string

	// batchL2 is set when distFunc is L2DistanceSquared: distances to several
	// nodes are then computed together (distBatch) with identical results.
	batchL2 bool

	// nodeCount tracks the number of nodes.
	nodeCount int64

	// nbrDistOK is set while the distances kept next to the neighbour lists
	// (Node.nbrDist) are the ones pruneConnections would compute: the metric
	// is a built-in one, which gives f(a, b) and f(b, a) the same bits, and no
	// node was replaced, removed or stripped of its vector since the graph was
	// made. Once it is cleared the distances are computed again on every prune.
	nbrDistOK bool

	// ins is the working memory of Insert, guarded by mu like the graph.
	ins insertScratch

	// searchPool holds the *searchScratch of finished searches for the next
	// ones: searches run concurrently under the read lock, each with its own.
	searchPool sync.Pool
}

// Node represents a single node in the HNSW graph. A node handed out by the
// graph (GetNode) is the graph's own: read it, do not change it.
type Node struct {
	ID        int64
	Vector    []float32
	VectorF16 []uint16
	Level     int
	// Neighbors at each level. neighbors[level] = list of neighbor IDs.
	Neighbors [][]int64

	// nbrDist[level][i] is the distance from this node to Neighbors[level][i]
	// as pruneConnections computes it, kept from the insert that made the
	// link so that pruning the list does not measure all of it again. It is
	// in step with the list when both have the same length; a list that came
	// from an index file has none until it is first pruned. Used only while
	// Graph.nbrDistOK is set, never serialized.
	nbrDist [][]float32
}

// Candidate is a (id, distance) pair used during search.
type Candidate struct {
	ID       int64
	Distance float32
}

// NewGraph creates a new HNSW graph with the given parameters.
func NewGraph(m, efConstruction, dimensions int) *Graph {
	return NewGraphWithOptions(m, efConstruction, dimensions, nil, true)
}

// NewGraphWithDistance creates a new HNSW graph with a custom distance function.
// If distFunc is nil, L2 squared distance is used.
func NewGraphWithDistance(m, efConstruction, dimensions int, distFunc DistanceFunc) *Graph {
	return NewGraphWithOptions(m, efConstruction, dimensions, distFunc, true)
}

// NewGraphWithOptions creates a new HNSW graph with all options.
func NewGraphWithOptions(m, efConstruction, dimensions int, distFunc DistanceFunc, useHeuristic bool) *Graph {
	if m <= 0 {
		m = 32
	}
	if efConstruction <= 0 {
		efConstruction = 200
	}
	if distFunc == nil {
		distFunc = L2DistanceSquared
	}

	return &Graph{
		entryPoint:     -1,
		maxLevel:       -1,
		m:              m,
		mMax:           m,
		mMax0:          m * 2, // Level 0 has double the connections
		efConstruction: efConstruction,
		ml:             1.0 / math.Log(float64(m)),
		distFunc:       distFunc,
		distMetricName: detectMetric(distFunc),
		batchL2:        isL2Func(distFunc),
		nbrDistOK:      isBuiltinDist(distFunc),
		useHeuristic:   useHeuristic,
		rng:            rand.New(rand.NewSource(42)),
		dimensions:     dimensions,
		vectorDType:    "f32",
	}
}

// detectMetric infers distance metric name from distance function.
func detectMetric(f DistanceFunc) string {
	if f == nil {
		return "l2"
	}
	a := []float32{1, 0}
	b := []float32{0, 1}
	d := f(a, b)
	if math.Abs(float64(d-1.0)) < 1e-5 {
		return "cosine"
	}
	if math.Abs(float64(d)) < 1e-5 {
		return "ip"
	}
	return "l2"
}

func (g *Graph) vectorNodeDist(vec []float32, node *Node) float32 {
	if len(node.VectorF16) > 0 {
		return GetDistanceFuncF16(g.distMetricName)(vec, node.VectorF16)
	}
	return g.distFunc(vec, node.Vector)
}

func (g *Graph) nodeNodeDist(nodeA, nodeB *Node) float32 {
	if len(nodeA.VectorF16) > 0 && len(nodeB.VectorF16) > 0 {
		return GetDistanceFuncF16F16(g.distMetricName)(nodeA.VectorF16, nodeB.VectorF16)
	}
	if len(nodeA.VectorF16) > 0 {
		return GetDistanceFuncF16(g.distMetricName)(nodeB.Vector, nodeA.VectorF16)
	}
	if len(nodeB.VectorF16) > 0 {
		return GetDistanceFuncF16(g.distMetricName)(nodeA.Vector, nodeB.VectorF16)
	}
	return g.distFunc(nodeA.Vector, nodeB.Vector)
}

// Insert adds a vector to the graph with the given ID.
func (g *Graph) Insert(id int64, vector []float32) {
	g.mu.Lock()
	defer g.mu.Unlock()

	level := g.randomLevel()

	node := &Node{
		ID:        id,
		Vector:    vector,
		Level:     level,
		Neighbors: make([][]int64, level+1),
	}
	for i := range node.Neighbors {
		node.Neighbors[i] = make([]int64, 0)
	}

	replaced := g.nodes.get(id) != nil
	if g.nbrDistOK && replaced {
		// The node replaces one that other nodes link to: the distances they
		// kept are those of the old vector.
		g.nbrDistOK = false
	}
	if g.nbrDistOK {
		node.nbrDist = make([][]float32, level+1)
	}

	g.nodes.put(id, node)
	if !replaced {
		g.nodeCount++
	}

	// First node
	if g.entryPoint == -1 {
		g.entryPoint = id
		g.maxLevel = level
		return
	}

	sc := &g.ins.search
	ep := g.entryPoint
	epNode := g.nodes.get(ep)

	// Phase 1: Traverse from top level to node's level + 1
	// using greedy search (ef=1).
	var epDist float32 // distance of epNode to vector, kept in step with epNode
	if g.maxLevel > level {
		epDist = g.vectorNodeDist(vector, epNode)
	}
	for lc := g.maxLevel; lc > level; lc-- {
		if lc > epNode.Level {
			continue
		}
		ep, epNode, epDist = g.descend(sc, vector, ep, epNode, epDist, lc)
	}

	// Phase 2: Insert at each level from min(level, maxLevel) down to 0.
	for lc := min(level, g.maxLevel); lc >= 0; lc-- {
		// Search for nearest neighbors at this level. candidates and neighbors
		// live in the scratch: they are used up before the next level.
		candidates := g.searchLevel(sc, vector, ep, g.efConstruction, lc)

		// Select M nearest neighbors.
		mMax := g.mMax
		if lc == 0 {
			mMax = g.mMax0
		}
		neighbors := g.selectNeighbors(candidates, g.m, lc)

		// Connect the new node to its neighbors. The list gets room for the
		// links other nodes add back later: it is pruned in place once it
		// holds mMax+1 (pruneConnections) and so never has to grow.
		own := make([]int64, len(neighbors), max(len(neighbors), mMax+1))
		for i, c := range neighbors {
			own[i] = c.ID
		}
		node.Neighbors[lc] = own
		if g.nbrDistOK {
			// c.Distance is vectorNodeDist(vector, c's node): what a prune of
			// this node's list computes for c.
			ownDist := make([]float32, len(neighbors), cap(own))
			for i, c := range neighbors {
				ownDist[i] = c.Distance
			}
			node.nbrDist[lc] = ownDist
		}

		// Add bidirectional connections.
		for _, c := range neighbors {
			neighbor := g.nodes.get(c.ID)
			if neighbor == nil {
				continue
			}
			// Ensure neighbor has enough levels.
			for len(neighbor.Neighbors) <= lc {
				neighbor.Neighbors = append(neighbor.Neighbors, make([]int64, 0))
			}
			neighbor.Neighbors[lc] = append(neighbor.Neighbors[lc], id)
			if g.nbrDistOK {
				// The distance from the neighbour back to the new node has the
				// bits of c.Distance (see nbrDistOK).
				neighbor.linkDist(lc, c.Distance)
			}

			// Prune if exceeds max connections.
			if len(neighbor.Neighbors[lc]) > mMax {
				// Keep the closest mMax neighbors.
				g.pruneConnections(neighbor, lc, mMax)
			}
		}

		// Update entry point for next level.
		if len(candidates) > 0 {
			ep = candidates[0].ID
		}
	}

	// Update entry point if new node has higher level.
	if level > g.maxLevel {
		g.entryPoint = id
		g.maxLevel = level
	}
}

// Search performs an approximate nearest neighbor search.
func (g *Graph) Search(query []float32, topK, ef int) []Candidate {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.entryPoint == -1 {
		return nil
	}

	if ef < topK {
		ef = topK
	}

	sc, _ := g.searchPool.Get().(*searchScratch)
	if sc == nil {
		sc = new(searchScratch)
	}
	defer g.searchPool.Put(sc)

	ep := g.entryPoint
	epNode := g.nodes.get(ep)

	// Traverse from top level down to level 1 with greedy search.
	if g.maxLevel > 0 {
		epDist := g.vectorNodeDist(query, epNode)
		for lc := g.maxLevel; lc > 0; lc-- {
			if lc > epNode.Level {
				continue
			}
			ep, epNode, epDist = g.descend(sc, query, ep, epNode, epDist, lc)
		}
	}

	// Search at level 0 with ef.
	candidates := g.searchLevel(sc, query, ep, ef, 0)

	// Return top K, copied out of the scratch.
	if len(candidates) > topK {
		candidates = candidates[:topK]
	}
	out := make([]Candidate, len(candidates))
	copy(out, candidates)
	return out
}

// descend is the greedy search (ef=1) of one level above the insertion or
// search level: from epNode, whose distance to query is epDist, it moves to a
// closer neighbour until no neighbour is closer, and returns where it ends.
// The neighbours of a step are measured together (distBatch) and then compared
// in their order, which takes the same steps as measuring them one by one.
func (g *Graph) descend(sc *searchScratch, query []float32, ep int64, epNode *Node, epDist float32, lc int) (int64, *Node, float32) {
	for changed := true; changed && lc < len(epNode.Neighbors); {
		changed = false
		ids, nodes := sc.freshIDs[:0], sc.freshNodes[:0]
		for _, neighborID := range epNode.Neighbors[lc] {
			if neighbor := g.nodes.get(neighborID); neighbor != nil {
				ids = append(ids, neighborID)
				nodes = append(nodes, neighbor)
			}
		}
		sc.freshIDs, sc.freshNodes = ids, nodes
		dist := sc.dists(len(nodes))
		g.distBatch(query, nodes, dist)
		for i, nd := range dist {
			if nd < epDist {
				ep = ids[i]
				epNode = nodes[i]
				epDist = nd
				changed = true
			}
		}
	}
	return ep, epNode, epDist
}

// SearchWithRecompute performs search using on-demand embedding recomputation.
// Instead of using stored vectors, it calls the recompute function to get
// embeddings only for nodes visited during graph traversal.
func (g *Graph) SearchWithRecompute(query []float32, topK, ef int, recompute func(ids []int64) [][]float32) []Candidate {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.entryPoint == -1 {
		return nil
	}

	if ef < topK {
		ef = topK
	}

	ep := g.entryPoint

	// Cache recomputed embeddings to avoid redundant computation.
	embeddingCache := make(map[int64][]float32)
	getEmbedding := func(id int64) []float32 {
		if vec, ok := embeddingCache[id]; ok {
			return vec
		}
		// Check if node has stored vector first.
		if node := g.nodes.get(id); node != nil && len(node.Vector) > 0 {
			embeddingCache[id] = node.Vector
			return node.Vector
		}
		// Recompute.
		vecs := recompute([]int64{id})
		if len(vecs) > 0 {
			embeddingCache[id] = vecs[0]
			return vecs[0]
		}
		return nil
	}

	// Traverse from top level to level 1.
	for lc := g.maxLevel; lc > 0; lc-- {
		node := g.nodes.get(ep)
		if node == nil || lc > node.Level {
			continue
		}
		epVec := getEmbedding(ep)
		if epVec == nil {
			continue
		}
		changed := true
		for changed {
			changed = false
			if lc < len(node.Neighbors) {
				for _, neighborID := range node.Neighbors[lc] {
					next := g.nodes.get(neighborID)
					if next == nil {
						continue // a neighbour without a node (removed): skip it, like Search
					}
					nVec := getEmbedding(neighborID)
					if nVec == nil {
						continue
					}
					if g.distFunc(query, nVec) < g.distFunc(query, epVec) {
						ep = neighborID
						node = next
						epVec = nVec
						changed = true
					}
				}
			}
		}
	}

	// Search at level 0 with recomputation.
	candidates := g.searchLevelWithRecompute(query, ep, ef, 0, getEmbedding)

	if len(candidates) > topK {
		candidates = candidates[:topK]
	}
	return candidates
}

// Size returns the number of nodes in the graph.
func (g *Graph) Size() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return int64(g.nodes.len())
}

// GetNode returns the node with the given ID. The returned node must not be
// read while another goroutine inserts, because concurrent inserts mutate
// neighbor slices in-place during pruning. Callers (cmd/gleann/graph_analysis.go,
// internal/graph/viz, the bench adapter) all read a finished graph.
func (g *Graph) GetNode(id int64) (*Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n := g.nodes.get(id)
	return n, n != nil
}

// GetEntryPoint returns the entry point node ID.
func (g *Graph) GetEntryPoint() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.entryPoint
}

// MaxLevel returns the maximum level in the graph.
func (g *Graph) MaxLevel() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.maxLevel
}

// Dimensions returns the vector dimensions.
func (g *Graph) Dimensions() int {
	return g.dimensions
}

// AllNodeIDs returns all node IDs in the graph.
func (g *Graph) AllNodeIDs() []int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids := make([]int64, 0, g.nodes.len())
	for id := range g.nodes.all() {
		ids = append(ids, id)
	}
	return ids
}

// RemoveVector sets a node's vector to nil (for embedding pruning).
func (g *Graph) RemoveVector(id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if node := g.nodes.get(id); node != nil {
		node.Vector = nil
		node.VectorF16 = nil
		g.nbrDistOK = false
	}
}

// searchLevel performs beam search at a given level. The result is sorted by
// distance and lives in sc: it is valid until sc is used again.
func (g *Graph) searchLevel(sc *searchScratch, query []float32, entryID int64, ef, level int) []Candidate {
	visited := &sc.visited
	visited.reset(g.nodes.denseLen())
	visited.visit(entryID)

	entryNode := g.nodes.get(entryID)
	entryDist := g.vectorNodeDist(query, entryNode)

	// candidates is a min-heap of candidates (ordered by distance, ascending).
	candidates := &sc.candidates
	candidates.items = append(candidates.items[:0], Candidate{ID: entryID, Distance: entryDist})
	candidates.isMin = true

	// results is a max-heap of results (ordered by distance, descending).
	results := &sc.results
	results.items = append(results.items[:0], Candidate{ID: entryID, Distance: entryDist})
	results.isMin = false

	for candidates.Len() > 0 {
		// Get closest candidate.
		current := candidates.Pop()

		// If this candidate is farther than the farthest result, stop.
		if results.Len() >= ef && current.Distance > results.Peek().Distance {
			break
		}

		// Examine neighbors at this level.
		currentNode := g.nodes.get(current.ID)
		if currentNode == nil || level >= len(currentNode.Neighbors) {
			continue
		}

		// Collect the unvisited neighbours first and measure them together
		// (distBatch); the heaps are then updated in the original order.
		freshIDs, freshNodes := sc.freshIDs[:0], sc.freshNodes[:0]
		for _, neighborID := range currentNode.Neighbors[level] {
			if visited.visit(neighborID) {
				continue
			}

			neighbor := g.nodes.get(neighborID)
			if neighbor == nil {
				continue
			}
			freshIDs = append(freshIDs, neighborID)
			freshNodes = append(freshNodes, neighbor)
		}
		sc.freshIDs, sc.freshNodes = freshIDs, freshNodes
		freshDist := sc.dists(len(freshNodes))
		g.distBatch(query, freshNodes, freshDist)

		for fi, neighborID := range freshIDs {
			dist := freshDist[fi]

			if results.Len() < ef || dist < results.Peek().Distance {
				candidates.Push(Candidate{ID: neighborID, Distance: dist})
				results.Push(Candidate{ID: neighborID, Distance: dist})

				if results.Len() > ef {
					results.Pop()
				}
			}
		}
	}

	// Sort results by distance (ascending).
	slices.SortFunc(results.items, byDistance)
	return results.items
}

// searchLevelWithRecompute performs beam search with on-demand embedding recomputation.
func (g *Graph) searchLevelWithRecompute(query []float32, entryID int64, ef, level int, getEmbedding func(int64) []float32) []Candidate {
	visited := make(map[int64]bool)
	visited[entryID] = true

	entryVec := getEmbedding(entryID)
	if entryVec == nil {
		return nil
	}
	entryDist := g.distFunc(query, entryVec)

	candidates := &candidateHeap{
		items: []Candidate{{ID: entryID, Distance: entryDist}},
		isMin: true,
	}

	results := &candidateHeap{
		items: []Candidate{{ID: entryID, Distance: entryDist}},
		isMin: false,
	}

	for candidates.Len() > 0 {
		current := candidates.Pop()

		if results.Len() >= ef && current.Distance > results.Peek().Distance {
			break
		}

		currentNode := g.nodes.get(current.ID)
		if currentNode == nil || level >= len(currentNode.Neighbors) {
			continue
		}

		for _, neighborID := range currentNode.Neighbors[level] {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true

			// A neighbour without a node (removed) is neither a result nor
			// recomputed, as in searchLevel.
			if g.nodes.get(neighborID) == nil {
				continue
			}
			nVec := getEmbedding(neighborID)
			if nVec == nil {
				continue
			}

			dist := g.distFunc(query, nVec)

			if results.Len() < ef || dist < results.Peek().Distance {
				candidates.Push(Candidate{ID: neighborID, Distance: dist})
				results.Push(Candidate{ID: neighborID, Distance: dist})

				if results.Len() > ef {
					results.Pop()
				}
			}
		}
	}

	sortedResults := results.items
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].Distance < sortedResults[j].Distance
	})
	return sortedResults
}

// selectNeighbors selects the best M neighbors from candidates.
// When useHeuristic is true, uses Algorithm 4 (diversity-aware selection) from
// Malkov & Yashunin 2018, preferring neighbors that are not closer to each
// other than they are to the query. This produces more diverse connections
// and improves recall, especially for clustered data.
//
// level is the level the candidates were found at.
func (g *Graph) selectNeighbors(candidates []Candidate, m, level int) []Candidate {
	if len(candidates) <= m {
		return candidates
	}
	if !g.useHeuristic {
		// Simple selection: closest M.
		return candidates[:m]
	}
	return g.selectNeighborsHeuristic(candidates, m, level)
}

// selectNeighborsHeuristic implements Algorithm 4 from the HNSW paper.
// It greedily picks candidates that are closer to the query than to any
// already-selected neighbor, promoting diversity in the neighbor set.
func (g *Graph) selectNeighborsHeuristic(candidates []Candidate, m, level int) []Candidate {
	// The result and the nodes of selected that exist live in the insert
	// scratch: the caller uses the result up before the next selection.
	selected := g.ins.selected[:0]
	selNodes := g.ins.selNodes[:0]

	// inSel is the set of the IDs in selNodes, for the kept distances below.
	inSel := &g.ins.inSel
	if g.nbrDistOK {
		inSel.reset(g.nodes.denseLen())
	}

	// Candidates are taken closest first until M are selected.
	for _, best := range candidates {
		if len(selected) >= m {
			break
		}

		// Check if this candidate is closer to query than to any selected neighbor.
		good := true
		bestNode := g.nodes.get(best.ID)
		if g.nbrDistOK && len(selNodes) > 0 && bestNode != nil &&
			level < len(bestNode.nbrDist) && level < len(bestNode.Neighbors) &&
			len(bestNode.nbrDist[level]) == len(bestNode.Neighbors[level]) {
			// The distances from best to its own neighbours are kept: when a
			// selected node is among them and closer to best than the query
			// is, best is rejected without measuring anything. The kept value
			// is the one the loops below compute for that pair.
			for k, d := range bestNode.nbrDist[level] {
				if d < best.Distance && inSel.has(bestNode.Neighbors[level][k]) {
					good = false
					break
				}
			}
		}
		if good && len(selNodes) > 0 && bestNode != nil {
			if len(bestNode.VectorF16) == 0 {
				// Measure the selected neighbours four at a time and stop at the
				// first one closer to best than the query is (same decision as
				// checking them one by one; nodeNodeDist(bestNode, s) is
				// vectorNodeDist(bestNode.Vector, s) for a float32 bestNode).
				var dist [4]float32
				for i := 0; i < len(selNodes) && good; i += 4 {
					end := min(i+4, len(selNodes))
					g.distBatch(bestNode.Vector, selNodes[i:end], dist[:end-i])
					for _, interDist := range dist[:end-i] {
						if interDist < best.Distance {
							good = false
							break
						}
					}
				}
			} else {
				for _, selNode := range selNodes {
					if g.nodeNodeDist(bestNode, selNode) < best.Distance {
						good = false
						break
					}
				}
			}
		}

		if good {
			selected = append(selected, best)
			// Selected candidates without a node are skipped by the check above.
			if bestNode != nil {
				selNodes = append(selNodes, bestNode)
				if g.nbrDistOK {
					inSel.visit(best.ID)
				}
			}
		}
	}

	// If the heuristic picked fewer than M, top up with the closest candidates
	// not yet selected (keepPrunedConnections in the HNSW paper). The loop
	// above only stops short of M once the candidates are used up, so this
	// must not require remaining candidates: it did, and never ran.
	if len(selected) < m {
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
	}

	g.ins.selected, g.ins.selNodes = selected, selNodes
	return selected
}

// linkDist records dist as the distance to the neighbour just appended to
// Neighbors[level], if the distances of that list were in step with it before
// the append. A list without distances (loaded from an index file) stays
// without: its first prune computes them all.
func (n *Node) linkDist(level int, dist float32) {
	for len(n.nbrDist) <= level {
		n.nbrDist = append(n.nbrDist, nil)
	}
	if len(n.nbrDist[level]) == len(n.Neighbors[level])-1 {
		n.nbrDist[level] = append(n.nbrDist[level], dist)
	} else {
		n.nbrDist[level] = nil
	}
}

// removeNode drops a node from the node table and leaves the edges that point
// at it behind; every traversal skips an ID without a node. No index path
// removes nodes (deleted passages are tombstones outside the graph, RFC 005):
// the tests use it to model a table that lost entries. Whatever removes a
// node has to come through here, because the distances kept next to the
// neighbour lists assume that every listed neighbour exists.
//
// It is a test helper and must be called with the write lock held or before
// the graph is shared.
func (g *Graph) removeNode(id int64) {
	if g.nodes.get(id) != nil {
		g.nodes.remove(id)
		g.nodeCount--
	}
	g.nbrDistOK = false
}

// pruneConnections prunes a node's connections at a given level to keep only the closest mMax.
// The kept connections are written over the list they came from.
func (g *Graph) pruneConnections(node *Node, level, mMax int) {
	neighbors := node.Neighbors[level]
	if len(neighbors) <= mMax {
		return
	}

	// Compute distances and sort.
	scored := g.ins.scored[:0]
	if g.nbrDistOK && level < len(node.nbrDist) && len(node.nbrDist[level]) == len(neighbors) {
		// Every distance is already known: from the insert that made the link
		// or from the previous prune of this list.
		for i, nID := range neighbors {
			scored = append(scored, Candidate{ID: nID, Distance: node.nbrDist[level][i]})
		}
	} else {
		nodes := g.ins.pruneNodes[:0]
		for _, nID := range neighbors {
			n := g.nodes.get(nID)
			if n == nil {
				continue
			}
			scored = append(scored, Candidate{ID: nID})
			nodes = append(nodes, n)
		}
		if cap(g.ins.pruneDist) < len(nodes) {
			g.ins.pruneDist = make([]float32, len(nodes), len(nodes)+len(nodes)/2)
		}
		dists := g.ins.pruneDist[:len(nodes)]
		if len(node.VectorF16) == 0 {
			// nodeNodeDist(node, n) is vectorNodeDist(node.Vector, n) here.
			g.distBatch(node.Vector, nodes, dists)
		} else {
			for i, n := range nodes {
				dists[i] = g.nodeNodeDist(node, n)
			}
		}
		for i := range scored {
			scored[i].Distance = dists[i]
		}
		g.ins.pruneNodes = nodes
	}
	g.ins.scored = scored

	slices.SortFunc(scored, byDistance)

	if len(scored) > mMax {
		scored = scored[:mMax]
	}

	kept := neighbors[:len(scored)]
	for i, s := range scored {
		kept[i] = s.ID
	}
	node.Neighbors[level] = kept

	if g.nbrDistOK {
		for len(node.nbrDist) <= level {
			node.nbrDist = append(node.nbrDist, nil)
		}
		keptDist := node.nbrDist[level][:0]
		if cap(keptDist) < len(scored) {
			keptDist = make([]float32, 0, cap(kept))
		}
		for _, s := range scored {
			keptDist = append(keptDist, s.Distance)
		}
		node.nbrDist[level] = keptDist
	}
}

// randomLevel generates a random level for a new node.
func (g *Graph) randomLevel() int {
	r := g.rng.Float64()
	return int(math.Floor(-math.Log(r) * g.ml))
}

// L2DistanceSquared computes the squared L2 (Euclidean) distance between two vectors.
func L2DistanceSquared(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var sum float32
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

// CosineDistance computes cosine distance: 1 - cosine_similarity.
// Values range from 0 (identical) to 2 (opposite).
func CosineDistance(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := float32(math.Sqrt(float64(normA)) * math.Sqrt(float64(normB)))
	if denom == 0 {
		return float32(math.MaxFloat32)
	}
	return 1.0 - dot/denom
}

// InnerProductDistance computes negative inner product distance.
// We negate because HNSW expects lower = more similar.
func InnerProductDistance(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return float32(math.MaxFloat32)
	}
	var dot float32
	for i := range a {
		dot += a[i] * b[i]
	}
	return -dot
}

// GetDistanceFunc returns the distance function for the given metric name.
func GetDistanceFunc(metric string) DistanceFunc {
	switch metric {
	case "cosine":
		return CosineDistance
	case "ip", "inner_product":
		return InnerProductDistance
	default:
		return L2DistanceSquared
	}
}

// candidateHeap is a simple priority queue for candidates.
type candidateHeap struct {
	items []Candidate
	isMin bool // true = min-heap (pop smallest), false = max-heap (pop largest)
}

func (h *candidateHeap) Len() int { return len(h.items) }

func (h *candidateHeap) Push(c Candidate) {
	h.items = append(h.items, c)
	h.siftUp(len(h.items) - 1)
}

func (h *candidateHeap) Pop() Candidate {
	n := len(h.items)
	if n == 0 {
		return Candidate{}
	}
	top := h.items[0]
	h.items[0] = h.items[n-1]
	h.items = h.items[:n-1]
	if len(h.items) > 0 {
		h.siftDown(0)
	}
	return top
}

func (h *candidateHeap) Peek() Candidate {
	if len(h.items) == 0 {
		return Candidate{}
	}
	return h.items[0]
}

func (h *candidateHeap) less(i, j int) bool {
	if h.isMin {
		return h.items[i].Distance < h.items[j].Distance
	}
	return h.items[i].Distance > h.items[j].Distance
}

func (h *candidateHeap) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if h.less(i, parent) {
			h.items[i], h.items[parent] = h.items[parent], h.items[i]
			i = parent
		} else {
			break
		}
	}
}

func (h *candidateHeap) siftDown(i int) {
	n := len(h.items)
	for {
		left := 2*i + 1
		right := 2*i + 2
		best := i
		if left < n && h.less(left, best) {
			best = left
		}
		if right < n && h.less(right, best) {
			best = right
		}
		if best == i {
			break
		}
		h.items[i], h.items[best] = h.items[best], h.items[i]
		i = best
	}
}
