// Package server — Memory Engine REST API handlers.
//
// These endpoints expose gleann's generic Knowledge Graph to external AI
// agents (e.g. Cursor, Claude) over HTTP.  An agent can inject entities and
// relationships, delete them, and traverse sub-graphs without coupling to
// gleann's internal RAG pipeline.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// ── Request / Response types ──────────────────────────────────────────────────

// DeleteEdgeRequest is the body for DELETE /api/memory/edges.
type DeleteEdgeRequest struct {
	From         string `json:"from"`
	To           string `json:"to"`
	RelationType string `json:"relation_type"`
}

// TraverseRequest is the body for POST /api/memory/traverse.
type TraverseRequest struct {
	StartID string `json:"start_id"`
	Depth   int    `json:"depth"`
}

// TraverseResponse is the response for POST /api/memory/traverse.
type TraverseResponse struct {
	Nodes []gleann.MemoryGraphNode `json:"nodes"`
	Edges []gleann.MemoryGraphEdge `json:"edges"`
	Count int                      `json:"count"`
}

// ── Memory DB pool ────────────────────────────────────────────────────────────

// SyncerFactory creates a VectorSyncer for a given index name.
// Return nil to disable vector synchronisation for that index.
type SyncerFactory func(name string) gleann.VectorSyncer

// memoryPool caches open MemoryService instances per logical index name,
// backed by the global SQLite memory database (RFC 003 section 3.5).
type memoryPool struct {
	mu            sync.RWMutex
	handles       map[string]gleann.MemoryService
	store         *memory.Store
	dir           string
	syncerFactory SyncerFactory // optional; nil disables vector sync
	warned        map[string]bool
}

func newMemoryPool(indexDir string) *memoryPool {
	return &memoryPool{
		handles: make(map[string]gleann.MemoryService),
		dir:     indexDir,
		warned:  make(map[string]bool),
	}
}

// get returns a cached MemoryService for name, opening it on first access.
func (p *memoryPool) get(name string) (gleann.MemoryService, error) {
	p.mu.RLock()
	h, ok := p.handles[name]
	p.mu.RUnlock()
	if ok {
		return h, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock.
	if h, ok := p.handles[name]; ok {
		return h, nil
	}

	if err := gleann.ValidateName(name); err != nil {
		return nil, err
	}

	if p.store == nil {
		s, err := memory.OpenStore(memory.DefaultStorePath())
		if err != nil {
			return nil, fmt.Errorf("open memory store: %w", err)
		}
		p.store = s
	}

	// Check if legacy Kùzu data exists and SQLite namespace is empty.
	kuzuPath := filepath.Join(p.dir, name+"_memory")
	if fi, err := os.Stat(kuzuPath); err == nil && fi.IsDir() {
		if !p.warned[name] {
			if empty, err := p.store.EntityNamespaceEmpty(name); err == nil && empty {
				p.warned[name] = true
				slog.Warn("legacy Kùzu memory data detected; old entities need gleann index migrate in a kuzu build",
					"index", name, "path", kuzuPath)
			}
		}
	}

	var syncer gleann.VectorSyncer
	if p.syncerFactory != nil {
		syncer = p.syncerFactory(name)
	}
	entitySvc := p.store.EntityService(name, syncer)
	p.handles[name] = entitySvc
	return entitySvc, nil
}

// closeAll releases the memory store handle and clears cached services.
func (p *memoryPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store != nil {
		_ = p.store.Close()
		p.store = nil
	}
	p.handles = make(map[string]gleann.MemoryService)
}

// ── HTTP Handlers ─────────────────────────────────────────────────────────────

// handleMemoryInject handles POST /api/memory/{name}/inject.
//
// Accepts a [gleann.GraphInjectionPayload] and upserts all nodes and edges in
// a single KuzuDB transaction.  The operation is idempotent.
func (s *Server) handleMemoryInject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var payload gleann.GraphInjectionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	svc, err := s.memoryPool.get(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open memory store %q: %v", name, err))
		return
	}

	if err := svc.InjectEntities(r.Context(), payload); err != nil {
		writeError(w, http.StatusInternalServerError, "inject entities: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"nodes_sent": len(payload.Nodes),
		"edges_sent": len(payload.Edges),
	})
}

// handleMemoryDeleteNode handles DELETE /api/memory/{name}/nodes/{id}.
//
// Removes the Entity identified by id together with all of its incident edges.
func (s *Server) handleMemoryDeleteNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id := r.PathValue("id")
	if name == "" || id == "" {
		writeError(w, http.StatusBadRequest, "index name and node id are required")
		return
	}
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	svc, err := s.memoryPool.get(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open memory store %q: %v", name, err))
		return
	}

	if err := svc.DeleteEntity(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete entity: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "deleted_id": id})
}

// handleMemoryDeleteEdge handles DELETE /api/memory/{name}/edges.
//
// Removes a single RELATES_TO relationship identified by (from, to, relation_type).
func (s *Server) handleMemoryDeleteEdge(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var req DeleteEdgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.From == "" || req.To == "" || req.RelationType == "" {
		writeError(w, http.StatusBadRequest, "from, to, and relation_type are required")
		return
	}

	svc, err := s.memoryPool.get(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open memory store %q: %v", name, err))
		return
	}

	if err := svc.DeleteEdge(r.Context(), req.From, req.To, req.RelationType); err != nil {
		writeError(w, http.StatusInternalServerError, "delete edge: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleMemoryTraverse handles POST /api/memory/{name}/traverse.
//
// Walks the knowledge graph from start_id up to depth hops and returns all
// reachable nodes and the edges connecting them.
func (s *Server) handleMemoryTraverse(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var req TraverseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.StartID == "" {
		writeError(w, http.StatusBadRequest, "start_id is required")
		return
	}
	if req.Depth <= 0 {
		req.Depth = 1 // sensible default
	}

	svc, err := s.memoryPool.get(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open memory store %q: %v", name, err))
		return
	}

	nodes, edges, err := svc.Traverse(r.Context(), req.StartID, req.Depth)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "traverse: "+err.Error())
		return
	}

	// Ensure empty slices rather than null in JSON output.
	if nodes == nil {
		nodes = []gleann.MemoryGraphNode{}
	}
	if edges == nil {
		edges = []gleann.MemoryGraphEdge{}
	}

	resp := TraverseResponse{
		Nodes: nodes,
		Edges: edges,
		Count: len(nodes),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		_ = err // response already started
	}
}

// stopMemoryPool is called by Server.Stop to release memory DB connections.
func (s *Server) stopMemoryPool(ctx context.Context) {
	_ = ctx
	if s.memoryPool != nil {
		s.memoryPool.closeAll()
	}
}
