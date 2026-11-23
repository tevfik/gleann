// Package server — graph API handlers for code graph queries.
// These endpoints allow external tools (e.g. external agents or IDE extensions) to query the
// code graph over HTTP. A format-2 index serves them from its SQLite graph in every build;
// a format-1 index has no code graph.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/pkg/gleann"
)

// GraphQueryRequest is the request body for POST /api/graph/{name}/query.
type GraphQueryRequest struct {
	// Query is one of the predefined query types:
	//   "callees", "callers", "symbols_in_file", "impact", "toc"
	// ("cypher" is answered with 400 for a format-2 index: raw Cypher was removed)
	Query    string `json:"query"`
	Symbol   string `json:"symbol,omitempty"`    // FQN for callees/callers/impact/toc
	File     string `json:"file,omitempty"`      // file path for symbols_in_file/toc
	Cypher   string `json:"cypher,omitempty"`    // removed: raw Cypher is answered with 400
	MaxDepth int    `json:"max_depth,omitempty"` // max traversal depth for impact (default 5)
}

// GraphQueryResponse is the response for graph queries. Notice is the optional
// non-Go/tree-sitter advisory (see indexing.NonGoSymbolNotice); it is omitted
// for Go symbols and in a treesitter build, so their JSON is unchanged.
type GraphQueryResponse struct {
	Results []GraphNode `json:"results"`
	Count   int         `json:"count"`
	QueryMs int64       `json:"query_ms"`
	Notice  string      `json:"notice,omitempty"`
}

