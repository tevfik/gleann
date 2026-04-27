package diskann

import (
	"math/rand"
	"sort"
	"sync"
)

// VamanaGraph is a single-level navigable graph built with the Vamana algorithm.
// Unlike HNSW's multi-level structure, Vamana uses one flat graph with
// robust pruning (α-parameter) for diversity-aware neighbor selection.
type VamanaGraph struct {
	mu sync.RWMutex

	// nodes stores all vectors, indexed by ID.
	nodes map[int64][]float32

	// neighbors[id] = list of neighbor IDs (max R neighbors per node).
	neighbors map[int64][]int64

	// medoid is the entry point — the node closest to the dataset centroid.
	medoid int64

	// Parameters
	r     int     // max out-degree
	l     int     // build candidate list size
	alpha float64 // prune diversity parameter

	dims     int
	distFunc DistanceFunc
	rng      *rand.Rand

	// nodeOrder is insertion order for deterministic iteration.
	nodeOrder []int64
}

// NewVamanaGraph creates an empty Vamana graph.
func NewVamanaGraph(r, l, dims int, alpha float64, distFunc DistanceFunc) *VamanaGraph {
	if r <= 0 {
		r = 64
	}
	if l <= 0 {
		l = 100
	}
	if alpha <= 0 {
		alpha = 1.2
	}
	if distFunc == nil {
		distFunc = L2DistanceSquared
	}
	return &VamanaGraph{
		nodes:     make(map[int64][]float32),
		neighbors: make(map[int64][]int64),
		medoid:    -1,
		r:         r,
		l:         l,
		alpha:     alpha,
		dims:      dims,
		distFunc:  distFunc,
		rng:       rand.New(rand.NewSource(42)),
	}
}

// Build constructs the Vamana graph from a batch of embeddings.
// This is the standard two-pass Vamana algorithm:
// Pass 1: random-order insertion with α=1.0 (greedy)
// Pass 2: re-insertion with α>1.0 (diverse)
func (g *VamanaGraph) Build(embeddings [][]float32) {
	g.mu.Lock()
	defer g.mu.Unlock()

	n := len(embeddings)
	if n == 0 {
		return
	}

	g.dims = len(embeddings[0])

	// Store all vectors.
	for i, emb := range embeddings {
		id := int64(i)
		g.nodes[id] = emb
		g.neighbors[id] = nil
		g.nodeOrder = append(g.nodeOrder, id)
	}

	// Find medoid (closest to centroid).
	g.medoid = g.findMedoid()

	// Generate random insertion order.
	order := make([]int64, n)
	for i := range order {
		order[i] = int64(i)
	}
	g.rng.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })

	// Pass 1: α = 1.0 (greedy, no diversity).
	for _, id := range order {
		g.insertNode(id, 1.0)
	}

	// Pass 2: α = configured value (diverse pruning).
	if g.alpha > 1.0 {
		g.rng.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })
		for _, id := range order {
			g.insertNode(id, g.alpha)
		}
	}
}

// insertNode performs greedy search from medoid, then robust-prunes neighbors.
func (g *VamanaGraph) insertNode(id int64, alpha float64) {
	vec := g.nodes[id]

	// Greedy search from medoid to find L nearest candidates.
	candidates := g.greedySearch(vec, g.l)

	// Robust prune: select up to R diverse neighbors.
	newNeighbors := g.robustPrune(id, candidates, alpha)
	g.neighbors[id] = newNeighbors

	// Add reverse edges and prune if over capacity.
	for _, nID := range newNeighbors {
		nNeighbors := g.neighbors[nID]
		// Check if already connected.
		found := false
		for _, existing := range nNeighbors {
			if existing == id {
				found = true
				break
			}
		}
		if !found {
			nNeighbors = append(nNeighbors, id)
			if len(nNeighbors) > g.r {
				// Prune the neighbor's list to R.
				nCandidates := make([]Candidate, len(nNeighbors))
				for i, cID := range nNeighbors {
					nCandidates[i] = Candidate{ID: cID, Distance: g.distFunc(g.nodes[nID], g.nodes[cID])}
				}
				nNeighbors = g.robustPrune(nID, nCandidates, alpha)
			}
			g.neighbors[nID] = nNeighbors
		}
	}
}

