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
//	POST   /api/blocks/failures    record a command failure and its fix
//	GET    /api/blocks/failures    list recorded command failures
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

// blockAddRequest is the body for POST /api/blocks.
type blockAddRequest struct {
	Content     string            `json:"content"`
	Tier        string            `json:"tier,omitempty"`   // "short" | "medium" | "long" (default: "long")
	Label       string            `json:"label,omitempty"`  // semantic label
	Source      string            `json:"source,omitempty"` // origin tag
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ExpiresIn   string            `json:"expires_in,omitempty"`   // e.g. "24h", "7d"
	CharLimit   int               `json:"char_limit,omitempty"`   // max characters (0 = use default)
	Scope       string            `json:"scope,omitempty"`        // isolation scope (e.g. conversation ID)
	Target      string            `json:"target,omitempty"`       // client target isolation (alias for scope)
	Repo        string            `json:"repo,omitempty"`         // provenance repository identifier
	Paths       []string          `json:"paths,omitempty"`        // provenance file paths
	Symbols     []string          `json:"symbols,omitempty"`      // provenance symbol names/FQNs
	Commit      string            `json:"commit,omitempty"`       // provenance commit hash
	Suspect     bool              `json:"suspect,omitempty"`      // whether block is marked suspect/stale
	StaleReason string            `json:"stale_reason,omitempty"` // reason for staleness/suspicion
}

