// Package community implements graph community detection algorithms
// on the code graph. It uses the Louvain method to discover
// tightly-connected groups of symbols (functions, types, files) and
// identify "god nodes" (high-degree hubs) and surprising cross-community edges.
package community

import (
	"fmt"
	"math"
	"sort"
)

// Node represents a graph node with its ID and metadata.
type Node struct {
	ID   string // FQN for symbols, path for files
	Name string
	Kind string // "function", "method", "type", "file", etc.
	File string // source file (empty for file nodes)
}

// Edge represents a directed edge in the graph.
type Edge struct {
	From       string
	To         string
	Weight     float64
	Confidence string // "extracted", "inferred", "ambiguous"
}

// Community is a group of nodes detected by the Louvain algorithm.
type Community struct {
	ID        int      `json:"id"`
	Label     string   `json:"label"` // auto-generated from dominant file/package
	NodeCount int      `json:"node_count"`
	Nodes     []string `json:"nodes"`    // node IDs
	Cohesion  float64  `json:"cohesion"` // internal edge density
}

// GodNode is a high-degree hub in the graph.
type GodNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	InDeg    int    `json:"in_degree"`
	OutDeg   int    `json:"out_degree"`
	TotalDeg int    `json:"total_degree"`
}

// SurprisingEdge is an edge that crosses community boundaries unexpectedly.
type SurprisingEdge struct {
	From          string  `json:"from"`
	To            string  `json:"to"`
	FromCommunity int     `json:"from_community"`
	ToCommunity   int     `json:"to_community"`
	Weight        float64 `json:"weight"`
}

// Result holds the complete output of community detection.
type Result struct {
	Communities     []Community      `json:"communities"`
	GodNodes        []GodNode        `json:"god_nodes"`
	SurprisingEdges []SurprisingEdge `json:"surprising_edges"`
	Modularity      float64          `json:"modularity"`
	NodeCount       int              `json:"node_count"`
	EdgeCount       int              `json:"edge_count"`
}

// Graph is an in-memory adjacency representation for community detection.
type Graph struct {
	nodes       map[string]*Node
	neighbors   map[string]map[string]float64 // undirected adjacency
	inDeg       map[string]int
	outDeg      map[string]int
	totalWeight float64

	// directed holds each distinct from→to pair once, in first-insertion
	// order, with the weights of repeated AddEdge calls summed. It makes
	// Edges() deterministic and truthful about direction: the undirected
	// neighbour maps carry no direction and Go randomises their iteration.
	directed   []Edge
	directedAt map[[2]string]int
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes:     make(map[string]*Node),
		neighbors: make(map[string]map[string]float64),
		inDeg:     make(map[string]int),
		outDeg:    make(map[string]int),

		directedAt: make(map[[2]string]int),
	}
}

// AddNode adds a node to the graph.
func (g *Graph) AddNode(n Node) {
	g.nodes[n.ID] = &n
	if g.neighbors[n.ID] == nil {
		g.neighbors[n.ID] = make(map[string]float64)
	}
}

// AddEdge adds an undirected weighted edge. Directed info is tracked separately.
func (g *Graph) AddEdge(from, to string, weight float64) {
	// Ensure nodes exist.
	if g.neighbors[from] == nil {
		g.neighbors[from] = make(map[string]float64)
	}
	if g.neighbors[to] == nil {
		g.neighbors[to] = make(map[string]float64)
	}
	// Undirected for modularity computation.
	g.neighbors[from][to] += weight
	g.neighbors[to][from] += weight
	g.totalWeight += weight

	g.outDeg[from]++
	g.inDeg[to]++

	key := [2]string{from, to}
	if i, ok := g.directedAt[key]; ok {
		g.directed[i].Weight += weight
	} else {
		g.directedAt[key] = len(g.directed)
		g.directed = append(g.directed, Edge{From: from, To: to, Weight: weight})
	}
}

// NodeCount returns the number of nodes.
func (g *Graph) NodeCount() int { return len(g.nodes) }

