package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

func (s *Server) buildSearchMultiTool() mcp.Tool {
	return mcp.NewTool("gleann_search_multi",
		mcp.WithDescription("Search across multiple Gleann indexes simultaneously. Returns merged results sorted by score, each tagged with the source index."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query text"),
		),
		mcp.WithString("indexes",
			mcp.Description("Comma-separated index names to search. Omit to search all available indexes."),
		),
		mcp.WithNumber("top_k",
			mcp.Description("Maximum number of results to return (default: 10)"),
		),
	)
}

func (s *Server) handleSearchMulti(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	query, _ := args["query"].(string)
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	var names []string
	if indexes, ok := args["indexes"].(string); ok && indexes != "" {
		for _, name := range strings.Split(indexes, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				names = append(names, name)
			}
		}
	}

	var opts []gleann.SearchOption
	if topK, ok := args["top_k"].(float64); ok && topK > 0 {
		opts = append(opts, gleann.WithTopK(int(topK)))
	}

	results, err := gleann.SearchMultiple(ctx, s.config, s.embedder, names, query, opts...)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("multi-search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No results found across indexes."), nil
	}

	var sb strings.Builder
	for i, r := range results {
		text := r.Text
		if len(text) > 300 {
			text = text[:300] + "..."
		}
		source, _ := r.Metadata["source"].(string)
		fmt.Fprintf(&sb, "[%d] (index: %s, score: %.4f", i+1, r.Index, r.Score)
		if source != "" {
			fmt.Fprintf(&sb, ", file: %s", source)
		}
		fmt.Fprintf(&sb, ")\n%s\n\n", text)
	}

	return mcp.NewToolResultText(sb.String()), nil
}
