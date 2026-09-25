package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// multiSearchRequest is the request body for POST /api/search.
type multiSearchRequest struct {
	Target       any                     `json:"target,omitempty"`  // single string or []string
	Targets      []string                `json:"targets,omitempty"` // list of targets
	Index        string                  `json:"index,omitempty"`   // single index
	Indexes      []string                `json:"indexes,omitempty"` // list of indexes
	Project      string                  `json:"project,omitempty"` // project target shorthand
	Query        string                  `json:"query"`
	TopK         int                     `json:"top_k,omitempty"`
	HybridAlpha  float32                 `json:"hybrid_alpha,omitempty"`
	MinScore     float32                 `json:"min_score,omitempty"`
	Rerank       bool                    `json:"rerank,omitempty"`
	RerankModel  string                  `json:"rerank_model,omitempty"`
	Filters      []gleann.MetadataFilter `json:"metadata_filters,omitempty"`
	FilterLogic  string                  `json:"filter_logic,omitempty"`
	IncludeTests bool                    `json:"include_tests,omitempty"`
	Kind         string                  `json:"kind,omitempty"`
}

// multiSearchResponse is the response for POST /api/search.
type multiSearchResponse struct {
	Results []gleann.MultiSearchResult `json:"results"`
	Count   int                        `json:"count"`
	QueryMs int64                      `json:"query_ms"`
}

// handleMultiSearch searches across multiple indexes concurrently.
// It enforces client target isolation and index governance:
// - If a target is specified via body, header, or query param, the search is strictly limited to that target.
// - If no target is specified, only public (MCP exposed) and tag-matching indexes are searched.
// - Private indexes are never leaked across tenants or unauthenticated queries.
func (s *Server) handleMultiSearch(w http.ResponseWriter, r *http.Request) {
	var req multiSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	// 1. Collect requested target names from body
	var rawTargets []string
	seen := make(map[string]bool)
	add := func(val string) {
		val = strings.TrimSpace(val)
		if val != "" && !seen[val] {
			seen[val] = true
			rawTargets = append(rawTargets, val)
		}
	}

	if req.Target != nil {
		switch v := req.Target.(type) {
		case string:
			for _, part := range strings.Split(v, ",") {
				add(part)
			}
		case []any:
			for _, item := range v {
				if str, ok := item.(string); ok {
					add(str)
				}
			}
		case []string:
			for _, str := range v {
				add(str)
			}
		}
	}
	for _, t := range req.Targets {
		for _, part := range strings.Split(t, ",") {
			add(part)
		}
	}
	if req.Index != "" {
		for _, part := range strings.Split(req.Index, ",") {
			add(part)
		}
	}
	for _, idx := range req.Indexes {
		for _, part := range strings.Split(idx, ",") {
			add(part)
		}
	}
	if req.Project != "" {
		add(req.Project)
	}

	// 2. Client target constraint check (via header or query param)
	clientTarget := r.Header.Get("X-Gleann-Target")
	if clientTarget == "" {
		clientTarget = r.Header.Get("X-Gleann-Index")
	}
	if clientTarget == "" {
		clientTarget = r.URL.Query().Get("target")
	}
	if clientTarget == "" {
		clientTarget = r.URL.Query().Get("index")
	}

	if clientTarget != "" {
		if len(rawTargets) == 0 {
			add(clientTarget)
		} else {
			for _, t := range rawTargets {
				if !strings.EqualFold(t, clientTarget) {
					writeError(w, http.StatusForbidden, fmt.Sprintf("access denied: client target is restricted to %q, cannot access index %q", clientTarget, t))
					return
				}
			}
		}
	}

	// 3. Resolve targets and enforce governance
	var resolvedNames []string
	resolvedSeen := make(map[string]bool)
	addResolved := func(name string) {
		if !resolvedSeen[name] {
			resolvedSeen[name] = true
			resolvedNames = append(resolvedNames, name)
		}
	}

	if len(rawTargets) > 0 {
		for _, name := range rawTargets {
			if strings.HasPrefix(name, "@") {
				tagName := strings.TrimPrefix(name, "@")
				tagged, err := gleann.ListIndexesByTag(s.config.IndexDir, tagName)
				if err != nil {
					writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list indexes for tag %q: %v", tagName, err))
					return
				}
				for _, idx := range tagged {
					if s.isIndexAccessibleMeta(&idx) {
						addResolved(idx.Name)
					}
				}
			} else {
				meta, err := gleann.GetIndexMeta(s.config.IndexDir, name)
				if err != nil {
					writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found: %v", name, err))
					return
				}
				if !s.isIndexAccessibleMeta(meta) {
					writeError(w, http.StatusForbidden, fmt.Sprintf("access denied: index %q is private or does not match required tags", name))
					return
				}
				addResolved(name)
			}
		}
		if len(resolvedNames) == 0 {
			writeJSON(w, http.StatusOK, multiSearchResponse{
				Results: []gleann.MultiSearchResult{},
				Count:   0,
				QueryMs: 0,
			})
			return
		}
	} else {
		// When no target was requested, find all accessible indexes (public & matching tags)
		indexes, err := gleann.ListIndexes(s.config.IndexDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, idx := range indexes {
			if s.isIndexAccessibleMeta(&idx) {
				addResolved(idx.Name)
			}
		}
		if len(resolvedNames) == 0 {
			writeJSON(w, http.StatusOK, multiSearchResponse{
				Results: []gleann.MultiSearchResult{},
				Count:   0,
				QueryMs: 0,
			})
			return
		}
	}

	var opts []gleann.SearchOption
	if req.TopK > 0 {
		opts = append(opts, gleann.WithTopK(req.TopK))
	}
	if req.HybridAlpha > 0 {
		opts = append(opts, gleann.WithHybridAlpha(req.HybridAlpha))
	}
	if req.MinScore > 0 {
		opts = append(opts, gleann.WithMinScore(req.MinScore))
	}
	if len(req.Filters) > 0 {
		opts = append(opts, gleann.WithMetadataFilter(req.Filters...))
	}
	if req.FilterLogic != "" {
		opts = append(opts, gleann.WithFilterLogic(req.FilterLogic))
	}
	if req.Rerank {
		opts = append(opts, gleann.WithReranker(true))
	}
	if req.IncludeTests {
		opts = append(opts, gleann.WithIncludeTests(true))
	}
	if req.Kind != "" {
		opts = append(opts, gleann.WithKind(req.Kind))
	}

	start := time.Now()

	results, err := gleann.SearchMultiple(r.Context(), s.config, s.embedder, resolvedNames, req.Query, opts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("multi-search failed: %v", err))
		return
	}

	serverMetrics.RecordMultiSearch()

	writeJSON(w, http.StatusOK, multiSearchResponse{
		Results: results,
		Count:   len(results),
		QueryMs: time.Since(start).Milliseconds(),
	})
}
