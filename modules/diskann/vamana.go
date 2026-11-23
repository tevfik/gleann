package diskann

import (
	"math/rand"
	"runtime"
	"sort"
	"sync"
)

const defaultNumLocks = 2048

// VamanaGraph is a single-level navigable graph built with the Vamana algorithm.
// Unlike HNSW's multi-level structure, Vamana uses one flat graph with
// robust pruning (α-parameter) for diversity-aware neighbor selection.
// This implementation features multi-threaded graph construction with fine-grained
// striped node locking for high-concurrency scaling.
type VamanaGraph struct {
	mu sync.RWMutex

	// nodes stores all vectors, indexed by ID [0..N-1].
	nodes [][]float32

	// neighbors[id] = list of neighbor IDs (max R neighbors per node).
	neighbors [][]int64

	// nodeLocks provides striped locking for thread-safe concurrent neighbor updates.
	nodeLocks []sync.Mutex
	numLocks  int

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
		medoid:    -1,
		r:         r,
		l:         l,
		alpha:     alpha,
		dims:      dims,
		distFunc:  distFunc,
		rng:       rand.New(rand.NewSource(42)),
		numLocks:  defaultNumLocks,
		nodeLocks: make([]sync.Mutex, defaultNumLocks),
	}
}

func (g *VamanaGraph) lockNode(id int64) {
	g.nodeLocks[id%int64(g.numLocks)].Lock()
}

func (g *VamanaGraph) unlockNode(id int64) {
	g.nodeLocks[id%int64(g.numLocks)].Unlock()
}

// Build constructs the Vamana graph from a batch of embeddings using parallel workers.
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
	g.nodes = embeddings
	g.neighbors = make([][]int64, n)
	g.nodeOrder = make([]int64, n)
	for i := range g.nodeOrder {
		g.nodeOrder[i] = int64(i)
	}

	// Find medoid (closest to centroid).
	g.medoid = g.findMedoid()

	// Generate random insertion order.
	order := make([]int64, n)
	copy(order, g.nodeOrder)
	g.rng.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers <= 0 {
		numWorkers = 1
	}
	if numWorkers > 32 {
		numWorkers = 32
	}

	runPass := func(alpha float64) {
		jobCh := make(chan int64, numWorkers*4)
		var wg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := range jobCh {
					g.insertNode(id, alpha)
				}
			}()
		}
		for _, id := range order {
			jobCh <- id
		}
		close(jobCh)
		wg.Wait()
	}

	// Pass 1: α = 1.0 (greedy, no diversity).
	runPass(1.0)

	// Pass 2: α = configured value (diverse pruning).
	if g.alpha > 1.0 {
		g.rng.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })
		runPass(g.alpha)
	}

	// Reverse edges were allowed to take a node up to slackDegree; bring
	// every node back to at most R.
	g.pruneToR(numWorkers)

	g.ensureConnected()
}

// graphSlack is how far over R a node's degree may grow from reverse edges
// before it is pruned back to R (DiskANN's GRAPH_SLACK_FACTOR). Without slack
// a full node is pruned again for every reverse edge it receives, and that
// pruning was 59 % of the build (20,000 x 384, R 64). At 100,000 x 768 the
// build went from 6,489 s to 1,773 s (4 threads) and recall@10 at search L 64
// from 0.878 to 0.920: a node now chooses among up to 1.3 R candidates at once
// instead of dropping one at a time.
const graphSlack = 1.3

// slackDegree is the degree above which a reverse edge triggers a prune.
func (g *VamanaGraph) slackDegree() int {
	return int(float64(g.r) * graphSlack)
}

// pruneToR prunes every node whose degree exceeds R down to R. It runs after
// the insertion passes, with no concurrent inserts.
func (g *VamanaGraph) pruneToR(workers int) {
	jobs := make(chan int64, workers*4)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				// Only node id is written; the vectors are read-only.
				nb := g.neighbors[id]
				cands := make([]Candidate, len(nb))
				for i, c := range nb {
					cands[i] = Candidate{ID: c, Distance: g.distFunc(g.nodes[id], g.nodes[c])}
				}
				g.neighbors[id] = g.robustPrune(id, cands, g.alpha)
			}
		}()
	}
	for id := range g.neighbors {
		if len(g.neighbors[id]) > g.r {
			jobs <- int64(id)
		}
	}
	close(jobs)
	wg.Wait()
}