// resolveTargetScope extracts scope from ?scope=, ?target=, or X-Gleann-Target
// header. All spellings are normalized (memory.NormalizeScope), so "repo-a",
// "repo-a/" and "/repo-a" name the same target. If both header and query target
// are specified and mismatch, it returns an error.
//
// The "all"/"*" scope sentinel may never bypass a restricted target: when a
// non-wildcard X-Gleann-Target or ?target= pins a repo, a sentinel ?scope= is
// rejected. A wildcard header/target counts as unrestricted. When unrestricted,
// the sentinel is mapped to the empty (all-scope) scope here, so header and
// query sentinels behave identically on every block route (v3 repair,
// finding 4).
func resolveTargetScope(r *http.Request) (string, error) {
	headerTarget := r.Header.Get("X-Gleann-Target")
	if headerTarget == "" {
		headerTarget = r.Header.Get("X-Gleann-Index")
	}
	headerTarget = memory.NormalizeScope(headerTarget)
	if memory.IsAllScopesSentinel(headerTarget) {
		headerTarget = "" // wildcard header = unrestricted
	}

	q := r.URL.Query()
	scope := memory.NormalizeScope(q.Get("scope"))
	queryTarget := memory.NormalizeScope(q.Get("target"))
	if scope == "" {
		scope = queryTarget
	}

	if memory.IsAllScopesSentinel(scope) &&
		(headerTarget != "" || (queryTarget != "" && !memory.IsAllScopesSentinel(queryTarget))) {
		return "", fmt.Errorf("access denied: scope %q cannot bypass restricted target (header %q, target %q)",
			q.Get("scope"), headerTarget, queryTarget)
	}

	if headerTarget != "" {
		if scope != "" && scope != headerTarget {
			return "", fmt.Errorf("access denied: target mismatch between header (%q) and query (%q)", headerTarget, scope)
		}
		if scope == "" {
			scope = headerTarget
		}
	}
	if memory.IsAllScopesSentinel(scope) {
		// Unrestricted (the guard above rejected every restricted case):
		// the sentinel selects every scope, exactly like a wildcard header.
		scope = ""
	}
	return scope, nil
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
	scope, err := resolveTargetScope(r)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

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

	headerTarget := r.Header.Get("X-Gleann-Target")
	if headerTarget == "" {
		headerTarget = r.Header.Get("X-Gleann-Index")
	}
	// Normalize both sides before comparing so padded spellings agree, and a
	// sentinel header imposes no restriction (same semantics as the read
	// routes). A sentinel body scope is global when unrestricted and can
	// never bypass a restricted header (v3 repair, finding 1).
	headerTarget = memory.NormalizeScope(headerTarget)
	if memory.IsAllScopesSentinel(headerTarget) {
		headerTarget = ""
	}
	if req.Scope == "" && req.Target != "" {
		req.Scope = req.Target
	}
	req.Scope = memory.NormalizeScope(req.Scope)
	if memory.IsAllScopesSentinel(req.Scope) {
		if headerTarget != "" {
			writeError(w, http.StatusForbidden, fmt.Sprintf("access denied: scope %q cannot bypass restricted target %q", req.Scope, headerTarget))
			return
		}
		req.Scope = ""
	}
	req.Repo = memory.NormalizeScope(req.Repo)
	if memory.IsAllScopesSentinel(req.Repo) {
		req.Repo = ""
	}
	if headerTarget != "" {
		if req.Scope != "" && req.Scope != headerTarget {
			writeError(w, http.StatusForbidden, fmt.Sprintf("access denied: target mismatch between header (%q) and body (%q)", headerTarget, req.Scope))
			return
		}
		if req.Scope == "" {
			req.Scope = headerTarget
		}
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
		Tier:        tier,
		Label:       label,
		Content:     req.Content,
		Source:      source,
		Tags:        req.Tags,
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

	var n int
	if r.URL.Query().Get("all") == "true" {
		n, err = mgr.ForgetAll(id)
	} else {
		n, err = mgr.Forget(id)
	}
	if err != nil {
		var amb *memory.ForgetAmbiguousError
		if errors.As(err, &amb) {
			// 409: the content phrase names several blocks and all=true was
			// not given. Nothing was deleted; the body lists the matches.
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "matches": amb.Matches})
			return
		}
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

	scope, err := resolveTargetScope(r)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

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
//	GET /api/blocks/context?command=...          — include failures matching command
//	GET /api/blocks/context?paths=a,b            — include failures matching files (comma-separated)
//	GET /api/blocks/context?path=...             — repeatable singular alias of paths
//	GET /api/blocks/context?symbols=X,Y          — include failures matching symbols (comma-separated)
//	GET /api/blocks/context?symbol=...           — repeatable singular alias of symbols
func (s *Server) handleBlockContext(w http.ResponseWriter, r *http.Request) {
	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	scope, err := resolveTargetScope(r)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	q := r.URL.Query()
	command := q.Get("command")
	paths := csvQueryParam(q, "paths", "path")
	symbols := csvQueryParam(q, "symbols", "symbol")

	var cw *memory.ContextWindow
	if command != "" || len(paths) > 0 || len(symbols) > 0 {
		cw, err = mgr.BuildTaskContextFor(scope, command, paths, symbols)
	} else if scope != "" {
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
		"budget":   cw.Budget,
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

// ── POST /api/blocks/failures ────────────────────────────────────────────────

// handleAddFailure records a command failure and its fix to failure memory.
// POST /api/blocks/failures
// Body: { "command": "...", "error": "...", "fix": "...", "paths": [...], "symbols": [...], "scope": "...", "repo": "..." }
//
// The target scope resolves from ?scope= / ?target= / X-Gleann-Target and must
// agree with any body scope after normalization (403 on mismatch). The "all"/"*"
// sentinel records a global block when unrestricted and is rejected (403)
// against a restricted target. Recording is admin-only (the security middleware
// classifies POST /api/blocks/failures as ScopeAdmin). The saved block is
// returned: 201 on first recording, 200 when the same failure is confirmed again
// (Confirms > 1), mirroring RecordFailure deduplication.
func (s *Server) handleAddFailure(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string   `json:"command"`
		Error   string   `json:"error"`
		Fix     string   `json:"fix"`
		Paths   []string `json:"paths,omitempty"`
		Symbols []string `json:"symbols,omitempty"`
		Scope   string   `json:"scope,omitempty"`
		Repo    string   `json:"repo,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Command) == "" || strings.TrimSpace(req.Error) == "" {
		writeError(w, http.StatusBadRequest, "command and error are required")
		return
	}

	scope, err := resolveTargetScope(r)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// Compare normalized targets: the body scope (falling back to repo) and
	// the resolved header/query target must agree after normalization.
	reqTarget := req.Scope
	if reqTarget == "" {
		reqTarget = req.Repo
	}
	reqTarget = memory.NormalizeScope(reqTarget)
	if memory.IsAllScopesSentinel(reqTarget) {
		if scope != "" {
			writeError(w, http.StatusForbidden, fmt.Sprintf("access denied: scope %q cannot bypass restricted target %q", reqTarget, scope))
			return
		}
		reqTarget = "" // unrestricted: record a global block, never a literal "all"
	}
	if reqTarget != "" {
		if scope != "" && scope != reqTarget {
			writeError(w, http.StatusForbidden, fmt.Sprintf("access denied: target mismatch between header (%q) and body (%q)", scope, reqTarget))
			return
		}
		scope = reqTarget
	}

	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	// Sanitize the provenance repo as well: it must not carry padding or the
	// sentinel into storage (v3 repair, finding 2).
	reqRepo := memory.NormalizeScope(req.Repo)
	if memory.IsAllScopesSentinel(reqRepo) {
		reqRepo = ""
	}
	saved, err := mgr.RecordFailureScoped(scope, reqRepo, memory.Failure{
		Command: req.Command,
		Error:   req.Error,
		Fix:     req.Fix,
		Paths:   req.Paths,
		Symbols: req.Symbols,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "record failure: "+err.Error())
		return
	}

	if saved.Confirms > 1 {
		writeJSON(w, http.StatusOK, saved)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

// handleListFailures lists recorded command failures visible to a scope.
// GET /api/blocks/failures                       — all failures in scope
// GET /api/blocks/failures?scope=...             — resolve scope (?scope= / ?target= / X-Gleann-Target)
// GET /api/blocks/failures?command=...           — filter by command
// GET /api/blocks/failures?paths=a,b&path=c      — filter by file paths
// GET /api/blocks/failures?symbols=X,Y&symbol=Z  — filter by symbols
//
// With no command/paths/symbols filter it lists every failure in the scope;
// otherwise it returns only the failures matching the task query. When
// unrestricted, ?scope=all / ?scope=* explicitly selects every scope; against a
// restricted target the sentinel is rejected (403). Read access requires the
// query auth scope (the middleware classifies GET /api/blocks* as ScopeQuery).
func (s *Server) handleListFailures(w http.ResponseWriter, r *http.Request) {
	scope, err := resolveTargetScope(r)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	q := r.URL.Query()
	command := q.Get("command")
	paths := csvQueryParam(q, "paths", "path")
	symbols := csvQueryParam(q, "symbols", "symbol")

	mgr, err := s.blockManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open memory store: "+err.Error())
		return
	}

	var failures []memory.Block
	if command != "" || len(paths) > 0 || len(symbols) > 0 {
		failures, err = mgr.FailuresForScoped(scope, command, paths, symbols)
	} else {
		failures, err = mgr.ListFailuresScoped(scope)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failures: "+err.Error())
		return
	}

	if failures == nil {
		failures = []memory.Block{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"failures": failures,
		"count":    len(failures),
	})
}

// csvQueryParam collects a list query parameter given either as a
// comma-separated plural (?paths=a,b) or as a repeated singular
// (?path=a&path=b), merging both forms. Empty entries are dropped, matching the
// splitList convention used elsewhere in the server.
func csvQueryParam(q url.Values, plural, singular string) []string {
	var out []string
	for _, v := range q[plural] {
		out = append(out, splitList(v)...)
	}
	for _, v := range q[singular] {
		out = append(out, splitList(v)...)
	}
	return out
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
