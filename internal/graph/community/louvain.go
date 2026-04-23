//go:build treesitter

// Package community implements graph community detection algorithms
// on the KuzuDB code graph. It uses the Louvain method to discover
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
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes:     make(map[string]*Node),
		neighbors: make(map[string]map[string]float64),
		inDeg:     make(map[string]int),
		outDeg:    make(map[string]int),
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

// Edges returns all directed edges in the graph.
func (g *Graph) Edges() []Edge {
	var out []Edge
	seen := make(map[string]bool)
	for from, nbrs := range g.neighbors {
		for to, w := range nbrs {
			// Only emit the from→to direction to avoid duplicates from undirected storage.
			key := from + "|" + to
			if seen[key] {
				continue
			}
			seen[key] = true
			seen[to+"|"+from] = true
			out = append(out, Edge{From: from, To: to, Weight: w})
		}
	}
	return out
}

// ExportForAnalysis returns all nodes and directed edges for use with PageRank, risk, etc.
func (g *Graph) ExportForAnalysis() ([]Node, []Edge) {
	nodes := make([]Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		nodes = append(nodes, *n)
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
	for nodeID, commID := range membership {
		communityMap[commID] = append(communityMap[commID], nodeID)
	}

	communities := make([]Community, 0, len(communityMap))
	for id, nodes := range communityMap {
		sort.Strings(nodes)
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
	sort.Slice(communities, func(i, j int) bool {
		return communities[i].NodeCount > communities[j].NodeCount
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

// louvain assigns each node to a community using the Louvain method.
func louvain(g *Graph) map[string]int {
	// Initialize: each node in its own community.
	membership := make(map[string]int, len(g.neighbors))
	commID := 0
	nodeList := make([]string, 0, len(g.neighbors))
	for id := range g.neighbors {
		membership[id] = commID
		nodeList = append(nodeList, id)
		commID++
	}

	m2 := g.totalWeight * 2 // 2*m for undirected
	if m2 == 0 {
		return membership
	}

	// Precompute node strengths (weighted degree in undirected graph).
	strength := make(map[string]float64, len(g.neighbors))
	for id, nbrs := range g.neighbors {
		s := 0.0
		for _, w := range nbrs {
			s += w
		}
		strength[id] = s
	}

	// Iterate until no improvement.
	for pass := 0; pass < 50; pass++ {
		improved := false
		for _, nodeID := range nodeList {
			currentComm := membership[nodeID]
			ki := strength[nodeID]

			// Compute neighbor communities and their edge weights.
			commWeights := make(map[int]float64)
			for nbr, w := range g.neighbors[nodeID] {
				commWeights[membership[nbr]] += w
			}

			// Compute sigma_tot for current community (excluding this node).
			sigmaTot := make(map[int]float64)
			for nid, comm := range membership {
				sigmaTot[comm] += strength[nid]
			}

			bestComm := currentComm
			bestDelta := 0.0

			for comm, kiIn := range commWeights {
				if comm == currentComm {
					continue
				}
				// Modularity gain of moving node to comm.
				sigmaOld := sigmaTot[currentComm] - ki
				sigmaNew := sigmaTot[comm]
				kiInOld := commWeights[currentComm]

				deltaRemove := -2.0 * (kiInOld - (sigmaOld*ki)/m2) / m2
				deltaAdd := 2.0 * (kiIn - (sigmaNew*ki)/m2) / m2
				delta := deltaRemove + deltaAdd

				if delta > bestDelta {
					bestDelta = delta
					bestComm = comm
				}
			}

			if bestComm != currentComm && bestDelta > 1e-10 {
				membership[nodeID] = bestComm
				improved = true
			}
		}
		if !improved {
			break
		}
	}

	// Compact community IDs to 0..N-1.
	seen := make(map[int]int)
	nextID := 0
	for _, comm := range membership {
		if _, ok := seen[comm]; !ok {
			seen[comm] = nextID
			nextID++
		}
	}
	for nodeID, comm := range membership {
		membership[nodeID] = seen[comm]
	}

	return membership
}

// computeModularity calculates the Newman-Girvan modularity Q.
func computeModularity(g *Graph, membership map[string]int) float64 {
	m2 := g.totalWeight * 2
	if m2 == 0 {
		return 0
	}

	strength := make(map[string]float64)
	for id, nbrs := range g.neighbors {
		for _, w := range nbrs {
			strength[id] += w
		}
	}

	q := 0.0
	for nodeA, nbrs := range g.neighbors {
		for nodeB, w := range nbrs {
			if membership[nodeA] != membership[nodeB] {
				continue
			}
			expected := strength[nodeA] * strength[nodeB] / m2
			q += w - expected
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

	bestDir := ""
	bestCount := 0
	for dir, count := range dirCount {
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
		for nbr, w := range g.neighbors[id] {
			if nodeSet[nbr] {
				internalEdges += w
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
	for id, n := range g.nodes {
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
	sort.Slice(gods, func(i, j int) bool {
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
	seen := make(map[string]bool)

	for from, nbrs := range g.neighbors {
		for to, w := range nbrs {
			key := from + "|" + to
			rev := to + "|" + from
			if seen[key] || seen[rev] {
				continue
			}
			seen[key] = true

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
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].Weight > edges[j].Weight
	})
	if len(edges) > max {
		edges = edges[:max]
	}
	return edges
}
