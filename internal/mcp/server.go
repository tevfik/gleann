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

// maxCachedSearchers is the maximum number of searchers to keep in memory.
// When exceeded, the least recently used searcher is evicted.
const maxCachedSearchers = 16

// Server wraps the mark3labs MCP server.
type Server struct {
	mcpServer   *server.MCPServer
	embedder    gleann.EmbeddingComputer
	config      gleann.Config
	searchers   map[string]*gleann.LeannSearcher
	searcherLRU []string       // tracks access order: most recent at end
	memPool     *mcpMemoryPool // Memory Engine: generic Entity/RELATES_TO graph
	blockMem    *blockMemPool  // BBolt hierarchical memory blocks (pkg/memory)
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
		memPool:   newMCPMemoryPool(cfg.IndexDir),
		blockMem:  &blockMemPool{},
	}

	// Register tools natively with the SDK
	s.AddTool(srv.buildSearchTool(), srv.handleSearch)
	s.AddTool(srv.buildSearchMultiTool(), srv.handleSearchMulti)
	s.AddTool(srv.buildListTool(), srv.handleList)
	s.AddTool(srv.buildAskTool(), srv.handleAsk)
	s.AddTool(srv.buildGraphNeighborsTool(), srv.handleGraphNeighbors)
	s.AddTool(srv.buildDocumentLinksTool(), srv.handleDocumentLinks)
	s.AddTool(srv.buildImpactTool(), srv.handleImpact)

	// Progressive disclosure — compact search + batch fetch + citation lookup.
	s.AddTool(srv.buildSearchIDsTool(), srv.handleSearchIDs)
	s.AddTool(srv.buildFetchTool(), srv.handleFetch)
	s.AddTool(srv.buildGetTool(), srv.handleGet)

	// Session tracking — log searches/asks to BBolt for cross-session context.
	s.AddTool(srv.buildSessionStartTool(), srv.handleSessionStart)
	s.AddTool(srv.buildSessionEndTool(), srv.handleSessionEnd)
	s.AddTool(srv.buildSessionStatusTool(), srv.handleSessionStatus)

	// Memory Block tools — persistent hierarchical memory (BBolt, no CGo).
	s.AddTool(srv.buildMemoryRememberTool(), srv.handleMemoryRemember)
	s.AddTool(srv.buildMemoryForgetTool(), srv.handleMemoryForget)
	s.AddTool(srv.buildMemorySearchTool(), srv.handleMemorySearch)
	s.AddTool(srv.buildMemoryListTool(), srv.handleMemoryList)
	s.AddTool(srv.buildMemoryContextTool(), srv.handleMemoryContext)

	// Batch query — run multiple questions concurrently.
	s.AddTool(srv.buildBatchAskTool(), srv.handleBatchAsk)

	// Memory Engine tools — external agents can manipulate the knowledge graph directly.
	s.AddTool(srv.buildInjectKGTool(), srv.handleInjectKG)
	s.AddTool(srv.buildDeleteEntityTool(), srv.handleDeleteEntity)
	s.AddTool(srv.buildTraverseKGTool(), srv.handleTraverseKG)

	// Register generic index list resource
	s.AddResource(mcp.Resource{
		URI:         "gleann://indexes",
		Name:        "Gleann Indexes List",
		Description: "List of all initialized Gleann indexes in the system",
		MIMEType:    "text/plain",
	}, srv.handleIndexListResource)

	// Register specific file read template
	s.AddResourceTemplate(mcp.NewResourceTemplate(
		"gleann://{index}/{file_path}",
		"Read File Content",
		mcp.WithTemplateDescription("Read the full extracted content of a source code file or document in a specific Gleann index"),
		mcp.WithTemplateMIMEType("text/plain"),
	), srv.handleReadResource)

	return srv
}

