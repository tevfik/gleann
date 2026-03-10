package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// multiSearchRequest is the request body for POST /api/search.
type multiSearchRequest struct {
	Indexes     []string                `json:"indexes"`
	Query       string                  `json:"query"`
	TopK        int                     `json:"top_k,omitempty"`
	HybridAlpha float32                 `json:"hybrid_alpha,omitempty"`
	MinScore    float32                 `json:"min_score,omitempty"`
	Rerank      bool                    `json:"rerank,omitempty"`
	RerankModel string                  `json:"rerank_model,omitempty"`
	Filters     []gleann.MetadataFilter `json:"metadata_filters,omitempty"`
	FilterLogic string                  `json:"filter_logic,omitempty"`
}

// multiSearchResponse is the response for POST /api/search.
type multiSearchResponse struct {
	Results []gleann.MultiSearchResult `json:"results"`
	Count   int                        `json:"count"`
	QueryMs int64                      `json:"query_ms"`
}

// handleMultiSearch searches across multiple indexes concurrently.
// If "indexes" is empty or omitted, all available indexes are searched.
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

	start := time.Now()

	results, err := gleann.SearchMultiple(r.Context(), s.config, s.embedder, req.Indexes, req.Query, opts...)
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
