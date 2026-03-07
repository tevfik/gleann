package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// Config holds MCP server configuration.
type Config struct {
	IndexDir          string
	EmbeddingProvider string
	EmbeddingModel    string
	OllamaHost        string
	OpenAIAPIKey      string
	OpenAIBaseURL     string
	Version           string
}

// Server wraps the mark3labs MCP server.
type Server struct {
	mcpServer *server.MCPServer
	embedder  gleann.EmbeddingComputer
	config    gleann.Config
	searchers map[string]*gleann.LeannSearcher
}

// NewServer initializes a new MCP server that exposes Gleann capabilities using the SDK.
func NewServer(cfg Config) *Server {
	version := cfg.Version
	if version == "" {
		version = "dev"
	}

	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = cfg.IndexDir
	glCfg.EmbeddingModel = cfg.EmbeddingModel
	glCfg.EmbeddingProvider = cfg.EmbeddingProvider
	glCfg.OllamaHost = cfg.OllamaHost
	glCfg.OpenAIAPIKey = cfg.OpenAIAPIKey
	glCfg.OpenAIBaseURL = cfg.OpenAIBaseURL

	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(cfg.EmbeddingProvider),
		Model:    cfg.EmbeddingModel,
		BaseURL:  cfg.OllamaHost,
	})

	s := server.NewMCPServer("gleann-mcp", version)

	srv := &Server{
		mcpServer: s,
		config:    glCfg,
		embedder:  embedder,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	// Register tools natively with the SDK
	s.AddTool(srv.buildSearchTool(), srv.handleSearch)
	s.AddTool(srv.buildListTool(), srv.handleList)
	s.AddTool(srv.buildAskTool(), srv.handleAsk)

	return srv
}

func (s *Server) Run() {
	log.Println("gleann MCP server starting with SDK (stdio)...")
	// Start stdio transport
	if err := server.ServeStdio(s.mcpServer); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func (s *Server) getSearcher(name string) (*gleann.LeannSearcher, error) {
	if searcher, ok := s.searchers[name]; ok {
		return searcher, nil
	}

	searcher := gleann.NewSearcher(s.config, s.embedder)
	ctx := context.Background()
	if err := searcher.Load(ctx, name); err != nil {
		return nil, err
	}

	s.searchers[name] = searcher
	return searcher, nil
}

// --- Search Tool ---

func (s *Server) buildSearchTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_search",
		Description: "Perform a semantic vector search across an indexed repository or memory graph. Use this to retrieve information.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to search",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query, question, or context to find related material for.",
				},
				"top_k": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (default 5).",
				},
				"filters": map[string]interface{}{
					"type":        "array",
					"description": "Optional list of metadata filters to narrow down the search. Example: [{'field': 'ext', 'operator': 'eq', 'value': '.go'}, {'field': 'type', 'operator': 'in', 'value': ['function', 'class']}]",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"field":    map[string]interface{}{"type": "string", "description": "The metadata field to filter on (e.g. ext, type, source)"},
							"operator": map[string]interface{}{"type": "string", "description": "Operator (eq, ne, gt, gte, lt, lte, in, nin, contains, startswith, endswith, exists)"},
							"value":    map[string]interface{}{"description": "The value to filter against"},
						},
						"required": []string{"field", "operator", "value"},
					},
				},
				"filter_logic": map[string]interface{}{
					"type":        "string",
					"description": "Logic to combine filters ('and' or 'or'). Default is 'and'.",
					"enum":        []string{"and", "or"},
				},
			},
			Required: []string{"index", "query"},
		},
	}
}

