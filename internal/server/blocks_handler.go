// Package server — Memory Block REST API handlers.
//
// These endpoints expose gleann's hierarchical BBolt memory (pkg/memory) over
// HTTP, enabling external AI agents to read, write, search, and clear memory
// blocks across the three tiers (short, medium, long).
//
// Routes (no build tag — pure Go, no CGo dependency):
//
//	GET    /api/blocks             list blocks (tier=short|medium|long optional)
//	POST   /api/blocks             add a memory block
//	DELETE /api/blocks             clear blocks (tier= optional, omit = all)
//	DELETE /api/blocks/{id}        forget a specific block
//	GET    /api/blocks/search?q=   full-text search
//	GET    /api/blocks/context     compiled <memory_context> window
//	GET    /api/blocks/stats       storage statistics
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

// blockAddRequest is the body for POST /api/blocks.
type blockAddRequest struct {
	Content   string            `json:"content"`
	Tier      string            `json:"tier,omitempty"`   // "short" | "medium" | "long" (default: "long")
	Label     string            `json:"label,omitempty"`  // semantic label
	Source    string            `json:"source,omitempty"` // origin tag
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	ExpiresIn   string            `json:"expires_in,omitempty"`   // e.g. "24h", "7d"
	CharLimit   int               `json:"char_limit,omitempty"`   // max characters (0 = use default)
	Scope       string            `json:"scope,omitempty"`        // isolation scope (e.g. conversation ID)
	Repo        string            `json:"repo,omitempty"`         // provenance repository identifier
	Paths       []string          `json:"paths,omitempty"`        // provenance file paths
	Symbols     []string          `json:"symbols,omitempty"`      // provenance symbol names/FQNs
	Commit      string            `json:"commit,omitempty"`       // provenance commit hash
	Suspect     bool              `json:"suspect,omitempty"`      // whether block is marked suspect/stale
	StaleReason string            `json:"stale_reason,omitempty"` // reason for staleness/suspicion
}

// ── lazy blockMem accessor ────────────────────────────────────────────────────

// blockManager returns the server's memory manager, opening it lazily on first
// call and caching it for the lifetime of the server.
func (s *Server) blockManager() (*memory.Manager, error) {
	s.mu.RLock()
	if s.blockMem != nil {
		m := s.blockMem
		s.mu.RUnlock()
		return m, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock.
	if s.blockMem != nil {
		return s.blockMem, nil
	}

	mgr, err := memory.DefaultManager()
	if err != nil {
		return nil, err
	}
	s.blockMem = mgr
	return mgr, nil
}

// ── GET /api/blocks ───────────────────────────────────────────────────────────

// handleListBlocks lists all memory blocks with an optional tier filter.
//
//	GET /api/blocks
//	GET /api/blocks?tier=short|medium|long
//	GET /api/blocks?scope=conversation_id
func (s *Server) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	tierStr := r.URL.Query().Get("tier")
	scope := r.URL.Query().Get("scope")

	var tier memory.Tier
	if tierStr != "" {
		t, err := memory.ParseTier(tierStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		tier = t
	}

	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	var blocks []memory.Block
	if scope != "" {
		blocks, err = mgr.ListScoped(scope, tier)
	} else {
		blocks, err = mgr.List(tier)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list blocks: "+err.Error())
		return
	}

	if blocks == nil {
		blocks = []memory.Block{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"blocks": blocks,
		"count":  len(blocks),
	})
}

// ── POST /api/blocks ──────────────────────────────────────────────────────────

// handleAddBlock stores a new memory block.
//
//	POST /api/blocks
//	Body: { "content": "...", "tier": "long", "label": "...", "tags": [...] }
func (s *Server) handleAddBlock(w http.ResponseWriter, r *http.Request) {
	var req blockAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	tier := memory.TierLong
	if req.Tier != "" {
		t, err := memory.ParseTier(req.Tier)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		tier = t
	}

	label := req.Label
	if label == "" {
		label = "api_memory"
	}
	source := req.Source
	if source == "" {
		source = "api"
	}

	block := &memory.Block{
		Tier:      tier,
		Label:     label,
		Content:   req.Content,
		Source:    source,
		Tags:      req.Tags,
		Metadata:    req.Metadata,
		CharLimit:   req.CharLimit,
		Scope:       req.Scope,
		Repo:        req.Repo,
		Paths:       req.Paths,
		Symbols:     req.Symbols,
		Commit:      req.Commit,
		Suspect:     req.Suspect,
		StaleReason: req.StaleReason,
	}

	// Parse optional expiry.
	if req.ExpiresIn != "" {
		d, err := time.ParseDuration(req.ExpiresIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid expires_in: "+err.Error())
			return
		}
		exp := time.Now().Add(d)
		block.ExpiresAt = &exp
	}

	saved, err := mgr.RememberBlock(block)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "add block: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, saved)
}

// ── DELETE /api/blocks/{id} ───────────────────────────────────────────────────

// handleDeleteBlock forgets a specific block by ID (or content query).
//
//	DELETE /api/blocks/{id}
func (s *Server) handleDeleteBlock(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "block id is required")
		return
	}

	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	n, err := mgr.Forget(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
}