func (s *Server) Run() {
	log.Println("gleann MCP server starting with SDK (stdio)...")
	// Start stdio transport
	if err := server.ServeStdio(s.mcpServer); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// Close releases all resources held by the MCP server (KuzuDB handles, etc.).
func (s *Server) Close() {
	if s.memPool != nil {
		s.memPool.closeAll()
	}
	if s.blockMem != nil {
		s.blockMem.close()
	}
}

func (s *Server) getSearcher(name string) (*gleann.LeannSearcher, error) {
	if searcher, ok := s.searchers[name]; ok {
		s.touchLRU(name)
		return searcher, nil
	}

	searcher := gleann.NewSearcher(s.config, s.embedder)
	ctx := context.Background()
	if err := searcher.Load(ctx, name); err != nil {
		return nil, err
	}

	// Evict oldest if at capacity.
	if len(s.searchers) >= maxCachedSearchers {
		s.evictOldest()
	}

	s.searchers[name] = searcher
	s.searcherLRU = append(s.searcherLRU, name)
	return searcher, nil
}

// touchLRU moves name to the end of the LRU list (most recently used).
func (s *Server) touchLRU(name string) {
	for i, n := range s.searcherLRU {
		if n == name {
			s.searcherLRU = append(s.searcherLRU[:i], s.searcherLRU[i+1:]...)
			s.searcherLRU = append(s.searcherLRU, name)
			return
		}
	}
}

// evictOldest removes the least recently used searcher from the cache.
func (s *Server) evictOldest() {
	if len(s.searcherLRU) == 0 {
		return
	}
	oldest := s.searcherLRU[0]
	s.searcherLRU = s.searcherLRU[1:]
	if searcher, ok := s.searchers[oldest]; ok {
		if searcher != nil {
			searcher.Close()
		}
		delete(s.searchers, oldest)
	}
}

// --- Resource Handlers ---

func (s *Server) handleIndexListResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil {
		return nil, fmt.Errorf("error listing indexes: %v", err)
	}

	var sb strings.Builder
	sb.WriteString("Available Gleann Indexes:\n")
	for _, idx := range indexes {
		sb.WriteString(fmt.Sprintf("- %s: %d passages, backend=%s, model=%s\n", idx.Name, idx.NumPassages, idx.Backend, idx.EmbeddingModel))
	}

	res := mcp.TextResourceContents{
		URI:      request.Params.URI,
		MIMEType: "text/plain",
		Text:     sb.String(),
	}
	return []mcp.ResourceContents{res}, nil
}

func (s *Server) handleReadResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	// Format expected: gleann://{index}/{file_path}
	uri := request.Params.URI
	prefix := "gleann://"
	if !strings.HasPrefix(uri, prefix) {
		return nil, fmt.Errorf("invalid URI scheme, expected gleann://")
	}

	trimmed := strings.TrimPrefix(uri, prefix)
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid URI format. Expected gleann://{index}/{file_path}")
	}
	indexName := parts[0]
	filePath := parts[1]

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return nil, fmt.Errorf("failed to load index %q: %v", indexName, err)
	}

	// Because we want an exact metadata match (not a vector search),
	// we use a dummy empty query. However, searchers normally require a semantic vector.
	// As a workaround, we can query just the passage manager directly for all texts matching source.
	// The passage manager provides `.All()`.
	allPassages := searcher.PassageManager().All()
	var fileChunks []gleann.Passage

	for _, p := range allPassages {
		if source, ok := p.Metadata["source"].(string); ok && source == filePath {
			fileChunks = append(fileChunks, p)
		}
	}

	if len(fileChunks) == 0 {
		return nil, fmt.Errorf("file %q not found in index %q", filePath, indexName)
	}

	// Sort chunks sequentially by passage ID assuming they were indexed in order
	// In production, adding an explicit chunk_index to metadata is better, but sorting by ID works generally.
	// For better robustness later you can rely on the doc_chunk graph.
	var sb strings.Builder
	for _, chunk := range fileChunks {
		sb.WriteString(chunk.Text)
		sb.WriteString("\n")
	}

	res := mcp.TextResourceContents{
		URI:      uri,
		MIMEType: "text/plain",
		Text:     sb.String(),
	}

	return []mcp.ResourceContents{res}, nil
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
				"graph_context": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, enrich results with graph context (callers/callees from the AST-based code graph).",
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
	if gc, ok := args["graph_context"].(bool); ok && gc {
		searchOpts = append(searchOpts, gleann.WithGraphContext(true))
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

		// Append graph context if available.
		if r.GraphContext != nil && len(r.GraphContext.Symbols) > 0 {
			sb.WriteString("Graph Context:\n")
			for _, sym := range r.GraphContext.Symbols {
				sb.WriteString(fmt.Sprintf("  • %s (%s)\n", sym.FQN, sym.Kind))
				if len(sym.Callers) > 0 {
					sb.WriteString(fmt.Sprintf("    ← callers: %s\n", strings.Join(sym.Callers, ", ")))
				}
				if len(sym.Callees) > 0 {
					sb.WriteString(fmt.Sprintf("    → callees: %s\n", strings.Join(sym.Callees, ", ")))
				}
			}
		}
	}

	// Log to active session if one is running.
	s.sessionLog("search", indexName, query, len(results))

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
				}, "graph_context": map[string]interface{}{
					"type":        "boolean",
					"description": "When true, enrich each result with graph-derived context: symbols in the same file and their caller/callee relationships. Requires a graph index to exist.",
				}},
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

	// Log to active session if one is running.
	s.sessionLog("ask", indexName, question, 1)

	return mcp.NewToolResultText(answer), nil
}

