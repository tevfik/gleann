package server

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/pkg/gleann"
)

// The REST and A2A graph features run on the format-2 SQLite code graph in
// every build (RFC 004 A2). graphDBPool opens it through gleann.OpenGraphStore;
// a format-1 index has no code graph (RFC 004 A3).

// errCypherRemoved is returned by the raw-Cypher query of a format-2 graph.
var errCypherRemoved = errors.New("raw Cypher was removed; use callers/callees/symbols_in_file/impact")

// errNoCodeGraph marks an index that has no code graph (never built, or not
// an index at all): a 404, not a server fault.
var errNoCodeGraph = errors.New("no code graph")

// storeHandle is the graphDBHandle of a format-2 index.
type storeHandle struct {
	gs *gleann.GraphStore
	db gleann.GraphDB // view of gs; owns nothing
}

func newStoreHandle(gs *gleann.GraphStore) *storeHandle {
	return &storeHandle{gs: gs, db: gs.GraphDB()}
}

func (h *storeHandle) Callees(fqn string) ([]GraphNode, error) {
	r, err := h.db.Callees(fqn)
	return toGraphNodes(r), err
}

func (h *storeHandle) Callers(fqn string) ([]GraphNode, error) {
	r, err := h.db.Callers(fqn)
	return toGraphNodes(r), err
}

func (h *storeHandle) SymbolsInFile(path string) ([]GraphNode, error) {
	r, err := h.db.SymbolsInFile(path)
	return toGraphNodes(r), err
}

func (h *storeHandle) Impact(fqn string, maxDepth int) (*ImpactResponse, error) {
	res, err := h.db.Impact(fqn, maxDepth)
	if err != nil {
		return nil, err
	}
	nonNil := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	return &ImpactResponse{
		Symbol:            res.Symbol,
		DirectCallers:     nonNil(res.DirectCallers),
		TransitiveCallers: nonNil(res.TransitiveCallers),
		AffectedFiles:     nonNil(res.AffectedFiles),
		Depth:             res.Depth,
		TotalAffected:     len(res.DirectCallers) + len(res.TransitiveCallers),
	}, nil
}

func (h *storeHandle) DocumentTOC(path string) (*gleann.DocumentTOCInfo, error) {
	return h.db.DocumentTOC(path)
}

func (h *storeHandle) ListDocuments() ([]gleann.DocumentTOCInfo, error) {
	return h.db.ListDocuments()
}

func (h *storeHandle) RawCypher(string) ([]map[string]any, error) {
	return nil, errCypherRemoved
}

// FileCount is the number of files that declare symbols (what the CodeFile
// count of the former Kùzu graph reported), not the rows of the shared files table, which also lists
// documents and files without symbols.
func (h *storeHandle) FileCount() (int, error) { return h.gs.CodeFileCount() }

func (h *storeHandle) SymbolCount() (int, error) {
	st, err := h.gs.Stats()
	return st.Symbols, err
}

func (h *storeHandle) EdgeCount(relType string) (int, error) {
	st, err := h.gs.Stats()
	switch relType {
	case "CALLS":
		return st.CallEdges, err
	case "DECLARES":
		return st.DeclareEdges, err
	case "IMPLEMENTS":
		return st.ImplementsEdges, err
	}
	return 0, fmt.Errorf("unknown relation %q", relType)
}

// Communities, FullGraph and CallGraph feed the A2A analysis skills.
func (h *storeHandle) Communities(god, maxSurprising int) (*community.Result, error) {
	return community.FromStore(h.gs.SQLite, god, maxSurprising)
}

func (h *storeHandle) CallGraph() (*community.Graph, error) {
	return community.LoadCallGraphFromStore(h.gs.SQLite)
}

func (h *storeHandle) Close() { h.gs.Close() }

// toGraphNodes maps the GraphDB rows to the REST node shape.
func toGraphNodes(callees []gleann.Callee) []GraphNode {
	nodes := make([]GraphNode, len(callees))
	for i, c := range callees {
		nodes[i] = GraphNode{FQN: c.FQN, Name: c.Name, Kind: c.Kind}
	}
	return nodes
}

// openStoreHandle opens the format-2 graph of an index, or returns (nil, nil)
// when the index has none.
func openStoreHandle(indexDir, name string) (graphDBHandle, error) {
	gs, err := gleann.OpenGraphStore(indexDir, name)
	if err != nil || gs == nil {
		return nil, err
	}
	return newStoreHandle(gs), nil
}

// runGraphIndex builds the code graph of an index from a directory into its
// index database (every build); the Go module is read from go.mod. An index
// in the old store format is refused with the migration hint.
func runGraphIndex(name, docsDir, indexDir string) error {
	switch gleann.DetectStoreFormat(filepath.Join(indexDir, name, name)) {
	case gleann.StoreOld:
		return &gleann.OldFormatError{Name: name}
	case gleann.StoreNone:
		return fmt.Errorf("%w: index %q not found", errNoCodeGraph, name)
	}
	if err := indexing.BuildGraphIndexErr(indexing.GraphOptions{
		Name:     name,
		DocsDir:  docsDir,
		IndexDir: indexDir,
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	}); err != nil {
		return fmt.Errorf("build code graph: %w", err)
	}
	return nil
}

// isDocumentNotFound reports whether err says that no document matches the
// path (graphstore wraps ErrDocumentNotFound).
func isDocumentNotFound(err error) bool {
	return errors.Is(err, graphstore.ErrDocumentNotFound)
}

// analysisHandle is implemented by the handles that can feed the A2A
// analysis skills (communities, repo map, risk).
type analysisHandle interface {
	Communities(godNodeThreshold, maxSurprising int) (*community.Result, error)
	CallGraph() (*community.Graph, error)
}