// ── DELETE /api/blocks ────────────────────────────────────────────────────────

// handleClearBlocks removes all blocks from a tier (or all tiers).
//
//	DELETE /api/blocks            — clear all tiers
//	DELETE /api/blocks?tier=long  — clear only long-term
func (s *Server) handleClearBlocks(w http.ResponseWriter, r *http.Request) {
	tierStr := r.URL.Query().Get("tier")

	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	var n int
	if tierStr == "" {
		n, err = mgr.ClearAll()
	} else {
		t, parseErr := memory.ParseTier(tierStr)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, parseErr.Error())
			return
		}
		n, err = mgr.Clear(t)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "clear blocks: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": n})
}

// ── GET /api/blocks/search ────────────────────────────────────────────────────

// handleSearchBlocks performs a full-text search across all memory tiers.
//
//	GET /api/blocks/search?q=your+query
//	GET /api/blocks/search?q=your+query&scope=conversation_id
func (s *Server) handleSearchBlocks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}
	scope := r.URL.Query().Get("scope")

	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	var blocks []memory.Block
	if scope != "" {
		blocks, err = mgr.SearchScoped(scope, q)
	} else {
		blocks, err = mgr.Search(q)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search blocks: "+err.Error())
		return
	}

	if blocks == nil {
		blocks = []memory.Block{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"blocks": blocks,
		"count":  len(blocks),
		"query":  q,
	})
}

// ── GET /api/blocks/context ───────────────────────────────────────────────────

// handleBlockContext returns the compiled memory context window.
// Used by AI agents to retrieve the current memory state as LLM-injectable text.
//
//	GET /api/blocks/context                      — returns JSON with context + rendered XML
//	GET /api/blocks/context?format=xml           — returns raw <memory_context> XML
//	GET /api/blocks/context?scope=conversation_id — filter by scope
func (s *Server) handleBlockContext(w http.ResponseWriter, r *http.Request) {
	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	scope := r.URL.Query().Get("scope")

	var cw *memory.ContextWindow
	if scope != "" {
		cw, err = mgr.BuildScopedContext(scope)
	} else {
		cw, err = mgr.BuildContext()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build context: "+err.Error())
		return
	}

	if r.URL.Query().Get("format") == "xml" {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(cw.Render()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"context":  cw,
		"rendered": cw.Render(),
	})
}

// ── GET /api/blocks/stats ─────────────────────────────────────────────────────

// handleBlockStats returns storage statistics for the memory system.
//
//	GET /api/blocks/stats
func (s *Server) handleBlockStats(w http.ResponseWriter, r *http.Request) {
	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	stats, err := mgr.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get stats: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// ── POST /api/blocks/compact ──────────────────────────────────────────────────

// handleCompactBlocks compacts memory by purging expired and unreliable (< 0.2 validity) blocks.
//
//	POST /api/blocks/compact
//	POST /api/blocks/compact?min_validity=0.3
func (s *Server) handleCompactBlocks(w http.ResponseWriter, r *http.Request) {
	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	minVal := 0.2
	if q := r.URL.Query().Get("min_validity"); q != "" {
		if v, err := strconv.ParseFloat(q, 64); err == nil {
			minVal = v
		}
	}

	pruned, err := mgr.Compact(minVal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "compact memory: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pruned":       pruned,
		"min_validity": minVal,
	})
}

// closeBlockMem is called by Server.Stop to release the memory manager.
func (s *Server) closeBlockMem() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.blockMem != nil {
		_ = s.blockMem.Close()
		s.blockMem = nil
	}
}
