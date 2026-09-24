package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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
	LLMProvider       string
	LLMModel          string
	Version           string
	CleanToolNames    bool
	ToolsProfile      string // "core" (default), "full", or comma-separated list of tool names
}

// maxCachedSearchers is the maximum number of searchers to keep in memory.
// When exceeded, the least recently used searcher is evicted.
const maxCachedSearchers = 16

// Server wraps the mark3labs MCP server.
type Server struct {
	mcpServer      *server.MCPServer
	embedder       gleann.EmbeddingComputer
	config         gleann.Config
	cleanToolNames bool
	toolsProfile   string
	searcherMu     sync.Mutex
	searchers      map[string]*gleann.LeannSearcher
	searcherLRU    []string       // tracks access order: most recent at end
	memPool        *mcpMemoryPool // Memory Engine: generic Entity/RELATES_TO graph
	blockMem       *blockMemPool  // BBolt hierarchical memory blocks (pkg/memory)
	gPool          *graphPool     // Community detection graph pool (treesitter only)
	syncRunner      syncRunnerFunc
	syncMu          sync.Mutex
	syncTasks       map[string]*syncTask
	syncWaitTimeout time.Duration
}

// syncTask tracks a background indexing or synchronization task for an index.
type syncTask struct {
	indexName string
	mode      string
	isNew     bool
	startTime time.Time
	done      chan struct{}
	output    string
	err       error
}

// SyncOptions contains options for synchronizing or building an index via gleann_sync.
type SyncOptions struct {
	IndexName string
	DocsDir   string
	Files     []string
	Mode      string // "code", "docs", "all"
	NoPlugins bool
	IsNew     bool
}

type syncRunnerFunc func(ctx context.Context, opts SyncOptions) (string, error)

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
	glCfg.LLMProvider = cfg.LLMProvider
	glCfg.LLMModel = cfg.LLMModel

	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(cfg.EmbeddingProvider),
		Model:    cfg.EmbeddingModel,
		BaseURL:  cfg.OllamaHost,
	})

	s := server.NewMCPServer("gleann-mcp", version, server.WithRoots())

	srv := &Server{
		mcpServer:      s,
		config:         glCfg,
		cleanToolNames: cfg.CleanToolNames,
		toolsProfile:   cfg.ToolsProfile,
		embedder:       embedder,
		searchers:      make(map[string]*gleann.LeannSearcher),
		memPool:        newMCPMemoryPool(cfg.IndexDir),
		blockMem:       &blockMemPool{},
		syncTasks:      make(map[string]*syncTask),
	}
	if srv.toolsProfile == "" {
		if envProfile := os.Getenv("GLEANN_TOOLS"); envProfile != "" {
			srv.toolsProfile = envProfile
		} else {
			srv.toolsProfile = "core"
		}
	}

	// Wire VectorSyncer factory (build-tag gated; no-op when !treesitter).
	srv.wireMemorySyncer(cfg, glCfg, embedder)

	// Register tools natively with the SDK (respecting cleanToolNames)
	srv.addTool(srv.buildSearchTool(), srv.handleSearch)
	srv.addTool(srv.buildSearchMultiTool(), srv.handleSearchMulti)
	srv.addTool(srv.buildListTool(), srv.handleList)
	srv.addTool(srv.buildAskTool(), srv.handleAsk)
	srv.addTool(srv.buildGraphNeighborsTool(), srv.handleGraphNeighbors)
	srv.addTool(srv.buildDocumentLinksTool(), srv.handleDocumentLinks)
	srv.addTool(srv.buildReadFullDocumentTool(), srv.handleReadFullDocument)
	srv.addTool(srv.buildDocumentTOCTool(), srv.handleDocumentTOC)
	srv.addTool(srv.buildImpactTool(), srv.handleImpact)

	// Progressive disclosure — compact search + batch fetch + citation lookup.
	srv.addTool(srv.buildSearchIDsTool(), srv.handleSearchIDs)
	srv.addTool(srv.buildFetchTool(), srv.handleFetch)
	srv.addTool(srv.buildGetTool(), srv.handleGet)

	// Session tracking — log searches/asks to BBolt for cross-session context.
	srv.addTool(srv.buildSessionStartTool(), srv.handleSessionStart)
	srv.addTool(srv.buildSessionEndTool(), srv.handleSessionEnd)
	srv.addTool(srv.buildSessionStatusTool(), srv.handleSessionStatus)

	// Memory Block tools — persistent hierarchical memory (BBolt, no CGo).
	srv.addTool(srv.buildMemoryRememberTool(), srv.handleMemoryRemember)
	srv.addTool(srv.buildMemoryForgetTool(), srv.handleMemoryForget)
	srv.addTool(srv.buildMemorySearchTool(), srv.handleMemorySearch)
	srv.addTool(srv.buildMemoryListTool(), srv.handleMemoryList)
	srv.addTool(srv.buildMemoryContextTool(), srv.handleMemoryContext)

	// Batch query — run multiple questions concurrently.
	srv.addTool(srv.buildBatchAskTool(), srv.handleBatchAsk)

	// Memory Engine tools — external agents can manipulate the knowledge graph directly.
	srv.addTool(srv.buildInjectKGTool(), srv.handleInjectKG)
	srv.addTool(srv.buildDeleteEntityTool(), srv.handleDeleteEntity)
	srv.addTool(srv.buildTraverseKGTool(), srv.handleTraverseKG)

	// Graph stats + symbols_in_file — available without treesitter.
	srv.addTool(srv.buildGraphStatsTool(), srv.handleGraphStats)
	srv.addTool(srv.buildSymbolsInFileTool(), srv.handleSymbolsInFile)

	// Shell compression + mode-aware file read + token gain tracking.
	srv.addTool(srv.buildShellTool(), srv.handleShell)
	srv.addTool(srv.buildReadTool(), srv.handleRead)
	srv.addTool(srv.buildGainTool(), srv.handleGain)

	// On-demand index synchronization tool
	srv.addTool(srv.buildSyncTool(), srv.handleSync)

	// Community detection tools — require treesitter build tag.
	srv.initGraphPool()
	srv.registerGraphTools()

	// Register Prompts API templates
	srv.registerPrompts()

	// Register Roots API handler
	srv.registerRootsHandler()

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