// EdgeCount returns the number of directed edges (based on degree tracking).
func (g *Graph) EdgeCount() int {
	total := 0
	for _, d := range g.outDeg {
		total += d
	}
	return total
}

// NodeIDs returns all node IDs sorted alphabetically.
func (g *Graph) NodeIDs() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetNode returns a node by ID, or nil.
func (g *Graph) GetNode(id string) *Node {
	return g.nodes[id]
}

// Degree returns the total degree (in + out) of a node.
func (g *Graph) Degree(id string) int {
	return g.inDeg[id] + g.outDeg[id]
}

// Edges returns the distinct directed edges, one per from→to pair, in the
// order they were first added (deterministic for a deterministic loader). A
// mutual pair (A→B and B→A) yields two edges.
func (g *Graph) Edges() []Edge {
	out := make([]Edge, len(g.directed))
	copy(out, g.directed)
	return out
}

// ExportForAnalysis returns all nodes (sorted by ID) and directed edges for use with PageRank, risk, etc.
func (g *Graph) ExportForAnalysis() ([]Node, []Edge) {
	nodes := make([]Node, 0, len(g.nodes))
	for _, id := range g.NodeIDs() {
		nodes = append(nodes, *g.nodes[id])
	}
	edges := g.Edges()
	return nodes, edges
}