// greedySearch performs beam search on the graph starting from medoid.
// Returns up to L nearest candidates sorted by distance.
func (g *VamanaGraph) greedySearch(query []float32, l int) []Candidate {
	if g.medoid < 0 {
		return nil
	}

	visited := make(map[int64]bool)
	visited[g.medoid] = true

	medoidDist := g.distFunc(query, g.nodes[g.medoid])
	candidates := []Candidate{{ID: g.medoid, Distance: medoidDist}}

	// Best-first search with candidate list of size L.
	for i := 0; i < len(candidates); i++ {
		current := candidates[i]

		for _, nID := range g.neighbors[current.ID] {
			if visited[nID] {
				continue
			}
			visited[nID] = true

			dist := g.distFunc(query, g.nodes[nID])
			candidates = append(candidates, Candidate{ID: nID, Distance: dist})
		}

		// Keep sorted and trim to L.
		if len(candidates) > l*2 {
			sort.Slice(candidates, func(a, b int) bool {
				return candidates[a].Distance < candidates[b].Distance
			})
			candidates = candidates[:l]
		}
	}

	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].Distance < candidates[b].Distance
	})
	if len(candidates) > l {
		candidates = candidates[:l]
	}
	return candidates
}

// robustPrune implements DiskANN's α-robust pruning (Algorithm 2).
// It selects up to R neighbors that are diverse — a candidate is kept only
// if it is not "dominated" by an already-selected neighbor within factor α.
func (g *VamanaGraph) robustPrune(nodeID int64, candidates []Candidate, alpha float64) []int64 {
	// Remove self from candidates.
	filtered := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.ID != nodeID {
			filtered = append(filtered, c)
		}
	}

	// Sort by distance ascending.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Distance < filtered[j].Distance
	})

	result := make([]int64, 0, g.r)

	for len(filtered) > 0 && len(result) < g.r {
		// Pick closest remaining candidate.
		best := filtered[0]
		result = append(result, best.ID)

		if len(result) >= g.r {
			break
		}

		// Remove candidates dominated by 'best'.
		var remaining []Candidate
		bestVec := g.nodes[best.ID]
		for _, c := range filtered[1:] {
			// Keep candidate if: α * dist(c, best) > dist(c, node)
			// i.e., c is NOT too close to an already-chosen neighbor.
			cVec := g.nodes[c.ID]
			distToBest := g.distFunc(cVec, bestVec)
			if float64(distToBest)*alpha > float64(c.Distance) {
				remaining = append(remaining, c)
			}
		}
		filtered = remaining
	}

	return result
}

// findMedoid finds the node closest to the dataset centroid.
func (g *VamanaGraph) findMedoid() int64 {
	if len(g.nodes) == 0 {
		return -1
	}

	// Compute centroid.
	centroid := make([]float32, g.dims)
	for _, vec := range g.nodes {
		for j, v := range vec {
			centroid[j] += v
		}
	}
	n := float32(len(g.nodes))
	for j := range centroid {
		centroid[j] /= n
	}

	// Find closest node to centroid.
	bestID := int64(-1)
	bestDist := float32(1e30)
	for id, vec := range g.nodes {
		d := g.distFunc(centroid, vec)
		if d < bestDist {
			bestDist = d
			bestID = id
		}
	}
	return bestID
}

// Search performs greedy search on the built graph.
func (g *VamanaGraph) Search(query []float32, topK, l int) []Candidate {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if l < topK {
		l = topK
	}
	candidates := g.greedySearch(query, l)
	if len(candidates) > topK {
		candidates = candidates[:topK]
	}
	return candidates
}

// Size returns the number of nodes.
func (g *VamanaGraph) Size() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// Medoid returns the entry point ID.
func (g *VamanaGraph) Medoid() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.medoid
}

// GetNeighbors returns the neighbors of a node.
func (g *VamanaGraph) GetNeighbors(id int64) []int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.neighbors[id]
}

// GetVector returns the vector for a node.
func (g *VamanaGraph) GetVector(id int64) []float32 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.nodes[id]
}

// AllNodeIDs returns all node IDs in insertion order.
func (g *VamanaGraph) AllNodeIDs() []int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids := make([]int64, len(g.nodeOrder))
	copy(ids, g.nodeOrder)
	return ids
}