// isToolEnabled checks whether a tool belongs to the active tools profile.
func (s *Server) isToolEnabled(name string) bool {
	profile := strings.TrimSpace(strings.ToLower(s.toolsProfile))
	if profile == "full" || profile == "all" {
		return true
	}
	if profile == "" || profile == "core" {
		switch name {
		case "gleann_search", "gleann_read", "gleann_graph_neighbors", "gleann_impact",
			"memory_remember", "memory_context", "memory_search", "memory_forget", "gleann_sync":
			return true
		default:
			return false
		}
	}
	// Custom comma-separated list of tool names
	parts := strings.Split(profile, ",")
	clean := strings.TrimPrefix(name, "gleann_")
	clean = strings.TrimPrefix(clean, "memory_")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == name || p == clean || p == "gleann_"+p || p == "memory_"+p {
			return true
		}
		// Aliases
		if p == "symbol" && (name == "gleann_graph_neighbors" || name == "gleann_navigate_symbol") {
			return true
		}
		if p == "recall" && (name == "memory_context" || name == "memory_search") {
			return true
		}
	}
	return false
}

// addTool registers a tool with the MCP server if enabled by the active tools profile.
// Strips the "gleann_" prefix if cleanToolNames is enabled.
func (s *Server) addTool(tool mcp.Tool, handler server.ToolHandlerFunc) {
	if !s.isToolEnabled(tool.Name) {
		return
	}
	if s.cleanToolNames {
		tool.Name = strings.TrimPrefix(tool.Name, "gleann_")
	}
	s.mcpServer.AddTool(tool, handler)
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
	s.searcherMu.Lock()
	for _, searcher := range s.searchers {
		if searcher != nil {
			searcher.Close()
		}
	}
	s.searchers = make(map[string]*gleann.LeannSearcher)
	s.searcherLRU = nil
	s.searcherMu.Unlock()
	s.closeGraphPool()
}

