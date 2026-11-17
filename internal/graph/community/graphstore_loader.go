package community

import (
	"fmt"

	"github.com/tevfik/gleann/internal/graphstore"
)

// LoadGraphFromStore loads the code graph from the format-2 SQLite tables
// (symbols and edges, RFC 003 section 3.7) into the same in-memory Graph
// value LoadGraphFromKuzu produces, so louvain.go and pagerank.go run on
// either backend unchanged. The weight scheme mirrors LoadGraphFromKuzu:
// CALLS 1.0, IMPLEMENTS 0.8, and the DECLARES relation (folded into
// symbols.file, section 3.4) becomes a file→symbol edge at 0.3, with a
// "file" node added for each declaring file. REFERENCES edges and external
// targets (which have no symbols row) are ignored, as Kùzu's MATCH between
// two Symbol nodes ignored them.
func LoadGraphFromStore(g *graphstore.SQLite) (*Graph, error) {
	graph := NewGraph()

	syms, err := g.AllSymbols()
	if err != nil {
		return nil, fmt.Errorf("load symbols: %w", err)
	}
	for _, s := range syms {
		graph.AddNode(Node{ID: s.FQN, Name: s.Name, Kind: s.Kind, File: s.File})
	}

	edges, err := g.AllEdges()
	if err != nil {
		return nil, fmt.Errorf("load edges: %w", err)
	}
	for _, e := range edges {
		if e.DstExternal {
			continue
		}
		switch e.Kind {
		case "CALLS":
			graph.AddEdge(e.Src, e.Dst, 1.0)
		case "IMPLEMENTS":
			graph.AddEdge(e.Src, e.Dst, 0.8)
		}
	}

	// DECLARES: the symbols.file anchor replaces the relation; each declaring
	// file becomes a node and an edge to its symbol at the lower weight.
	for _, s := range syms {
		if s.File == "" {
			continue
		}
		if graph.nodes[s.File] == nil {
			graph.AddNode(Node{ID: s.File, Name: s.File, Kind: "file"})
		}
		graph.AddEdge(s.File, s.FQN, 0.3)
	}

	return graph, nil
}