// ensureConnected links every node a search from the medoid cannot reach to
// its nearest reachable node. Workers insert concurrently and pruning drops
// reverse edges, so a node can lose its only in-edge; searches would then never
// return it. The extra edges may take a node a little over R.
func (g *VamanaGraph) ensureConnected() {
	n := len(g.nodes)
	seen := make([]bool, n)
	var reached []int64
	visit := func(from int64) {
		seen[from] = true
		queue := []int64{from}
		reached = append(reached, from)
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			for _, nb := range g.neighbors[id] {
				if !seen[nb] {
					seen[nb] = true
					reached = append(reached, nb)
					queue = append(queue, nb)
				}
			}
		}
	}
	visit(g.medoid)
	for id := int64(0); id < int64(n); id++ {
		if seen[id] {
			continue
		}
		best, bestDist := reached[0], g.distFunc(g.nodes[id], g.nodes[reached[0]])
		for _, r := range reached[1:] {
			if d := g.distFunc(g.nodes[id], g.nodes[r]); d < bestDist {
				best, bestDist = r, d
			}
		}
		g.neighbors[best] = append(g.neighbors[best], id)
		visit(id) // what id points at is now reachable too
	}
}

// insertNode performs greedy search from medoid, then robust-prunes neighbors.
func (g *VamanaGraph) insertNode(id int64, alpha float64) {
	vec := g.nodes[id]

	// Search from the medoid for the candidates to prune from.
	candidates := g.buildCandidates(vec, g.l)

	// Robust prune: select up to R diverse neighbors.
	newNeighbors := g.robustPrune(id, candidates, alpha)

	g.lockNode(id)
	g.neighbors[id] = newNeighbors
	g.unlockNode(id)

	// Add reverse edges and prune if over capacity.
	for _, nID := range newNeighbors {
		g.lockNode(nID)
		nNeighbors := g.neighbors[nID]
		found := false
		for _, existing := range nNeighbors {
			if existing == id {
				found = true
				break
			}
		}
		if !found {
			nNeighbors = append(nNeighbors, id)
			if len(nNeighbors) > g.slackDegree() {
				nCandidates := make([]Candidate, len(nNeighbors))
				for i, cID := range nNeighbors {
					nCandidates[i] = Candidate{ID: cID, Distance: g.distFunc(g.nodes[nID], g.nodes[cID])}
				}
				nNeighbors = g.robustPrune(nID, nCandidates, alpha)
			}
			g.neighbors[nID] = nNeighbors
		}
		g.unlockNode(nID)
	}
}

