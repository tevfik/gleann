// Package server — graph API handlers for KuzuDB graph queries.
// These endpoints allow external tools (e.g. yaver-go) to query the
// code graph over HTTP instead of linking KuzuDB as an embedded library.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// GraphQueryRequest is the request body for POST /api/graph/{name}/query.
type GraphQueryRequest struct {
	// Query is one of the predefined query types:
	//   "callees", "callers", "symbols_in_file", "cypher"
	Query  string `json:"query"`
	Symbol string `json:"symbol,omitempty"` // FQN for callees/callers
	File   string `json:"file,omitempty"`   // file path for symbols_in_file
	Cypher string `json:"cypher,omitempty"` // raw Cypher for advanced queries
}

// GraphQueryResponse is the response for graph queries.
type GraphQueryResponse struct {
	Results []GraphNode `json:"results"`
	Count   int         `json:"count"`
	QueryMs int64       `json:"query_ms"`
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
	Module  string `json:"module,omitempty"` // Go module name, e.g. "github.com/foo/bar"
}

// GraphStatsResponse is returned by GET /api/graph/{name}.
type GraphStatsResponse struct {
	Name       string `json:"name"`
	DBPath     string `json:"db_path"`
	Available  bool   `json:"available"`
	FileCount  int    `json:"file_count,omitempty"`
	SymCount   int    `json:"symbol_count,omitempty"`
	CallsCount int    `json:"calls_count,omitempty"`
	DeclCount  int    `json:"declares_count,omitempty"`
}

// graphDBPool caches open KuzuDB connections per index name.
// KuzuDB is an embedded database — only one process can open a given
// directory at a time. The pool ensures we reuse connections.
type graphDBPool struct {
	mu  sync.RWMutex
	dbs map[string]graphDBHandle
	dir string // base index directory
}

// graphDBHandle holds a cached graph database reference.
// We use an interface so the CGo-dependent concrete type stays in
// a build-tagged file.
type graphDBHandle interface {
	Callees(fqn string) ([]GraphNode, error)
	Callers(fqn string) ([]GraphNode, error)
	SymbolsInFile(path string) ([]GraphNode, error)
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

	dbPath := filepath.Join(p.dir, name+"_graph")
	h, err := openGraphDB(dbPath) // build-tagged function
	if err != nil {
		return nil, err
	}
	p.dbs[name] = h
	return h, nil
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
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
		return
	}

	var req GraphQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if s.graphPool == nil {
		writeError(w, http.StatusServiceUnavailable, "graph database not available (build with -tags treesitter)")
		return
	}

	db, err := s.graphPool.get(name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("graph index %q not found: %v", name, err))
		return
	}

	start := time.Now()
	var results []GraphNode

	switch req.Query {
	case "callees":
		if req.Symbol == "" {
			writeError(w, http.StatusBadRequest, "symbol is required for callees query")
			return
		}
		results, err = db.Callees(req.Symbol)

	case "callers":
		if req.Symbol == "" {
			writeError(w, http.StatusBadRequest, "symbol is required for callers query")
			return
		}
		results, err = db.Callers(req.Symbol)

	case "symbols_in_file":
		if req.File == "" {
			writeError(w, http.StatusBadRequest, "file is required for symbols_in_file query")
			return
		}
		results, err = db.SymbolsInFile(req.File)

	case "cypher":
		if req.Cypher == "" {
			writeError(w, http.StatusBadRequest, "cypher field is required for cypher query")
			return
		}
		rows, qErr := db.RawCypher(req.Cypher)
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

	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown query type %q (use callees, callers, symbols_in_file)", req.Query))
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
	})
}

// handleGraphStats handles GET /api/graph/{name}.
func (s *Server) handleGraphStats(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
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
		writeJSON(w, http.StatusOK, GraphStatsResponse{
			Name:      name,
			DBPath:    filepath.Join(s.config.IndexDir, name+"_graph"),
			Available: false,
		})
		return
	}

	fc, _ := db.FileCount()
	sc, _ := db.SymbolCount()
	cc, _ := db.EdgeCount("CALLS")
	dc, _ := db.EdgeCount("DECLARES")

	writeJSON(w, http.StatusOK, GraphStatsResponse{
		Name:       name,
		DBPath:     filepath.Join(s.config.IndexDir, name+"_graph"),
		Available:  true,
		FileCount:  fc,
		SymCount:   sc,
		CallsCount: cc,
		DeclCount:  dc,
	})
}

// handleGraphIndex handles POST /api/graph/{name}/index.
// Triggers AST indexing for a directory into the KuzuDB graph.
func (s *Server) handleGraphIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
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

	// Close existing cached DB so indexer can get exclusive access.
	if s.graphPool != nil {
		s.graphPool.close(name)
	}

	start := time.Now()
	err := runGraphIndex(name, req.DocsDir, s.config.IndexDir, req.Module)
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
