//go:build treesitter

package server

import (
	"fmt"

	"github.com/tevfik/gleann/internal/graph/indexer"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// kuzuHandle wraps kuzu.DB to satisfy graphDBHandle interface.
type kuzuHandle struct {
	db *kgraph.DB
}

func openGraphDB(dbPath string) (graphDBHandle, error) {
	db, err := kgraph.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open kuzu db %s: %w", dbPath, err)
	}
	return &kuzuHandle{db: db}, nil
}

func (h *kuzuHandle) Callees(fqn string) ([]GraphNode, error) {
	callees, err := h.db.Callees(fqn)
	if err != nil {
		return nil, err
	}
	return toGraphNodes(callees), nil
}

func (h *kuzuHandle) Callers(fqn string) ([]GraphNode, error) {
	callers, err := h.db.Callers(fqn)
	if err != nil {
		return nil, err
	}
	return toGraphNodes(callers), nil
}

func (h *kuzuHandle) SymbolsInFile(path string) ([]GraphNode, error) {
	syms, err := h.db.SymbolsInFile(path)
	if err != nil {
		return nil, err
	}
	return toGraphNodes(syms), nil
}

func (h *kuzuHandle) FileCount() (int, error) {
	res, err := h.db.Conn().Query("MATCH (f:CodeFile) RETURN count(f) AS cnt")
	if err != nil {
		return 0, err
	}
	defer res.Close()
	if res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return 0, err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return 0, err
		}
		if v, ok := m["cnt"].(int64); ok {
			return int(v), nil
		}
	}
	return 0, nil
}

func (h *kuzuHandle) SymbolCount() (int, error) {
	res, err := h.db.Conn().Query("MATCH (s:Symbol) RETURN count(s) AS cnt")
	if err != nil {
		return 0, err
	}
	defer res.Close()
	if res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return 0, err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return 0, err
		}
		if v, ok := m["cnt"].(int64); ok {
			return int(v), nil
		}
	}
	return 0, nil
}

func (h *kuzuHandle) Close() {
	h.db.Close()
}

func toGraphNodes(callees []gleann.Callee) []GraphNode {
	nodes := make([]GraphNode, len(callees))
	for i, c := range callees {
		nodes[i] = GraphNode{FQN: c.FQN, Name: c.Name, Kind: c.Kind}
	}
	return nodes
}

// runGraphIndex runs the AST indexer for a directory.
func runGraphIndex(name, docsDir, indexDir, module string) error {
	dbPath := indexDir + "/" + name + "_graph"
	db, err := kgraph.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open kuzu db: %w", err)
	}
	defer db.Close()

	if module == "" {
		module = name
	}

	idx := indexer.New(db, module, docsDir)
	return idx.IndexDir(docsDir)
}
