package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

func (s *Server) buildDefTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_def",
		Description: "Go-to-definition for symbols (functions, methods, types, classes, structs). " +
			"Returns file:line, signature, and documentation in a compact format (~500 tokens). " +
			"Fastest way to understand an API without reading entire files.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"symbol": map[string]interface{}{
					"type":        "string",
					"description": "Name or identifier of the symbol to find definition for (e.g. 'TokenManager', 'calculateMetrics', 'NewEngine')",
				},
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Optional name of index to search. Auto-resolves if omitted.",
				},
				"kind": map[string]interface{}{
					"type":        "string",
					"description": "Optional filter by kind ('function', 'method', 'struct', 'class', 'interface', 'type')",
				},
				"top_k": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of definition candidates to return (default 5)",
				},
			},
			Required: []string{"symbol"},
		},
	}
}

func (s *Server) handleDef(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	symbol, _ := args["symbol"].(string)
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return mcp.NewToolResultError("symbol argument is required"), nil
	}

	indexName, _ := args["index"].(string)
	if indexName == "" {
		indexName = s.resolveIndexName()
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	topK := 5
	if limit, ok := args["top_k"].(float64); ok && limit > 0 {
		topK = int(limit)
	}

	kind, _ := args["kind"].(string)

	opts := gleann.DefinitionOptions{
		TopK:    topK,
		Kind:    kind,
		RootDir: s.config.IndexDir,
	}

	defs, err := gleann.FindDefinitions(ctx, searcher, symbol, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Definition lookup failed: %v", err)), nil
	}

	if len(defs) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No definition found for symbol %q. Try gleann_search to locate references.", symbol)), nil
	}

	return mcp.NewToolResultText(gleann.FormatDefinitions(defs)), nil
}
