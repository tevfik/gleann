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
	nodes map[int64]*Node

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

	// nodeCount tracks the number of nodes.
	nodeCount int64
}

// Node represents a single node in the HNSW graph.
type Node struct {
	ID     int64
	Vector []float32
	Level  int
	// Neighbors at each level. neighbors[level] = list of neighbor IDs.
	Neighbors [][]int64
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
		nodes:          make(map[int64]*Node),
		entryPoint:     -1,
		maxLevel:       -1,
		m:              m,
		mMax:           m,
		mMax0:          m * 2, // Level 0 has double the connections
		efConstruction: efConstruction,
		ml:             1.0 / math.Log(float64(m)),
		distFunc:       distFunc,
		useHeuristic:   useHeuristic,
		rng:            rand.New(rand.NewSource(42)),
		dimensions:     dimensions,
	}
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

	g.nodes[id] = node
	g.nodeCount++

	// First node
	if g.entryPoint == -1 {
		g.entryPoint = id
		g.maxLevel = level
		return
	}

	ep := g.entryPoint
	epNode := g.nodes[ep]

	// Phase 1: Traverse from top level to node's level + 1
	// using greedy search (ef=1).
	for lc := g.maxLevel; lc > level; lc-- {
		if lc > epNode.Level {
			continue
		}
		changed := true
		for changed {
			changed = false
			if lc < len(epNode.Neighbors) {
				for _, neighborID := range epNode.Neighbors[lc] {
					neighbor, ok := g.nodes[neighborID]
					if !ok {
						continue
					}
					if g.distFunc(vector, neighbor.Vector) < g.distFunc(vector, epNode.Vector) {
						ep = neighborID
						epNode = neighbor
						changed = true
					}
				}
			}
		}
	}

	// Phase 2: Insert at each level from min(level, maxLevel) down to 0.
	for lc := min(level, g.maxLevel); lc >= 0; lc-- {
		// Search for nearest neighbors at this level.
		candidates := g.searchLevel(vector, ep, g.efConstruction, lc)

		// Select M nearest neighbors.
		mMax := g.mMax
		if lc == 0 {
			mMax = g.mMax0
		}
		neighbors := g.selectNeighbors(candidates, g.m)

		// Connect the new node to its neighbors.
		node.Neighbors[lc] = make([]int64, len(neighbors))
		for i, c := range neighbors {
			node.Neighbors[lc][i] = c.ID
		}

		// Add bidirectional connections.
		for _, c := range neighbors {
			neighbor, ok := g.nodes[c.ID]
			if !ok {
				continue
			}
			// Ensure neighbor has enough levels.
			for len(neighbor.Neighbors) <= lc {
				neighbor.Neighbors = append(neighbor.Neighbors, make([]int64, 0))
			}
			neighbor.Neighbors[lc] = append(neighbor.Neighbors[lc], id)

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

	ep := g.entryPoint
	epNode := g.nodes[ep]

	// Traverse from top level down to level 1 with greedy search.
	for lc := g.maxLevel; lc > 0; lc-- {
		if lc > epNode.Level {
			continue
		}
		changed := true
		for changed {
			changed = false
			if lc < len(epNode.Neighbors) {
				for _, neighborID := range epNode.Neighbors[lc] {
					neighbor, ok := g.nodes[neighborID]
					if !ok {
						continue
					}
					if g.distFunc(query, neighbor.Vector) < g.distFunc(query, epNode.Vector) {
						ep = neighborID
						epNode = neighbor
						changed = true
					}
				}
			}
		}
	}

	// Search at level 0 with ef.
	candidates := g.searchLevel(query, ep, ef, 0)

	// Return top K.
	if len(candidates) > topK {
		candidates = candidates[:topK]
	}
	return candidates
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
		if node, ok := g.nodes[id]; ok && len(node.Vector) > 0 {
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
		node, ok := g.nodes[ep]
		if !ok || lc > node.Level {
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
					nVec := getEmbedding(neighborID)
					if nVec == nil {
						continue
					}
					if g.distFunc(query, nVec) < g.distFunc(query, epVec) {
						ep = neighborID
						node = g.nodes[ep]
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
	return g.nodeCount
}

// GetNode returns the node with the given ID.
func (g *Graph) GetNode(id int64) (*Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
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
	ids := make([]int64, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	return ids
}

// RemoveVector sets a node's vector to nil (for embedding pruning).
func (g *Graph) RemoveVector(id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if node, ok := g.nodes[id]; ok {
		node.Vector = nil
	}
}

// searchLevel performs beam search at a given level.
func (g *Graph) searchLevel(query []float32, entryID int64, ef, level int) []Candidate {
	visited := make(map[int64]bool)
	visited[entryID] = true

	entryNode := g.nodes[entryID]
	entryDist := g.distFunc(query, entryNode.Vector)

	// candidates is a min-heap of candidates (ordered by distance, ascending).
	candidates := &candidateHeap{
		items: []Candidate{{ID: entryID, Distance: entryDist}},
		isMin: true,
	}

	// results is a max-heap of results (ordered by distance, descending).
	results := &candidateHeap{
		items: []Candidate{{ID: entryID, Distance: entryDist}},
		isMin: false,
	}

	for candidates.Len() > 0 {
		// Get closest candidate.
		current := candidates.Pop()

		// If this candidate is farther than the farthest result, stop.
		if results.Len() >= ef && current.Distance > results.Peek().Distance {
			break
		}

		// Examine neighbors at this level.
		currentNode, ok := g.nodes[current.ID]
		if !ok || level >= len(currentNode.Neighbors) {
			continue
		}

		for _, neighborID := range currentNode.Neighbors[level] {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true

			neighbor, ok := g.nodes[neighborID]
			if !ok {
				continue
			}

			dist := g.distFunc(query, neighbor.Vector)

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
	sortedResults := results.items
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].Distance < sortedResults[j].Distance
	})
	return sortedResults
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

		currentNode, ok := g.nodes[current.ID]
		if !ok || level >= len(currentNode.Neighbors) {
			continue
		}

		for _, neighborID := range currentNode.Neighbors[level] {
			if visited[neighborID] {
				continue
			}
			visited[neighborID] = true

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
func (g *Graph) selectNeighbors(candidates []Candidate, m int) []Candidate {
	if len(candidates) <= m {
		return candidates
	}
	if !g.useHeuristic {
		// Simple selection: closest M.
		return candidates[:m]
	}
	return g.selectNeighborsHeuristic(candidates, m)
}

// selectNeighborsHeuristic implements Algorithm 4 from the HNSW paper.
// It greedily picks candidates that are closer to the query than to any
// already-selected neighbor, promoting diversity in the neighbor set.
func (g *Graph) selectNeighborsHeuristic(candidates []Candidate, m int) []Candidate {
	selected := make([]Candidate, 0, m)
	remaining := make([]Candidate, len(candidates))
	copy(remaining, candidates)

	for len(selected) < m && len(remaining) > 0 {
		// Pick the closest remaining candidate.
		best := remaining[0]
		remaining = remaining[1:]

		// Check if this candidate is closer to query than to any selected neighbor.
		good := true
		if len(selected) > 0 {
			bestNode, ok := g.nodes[best.ID]
			if ok {
				for _, sel := range selected {
					selNode, ok2 := g.nodes[sel.ID]
					if !ok2 {
						continue
					}
					interDist := g.distFunc(bestNode.Vector, selNode.Vector)
					if interDist < best.Distance {
						good = false
						break
					}
				}
			}
		}

		if good {
			selected = append(selected, best)
		}
	}

	// If we couldn't fill M slots with heuristic, fall back to closest remaining.
	if len(selected) < m && len(remaining) > 0 {
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

	return selected
}

// pruneConnections prunes a node's connections at a given level to keep only the closest mMax.
func (g *Graph) pruneConnections(node *Node, level, mMax int) {
	neighbors := node.Neighbors[level]
	if len(neighbors) <= mMax {
		return
	}

	// Compute distances and sort.
	type neighbor struct {
		id   int64
		dist float32
	}
	scored := make([]neighbor, 0, len(neighbors))
	for _, nID := range neighbors {
		n, ok := g.nodes[nID]
		if !ok {
			continue
		}
		dist := g.distFunc(node.Vector, n.Vector)
		scored = append(scored, neighbor{id: nID, dist: dist})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].dist < scored[j].dist
	})

	if len(scored) > mMax {
		scored = scored[:mMax]
	}

	node.Neighbors[level] = make([]int64, len(scored))
	for i, s := range scored {
		node.Neighbors[level][i] = s.id
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
