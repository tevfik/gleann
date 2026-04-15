package mcp

// Progressive Disclosure search tools:
//
//   gleann_search_ids — compact search; returns only IDs + metadata, no full text.
//                       Use this first to find relevant passages cheaply (5-10x fewer tokens).
//
//   gleann_fetch      — hydrate a batch of IDs from a previous gleann_search_ids call.
//                       Returns full passage text for the IDs you actually need.
//
//   gleann_get        — citation lookup: resolve a stable reference "{index}:{id}"
//                       to its full passage text.  References survive as long as the
//                       index is not rebuilt.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── gleann_search_ids ─────────────────────────────────────────────────────────

func (s *Server) buildSearchIDsTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_search_ids",
		Description: "Compact semantic search — returns only passage IDs, scores, and metadata " +
			"(no full text).  Use this to discover relevant passages at low token cost, then " +
			"call gleann_fetch with the IDs you actually want to read.  " +
			"This two-step pattern uses 5-10x fewer tokens than gleann_search for large indexes.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to search",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query",
				},
				"top_k": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (default 10)",
				},
				"filters": map[string]interface{}{
					"type":        "array",
					"description": "Optional metadata filters (same format as gleann_search)",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"field":    map[string]interface{}{"type": "string"},
							"operator": map[string]interface{}{"type": "string"},
							"value":    map[string]interface{}{},
						},
						"required": []string{"field", "operator", "value"},
					},
				},
				"filter_logic": map[string]interface{}{
					"type": "string",
					"enum": []string{"and", "or"},
				},
			},
			Required: []string{"index", "query"},
		},
	}
}

// compactResult is the slim representation returned by gleann_search_ids.
type compactResult struct {
	Ref    string  `json:"ref"` // citation reference: "{index}:{id}"
	ID     int64   `json:"id"`
	Score  float32 `json:"score"`
	Source string  `json:"source,omitempty"`
	Ext    string  `json:"ext,omitempty"`
	Type   string  `json:"type,omitempty"`
	Title  string  `json:"title,omitempty"`
	Peek   string  `json:"peek"` // first 120 chars of text
}

func (s *Server) handleSearchIDs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	query, _ := args["query"].(string)
	topK := 10
	if limit, ok := args["top_k"].(float64); ok {
		topK = int(limit)
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error loading index %q: %v", indexName, err)), nil
	}

	searchOpts := []gleann.SearchOption{gleann.WithTopK(topK)}
	if filters, logic := parseFilters(args); len(filters) > 0 {
		searchOpts = append(searchOpts, gleann.WithMetadataFilter(filters...))
		searchOpts = append(searchOpts, gleann.WithFilterLogic(logic))
	}

	results, err := searcher.Search(ctx, query, searchOpts...)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search error: %v", err)), nil
	}
	if len(results) == 0 {
		return mcp.NewToolResultText("[]"), nil
	}

	compact := make([]compactResult, 0, len(results))
	for _, r := range results {
		cr := compactResult{
			Ref:   fmt.Sprintf("%s:%d", indexName, r.ID),
			ID:    r.ID,
			Score: r.Score,
		}
		if v, ok := r.Metadata["source"].(string); ok {
			cr.Source = v
		}
		if v, ok := r.Metadata["ext"].(string); ok {
			cr.Ext = v
		}
		if v, ok := r.Metadata["type"].(string); ok {
			cr.Type = v
		}
		if v, ok := r.Metadata["title"].(string); ok {
			cr.Title = v
		}
		// Include a short peek so the LLM can decide without fetching.
		peek := r.Text
		if len(peek) > 120 {
			peek = peek[:120] + "…"
		}
		cr.Peek = peek
		compact = append(compact, cr)
	}

	// Log to active session if one is running.
	s.sessionLog("search_ids", indexName, query, len(results))

	b, _ := json.MarshalIndent(compact, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

// ── gleann_fetch ──────────────────────────────────────────────────────────────

func (s *Server) buildFetchTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_fetch",
		Description: "Fetch full passage text for a list of IDs returned by gleann_search_ids. " +
			"Only fetches the passages you actually need, keeping token usage low.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Index name (must match the index used in gleann_search_ids)",
				},
				"ids": map[string]interface{}{
					"type":        "array",
					"description": "List of passage IDs to fetch",
					"items":       map[string]interface{}{"type": "integer"},
				},
			},
			Required: []string{"index", "ids"},
		},
	}
}

func (s *Server) handleFetch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	rawIDs, ok := args["ids"].([]interface{})
	if !ok || len(rawIDs) == 0 {
		return mcp.NewToolResultError("ids must be a non-empty array of integers"), nil
	}

	ids := make([]int64, 0, len(rawIDs))
	for _, v := range rawIDs {
		switch n := v.(type) {
		case float64:
			ids = append(ids, int64(n))
		case int64:
			ids = append(ids, n)
		}
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error loading index %q: %v", indexName, err)), nil
	}

	pm := searcher.PassageManager()
	passages, err := pm.GetBatch(ids)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("fetch error: %v", err)), nil
	}
	if len(passages) == 0 {
		return mcp.NewToolResultText("No passages found for the given IDs."), nil
	}

	var sb strings.Builder
	for _, p := range passages {
		source := ""
		if v, ok := p.Metadata["source"]; ok {
			source = fmt.Sprintf(" [%v]", v)
		}
		sb.WriteString(fmt.Sprintf("--- ID:%d%s ---\n%s\n\n", p.ID, source, p.Text))
	}
	return mcp.NewToolResultText(sb.String()), nil
}

// ── gleann_get (citation lookup) ──────────────────────────────────────────────

func (s *Server) buildGetTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_get",
		Description: "Resolve a citation reference of the form \"{index}:{id}\" to its full " +
			"passage text.  References are stable as long as the index is not rebuilt.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"ref": map[string]interface{}{
					"type":        "string",
					"description": "Citation reference in the form \"{index}:{id}\", e.g. \"myproject:42\"",
				},
			},
			Required: []string{"ref"},
		},
	}
}

func (s *Server) handleGet(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	ref, _ := args["ref"].(string)
	sep := strings.LastIndex(ref, ":")
	if sep <= 0 {
		return mcp.NewToolResultError("ref must be in the form \"{index}:{id}\""), nil
	}

	indexName := ref[:sep]
	var passageID int64
	if _, err := fmt.Sscanf(ref[sep+1:], "%d", &passageID); err != nil {
		return mcp.NewToolResultError("invalid passage ID in ref: " + ref), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("error loading index %q: %v", indexName, err)), nil
	}

	p, err := searcher.PassageManager().Get(passageID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("passage not found: %v", err)), nil
	}

	source := ""
	if v, ok := p.Metadata["source"]; ok {
		source = fmt.Sprintf(" [%v]", v)
	}
	return mcp.NewToolResultText(fmt.Sprintf("Ref: %s%s\n\n%s", ref, source, p.Text)), nil
}
