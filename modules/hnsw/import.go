package hnsw

import "math"

// GraphTopology holds the raw graph structure for importing an
// externally-constructed HNSW graph (e.g. from FAISS).
type GraphTopology struct {
	NumNodes   int
	Dimensions int
	M          int
	MaxLevel   int
	EntryPoint int64

	// Per-node data (len = NumNodes).
	NodeLevels []int
	Embeddings [][]float32

	// Per-node-per-level neighbor lists.
	// Neighbors[i] has len = NodeLevels[i]+1; Neighbors[i][l] = IDs at level l.
	Neighbors [][][]int64
}

// NewGraphFromTopology constructs a Graph from an externally-built topology.
// The returned graph is ready for ConvertToCSR / PruneEmbeddings / Search.
func NewGraphFromTopology(topo GraphTopology, distFunc DistanceFunc) *Graph {
	if distFunc == nil {
		distFunc = L2DistanceSquared
	}
	m := topo.M
	if m <= 0 {
		m = 32
	}

	g := &Graph{
		nodes:          make(map[int64]*Node, topo.NumNodes),
		entryPoint:     topo.EntryPoint,
		maxLevel:       topo.MaxLevel,
		m:              m,
		mMax:           m,
		mMax0:          m * 2,
		efConstruction: 200,
		ml:             1.0 / math.Log(float64(m)),
		distFunc:       distFunc,
		useHeuristic:   true,
		dimensions:     topo.Dimensions,
		nodeCount:      int64(topo.NumNodes),
	}

	for i := 0; i < topo.NumNodes; i++ {
		id := int64(i)
		node := &Node{
			ID:        id,
			Level:     topo.NodeLevels[i],
			Neighbors: topo.Neighbors[i],
		}
		if i < len(topo.Embeddings) && len(topo.Embeddings[i]) > 0 {
			node.Vector = topo.Embeddings[i]
		}
		g.nodes[id] = node
	}

	return g
}
