//go:build treesitter

package community

import (
	"fmt"

	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
)

// FromKuzu loads the code graph from a KuzuDB instance and runs community detection.
// godNodeThreshold: minimum total degree for god node classification (default 5).
// maxSurprising: maximum surprising edges to return (default 20).
func FromKuzu(db *kgraph.DB, godNodeThreshold, maxSurprising int) (*Result, error) {
	g := NewGraph()

	// 1. Load all Symbol nodes.
	res, err := db.Conn().Query(`MATCH (s:Symbol) RETURN s.fqn AS fqn, s.name AS name, s.kind AS kind, s.file AS file`)
	if err != nil {
		return nil, fmt.Errorf("load symbols: %w", err)
	}
	defer res.Close()
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			continue
		}
		m, err := row.GetAsMap()
		if err != nil {
			continue
		}
		g.AddNode(Node{
			ID:   str(m["fqn"]),
			Name: str(m["name"]),
			Kind: str(m["kind"]),
			File: str(m["file"]),
		})
	}
	res.Close()

	// 2. Load all CALLS edges.
	res2, err := db.Conn().Query(`MATCH (a:Symbol)-[r:CALLS]->(b:Symbol) RETURN a.fqn AS from, b.fqn AS to`)
	if err != nil {
		return nil, fmt.Errorf("load calls: %w", err)
	}
	defer res2.Close()
	for res2.HasNext() {
		row, err := res2.Next()
		if err != nil {
			continue
		}
		m, err := row.GetAsMap()
		if err != nil {
			continue
		}
		g.AddEdge(str(m["from"]), str(m["to"]), 1.0)
	}
	res2.Close()

	// 3. Load DECLARES edges (file→symbol, lower weight).
	res3, err := db.Conn().Query(`MATCH (f:CodeFile)-[:DECLARES]->(s:Symbol) RETURN f.path AS from, s.fqn AS to`)
	if err != nil {
		return nil, fmt.Errorf("load declares: %w", err)
	}
	defer res3.Close()
	for res3.HasNext() {
		row, err := res3.Next()
		if err != nil {
			continue
		}
		m, err := row.GetAsMap()
		if err != nil {
			continue
		}
		filePath := str(m["from"])
		symFQN := str(m["to"])
		// Add file nodes on demand.
		if g.nodes[filePath] == nil {
			g.AddNode(Node{ID: filePath, Name: filePath, Kind: "file"})
		}
		g.AddEdge(filePath, symFQN, 0.3) // lower weight for structural edges
	}
	res3.Close()

	// 4. Load IMPLEMENTS edges.
	res4, err := db.Conn().Query(`MATCH (a:Symbol)-[:IMPLEMENTS]->(b:Symbol) RETURN a.fqn AS from, b.fqn AS to`)
	if err == nil {
		defer res4.Close()
		for res4.HasNext() {
			row, err := res4.Next()
			if err != nil {
				continue
			}
			m, err := row.GetAsMap()
			if err != nil {
				continue
			}
			g.AddEdge(str(m["from"]), str(m["to"]), 0.8)
		}
		res4.Close()
	}

	return Detect(g, godNodeThreshold, maxSurprising)
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