// GraphNode is a generic graph result item.
type GraphNode struct {
	FQN  string `json:"fqn"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// GraphIndexRequest is the request body for POST /api/graph/{name}/index.
type GraphIndexRequest struct {
	DocsDir string `json:"docs_dir"`
	Module  string `json:"module,omitempty"` // accepted and ignored: the Go module is read from go.mod
}

// GraphStatsResponse is returned by GET /api/graph/{name}. file_count is the
// number of files that declare symbols; DBPath is the index database
// (<name>/<name>.db) of a format-2 index, the <name>_graph sidecar otherwise.
type GraphStatsResponse struct {
	Name       string `json:"name"`
	DBPath     string `json:"db_path"`
	Available  bool   `json:"available"`
	FileCount  int    `json:"file_count,omitempty"`
	SymCount   int    `json:"symbol_count,omitempty"`
	CallsCount int    `json:"calls_count,omitempty"`
	DeclCount  int    `json:"declares_count,omitempty"`
}

// ImpactResponse is the response for impact analysis queries. TestCallers lists
// the callers that live in test files: they are reported for context but stay
// outside the blast radius, so TotalAffected counts only DirectCallers and
// TransitiveCallers. Notice carries the optional non-Go/tree-sitter advisory
// (omitted when empty).
type ImpactResponse struct {
	Symbol            string   `json:"symbol"`
	DirectCallers     []string `json:"direct_callers"`
	TransitiveCallers []string `json:"transitive_callers"`
	TestCallers       []string `json:"test_callers"`
	AffectedFiles     []string `json:"affected_files"`
	Depth             int      `json:"depth"`
	TotalAffected     int      `json:"total_affected"`
	QueryMs           int64    `json:"query_ms"`
	Notice            string   `json:"notice,omitempty"`
}

// graphDBPool caches open code graph handles per index name (the SQLite graph
// of a format-2 index). The pool reuses them; runBuild and index
// removal close them so the files can be rewritten or deleted.
type graphDBPool struct {
	mu  sync.RWMutex
	dbs map[string]graphDBHandle
	dir string // base index directory
}

// graphDBHandle holds a cached graph database reference. storeHandle
// (graph_sqlite.go) serves format-2 indexes in every build.
type graphDBHandle interface {
	Callees(fqn string) ([]GraphNode, error)
	Callers(fqn string) ([]GraphNode, error)
	SymbolsInFile(path string) ([]GraphNode, error)
	Impact(fqn string, maxDepth int) (*ImpactResponse, error)
	DocumentTOC(path string) (*gleann.DocumentTOCInfo, error)
	ListDocuments() ([]gleann.DocumentTOCInfo, error)
	RawCypher(cypher string) ([]map[string]any, error)
	FileCount() (int, error)
	SymbolCount() (int, error)
	EdgeCount(relType string) (int, error)
	Close()
}

func newGraphDBPool(indexDir string) *graphDBPool {
	return &graphDBPool{
		dbs: make(map[string]graphDBHandle),
		dir: indexDir,
	}
}

func (p *graphDBPool) get(name string) (graphDBHandle, error) {
	p.mu.RLock()
	h, ok := p.dbs[name]
	p.mu.RUnlock()
	if ok {
		return h, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after write lock.
	if h, ok := p.dbs[name]; ok {
		return h, nil
	}

	if err := gleann.ValidateName(name); err != nil {
		return nil, err
	}
	h, err := openStoreHandle(p.dir, name)
	if err != nil {
		return nil, err
	}
	if h == nil {
		if err := gleann.CheckStoreFormat(p.dir, name); err != nil {
			return nil, fmt.Errorf("%w: %w", errNoCodeGraph, err)
		}
		return nil, fmt.Errorf("%w: %v (or POST /api/graph/%s/index)", errNoCodeGraph, gleann.NoCodeGraphError(p.dir, name), name)
	}
	p.dbs[name] = h
	return h, nil
}

// graphDBPath is where the code graph of an index lives: the index database
// <name>/<name>.db.
func graphDBPath(indexDir, name string) string {
	return filepath.Join(indexDir, name, name+".db")
}

// writeGraphOpenError reports a failed graph open: a missing or unreadable
// graph index.
func writeGraphOpenError(w http.ResponseWriter, name string, err error) {
	writeError(w, http.StatusNotFound, fmt.Sprintf("graph index %q not found: %v", name, err))
}

func (p *graphDBPool) close(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if h, ok := p.dbs[name]; ok {
		h.Close()
		delete(p.dbs, name)
	}
}

func (p *graphDBPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, h := range p.dbs {
		h.Close()
	}
	p.dbs = make(map[string]graphDBHandle)
}

// ── HTTP Handlers ────────────────────────────────────────────────────────

// handleGraphQuery handles POST /api/graph/{name}/query.
func (s *Server) handleGraphQuery(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var req GraphQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if s.graphPool == nil {
		writeError(w, http.StatusServiceUnavailable, "graph database not available")
		return
	}

	db, err := s.graphPool.get(name)
	if err != nil {
		writeGraphOpenError(w, name, err)
		return
	}

	start := time.Now()
	var results []GraphNode
	var notice string

	switch req.Query {
	case "callees":
		if req.Symbol == "" {
			writeError(w, http.StatusBadRequest, "symbol is required for callees query")
			return
		}
		results, err = db.Callees(req.Symbol)
		notice = graphQueryNotice(db, req.Symbol, gleann.DirectionCallees)

	case "callers":
		if req.Symbol == "" {
			writeError(w, http.StatusBadRequest, "symbol is required for callers query")
			return
		}
		results, err = db.Callers(req.Symbol)
		notice = graphQueryNotice(db, req.Symbol, gleann.DirectionCallers)

	case "symbols_in_file":
		if req.File == "" {
			writeError(w, http.StatusBadRequest, "file is required for symbols_in_file query")
			return
		}
		results, err = db.SymbolsInFile(req.File)

	case "impact":
		if req.Symbol == "" {
			writeError(w, http.StatusBadRequest, "symbol is required for impact query")
			return
		}
		maxDepth := req.MaxDepth
		if maxDepth <= 0 {
			maxDepth = 5
		}
		impact, impactErr := db.Impact(req.Symbol, maxDepth)
		if impactErr != nil {
			writeError(w, http.StatusInternalServerError, "impact analysis failed: "+impactErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, ImpactResponse{
			Symbol:            impact.Symbol,
			DirectCallers:     impact.DirectCallers,
			TransitiveCallers: impact.TransitiveCallers,
			TestCallers:       impact.TestCallers,
			AffectedFiles:     impact.AffectedFiles,
			Depth:             impact.Depth,
			TotalAffected:     len(impact.DirectCallers) + len(impact.TransitiveCallers),
			QueryMs:           time.Since(start).Milliseconds(),
			Notice:            graphQueryNotice(db, req.Symbol, gleann.DirectionCallers),
		})
		return

	case "cypher":
		if req.Cypher == "" {
			writeError(w, http.StatusBadRequest, "cypher field is required for cypher query")
			return
		}
		rows, qErr := db.RawCypher(req.Cypher)
		if errors.Is(qErr, errCypherRemoved) {
			writeError(w, http.StatusBadRequest, qErr.Error())
			return
		}
		if qErr != nil {
			writeError(w, http.StatusInternalServerError, "cypher failed: "+qErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"rows":     rows,
			"count":    len(rows),
			"query_ms": time.Since(start).Milliseconds(),
		})
		return

	case "toc":
		path := req.File
		if path == "" {
			path = req.Symbol
		}
		if path == "" {
			writeError(w, http.StatusBadRequest, "file or symbol is required for toc query")
			return
		}
		toc, tocErr := db.DocumentTOC(path)
		if tocErr != nil {
			if isDocumentNotFound(tocErr) {
				// A code file (or any path that is not an indexed document)
				// has no TOC: a missing resource, not a server fault.
				writeError(w, http.StatusNotFound, fmt.Sprintf("no document %q in graph %q: %v", path, name, tocErr))
				return
			}
			writeError(w, http.StatusInternalServerError, "document toc failed: "+tocErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"toc":      toc,
			"query_ms": time.Since(start).Milliseconds(),
		})
		return

	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown query type %q (use callees, callers, symbols_in_file, impact, toc)", req.Query))
		return
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, GraphQueryResponse{
		Results: results,
		Count:   len(results),
		QueryMs: time.Since(start).Milliseconds(),
		Notice:  notice,
	})
}

// graphQueryNotice returns the non-Go/tree-sitter advisory for a symbol-based
// REST query. Handles without a GraphDB() view (test doubles) and a treesitter
// build degrade to the empty string, which omitempty keeps out of the JSON.
func graphQueryNotice(db graphDBHandle, symbol string, dir int) string {
	if symbol == "" {
		return ""
	}
	h, ok := db.(interface{ GraphDB() gleann.GraphDB })
	if !ok {
		return ""
	}
	return indexing.NonGoSymbolNotice(symbol, h.GraphDB(), dir)
}

// handleGraphStats handles GET /api/graph/{name}.
func (s *Server) handleGraphStats(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	if s.graphPool == nil {
		writeJSON(w, http.StatusOK, GraphStatsResponse{
			Name:      name,
			Available: false,
		})
		return
	}

	db, err := s.graphPool.get(name)
	if err != nil {
		// An index without a code graph is a 404.
		writeGraphOpenError(w, name, err)
		return
	}

	fc, _ := db.FileCount()
	sc, _ := db.SymbolCount()
	cc, _ := db.EdgeCount("CALLS")
	dc, _ := db.EdgeCount("DECLARES")

	writeJSON(w, http.StatusOK, GraphStatsResponse{
		Name:       name,
		DBPath:     graphDBPath(s.cfg().IndexDir, name),
		Available:  true,
		FileCount:  fc,
		SymCount:   sc,
		CallsCount: cc,
		DeclCount:  dc,
	})
}

// handleGraphIndex handles POST /api/graph/{name}/index.
// Triggers AST indexing for a directory into the index's code graph (the
// SQLite tables of a format-2 index; a format-1 index answers 409).
func (s *Server) handleGraphIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var req GraphIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.DocsDir == "" {
		writeError(w, http.StatusBadRequest, "docs_dir is required")
		return
	}

	docsDir, ok := s.containClientPath(w, req.DocsDir)
	if !ok {
		return
	}

	// Close existing cached DB so indexer can get exclusive access.
	if s.graphPool != nil {
		s.graphPool.close(name)
	}

	start := time.Now()
	err := runGraphIndex(name, docsDir, s.cfg().IndexDir)
	if errors.Is(err, errNoCodeGraph) || errors.Is(err, gleann.ErrOldStoreFormat) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "indexing failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"name":     name,
		"docs_dir": req.DocsDir,
		"buildMs":  time.Since(start).Milliseconds(),
	})
}

// handleDocumentTOC handles GET /api/graph/{name}/toc?path=...
func (s *Server) handleDocumentTOC(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	docPath := r.URL.Query().Get("path")
	if docPath == "" {
		docPath = r.URL.Query().Get("vpath")
	}

	if s.graphPool == nil {
		writeError(w, http.StatusServiceUnavailable, "graph database not available")
		return
	}

	db, err := s.graphPool.get(name)
	if err != nil {
		writeGraphOpenError(w, name, err)
		return
	}

	if docPath == "" {
		// If no path specified, return all documents list
		docs, err := db.ListDocuments()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list documents: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"documents": docs,
			"count":     len(docs),
		})
		return
	}

	toc, err := db.DocumentTOC(docPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("document %q not found in graph %q: %v", docPath, name, err))
		return
	}

	writeJSON(w, http.StatusOK, toc)
}

// handleListDocuments handles GET /api/graph/{name}/documents.
func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	if s.graphPool == nil {
		writeError(w, http.StatusServiceUnavailable, "graph database not available")
		return
	}

	db, err := s.graphPool.get(name)
	if err != nil {
		writeGraphOpenError(w, name, err)
		return
	}

	docs, err := db.ListDocuments()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list documents: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"documents": docs,
		"count":     len(docs),
	})
}