// parseFilters extracts metadata filters from MCP tool arguments
func parseFilters(args map[string]interface{}) ([]gleann.MetadataFilter, string) {
	var filters []gleann.MetadataFilter
	logic := "and"

	if l, ok := args["filter_logic"].(string); ok && (l == "and" || l == "or") {
		logic = l
	}

	rawFilters, ok := args["filters"].([]interface{})
	if !ok {
		return nil, logic
	}

	for _, rf := range rawFilters {
		fMap, ok := rf.(map[string]interface{})
		if !ok {
			continue
		}
		field, okF := fMap["field"].(string)
		opStr, okO := fMap["operator"].(string)
		val, okV := fMap["value"]

		if okF && okO && okV {
			filters = append(filters, gleann.MetadataFilter{
				Field:    field,
				Operator: gleann.FilterOperator(opStr),
				Value:    val,
			})
		}
	}
	return filters, logic
}

func (s *Server) handleSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	query, _ := args["query"].(string)
	topK := 5
	if limit, ok := args["top_k"].(float64); ok {
		topK = int(limit)
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	searchOpts := []gleann.SearchOption{gleann.WithTopK(topK)}
	if filters, logic := parseFilters(args); len(filters) > 0 {
		searchOpts = append(searchOpts, gleann.WithMetadataFilter(filters...))
		searchOpts = append(searchOpts, gleann.WithFilterLogic(logic))
	}

	results, err := searcher.Search(ctx, query, searchOpts...)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error searching memory: %v", err)), nil
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No relevant memory fragments found."), nil
	}

	var sb strings.Builder
	for i, r := range results {
		source := ""
		if metaSource, ok := r.Metadata["source"]; ok {
			source = fmt.Sprintf(" [%v]", metaSource)
		}
		sb.WriteString(fmt.Sprintf("---\nResult [%d]%s (Score: %.4f):\n%s\n", i+1, source, r.Score, r.Text))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// --- List Tool ---

func (s *Server) buildListTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_list",
		Description: "List all available gleann indexes with their metadata (name, backend, model, passage count).",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{},
		},
	}
}

func (s *Server) handleList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error listing indexes: %v", err)), nil
	}

	if len(indexes) == 0 {
		return mcp.NewToolResultText("No indexes found."), nil
	}

	var sb strings.Builder
	for _, idx := range indexes {
		sb.WriteString(fmt.Sprintf("- %s: %d passages, backend=%s, model=%s\n", idx.Name, idx.NumPassages, idx.Backend, idx.EmbeddingModel))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// --- Ask Tool ---

func (s *Server) buildAskTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_ask",
		Description: "Ask a question about indexed data using RAG (Retrieval-Augmented Generation). Retrieves relevant context and generates an answer.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to query",
				},
				"question": map[string]interface{}{
					"type":        "string",
					"description": "Question to ask the LLM based on context.",
				},
				"filters": map[string]interface{}{
					"type":        "array",
					"description": "Optional list of metadata filters to narrow down the retrieved context. Example: [{'field': 'ext', 'operator': 'eq', 'value': '.go'}]",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"field":    map[string]interface{}{"type": "string", "description": "The metadata field to filter on"},
							"operator": map[string]interface{}{"type": "string", "description": "Operator (eq, ne, gt, lt, in, contains, etc.)"},
							"value":    map[string]interface{}{"description": "The value to filter against"},
						},
						"required": []string{"field", "operator", "value"},
					},
				},
				"filter_logic": map[string]interface{}{
					"type":        "string",
					"description": "Logic to combine filters ('and' or 'or'). Default is 'and'.",
					"enum":        []string{"and", "or"},
				},
			},
			Required: []string{"index", "question"},
		},
	}
}

func (s *Server) handleAsk(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	question, _ := args["question"].(string)

	if indexName == "" || question == "" {
		return mcp.NewToolResultError("index and question are required"), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	chatConfig := gleann.DefaultChatConfig()
	chat := gleann.NewChat(searcher, chatConfig)

	var searchOpts []gleann.SearchOption
	if filters, logic := parseFilters(args); len(filters) > 0 {
		searchOpts = append(searchOpts, gleann.WithMetadataFilter(filters...))
		searchOpts = append(searchOpts, gleann.WithFilterLogic(logic))
	}

	answer, err := chat.Ask(ctx, question, searchOpts...)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error asking question: %v", err)), nil
	}

	return mcp.NewToolResultText(answer), nil
}
