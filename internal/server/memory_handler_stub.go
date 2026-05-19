//go:build !treesitter

// Stub implementations of the Memory Engine HTTP handlers for builds that do
// not include the treesitter (CGO) tag.  All endpoints return 501 Not
// Implemented so that the binary remains fully functional for every other
// feature.

package server

import (
	"context"
	"net/http"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── Request / Response types ──────────────────────────────────────────────────
// These mirror the definitions in memory_handler.go (treesitter build).

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

// ── Stub memory pool ──────────────────────────────────────────────────────────

// SyncerFactory stub — not used in !treesitter builds.
type SyncerFactory func(name string) interface{}

type memoryPool struct {
	syncerFactory SyncerFactory // always nil in stub builds
}

func newMemoryPool(_ string) *memoryPool { return &memoryPool{} }
func (p *memoryPool) closeAll()          {}

// initMemorySyncer is a no-op in !treesitter builds.
func (s *Server) initMemorySyncer(_ gleann.Config, _ *embedding.Computer) {}

// ── Stub HTTP handlers ────────────────────────────────────────────────────────

func (s *Server) handleMemoryInject(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "Memory Engine requires CGO (build with -tags treesitter)")
}

func (s *Server) handleMemoryDeleteNode(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "Memory Engine requires CGO (build with -tags treesitter)")
}

func (s *Server) handleMemoryDeleteEdge(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "Memory Engine requires CGO (build with -tags treesitter)")
}

func (s *Server) handleMemoryTraverse(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "Memory Engine requires CGO (build with -tags treesitter)")
}

func (s *Server) stopMemoryPool(_ context.Context) {}