// --- Graph Tools ---

func (s *Server) buildGraphNeighborsTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_graph_neighbors",
		Description: "Query the code graph to find caller/callee relationships for a given node. Use this to understand code architecture and dependencies without semantic searching.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to query",
				},
				"node_fqn": map[string]interface{}{
					"type":        "string",
					"description": "The Fully Qualified Name of the symbol to query (e.g. 'pkg.MyStruct.MyMethod')",
				},
			},
			Required: []string{"index", "node_fqn"},
		},
	}
}

func (s *Server) handleGraphNeighbors(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	nodeFqn, _ := args["node_fqn"].(string)

	if indexName == "" || nodeFqn == "" {
		return mcp.NewToolResultError("index and node_fqn are required"), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	db := searcher.GraphDB()
	if db == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Graph database not found or not initialized for index %q", indexName)), nil
	}

	callees, err := db.Callees(nodeFqn)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error querying callees: %v", err)), nil
	}

	callers, err := db.Callers(nodeFqn)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error querying callers: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Graph Neighbors for %s:\n\n", nodeFqn))

	sb.WriteString("=== Callers (Symbols that call this node) ===\n")
	if len(callers) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for _, c := range callers {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", c.FQN, c.Kind))
		}
	}

	sb.WriteString("\n=== Callees (Symbols this node calls) ===\n")
	if len(callees) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for _, c := range callees {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", c.FQN, c.Kind))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) buildDocumentLinksTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_document_links",
		Description: "Query the code graph to find code symbols directly explained, referenced, or linked by a specific Markdown document. Useful for tying notes to implementation.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to query",
				},
				"doc_path": map[string]interface{}{
					"type":        "string",
					"description": "The exact document file path (e.g. 'docs/architecture.md')",
				},
			},
			Required: []string{"index", "doc_path"},
		},
	}
}

func (s *Server) handleDocumentLinks(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	docPath, _ := args["doc_path"].(string)

	if indexName == "" || docPath == "" {
		return mcp.NewToolResultError("index and doc_path are required"), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	db := searcher.GraphDB()
	if db == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Graph database not found or not initialized for index %q", indexName)), nil
	}

	// We use the gleann.GraphDB interface to query document explanation links.
	symbols, err := db.DocumentSymbols(docPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error executing graph query: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Symbols explained by document %s:\n\n", docPath))

	found := false
	for _, sym := range symbols {
		sb.WriteString(fmt.Sprintf("- %s (%s) [File: %s]\n", sym.FQN, sym.Kind, sym.File))
		found = true
	}

	if !found {
		sb.WriteString("No symbols explicitly explained by this document in the graph.\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// --- Impact Analysis Tool ---

func (s *Server) buildImpactTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_impact",
		Description: "Analyze the blast radius of changing a symbol. Returns all direct and transitive callers plus affected files. Use this before making code changes to understand the impact.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to query",
				},
				"symbol": map[string]interface{}{
					"type":        "string",
					"description": "The Fully Qualified Name of the symbol to analyze (e.g. 'pkg.MyStruct.MyMethod')",
				},
				"max_depth": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum traversal depth for transitive callers (default 5, max 10)",
				},
			},
			Required: []string{"index", "symbol"},
		},
	}
}

func (s *Server) handleImpact(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	symbol, _ := args["symbol"].(string)
	maxDepth := 5
	if d, ok := args["max_depth"].(float64); ok && d > 0 {
		maxDepth = int(d)
	}

	if indexName == "" || symbol == "" {
		return mcp.NewToolResultError("index and symbol are required"), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	db := searcher.GraphDB()
	if db == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Graph database not found or not initialized for index %q", indexName)), nil
	}

	impact, err := db.Impact(symbol, maxDepth)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Impact analysis failed: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Impact Analysis for %s (depth: %d):\n\n", symbol, impact.Depth))

	sb.WriteString("=== Direct Callers ===\n")
	if len(impact.DirectCallers) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for _, c := range impact.DirectCallers {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
	}

	sb.WriteString("\n=== Transitive Callers ===\n")
	if len(impact.TransitiveCallers) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for _, c := range impact.TransitiveCallers {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
	}

	sb.WriteString("\n=== Affected Files ===\n")
	if len(impact.AffectedFiles) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for _, f := range impact.AffectedFiles {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}

	total := len(impact.DirectCallers) + len(impact.TransitiveCallers)
	sb.WriteString(fmt.Sprintf("\nTotal: %d affected symbols, %d affected files\n", total, len(impact.AffectedFiles)))

	return mcp.NewToolResultText(sb.String()), nil
}