// Detect runs the Louvain algorithm and returns community detection results.
// godNodeThreshold is the minimum total degree for a node to be considered a god node.
// maxSurprising limits the number of surprising edges returned.
func Detect(g *Graph, godNodeThreshold, maxSurprising int) (*Result, error) {
	if g.NodeCount() == 0 {
		return &Result{}, nil
	}
	if godNodeThreshold <= 0 {
		godNodeThreshold = 5
	}
	if maxSurprising <= 0 {
		maxSurprising = 20
	}

	// Phase 1: Louvain community assignment.
	membership := louvain(g)

	// Phase 2: Build communities.
	communityMap := make(map[int][]string)
	for _, nodeID := range g.sortedNeighborIDs() {
		commID := membership[nodeID]
		communityMap[commID] = append(communityMap[commID], nodeID)
	}

	communities := make([]Community, 0, len(communityMap))
	for _, id := range sortedIntKeys(communityMap) {
		nodes := communityMap[id]
		label := inferCommunityLabel(g, nodes)
		cohesion := computeCohesion(g, nodes)
		communities = append(communities, Community{
			ID:        id,
			Label:     label,
			NodeCount: len(nodes),
			Nodes:     nodes,
			Cohesion:  cohesion,
		})
	}
	// Largest first; equal sizes ordered by their first (smallest) member so
	// the numbering does not depend on the Louvain pass.
	sort.SliceStable(communities, func(i, j int) bool {
		if communities[i].NodeCount != communities[j].NodeCount {
			return communities[i].NodeCount > communities[j].NodeCount
		}
		return communities[i].Nodes[0] < communities[j].Nodes[0]
	})
	// Renumber community IDs by size (0 = largest).
	idRemap := make(map[int]int)
	for i, c := range communities {
		idRemap[c.ID] = i
		communities[i].ID = i
	}
	for nodeID := range membership {
		membership[nodeID] = idRemap[membership[nodeID]]
	}

	// Phase 3: God nodes.
	godNodes := detectGodNodes(g, godNodeThreshold)

	// Phase 4: Surprising edges (cross-community).
	surprising := detectSurprisingEdges(g, membership, maxSurprising)

	// Modularity.
	mod := computeModularity(g, membership)

	return &Result{
		Communities:     communities,
		GodNodes:        godNodes,
		SurprisingEdges: surprising,
		Modularity:      mod,
		NodeCount:       g.NodeCount(),
		EdgeCount:       g.EdgeCount(),
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Louvain algorithm implementation
// ──────────────────────────────────────────────────────────────────────────────

// sortedNeighborIDs returns every vertex ID (including edge endpoints that
// were never added as nodes) in ascending order.
func (g *Graph) sortedNeighborIDs() []string {
	ids := make([]string, 0, len(g.neighbors))
	for id := range g.neighbors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedIntKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

type weightedNbr struct {
	id string
	w  float64
}

// sortedAdjacency returns the undirected adjacency of every vertex with the
// neighbours in ascending ID order, so float sums and tie-breaks do not
// depend on Go's randomised map iteration.
func (g *Graph) sortedAdjacency() map[string][]weightedNbr {
	adj := make(map[string][]weightedNbr, len(g.neighbors))
	for id, nbrs := range g.neighbors {
		list := make([]weightedNbr, 0, len(nbrs))
		for nb, w := range nbrs {
			list = append(list, weightedNbr{nb, w})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })
		adj[id] = list
	}
	return adj
}

// louvain assigns each node to a community using the Louvain method. The
// result is deterministic for a given graph: vertices are visited in ID
// order, neighbours are summed in ID order and ties between equally good
// target communities go to the lowest community number.
func louvain(g *Graph) map[string]int {
	// Initialize: each node in its own community.
	nodeList := g.sortedNeighborIDs()
	membership := make(map[string]int, len(nodeList))
	for commID, id := range nodeList {
		membership[id] = commID
	}
	adj := g.sortedAdjacency()

	m2 := g.totalWeight * 2 // 2*m for undirected
	if m2 == 0 {
		return membership
	}

	// Precompute node strengths (weighted degree in undirected graph).
	strength := make(map[string]float64, len(nodeList))
	for _, id := range nodeList {
		s := 0.0
		for _, nb := range adj[id] {
			s += nb.w
		}
		strength[id] = s
	}

	// Precompute sigmaTot (total edge weight for each community).
	// Initially, each node is in its own community, so sigmaTot[commID] = strength[nodeID].
	sigmaTot := make(map[int]float64, len(nodeList))
	for _, id := range nodeList {
		sigmaTot[membership[id]] = strength[id]
	}

	// Iterate until no improvement.
	for pass := 0; pass < 50; pass++ {
		improved := false
		for _, nodeID := range nodeList {
			currentComm := membership[nodeID]
			ki := strength[nodeID]

			// Compute neighbor communities and their edge weights.
			commWeights := make(map[int]float64)
			for _, nb := range adj[nodeID] {
				commWeights[membership[nb.id]] += nb.w
			}

			bestComm := currentComm
			bestDelta := 0.0

			// sigmaOld is the community weight without this node.
			sigmaOld := sigmaTot[currentComm] - ki
			kiInOld := commWeights[currentComm]

			for _, comm := range sortedIntKeys(commWeights) {
				if comm == currentComm {
					continue
				}
				kiIn := commWeights[comm]
				// Modularity gain of moving node to comm.
				sigmaNew := sigmaTot[comm]

				deltaRemove := -2.0 * (kiInOld - (sigmaOld*ki)/m2) / m2
				deltaAdd := 2.0 * (kiIn - (sigmaNew*ki)/m2) / m2
				delta := deltaRemove + deltaAdd

				if delta > bestDelta {
					bestDelta = delta
					bestComm = comm
				}
			}

			if bestComm != currentComm && bestDelta > 1e-10 {
				sigmaTot[currentComm] -= ki
				sigmaTot[bestComm] += ki
				membership[nodeID] = bestComm
				improved = true
			}
		}
		if !improved {
			break
		}
	}

	// Compact community IDs to 0..N-1 in order of first (smallest) member.
	seen := make(map[int]int)
	nextID := 0
	for _, id := range nodeList {
		comm := membership[id]
		if _, ok := seen[comm]; !ok {
			seen[comm] = nextID
			nextID++
		}
	}
	for _, id := range nodeList {
		membership[id] = seen[membership[id]]
	}

	return membership
}

// computeModularity calculates the Newman-Girvan modularity Q.
func computeModularity(g *Graph, membership map[string]int) float64 {
	m2 := g.totalWeight * 2
	if m2 == 0 {
		return 0
	}

	adj := g.sortedAdjacency()
	ids := g.sortedNeighborIDs()
	strength := make(map[string]float64, len(ids))
	for _, id := range ids {
		for _, nb := range adj[id] {
			strength[id] += nb.w
		}
	}

	q := 0.0
	for _, nodeA := range ids {
		for _, nb := range adj[nodeA] {
			if membership[nodeA] != membership[nb.id] {
				continue
			}
			expected := strength[nodeA] * strength[nb.id] / m2
			q += nb.w - expected
		}
	}
	return q / m2
}

// inferCommunityLabel generates a label from the dominant package/directory.
func inferCommunityLabel(g *Graph, nodeIDs []string) string {
	dirCount := make(map[string]int)
	for _, id := range nodeIDs {
		n := g.nodes[id]
		if n == nil {
			continue
		}
		file := n.File
		if file == "" {
			file = id
		}
		// Extract directory/package from file path.
		dir := extractPackage(file)
		if dir != "" {
			dirCount[dir]++
		}
	}

	if len(dirCount) == 0 {
		return fmt.Sprintf("cluster-%d", len(nodeIDs))
	}

	dirs := make([]string, 0, len(dirCount))
	for dir := range dirCount {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	bestDir := ""
	bestCount := 0
	for _, dir := range dirs {
		count := dirCount[dir]
		if count > bestCount {
			bestCount = count
			bestDir = dir
		}
	}
	return bestDir
}

// extractPackage extracts package/directory name from a file path or FQN.
func extractPackage(path string) string {
	// Handle FQN format: "github.com/org/repo/pkg/sub.Func"
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			path = path[:i]
			break
		}
	}
	// Get last path segment.
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// computeCohesion calculates internal edge density of a community.
func computeCohesion(g *Graph, nodeIDs []string) float64 {
	if len(nodeIDs) <= 1 {
		return 1.0
	}
	nodeSet := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		nodeSet[id] = true
	}

	internalEdges := 0.0
	for _, id := range nodeIDs {
		nbrs := g.neighbors[id]
		keys := make([]string, 0, len(nbrs))
		for nbr := range nbrs {
			keys = append(keys, nbr)
		}
		sort.Strings(keys)
		for _, nbr := range keys {
			if nodeSet[nbr] {
				internalEdges += nbrs[nbr]
			}
		}
	}
	// Divide by 2 for undirected counting, normalize by max possible edges.
	n := float64(len(nodeIDs))
	maxEdges := n * (n - 1)
	if maxEdges == 0 {
		return 1.0
	}
	return math.Min((internalEdges/2)/maxEdges, 1.0)
}

// detectGodNodes finds high-degree hubs.
func detectGodNodes(g *Graph, threshold int) []GodNode {
	var gods []GodNode
	for _, id := range g.NodeIDs() {
		n := g.nodes[id]
		in := g.inDeg[id]
		out := g.outDeg[id]
		total := in + out
		if total >= threshold {
			gods = append(gods, GodNode{
				ID:       id,
				Name:     n.Name,
				Kind:     n.Kind,
				InDeg:    in,
				OutDeg:   out,
				TotalDeg: total,
			})
		}
	}
	sort.SliceStable(gods, func(i, j int) bool {
		return gods[i].TotalDeg > gods[j].TotalDeg
	})
	if len(gods) > 20 {
		gods = gods[:20]
	}
	return gods
}

// detectSurprisingEdges finds edges crossing community boundaries.
func detectSurprisingEdges(g *Graph, membership map[string]int, max int) []SurprisingEdge {
	var edges []SurprisingEdge
	adj := g.sortedAdjacency()

	// Each undirected pair once, from the lower to the higher ID.
	for _, from := range g.sortedNeighborIDs() {
		for _, nb := range adj[from] {
			to, w := nb.id, nb.w
			if from > to {
				continue
			}
			fc := membership[from]
			tc := membership[to]
			if fc != tc {
				edges = append(edges, SurprisingEdge{
					From:          from,
					To:            to,
					FromCommunity: fc,
					ToCommunity:   tc,
					Weight:        w,
				})
			}
		}
	}

	// Sort by weight descending (most "significant" cross-community edges first).
	sort.SliceStable(edges, func(i, j int) bool {
		return edges[i].Weight > edges[j].Weight
	})
	if len(edges) > max {
		edges = edges[:max]
	}
	return edges
}