func (s *Server) getSearcher(name string) (*gleann.LeannSearcher, error) {
	// Auto-resolve index name if empty
	if name == "" {
		if envIdx := os.Getenv("GLEANN_INDEX"); envIdx != "" {
			name = envIdx
		} else {
			indexes, err := gleann.ListIndexes(s.config.IndexDir)
			if err == nil {
				var exposed []gleann.IndexMeta
				for _, idx := range indexes {
					if idx.IsMCPExposed() {
						exposed = append(exposed, idx)
					}
				}

				// Check if current working directory name matches an available index
				if cwd, err := os.Getwd(); err == nil {
					base := filepath.Base(cwd)
					for _, idx := range exposed {
						if strings.EqualFold(idx.Name, base) {
							name = idx.Name
							break
						}
					}
				}

				if name == "" && len(exposed) == 1 {
					name = exposed[0].Name
				}
			}
		}
	}
	if name == "" {
		return nil, fmt.Errorf("index name required (use gleann_list to see available indexes)")
	}

	// Verify index is exposed to MCP
	meta, err := gleann.GetIndexMeta(s.config.IndexDir, name)
	if err == nil {
		if !meta.IsMCPExposed() {
			return nil, fmt.Errorf("access denied: index %q is private and not exposed to MCP", name)
		}
		if tagEnv := os.Getenv("GLEANN_TAGS"); tagEnv != "" {
			matched := false
			for _, t := range strings.Split(tagEnv, ",") {
				if meta.HasTag(t) {
					matched = true
					break
				}
			}
			if !matched {
				return nil, fmt.Errorf("access denied: index %q does not match required tags (%s)", name, tagEnv)
			}
		}
	}

	s.searcherMu.Lock()
	if searcher, ok := s.searchers[name]; ok {
		s.touchLRU(name)
		s.searcherMu.Unlock()
		return searcher, nil
	}
	s.searcherMu.Unlock()

	embedder := s.embedder
	if meta, err := gleann.GetIndexMeta(s.config.IndexDir, name); err == nil && meta.EmbeddingModel != "" {
		if embedder == nil || embedder.ModelName() != meta.EmbeddingModel {
			// Auto-adapt to the index's embedding model using the configured provider
			embedder = embedding.NewComputer(embedding.Options{
				Provider:    embedding.Provider(s.config.EmbeddingProvider),
				Model:       meta.EmbeddingModel,
				BaseURL:     s.config.OllamaHost,
				BatchSize:   s.config.BatchSize,
				Concurrency: s.config.Concurrency,
			})
		}
	}

	searcher := gleann.NewSearcher(s.config, embedder)
	// Enable BM25 hybrid scoring by default for MCP-facing search/ask tools.
	searcher.SetScorer(gleann.NewBM25Adapter())

	// Configure reranker if enabled via config or environment
	if s.config.SearchConfig.UseReranker || os.Getenv("GLEANN_RERANK") == "1" || os.Getenv("GLEANN_RERANK") == "true" {
		rerankModel := s.config.SearchConfig.RerankerConfig.Model
		if rerankModel == "" {
			rerankModel = os.Getenv("GLEANN_RERANK_MODEL")
		}
		if rerankModel == "" {
			rerankModel = "bge-reranker-v2-m3"
		}
		provider := gleann.RerankerProvider(s.config.EmbeddingProvider)
		if pEnv := os.Getenv("GLEANN_RERANK_PROVIDER"); pEnv != "" {
			provider = gleann.RerankerProvider(pEnv)
		}
		rerankerCfg := gleann.RerankerConfig{
			Provider: provider,
			Model:    rerankModel,
			BaseURL:  s.config.OllamaHost,
		}
		searcher.SetReranker(gleann.NewReranker(rerankerCfg))
	}

	ctx := context.Background()
	if err := searcher.Load(ctx, name); err != nil {
		return nil, err
	}

	s.searcherMu.Lock()
	defer s.searcherMu.Unlock()

	// Double-check in case another goroutine loaded the same index concurrently
	if existing, ok := s.searchers[name]; ok {
		s.touchLRU(name)
		return existing, nil
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
// Caller must hold s.searcherMu.
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
// Caller must hold s.searcherMu.
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

// evictIndex closes and removes any cached searcher and graph database handle for the given index.
func (s *Server) evictIndex(name string) {
	s.searcherMu.Lock()
	if searcher, ok := s.searchers[name]; ok {
		if searcher != nil {
			searcher.Close()
		}
		delete(s.searchers, name)
		for i, n := range s.searcherLRU {
			if n == name {
				s.searcherLRU = append(s.searcherLRU[:i], s.searcherLRU[i+1:]...)
				break
			}
		}
	}
	s.searcherMu.Unlock()
	s.evictGraph(name)
}

// --- Resource Handlers ---

func (s *Server) handleIndexListResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil {
		return nil, fmt.Errorf("error listing indexes: %v", err)
	}

	tagEnv := os.Getenv("GLEANN_TAGS")

	var sb strings.Builder
	sb.WriteString("Available Gleann Indexes:\n")
	count := 0
	for _, idx := range indexes {
		if !idx.IsMCPExposed() {
			continue
		}
		if tagEnv != "" {
			matched := false
			for _, t := range strings.Split(tagEnv, ",") {
				if idx.HasTag(t) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		count++
		sb.WriteString(fmt.Sprintf("- %s: %d passages, backend=%s", idx.Name, idx.NumPassages, idx.Backend))
		if len(idx.Tags) > 0 {
			sb.WriteString(fmt.Sprintf(", tags=[%s]", strings.Join(idx.Tags, ", ")))
		}
		if idx.Description != "" {
			sb.WriteString(fmt.Sprintf(" — %s", idx.Description))
		}
		sb.WriteString("\n")
	}

	if count == 0 {
		sb.WriteString("No exposed indexes found.\n")
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
		Description: "Semantic search across indexed code and documents. Returns ranked text passages with their source file paths. Each result shows the file path in a 'Source:' line — use gleann_read with that path to read the full source file. Workflow: search → identify relevant file from results → gleann_read to get complete code.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to search, or a tag collection starting with '@' (e.g. '@work') to search across all indexes with that tag.",
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
				"include_tests": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, includes test files and test functions without score demotion. Default is false (test code is demoted in favor of production code).",
				},
				"kind": map[string]interface{}{
					"type":        "string",
					"description": "Filter by content type: 'code' (source code), 'docs' (documentation and markdown), or 'all'. Default is 'all'.",
					"enum":        []string{"all", "code", "docs"},
				},
				"rerank": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, re-score candidates using a cross-encoder reranker for higher precision.",
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

	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	// Auto-resolve index if empty
	if indexName == "" {
		indexName = s.resolveIndexName()
		if indexName == "" {
			if indexes, err := gleann.ListIndexes(s.config.IndexDir); err == nil {
				var count int
				for _, idx := range indexes {
					if idx.IsMCPExposed() {
						count++
					}
				}
				if count > 1 {
					indexName = "@all"
				}
			}
		}
	}

	topK := 5
	if limit, ok := args["top_k"].(float64); ok {
		topK = int(limit)
	}

	searchOpts := []gleann.SearchOption{gleann.WithTopK(topK)}
	if filters, logic := parseFilters(args); len(filters) > 0 {
		searchOpts = append(searchOpts, gleann.WithMetadataFilter(filters...))
		searchOpts = append(searchOpts, gleann.WithFilterLogic(logic))
	}
	if gc, ok := args["graph_context"].(bool); ok && gc {
		searchOpts = append(searchOpts, gleann.WithGraphContext(true))
	}
	if incTests, ok := args["include_tests"].(bool); ok {
		searchOpts = append(searchOpts, gleann.WithIncludeTests(incTests))
	}
	if kind, ok := args["kind"].(string); ok && kind != "" {
		searchOpts = append(searchOpts, gleann.WithKind(kind))
	}
	if rerank, ok := args["rerank"].(bool); ok && rerank {
		searchOpts = append(searchOpts, gleann.WithReranker(true))
	}

	var results []gleann.SearchResult

	// Support federated search across tag collections (e.g. index="@work" or "@all")
	if strings.HasPrefix(indexName, "@") {
		tagName := strings.TrimPrefix(indexName, "@")
		var matchingIndexes []gleann.IndexMeta
		var err error
		if tagName == "all" || tagName == "" {
			matchingIndexes, err = gleann.ListIndexes(s.config.IndexDir)
		} else {
			matchingIndexes, err = gleann.ListIndexesByTag(s.config.IndexDir, tagName)
		}
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error listing indexes: %v", err)), nil
		}
		if len(matchingIndexes) == 0 {
			return mcp.NewToolResultText("No indexes found. Create an index first with: gleann index build <name> --docs <dir>"), nil
		}

		tagEnv := os.Getenv("GLEANN_TAGS")
		var targetNames []string
		for _, idx := range matchingIndexes {
			if !idx.IsMCPExposed() {
				continue
			}
			if tagEnv != "" {
				matched := false
				for _, t := range strings.Split(tagEnv, ",") {
					if idx.HasTag(t) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			targetNames = append(targetNames, idx.Name)
		}

		if len(targetNames) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No accessible indexes found with tag %q.", tagName)), nil
		}

		for _, name := range targetNames {
			searcher, err := s.getSearcher(name)
			if err != nil {
				continue
			}
			res, err := searcher.Search(ctx, query, searchOpts...)
			if err != nil {
				continue
			}
			for _, r := range res {
				if r.Metadata == nil {
					r.Metadata = make(map[string]any)
				}
				r.Metadata["_index"] = name
				results = append(results, r)
			}
		}

		// Sort merged results descending by score
		sort.Slice(results, func(i, j int) bool {
			return results[i].Score > results[j].Score
		})
		if len(results) > topK {
			results = results[:topK]
		}
	} else {
		searcher, err := s.getSearcher(indexName)
		if err != nil {
			if s.isSyncRunning(indexName) {
				return mcp.NewToolResultError(fmt.Sprintf("Index %q is currently being built in the background. Please wait a moment for initial indexing to complete.", indexName)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
		}

		res, err := searcher.Search(ctx, query, searchOpts...)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error searching memory: %v", err)), nil
		}
		results = res
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No relevant memory fragments found."), nil
	}

	// Apply Context Field Theory (Φ) re-ranking when graph context is requested.
	// Build lightweight signals from graph structure data if available.
	if gc, ok := args["graph_context"].(bool); ok && gc {
		cft := gleann.DefaultContextField()
		signalMap := make(map[string]gleann.ContextSignal, len(results))
		for _, r := range results {
			source, _ := r.Metadata["source"].(string)
			structScore := 0.0
			if r.GraphContext != nil {
				// Degree centrality proxy: normalise caller+callee count.
				deg := len(r.GraphContext.Symbols)
				if deg > 0 {
					structScore = math.Min(1.0, float64(deg)/10.0)
				}
			}
			signalMap[source] = gleann.ContextSignal{
				SemanticScore:  float64(r.Score),
				StructureScore: structScore,
			}
		}
		results = cft.EnrichSearchResults(results, signalMap)
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("---\nResult [%d] (Score: %.4f)\n", i+1, r.Score))
		if metaSource, ok := r.Metadata["source"]; ok {
			sb.WriteString(fmt.Sprintf("Source: %v\n", metaSource))
		}
		if idxName, ok := r.Metadata["_index"].(string); ok {
			sb.WriteString(fmt.Sprintf("Index: %s\n", idxName))
		}
		sb.WriteString(r.Text)
		sb.WriteString("\n")

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

	// Collect unique source files for the footer hint
	sourceFiles := make(map[string]struct{})
	for _, r := range results {
		if src, ok := r.Metadata["source"].(string); ok && src != "" {
			sourceFiles[src] = struct{}{}
		}
	}
	if len(sourceFiles) > 0 {
		sb.WriteString("\n---\nTip: To read the full source code of any file above, use gleann_read with the Source path.\n")
	}

	// Log to active session if one is running.
	s.sessionLog("search", indexName, query, len(results))

	return mcp.NewToolResultText(sb.String()), nil
}

// --- List Tool ---

func (s *Server) buildListTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_list",
		Description: "List all available gleann indexes with their metadata (name, backend, model, passage count, tags, description).",
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

	tagEnv := os.Getenv("GLEANN_TAGS")

	var sb strings.Builder
	for _, idx := range indexes {
		if !idx.IsMCPExposed() {
			continue
		}
		if tagEnv != "" {
			matched := false
			for _, t := range strings.Split(tagEnv, ",") {
				if idx.HasTag(t) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		tagStr := ""
		if len(idx.Tags) > 0 {
			tagStr = fmt.Sprintf(", tags=[%s]", strings.Join(idx.Tags, ", "))
		}
		descStr := ""
		if idx.Description != "" {
			descStr = fmt.Sprintf(" - %s", idx.Description)
		}
		sb.WriteString(fmt.Sprintf("- %s: %d passages, backend=%s, model=%s%s%s\n", idx.Name, idx.NumPassages, idx.Backend, idx.EmbeddingModel, tagStr, descStr))
	}

	if sb.Len() == 0 {
		return mcp.NewToolResultText("No accessible indexes found for MCP."), nil
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
	if s.config.LLMModel != "" {
		chatConfig.Model = s.config.LLMModel
	}
	if s.config.LLMProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(s.config.LLMProvider)
	}
	if s.config.OllamaHost != "" {
		chatConfig.BaseURL = s.config.OllamaHost
	}
	if s.config.OpenAIAPIKey != "" {
		chatConfig.APIKey = s.config.OpenAIAPIKey
	}
	if s.config.OpenAIBaseURL != "" && chatConfig.Provider == gleann.LLMOpenAI {
		chatConfig.BaseURL = s.config.OpenAIBaseURL
	}
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
		Description: "Find caller/callee relationships for a code symbol in the AST graph. Input a fully-qualified name (e.g. 'MyClass::method') or partial name. Returns who calls this symbol and what it calls. Use gleann_search first to discover symbol names if you don't know the exact FQN.",
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

	const maxNeighbors = 25
	var prodCallers, testCallers []gleann.Callee
	for _, c := range callers {
		if c.IsTest {
			testCallers = append(testCallers, c)
		} else {
			prodCallers = append(prodCallers, c)
		}
	}

	sb.WriteString(fmt.Sprintf("=== Callers (%d production, %d test) ===\n", len(prodCallers), len(testCallers)))
	if len(prodCallers) == 0 {
		sb.WriteString("No production callers found.\n")
	} else {
		for i, c := range prodCallers {
			if i >= maxNeighbors {
				sb.WriteString(fmt.Sprintf("... and %d more production callers\n", len(prodCallers)-maxNeighbors))
				break
			}
			loc := ""
			if c.File != "" {
				if c.Line > 0 {
					loc = fmt.Sprintf(" [%s:%d]", c.File, c.Line)
				} else {
					loc = fmt.Sprintf(" [%s]", c.File)
				}
			}
			sb.WriteString(fmt.Sprintf("- %s (%s)%s\n", c.FQN, c.Kind, loc))
		}
	}
	if len(testCallers) > 0 {
		sb.WriteString(fmt.Sprintf("\nTest Callers (%d total):\n", len(testCallers)))
		for i, c := range testCallers {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... and %d more test callers\n", len(testCallers)-10))
				break
			}
			loc := ""
			if c.File != "" {
				if c.Line > 0 {
					loc = fmt.Sprintf(" [%s:%d]", c.File, c.Line)
				} else {
					loc = fmt.Sprintf(" [%s]", c.File)
				}
			}
			sb.WriteString(fmt.Sprintf("  [test] %s (%s)%s\n", c.FQN, c.Kind, loc))
		}
	}

	sb.WriteString(fmt.Sprintf("\n=== Callees (Symbols this node calls - %d total) ===\n", len(callees)))
	if len(callees) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for i, c := range callees {
			if i >= maxNeighbors {
				sb.WriteString(fmt.Sprintf("... and %d more callees\n", len(callees)-maxNeighbors))
				break
			}
			loc := ""
			if c.File != "" {
				if c.Line > 0 {
					loc = fmt.Sprintf(" [%s:%d]", c.File, c.Line)
				} else {
					loc = fmt.Sprintf(" [%s]", c.File)
				}
			}
			sb.WriteString(fmt.Sprintf("- %s (%s)%s\n", c.FQN, c.Kind, loc))
		}
	}

	if len(callers) == 0 && len(callees) == 0 {
		if matches, err := db.SymbolSearch(nodeFqn); err == nil && len(matches) > 0 {
			sb.WriteString("\nTip: No direct relationships found for this exact name. Did you mean one of these symbols?\n")
			limit := len(matches)
			if limit > 5 {
				limit = 5
			}
			for _, m := range matches[:limit] {
				sb.WriteString(fmt.Sprintf("  - %s (%s)\n", m.FQN, m.Kind))
			}
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

func (s *Server) buildReadFullDocumentTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_read_full_document",
		Description: "Retrieve the complete text of an indexed document (e.g. markdown docs, architecture notes) using its virtual path. For reading source code files, use gleann_read instead — it's faster and supports mode-aware compression.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to query",
				},
				"vpath": map[string]interface{}{
					"type":        "string",
					"description": "The virtual or relative document path (e.g. 'docs/architecture.md')",
				},
			},
			Required: []string{"index", "vpath"},
		},
	}
}

func (s *Server) handleReadFullDocument(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	vpath, _ := args["vpath"].(string)

	if indexName == "" || vpath == "" {
		return mcp.NewToolResultError("index and vpath are required"), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err == nil && searcher != nil {
		db := searcher.GraphDB()
		if db != nil {
			if content, err := db.FullDocument(vpath); err == nil && content != "" {
				return mcp.NewToolResultText(content), nil
			}
		}
	}

	// Fallback to direct file read if path exists on disk
	if data, err := os.ReadFile(vpath); err == nil && len(data) > 0 {
		return mcp.NewToolResultText(string(data)), nil
	}
	if searcher != nil && searcher.Meta().SourceDir != "" {
		cand := filepath.Join(searcher.Meta().SourceDir, vpath)
		if data, err := os.ReadFile(cand); err == nil && len(data) > 0 {
			return mcp.NewToolResultText(string(data)), nil
		}
	}

	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	return mcp.NewToolResultError(fmt.Sprintf("document %q not found in index %q (or on disk). Do not guess markdown file paths. Use gleann_search to locate relevant passages, or check available documents using gleann_document_toc.", vpath, indexName)), nil
}

// --- Document TOC & Structure Tool ---

func (s *Server) buildDocumentTOCTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_document_toc",
		Description: "Inspect the hierarchical Table of Contents (TOC) and heading outline for an indexed document, or list all indexed documents. Enables AI agents to understand document structure and sections before reading full content.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to query",
				},
				"vpath": map[string]interface{}{
					"type":        "string",
					"description": "Virtual or relative document path (e.g. 'docs/architecture.md'). If omitted, lists all indexed documents.",
				},
				"format": map[string]interface{}{
					"type":        "string",
					"description": "Output format: 'outline' (indented markdown hierarchy) or 'json'. Defaults to 'outline'.",
					"enum":        []interface{}{"outline", "json"},
				},
			},
			Required: []string{"index"},
		},
	}
}