// buildCandidates returns the up to L closest nodes an insertion found, for
// robustPrune to pick the node's neighbours from.
//
// It is deliberately not a best-first search: it expands the candidates in
// the order they were found and trims the list to the L closest whenever it
// passes 2L, so it also expands nodes a best-first search would never reach
// and sees a wider part of the graph for about the same number of
// expansions. On data whose distances concentrate that width is what the
// graph's quality rests on. Measured at 100,000 x 768 (bench/vectordb's
// Gaussian mixture, R 64, L 100, where pruning removes nothing and every
// node keeps its 64 nearest candidates), recall@10 at search L 64 / 128 /
// 256: this walk 0.920 / 0.981 / 0.994; greedySearch with L 100: 0.512 /
// 0.737 / 0.908, with L 400 (1.7 times the build time of the walk): 0.726 /
// 0.927 / 0.991. On real embeddings (29,807 x 384) the two build equally
// good graphs (0.992 and 0.993 at search L 64).
func (g *VamanaGraph) buildCandidates(query []float32, l int) []Candidate {
	if g.medoid < 0 || len(g.nodes) == 0 {
		return nil
	}

	visited := make(map[int64]bool)
	visited[g.medoid] = true

	medoidDist := g.distFunc(query, g.nodes[g.medoid])
	candidates := []Candidate{{ID: g.medoid, Distance: medoidDist}}

	var nIDs []int64
	for i := 0; i < len(candidates); i++ {
		current := candidates[i]

		g.lockNode(current.ID)
		nIDs = append(nIDs[:0], g.neighbors[current.ID]...)
		g.unlockNode(current.ID)

		for _, nID := range nIDs {
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

// greedySearch is the query search on the in-memory graph (Search): a beam
// search from the medoid that returns up to L nearest candidates sorted by
// distance.
//
// It always expands the closest candidate not expanded yet and stops when the
// L closest are all expanded (DiskANN's GreedySearch). Until v1.20.1 Search
// used the walk of buildCandidates, which can skip a close candidate that
// sorts in behind the position it has reached and so miss the nearest node
// (TestGreedySearchExpandsClosestCandidate). The build keeps that walk, for
// the reason given there.
func (g *VamanaGraph) greedySearch(query []float32, l int) []Candidate {
	if g.medoid < 0 || len(g.nodes) == 0 {
		return nil
	}
	if l < 1 {
		l = 1
	}

	visited := make(map[int64]bool, 4*l)
	visited[g.medoid] = true

	// beam is sorted by distance and holds at most l candidates; expanded[i]
	// belongs to beam[i].
	beam := make([]Candidate, 1, l+1)
	expanded := make([]bool, 1, l+1)
	beam[0] = Candidate{ID: g.medoid, Distance: g.distFunc(query, g.nodes[g.medoid])}

	var nIDs []int64
	for {
		next := -1
		for i := range beam {
			if !expanded[i] {
				next = i
				break
			}
		}
		if next < 0 {
			break // the l closest candidates are all expanded
		}
		expanded[next] = true
		current := beam[next].ID

		g.lockNode(current)
		nIDs = append(nIDs[:0], g.neighbors[current]...)
		g.unlockNode(current)

		for _, nID := range nIDs {
			if visited[nID] {
				continue
			}
			visited[nID] = true

			dist := g.distFunc(query, g.nodes[nID])
			if len(beam) == l && dist >= beam[l-1].Distance {
				continue // not among the l closest
			}
			// Insert in distance order, after equal distances.
			pos := sort.Search(len(beam), func(i int) bool { return beam[i].Distance > dist })
			beam = append(beam, Candidate{})
			expanded = append(expanded, false)
			copy(beam[pos+1:], beam[pos:])
			copy(expanded[pos+1:], expanded[pos:])
			beam[pos] = Candidate{ID: nID, Distance: dist}
			expanded[pos] = false
			if len(beam) > l {
				beam, expanded = beam[:l], expanded[:l]
			}
		}
	}
	return beam
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
		best := filtered[0]
		result = append(result, best.ID)

		if len(result) >= g.r {
			break
		}

		var remaining []Candidate
		bestVec := g.nodes[best.ID]
		for _, c := range filtered[1:] {
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
	n := len(g.nodes)
	if n == 0 {
		return -1
	}

	centroid := make([]float32, g.dims)
	for _, vec := range g.nodes {
		for j, v := range vec {
			centroid[j] += v
		}
	}
	fn := float32(n)
	for j := range centroid {
		centroid[j] /= fn
	}

	bestID := int64(-1)
	bestDist := float32(1e30)
	for id, vec := range g.nodes {
		d := g.distFunc(centroid, vec)
		if d < bestDist {
			bestDist = d
			bestID = int64(id)
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
	if id < 0 || int(id) >= len(g.neighbors) {
		return nil
	}
	res := make([]int64, len(g.neighbors[id]))
	copy(res, g.neighbors[id])
	return res
}

// GetVector returns the vector for a node.
func (g *VamanaGraph) GetVector(id int64) []float32 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if id < 0 || int(id) >= len(g.nodes) {
		return nil
	}
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
