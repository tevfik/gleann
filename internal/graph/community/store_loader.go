package community

import (
	"fmt"

	"github.com/tevfik/gleann/internal/graphstore"
)

// LoadCallGraphFromStore loads the CALLS-only view of the format-2 code graph:
// every declaration as a node and every internal CALLS edge at weight 1.0, no
// file nodes and no DECLARES or IMPLEMENTS edges. It is the graph `graph viz`,
// `graph export`, `graph map`, `graph risk` and the repo-map and risk tools
// analyse, the shape the Kùzu-era loadGraphForViz produced, whereas
// LoadGraphFromStore adds the file nodes community detection works on.
func LoadCallGraphFromStore(g *graphstore.SQLite) (*Graph, error) {
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
		if e.DstExternal || e.Kind != "CALLS" {
			continue
		}
		graph.AddEdge(e.Src, e.Dst, 1.0)
	}
	return graph, nil
}

// FromStore loads the code graph of a format-2 index and runs community
// detection on it (the SQLite counterpart of FromKuzu).
func FromStore(g *graphstore.SQLite, godNodeThreshold, maxSurprising int) (*Result, error) {
	graph, err := LoadGraphFromStore(g)
	if err != nil {
		return nil, err
	}
	return Detect(graph, godNodeThreshold, maxSurprising)
}