func (s *Server) handleDocumentTOC(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	vpath, _ := args["vpath"].(string)
	format, _ := args["format"].(string)
	if format == "" {
		format = "outline"
	}

	if indexName == "" {
		return mcp.NewToolResultError("index is required"), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}
	db := searcher.GraphDB()
	if db == nil {
		return mcp.NewToolResultError(fmt.Sprintf("graph database not available for index %q", indexName)), nil
	}

	// 1. If vpath is empty, list all indexed documents
	if vpath == "" {
		docs, err := db.ListDocuments()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list documents in %q: %v", indexName, err)), nil
		}
		if format == "json" {
			data, _ := json.MarshalIndent(docs, "", "  ")
			return mcp.NewToolResultText(string(data)), nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "📑 Indexed Documents in %q (%d total):\n\n", indexName, len(docs))
		if len(docs) == 0 {
			sb.WriteString("No documents found in graph index.\n")
		}
		for _, doc := range docs {
			folderInfo := ""
			if doc.Folder != "" {
				folderInfo = fmt.Sprintf(" [folder: %s]", doc.Folder)
			}
			fmt.Fprintf(&sb, "- **%s** (%d headings)%s\n", doc.VPath, doc.TotalNodes, folderInfo)
			if doc.Summary != "" {
				fmt.Fprintf(&sb, "  *Summary:* %s\n", doc.Summary)
			}
		}
		return mcp.NewToolResultText(sb.String()), nil
	}

	// 2. Specific document TOC
	toc, err := db.DocumentTOC(vpath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("document %q not found in graph index %q: %v", vpath, indexName, err)), nil
	}

	if format == "json" {
		data, _ := json.MarshalIndent(toc, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Document: %s\n", toc.Title)
	fmt.Fprintf(&sb, "- **Path:** `%s`\n", toc.VPath)
	if toc.Folder != "" {
		fmt.Fprintf(&sb, "- **Folder:** `%s`\n", toc.Folder)
	}
	if toc.Summary != "" {
		fmt.Fprintf(&sb, "- **Summary:** %s\n", toc.Summary)
	}
	fmt.Fprintf(&sb, "- **Total Headings:** %d\n\n", toc.TotalNodes)
	sb.WriteString("## Table of Contents\n\n")

	if len(toc.Headings) == 0 {
		sb.WriteString("*(No headings indexed)*\n")
	} else {
		formatHeadingsOutline(&sb, toc.Headings, 0)
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func formatHeadingsOutline(sb *strings.Builder, headings []gleann.DocumentHeadingItem, indent int) {
	prefix := strings.Repeat("  ", indent)
	for _, h := range headings {
		fmt.Fprintf(sb, "%s- [H%d] %s\n", prefix, h.Level, h.Title)
		if len(h.Children) > 0 {
			formatHeadingsOutline(sb, h.Children, indent+1)
		}
	}
}

// --- Impact Analysis Tool ---

func (s *Server) buildImpactTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_impact",
		Description: "Analyze blast radius of changing a code symbol. Returns direct callers, transitive callers (BFS), and affected files ranked by relevance (core files first, submodules last). Use gleann_search or gleann_graph_neighbors first to find the exact symbol FQN.",
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

	const maxItems = 20

	sb.WriteString(fmt.Sprintf("=== Direct Callers (%d total) ===\n", len(impact.DirectCallers)))
	if len(impact.DirectCallers) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for i, c := range impact.DirectCallers {
			if i >= maxItems {
				sb.WriteString(fmt.Sprintf("... and %d more direct callers\n", len(impact.DirectCallers)-maxItems))
				break
			}
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
	}

	sb.WriteString(fmt.Sprintf("\n=== Transitive Callers (%d total) ===\n", len(impact.TransitiveCallers)))
	if len(impact.TransitiveCallers) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for i, c := range impact.TransitiveCallers {
			if i >= maxItems {
				sb.WriteString(fmt.Sprintf("... and %d more transitive callers\n", len(impact.TransitiveCallers)-maxItems))
				break
			}
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
	}

	// Partition affected files so core project files appear before noise/submodule files
	var cleanFiles, noiseFiles []string
	for _, f := range impact.AffectedFiles {
		if isNoisePath(f) {
			noiseFiles = append(noiseFiles, f)
		} else {
			cleanFiles = append(cleanFiles, f)
		}
	}
	orderedFiles := append(cleanFiles, noiseFiles...)

	sb.WriteString(fmt.Sprintf("\n=== Affected Files (%d total: %d core, %d submodules/tests) ===\n",
		len(impact.AffectedFiles), len(cleanFiles), len(noiseFiles)))
	if len(orderedFiles) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for i, f := range orderedFiles {
			if i >= maxItems {
				sb.WriteString(fmt.Sprintf("... and %d more affected files\n", len(orderedFiles)-maxItems))
				break
			}
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}

	total := len(impact.DirectCallers) + len(impact.TransitiveCallers)
	sb.WriteString(fmt.Sprintf("\nTotal blast radius: %d affected symbols across %d files\n", total, len(impact.AffectedFiles)))

	if total == 0 {
		if matches, err := db.SymbolSearch(symbol); err == nil && len(matches) > 0 {
			sb.WriteString("\nTip: No callers found for this exact name. Did you mean one of these symbols in the graph?\n")
			limit := len(matches)
			if limit > 5 {
				limit = 5
			}
			for _, m := range matches[:limit] {
				sb.WriteString(fmt.Sprintf("  - %s (%s)\n", m.FQN, m.Kind))
			}
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// --- Graph Stats Tool ---

func (s *Server) buildGraphStatsTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_graph_stats",
		Description: "Get statistics about the code graph for an index: file count, symbol count, edge counts by type.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to query",
				},
			},
			Required: []string{"index"},
		},
	}
}

func (s *Server) handleGraphStats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	if indexName == "" {
		return mcp.NewToolResultError("index is required"), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	db := searcher.GraphDB()
	if db == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Graph database not available for index %q", indexName)), nil
	}

	stats, err := db.Stats()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get graph stats: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Graph Statistics — %s\n\n", indexName))
	sb.WriteString(fmt.Sprintf("  Files:           %d\n", stats.Files))
	sb.WriteString(fmt.Sprintf("  Symbols:         %d\n", stats.Symbols))
	sb.WriteString(fmt.Sprintf("  Call edges:      %d\n", stats.CallEdges))
	sb.WriteString(fmt.Sprintf("  Declare edges:   %d\n", stats.DeclareEdges))
	sb.WriteString(fmt.Sprintf("  Implements edges: %d\n", stats.ImplementsEdges))

	return mcp.NewToolResultText(sb.String()), nil
}

// --- Symbols In File Tool ---

func (s *Server) buildSymbolsInFileTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_symbols_in_file",
		Description: "List all symbols (functions, methods, types) defined in a specific source file. Useful for understanding file contents without reading the full source.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to query",
				},
				"file_path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the source file (relative to project root)",
				},
			},
			Required: []string{"index", "file_path"},
		},
	}
}

func (s *Server) handleSymbolsInFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	filePath, _ := args["file_path"].(string)

	if indexName == "" || filePath == "" {
		return mcp.NewToolResultError("index and file_path are required"), nil
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err)), nil
	}

	db := searcher.GraphDB()
	if db == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Graph database not available for index %q", indexName)), nil
	}

	symbols, err := db.SymbolsInFile(filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error querying symbols: %v", err)), nil
	}

	if len(symbols) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No symbols found in file %q (index: %s)", filePath, indexName)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Symbols in %s (%d found):\n\n", filePath, len(symbols)))
	for _, sym := range symbols {
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", sym.Kind, sym.FQN))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// --- Sync Tool ---

func (s *Server) buildSyncTool() mcp.Tool {
	return mcp.NewTool("gleann_sync",
		mcp.WithDescription("Synchronize, update, or initialize a Gleann index (vector search passages, AST code graph, and document outlines) with workspace files. Defaults to fast 'code' mode. For large repositories, synchronization runs asynchronously in the background. If in_progress is returned, DO NOT sleep or block in a loop; immediately inform the user that background synchronization is in progress and continue."),
		mcp.WithString("index",
			mcp.Description("Name of the index to synchronize or create. If omitted, automatically inferred from the workspace directory name."),
		),
		mcp.WithString("docs_dir",
			mcp.Description("Root directory of the codebase/documents. Defaults to the current workspace directory or the source directory recorded when the index was built."),
		),
		mcp.WithString("mode",
			mcp.Description("Indexing mode: 'code' (fast: source code & AST code graph only, skips office documents and plugins; default and recommended for coding agents), 'docs' (office documents and markdown only), 'all' (both code and office documents)."),
			mcp.Enum("code", "docs", "all"),
		),
		mcp.WithBoolean("no_plugins",
			mcp.Description("Explicitly disable external document extraction plugins (e.g. markitdown). Defaults to true when mode is 'code'."),
		),
		mcp.WithArray("files",
			mcp.Description("Optional list of specific file paths to sync. If omitted, all modified, added, and deleted files in the workspace are automatically detected and synced."),
			mcp.Items(map[string]any{"type": "string"}),
		),
	)
}

func (s *Server) isSyncRunning(indexName string) bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	task, exists := s.syncTasks[indexName]
	if !exists {
		return false
	}
	select {
	case <-task.done:
		return false
	default:
		return true
	}
}

func (s *Server) handleSync(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var indexName string
	if args, ok := request.Params.Arguments.(map[string]interface{}); ok && args != nil {
		if idx, ok := args["index"].(string); ok {
			indexName = strings.TrimSpace(idx)
		}
	}

	cwd, _ := os.Getwd()

	// Auto-resolve indexName if omitted
	if indexName == "" {
		if envIdx := os.Getenv("GLEANN_INDEX"); envIdx != "" {
			indexName = envIdx
		} else if cwd != "" && cwd != "/" {
			indexName = strings.ToLower(filepath.Base(cwd))
		}
	}
	if indexName == "" {
		return mcp.NewToolResultError("missing parameter: index (and could not auto-resolve from workspace directory)"), nil
	}

	meta, metaErr := gleann.GetIndexMeta(s.config.IndexDir, indexName)
	isNew := (metaErr != nil)

	docsDir := request.GetString("docs_dir", "")
	if docsDir == "" && meta != nil {
		docsDir = meta.SourceDir
	}
	// Fall back to current working directory if docs_dir is not provided
	if docsDir == "" && cwd != "" && cwd != "/" {
		docsDir = cwd
	}
	if docsDir == "" {
		if isNew {
			return mcp.NewToolResultError(fmt.Sprintf("index %q does not exist; please provide 'docs_dir' to build it", indexName)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("index %q does not have a recorded source_dir; please specify docs_dir", indexName)), nil
	}

	mode := strings.ToLower(request.GetString("mode", "code"))
	if mode == "" {
		mode = "code"
	}
	if mode != "code" && mode != "docs" && mode != "all" {
		return mcp.NewToolResultError(fmt.Sprintf("invalid mode %q: must be 'code', 'docs', or 'all'", mode)), nil
	}

	noPlugins := (mode == "code")
	if args, ok := request.Params.Arguments.(map[string]interface{}); ok && args != nil {
		if np, ok := args["no_plugins"].(bool); ok {
			noPlugins = np
		}
	}

	var files []string
	if args, ok := request.Params.Arguments.(map[string]interface{}); ok && args != nil {
		if rawFiles, ok := args["files"].([]any); ok {
			for _, rf := range rawFiles {
				if str, ok := rf.(string); ok && strings.TrimSpace(str) != "" {
					files = append(files, strings.TrimSpace(str))
				}
			}
		} else if rawFiles, ok := args["files"].([]string); ok {
			for _, str := range rawFiles {
				if strings.TrimSpace(str) != "" {
					files = append(files, strings.TrimSpace(str))
				}
			}
		}
	}

	// Evict cached handles to release file locks before synchronizing
	s.evictIndex(indexName)

	runner := s.syncRunner
	if runner == nil {
		runner = s.defaultSyncRunner
	}

	opts := SyncOptions{
		IndexName: indexName,
		DocsDir:   docsDir,
		Files:     files,
		Mode:      mode,
		NoPlugins: noPlugins,
		IsNew:     isNew,
	}

	s.syncMu.Lock()
	if existing, running := s.syncTasks[indexName]; running {
		select {
		case <-existing.done:
			// Previous background task finished! Return its result
			taskErr := existing.err
			taskOut := existing.output
			taskWasNew := existing.isNew
			delete(s.syncTasks, indexName)
			s.syncMu.Unlock()

			action := "sync"
			if taskWasNew {
				action = "build"
			}
			if taskErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("%s failed for index %q: %v\nOutput: %s", action, indexName, taskErr, taskOut)), nil
			}

			actionMsg := "Index synchronized successfully"
			if taskWasNew {
				actionMsg = "Index built successfully"
			}

			res := map[string]any{
				"status":  "success",
				"index":   indexName,
				"mode":    existing.mode,
				"action":  actionMsg,
				"message": actionMsg,
				"details": strings.TrimSpace(taskOut),
			}
			resBytes, _ := json.MarshalIndent(res, "", "  ")
			return mcp.NewToolResultText(string(resBytes)), nil

		default:
			// Task is already actively running! Prevent duplicate process!
			s.syncMu.Unlock()
			elapsed := time.Since(existing.startTime).Round(time.Second)
			actionMsg := "Indexing"
			if !existing.isNew {
				actionMsg = "Sync"
			}
			res := map[string]any{
				"status":                "in_progress",
				"index":                 indexName,
				"mode":                  existing.mode,
				"action":                "in_progress",
				"message":               fmt.Sprintf("%s for %q is actively running in the background (started %s ago). DO NOT sleep or block. The current index remains available for search. You must immediately inform the user that synchronization is actively progressing in the background and proceed with other tasks.", actionMsg, indexName, elapsed),
				"elapsed":               elapsed.String(),
				"instruction_for_agent": "CRITICAL: Do NOT execute sleep commands or poll repeatedly in a loop. Inform the user immediately that background synchronization is active and continue.",
			}
			resBytes, _ := json.MarshalIndent(res, "", "  ")
			return mcp.NewToolResultText(string(resBytes)), nil
		}
	}

	// Create and register new background task
	task := &syncTask{
		indexName: indexName,
		mode:      mode,
		isNew:     isNew,
		startTime: time.Now(),
		done:      make(chan struct{}),
	}
	s.syncTasks[indexName] = task
	s.syncMu.Unlock()

	go func() {
		defer close(task.done)
		// Detach context so client timeout does not sever indexing
		out, runErr := runner(context.Background(), opts)
		s.syncMu.Lock()
		task.output = out
		task.err = runErr
		s.syncMu.Unlock()
	}()

	waitTimeout := s.syncWaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = 5 * time.Second
	}

	// Wait up to waitTimeout. If small repo or incremental sync, return immediately.
	// If large repo, return in_progress so MCP client NEVER hits request timeout.
	select {
	case <-task.done:
		s.syncMu.Lock()
		delete(s.syncTasks, indexName)
		taskErr := task.err
		taskOut := task.output
		s.syncMu.Unlock()

		action := "sync"
		if isNew {
			action = "build"
		}
		if taskErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("%s failed for index %q: %v\nOutput: %s", action, indexName, taskErr, taskOut)), nil
		}

		actionMsg := "Index synchronized successfully"
		if isNew {
			actionMsg = "Index built successfully"
		}

		if len(opts.Files) > 0 {
			if mgr, err := s.blockMem.get(); err == nil {
				_, _ = mgr.MarkSuspect(opts.Files, nil)
			}
		}

		res := map[string]any{
			"status":  "success",
			"index":   indexName,
			"mode":    mode,
			"action":  actionMsg,
			"message": actionMsg,
			"details": strings.TrimSpace(taskOut),
		}
		resBytes, _ := json.MarshalIndent(res, "", "  ")
		return mcp.NewToolResultText(string(resBytes)), nil

	case <-time.After(waitTimeout):
		actionMsg := "Indexing started in background"
		if isNew {
			actionMsg = "Initial index build started in background"
		}
		elapsedStr := waitTimeout.String()
		res := map[string]any{
			"status":                "in_progress",
			"index":                 indexName,
			"mode":                  mode,
			"action":                actionMsg,
			"message":               fmt.Sprintf("%s for %q. Because this is a large codebase, indexing is progressing asynchronously in the background. The current index remains available for search. DO NOT sleep or block.", actionMsg, indexName),
			"elapsed":               elapsedStr,
			"instruction_for_agent": "CRITICAL: Do NOT execute sleep commands or poll in a loop. Inform the user immediately that background synchronization has started and continue.",
		}
		resBytes, _ := json.MarshalIndent(res, "", "  ")
		return mcp.NewToolResultText(string(resBytes)), nil
	}
}

func (s *Server) defaultSyncRunner(ctx context.Context, opts SyncOptions) (string, error) {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "gleann"
	}

	var cmdArgs []string
	if opts.IsNew {
		cmdArgs = []string{"index", "build", opts.IndexName, "--docs", opts.DocsDir, "--graph", "--mode", opts.Mode}
	} else {
		cmdArgs = []string{"index", "sync", opts.IndexName, "--docs", opts.DocsDir, "--graph", "--mode", opts.Mode}
		if len(opts.Files) > 0 {
			cmdArgs = append(cmdArgs, "--files", strings.Join(opts.Files, ","))
		}
	}
	if opts.NoPlugins {
		cmdArgs = append(cmdArgs, "--no-plugins")
	}
	if s.config.IndexDir != "" {
		cmdArgs = append(cmdArgs, "--index-dir", s.config.IndexDir)
	}

	cmd := exec.CommandContext(ctx, exe, cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// resolveIndexName tries to auto-detect an exposed index name if none is provided.
// It checks GLEANN_INDEX, current working directory match, and single exposed index fallback.
func (s *Server) resolveIndexName() string {
	if envIdx := os.Getenv("GLEANN_INDEX"); envIdx != "" {
		return envIdx
	}
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil {
		return ""
	}
	var exposed []gleann.IndexMeta
	for _, idx := range indexes {
		if idx.IsMCPExposed() {
			exposed = append(exposed, idx)
		}
	}

	// Check if current working directory name matches an index
	if cwd, err := os.Getwd(); err == nil {
		base := filepath.Base(cwd)
		for _, idx := range exposed {
			if strings.EqualFold(idx.Name, base) {
				return idx.Name
			}
		}
	}

	if len(exposed) == 1 {
		return exposed[0].Name
	}
	return ""
}

