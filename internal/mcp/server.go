package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/pathpolicy"
	"github.com/tevfik/gleann/pkg/gleann"
)

// Config holds MCP server configuration.
type Config struct {
	IndexDir          string
	EmbeddingProvider string
	EmbeddingModel    string
	OllamaHost        string
	OllamaAPIKey      string // optional Bearer key for Ollama (auth proxy / hosted)
	OpenAIAPIKey      string
	OpenAIBaseURL     string
	LLMProvider       string
	LLMModel          string
	Version           string
	CleanToolNames    bool
	ToolsProfile      string // "core" (default), "full", or comma-separated list of tool names
	// BuildConfig optionally supplies the full gleann configuration used
	// when gleann_sync builds or syncs an index in-process (backend,
	// chunking, multimodal model, ...). IndexDir is always taken from
	// IndexDir above. When nil, a config derived from the fields above is
	// used.
	BuildConfig *gleann.Config
	// SearcherCacheBudget is the memory budget in bytes for cached searchers (0 = disabled).
	SearcherCacheBudget int64
}

// maxCachedSearchers is the maximum number of searchers to keep in memory.
// When exceeded, the least recently used searcher is evicted.
const maxCachedSearchers = 16

// Server wraps the mark3labs MCP server.
type Server struct {
	mcpServer       *server.MCPServer
	embedder        gleann.EmbeddingComputer
	config          gleann.Config
	cleanToolNames  bool
	toolsProfile    string
	searcherMu      sync.Mutex
	searchers       map[string]*gleann.LeannSearcher
	searcherLRU     []string                               // tracks access order: most recent at end
	searcherRefs    map[*gleann.LeannSearcher]*searcherRef // borrowers of each searcher, guarded by searcherMu
	memPool         *mcpMemoryPool                         // Memory Engine: generic Entity/RELATES_TO graph
	blockMem        *blockMemPool                          // BBolt hierarchical memory blocks (pkg/memory)
	gPool           *graphPool                             // Community detection graph pool
	syncRunner      syncRunnerFunc
	syncMu          sync.Mutex
	syncTasks       map[string]*syncTask
	syncWaitTimeout time.Duration
	// buildConfig is the full configuration used for in-process builds
	// (backend, chunking, multimodal, ...); nil falls back to config.
	buildConfig *gleann.Config
	lifeOnce    sync.Once
	lifeCtx     context.Context
	lifeCancel  context.CancelFunc
	// expander is the shared HyDE query expander (created on first use).
	expandOnce sync.Once
	expander   *gleann.LLMQueryExpander
	// gain tracks cumulative token savings from gleann_shell and gleann_read
	// calls in this server process (reported by gleann_gain); guarded by gainMu.
	gainMu sync.Mutex
	gain   gleann.TokenGain
}

// chatConfig returns the LLM settings of the server configuration.
func (s *Server) chatConfig() gleann.ChatConfig {
	chatConfig := gleann.DefaultChatConfig()
	if s.config.LLMModel != "" {
		chatConfig.Model = s.config.LLMModel
	}
	if s.config.LLMProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(s.config.LLMProvider)
	}
	s.config.ApplyChatEndpoint(&chatConfig)
	return chatConfig
}

// queryExpander returns the shared HyDE expander (expand_model or the chat
// model); its cache is shared by all requests.
func (s *Server) queryExpander() *gleann.LLMQueryExpander {
	s.expandOnce.Do(func() {
		cc := s.chatConfig()
		if m := s.config.SearchConfig.ExpandModel; m != "" {
			cc.Model = m
		}
		cc.MaxTokens = 120
		s.expander = gleann.NewLLMQueryExpander(gleann.NullSearcher{}, cc)
	})
	return s.expander
}

// syncTask tracks a background indexing or synchronization task for an index.
type syncTask struct {
	indexName  string
	mode       string
	isNew      bool
	startTime  time.Time
	done       chan struct{}
	output     string
	err        error
	mu         sync.RWMutex
	phase      string
	pct        float64
	itemsDone  int
	totalItems int
	rate       float64
	etaSec     float64
}

// SyncOptions contains options for synchronizing or building an index via gleann_sync.
type SyncOptions struct {
	IndexName  string
	DocsDir    string
	Files      []string
	Mode       string // "code", "docs", "all"
	NoPlugins  bool
	IsNew      bool
	OnProgress func(phase string, pct float64, done, total int, rate, etaSec float64)
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
	glCfg.OllamaAPIKey = cfg.OllamaAPIKey
	glCfg.OpenAIAPIKey = cfg.OpenAIAPIKey
	glCfg.OpenAIBaseURL = cfg.OpenAIBaseURL
	glCfg.LLMProvider = cfg.LLMProvider
	glCfg.LLMModel = cfg.LLMModel
	if cfg.SearcherCacheBudget > 0 {
		glCfg.SearcherCacheBudget = cfg.SearcherCacheBudget
	}

	baseURL, apiKey := glCfg.EmbeddingEndpoint()
	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(cfg.EmbeddingProvider),
		Model:    cfg.EmbeddingModel,
		BaseURL:  baseURL,
		APIKey:   apiKey,
	})

	// Resolve the effective profile before the SDK server is built: the
	// initialize instructions must describe the tools this server actually
	// registers, including when the profile comes from GLEANN_TOOLS.
	toolsProfile := effectiveToolsProfile(cfg.ToolsProfile)

	s := server.NewMCPServer("gleann-mcp", version,
		server.WithRoots(),
		server.WithInstructions(serverInstructionsFor(toolsProfile)),
	)

	srv := &Server{
		mcpServer:      s,
		config:         glCfg,
		cleanToolNames: cfg.CleanToolNames,
		toolsProfile:   toolsProfile,
		embedder:       embedder,
		searchers:      make(map[string]*gleann.LeannSearcher),
		memPool:        newMCPMemoryPool(cfg.IndexDir),
		blockMem:       &blockMemPool{},
		syncTasks:      make(map[string]*syncTask),
		buildConfig:    cfg.BuildConfig,
	}

	// Wire the VectorSyncer factory (every build).
	srv.wireMemorySyncer(cfg, glCfg, embedder)

	// Register tools natively with the SDK (respecting cleanToolNames)
	srv.addTool(srv.buildSearchTool(), srv.handleSearch)
	srv.addTool(srv.buildSearchMultiTool(), srv.handleSearchMulti)
	srv.addTool(srv.buildDefTool(), srv.handleDef)
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
	srv.addTool(srv.buildMemoryFailureAddTool(), srv.handleMemoryFailureAdd)
	srv.addTool(srv.buildMemoryFailureListTool(), srv.handleMemoryFailureList)

	// Batch query — run multiple questions concurrently.
	srv.addTool(srv.buildBatchAskTool(), srv.handleBatchAsk)

	// Memory Engine tools — external agents can manipulate the knowledge graph directly.
	srv.addTool(srv.buildInjectKGTool(), srv.handleInjectKG)
	srv.addTool(srv.buildDeleteEntityTool(), srv.handleDeleteEntity)
	srv.addTool(srv.buildTraverseKGTool(), srv.handleTraverseKG)

	// Graph stats + symbols_in_file.
	srv.addTool(srv.buildGraphStatsTool(), srv.handleGraphStats)
	srv.addTool(srv.buildSymbolsInFileTool(), srv.handleSymbolsInFile)

	// Shell compression + mode-aware file read + token gain tracking.
	srv.addTool(srv.buildShellTool(), srv.handleShell)
	srv.addTool(srv.buildReadTool(), srv.handleRead)
	srv.addTool(srv.buildGainTool(), srv.handleGain)

	// On-demand index synchronization tool
	srv.addTool(srv.buildSyncTool(), srv.handleSync)

	// Community detection tools (SQLite code graph, every build).
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
	return toolEnabledForProfile(s.toolsProfile, name)
}

// effectiveToolsProfile resolves the configured tools profile, falling back to
// GLEANN_TOOLS and then to "core". It runs before the SDK server (and therefore
// the initialize instructions) is built.
func effectiveToolsProfile(profile string) string {
	if p := strings.TrimSpace(profile); p != "" {
		return p
	}
	if env := strings.TrimSpace(os.Getenv("GLEANN_TOOLS")); env != "" {
		return env
	}
	return "core"
}

// toolEnabledForProfile reports whether name is registered for profile. An
// empty profile means the default "core" set.
func toolEnabledForProfile(profile, name string) bool {
	profile = strings.TrimSpace(strings.ToLower(profile))
	if profile == "full" || profile == "all" {
		return true
	}
	if profile == "" || profile == "core" {
		switch name {
		case "gleann_search", "gleann_read", "gleann_def", "gleann_graph_neighbors", "gleann_impact",
			"memory_remember", "memory_failure_add", "memory_context", "memory_search", "memory_forget", "gleann_sync":
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
	// Annotations are keyed by the canonical name, so attach them before
	// --clean-names strips a prefix.
	annotateTool(&tool)
	if s.cleanToolNames {
		tool.Name = strings.TrimPrefix(tool.Name, "gleann_")
	}
	s.mcpServer.AddTool(tool, handler)
}

func (s *Server) Run() {
	log.Println("gleann MCP server starting with SDK (stdio)...")
	// Start stdio transport
	if err := server.ServeStdio(s.mcpServer); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
			log.Println("gleann MCP server stopped cleanly")
			return
		}
		log.Fatalf("Server error: %v", err)
	}
}

// lifetime returns a context cancelled by Close; background index builds
// started by gleann_sync are bound to it.
func (s *Server) lifetime() context.Context {
	s.lifeOnce.Do(func() {
		s.lifeCtx, s.lifeCancel = context.WithCancel(context.Background())
	})
	return s.lifeCtx
}

// Close releases all resources held by the MCP server (graph and memory handles, etc.)
// and cancels in-flight background index builds.
func (s *Server) Close() {
	s.lifetime()
	s.lifeCancel()
	if s.memPool != nil {
		s.memPool.closeAll()
	}
	if s.blockMem != nil {
		s.blockMem.close()
	}
	s.searcherMu.Lock()
	var toClose []*gleann.LeannSearcher
	for _, searcher := range s.searchers {
		if sr := s.retireSearcherLocked(searcher); sr != nil {
			toClose = append(toClose, sr)
		}
	}
	s.searchers = make(map[string]*gleann.LeannSearcher)
	s.searcherLRU = nil
	s.searcherMu.Unlock()
	for _, sr := range toClose {
		sr.Close()
	}
	s.closeGraphPool()
}

// acquireSearcher returns the cached searcher for name (loading it when
// needed) and a release func the caller must call exactly once when done. A
// searcher replaced or evicted while borrowed stays open until its last
// borrower releases it.
func (s *Server) acquireSearcher(name string) (*gleann.LeannSearcher, func(), error) {
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
		return nil, nil, fmt.Errorf("index name required (use gleann_list to see available indexes)")
	}
	if err := gleann.ValidateName(name); err != nil {
		return nil, nil, err
	}

	// Verify index is exposed to MCP
	meta, err := gleann.GetIndexMeta(s.config.IndexDir, name)
	if err == nil {
		if !meta.IsMCPExposed() {
			return nil, nil, fmt.Errorf("access denied: index %q is private and not exposed to MCP", name)
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
				return nil, nil, fmt.Errorf("access denied: index %q does not match required tags (%s)", name, tagEnv)
			}
		}
	}

	s.searcherMu.Lock()
	if searcher, ok := s.searchers[name]; ok {
		s.touchLRU(name)
		release := s.retainSearcherLocked(searcher)
		s.searcherMu.Unlock()
		// Another process (a CLI sync or build) may have written to the index
		// since this searcher loaded: one small read decides. A delta-only
		// change (an incremental sync) is absorbed in place; anything else
		// replaces the searcher, which closes when its last borrower releases
		// it.
		ctx := context.Background()
		change, serr := searcher.Change(ctx)
		if change == gleann.IndexDeltaChanged && serr == nil {
			if rerr := searcher.RefreshDelta(ctx); rerr == nil {
				return searcher, release, nil
			} else {
				serr = rerr
			}
		}
		if change == gleann.IndexUnchanged && serr == nil {
			return searcher, release, nil
		}
		if serr != nil {
			log.Printf("ℹ️  Index %q freshness check failed (%v); reloading", name, serr)
		}
		release()
		s.retireStale(name, searcher)
	} else {
		s.searcherMu.Unlock()
	}

	embedder := s.embedder
	if meta, err := gleann.GetIndexMeta(s.config.IndexDir, name); err == nil && meta.EmbeddingModel != "" {
		if embedder == nil || embedder.ModelName() != meta.EmbeddingModel {
			// Auto-adapt to the index's embedding model using the configured provider
			baseURL, apiKey := s.config.EmbeddingEndpoint()
			embedder = embedding.NewComputer(embedding.Options{
				Provider:    embedding.Provider(s.config.EmbeddingProvider),
				Model:       meta.EmbeddingModel,
				BaseURL:     baseURL,
				APIKey:      apiKey,
				BatchSize:   s.config.BatchSize,
				Concurrency: s.config.Concurrency,
			})
		}
	}

	searcher := gleann.NewSearcher(s.config, embedder)

	// Configure reranker if enabled via config or environment
	if s.config.SearchConfig.UseReranker || os.Getenv("GLEANN_RERANK") == "1" || os.Getenv("GLEANN_RERANK") == "true" {
		cfg := s.config
		if s.config.SearchConfig.RerankerConfig.Model == "" {
			cfg.SearchConfig.RerankerConfig.Model = os.Getenv("GLEANN_RERANK_MODEL")
		}
		if pEnv := os.Getenv("GLEANN_RERANK_PROVIDER"); pEnv != "" {
			cfg.SearchConfig.RerankerConfig.Provider = gleann.RerankerProvider(pEnv)
		}
		searcher.SetReranker(gleann.NewReranker(gleann.ResolveRerankerConfig(cfg, "")))
	}

	ctx := context.Background()
	if err := searcher.Load(ctx, name); err != nil {
		return nil, nil, err
	}

	s.searcherMu.Lock()
	// Double-check in case another goroutine loaded the same index concurrently
	if existing, ok := s.searchers[name]; ok {
		s.touchLRU(name)
		release := s.retainSearcherLocked(existing)
		s.searcherMu.Unlock()
		searcher.Close()
		return existing, release, nil
	}

	s.searchers[name] = searcher
	s.searcherLRU = append(s.searcherLRU, name)
	release := s.retainSearcherLocked(searcher)
	toClose := s.enforceCacheBudgetLocked()
	s.searcherMu.Unlock()
	for _, sr := range toClose {
		sr.Close()
	}
	return searcher, release, nil
}

// searcherRef counts the borrowers of a cached searcher. Guarded by
// Server.searcherMu.
type searcherRef struct {
	refs    int
	retired bool // out of the cache: close when refs drops to 0
}

// retainSearcherLocked counts one more borrower of sr and returns the func that
// releases it (extra calls are no-ops). Caller holds s.searcherMu.
func (s *Server) retainSearcherLocked(sr *gleann.LeannSearcher) func() {
	if s.searcherRefs == nil {
		s.searcherRefs = make(map[*gleann.LeannSearcher]*searcherRef)
	}
	ref := s.searcherRefs[sr]
	if ref == nil {
		ref = &searcherRef{}
		s.searcherRefs[sr] = ref
	}
	ref.refs++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.searcherMu.Lock()
			ref.refs--
			closeNow := ref.retired && ref.refs <= 0
			if ref.refs <= 0 {
				delete(s.searcherRefs, sr)
			}
			s.searcherMu.Unlock()
			if closeNow {
				sr.Close()
			}
		})
	}
}

// retireSearcherLocked takes sr out of use: it returns sr when it can be
// closed right away, or nil when a borrower still holds it (the last release
// closes it). Caller holds s.searcherMu and closes the result after unlocking.
func (s *Server) retireSearcherLocked(sr *gleann.LeannSearcher) *gleann.LeannSearcher {
	if sr == nil {
		return nil
	}
	if ref := s.searcherRefs[sr]; ref != nil && ref.refs > 0 {
		ref.retired = true
		return nil
	}
	delete(s.searcherRefs, sr)
	return sr
}

// retireStale removes sr from the cache when it is still the cached searcher
// for name. It closes as soon as its last borrower releases it.
func (s *Server) retireStale(name string, sr *gleann.LeannSearcher) {
	s.searcherMu.Lock()
	var toClose *gleann.LeannSearcher
	if cur, ok := s.searchers[name]; ok && cur == sr {
		delete(s.searchers, name)
		for i, n := range s.searcherLRU {
			if n == name {
				s.searcherLRU = append(s.searcherLRU[:i], s.searcherLRU[i+1:]...)
				break
			}
		}
		toClose = s.retireSearcherLocked(sr)
	}
	s.searcherMu.Unlock()
	if toClose != nil {
		toClose.Close()
	}
}

func (s *Server) searcherBudget() int64 {
	if env := os.Getenv("GLEANN_SEARCHER_CACHE_BUDGET"); env != "" {
		if b, err := gleann.ParseByteSize(env); err == nil {
			return b
		}
	}
	return s.config.SearcherCacheBudget
}

func (s *Server) enforceCacheBudgetLocked() []*gleann.LeannSearcher {
	var toClose []*gleann.LeannSearcher
	for len(s.searchers) > maxCachedSearchers && len(s.searcherLRU) > 1 {
		if sr := s.evictOldest(); sr != nil {
			toClose = append(toClose, sr)
		}
	}

	budget := s.searcherBudget()
	if budget <= 0 {
		return toClose
	}

	totalSize := int64(0)
	for _, sr := range s.searchers {
		if sr != nil {
			totalSize += sr.EstimatedSize()
		}
	}

	for totalSize > budget && len(s.searcherLRU) > 1 {
		oldest := s.searcherLRU[0]
		var size int64
		if sr, ok := s.searchers[oldest]; ok && sr != nil {
			size = sr.EstimatedSize()
		}
		if sr := s.evictOldest(); sr != nil {
			toClose = append(toClose, sr)
		}
		totalSize -= size
	}
	return toClose
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

// evictOldest removes the least recently used searcher from the cache and
// returns it when it can be closed right away (no borrower). Caller must hold
// s.searcherMu and close the result after unlocking.
func (s *Server) evictOldest() *gleann.LeannSearcher {
	if len(s.searcherLRU) == 0 {
		return nil
	}
	oldest := s.searcherLRU[0]
	s.searcherLRU = s.searcherLRU[1:]
	searcher, ok := s.searchers[oldest]
	if !ok {
		return nil
	}
	delete(s.searchers, oldest)
	return s.retireSearcherLocked(searcher)
}

// evictIndex closes and removes any cached searcher and graph database handle for the given index.
func (s *Server) evictIndex(name string) {
	s.searcherMu.Lock()
	var toClose *gleann.LeannSearcher
	if searcher, ok := s.searchers[name]; ok {
		toClose = s.retireSearcherLocked(searcher)
		delete(s.searchers, name)
		for i, n := range s.searcherLRU {
			if n == name {
				s.searcherLRU = append(s.searcherLRU[:i], s.searcherLRU[i+1:]...)
				break
			}
		}
	}
	s.searcherMu.Unlock()
	if toClose != nil {
		toClose.Close()
	}
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

	searcher, release, err := s.acquireSearcher(indexName)
	if err != nil {
		return nil, fmt.Errorf("failed to load index %q: %v", indexName, err)
	}
	defer release()

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
		Description: "Hybrid semantic + lexical search across indexed code and documents. PREFER this over grep/ripgrep for conceptual, behavioral or cross-file questions (\"how does auth work\", \"where are failed charges retried\"): it matches meaning, spans files and returns ranked passages with Source: paths. Use grep only for exact literal/regex occurrences. Returns ranked passages with source paths; follow with gleann_def for a symbol, gleann_graph_neighbors/gleann_impact for callers, or gleann_read (mode=signatures|map|task) for the full file. For large indexes use gleann_search_ids -> gleann_fetch to keep tokens low.",
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
				"expand": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, an LLM first sketches code that would answer the query and the search uses it too (HyDE). Much better for natural-language 'how/where is X done' questions (benchmark question MRR 0.49 -> 0.63 with a 9B model); adds 1-3 s. Ignored for identifier queries.",
				},
				"focus": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, each result shows only its most query-relevant lines (about a quarter of the passage, chosen by occlusion with the embedding model) instead of the head of the passage. Uses ~4x fewer tokens; adds ~70 ms per result.",
				},
				"max_tokens": map[string]interface{}{
					"type":        "integer",
					"description": "Optional strict token budget for the response (minimum 32). When exceeded, subsequent results degrade to locators (citations only) and extra results are dropped to guarantee context limit.",
				},
			},
			Required: []string{"index", "query"},
		},
	}
}

// parseFilters extracts metadata filters from MCP tool arguments and refuses
// malformed ones: a filter that is not an object, has a key other than field,
// operator and value, or lacks a field or a valid operator (the same rule as
// REST). value is optional: exists needs none.
func parseFilters(args map[string]interface{}) ([]gleann.MetadataFilter, string, error) {
	var filters []gleann.MetadataFilter
	logic := "and"

	if l, ok := args["filter_logic"].(string); ok && (l == "and" || l == "or") {
		logic = l
	}

	rawVal, present := args["filters"]
	if !present || rawVal == nil {
		return nil, logic, nil
	}
	rawFilters, ok := rawVal.([]interface{})
	if !ok {
		return nil, logic, fmt.Errorf("filters must be an array of {field, operator, value} objects")
	}

	for i, rf := range rawFilters {
		fMap, ok := rf.(map[string]interface{})
		if !ok {
			return nil, logic, fmt.Errorf("filters[%d] must be an object with field, operator and value", i)
		}
		for k := range fMap {
			if k != "field" && k != "operator" && k != "value" {
				return nil, logic, fmt.Errorf("filters[%d]: unknown key %q (keys are field, operator, value)", i, k)
			}
		}
		field, _ := fMap["field"].(string)
		opStr, _ := fMap["operator"].(string)
		filters = append(filters, gleann.MetadataFilter{
			Field:    field,
			Operator: gleann.FilterOperator(opStr),
			Value:    fMap["value"],
		})
	}
	if err := gleann.ValidateMetadataFilters(filters); err != nil {
		return nil, logic, err
	}
	return filters, logic, nil
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
	filters, logic, ferr := parseFilters(args)
	if ferr != nil {
		return mcp.NewToolResultError("invalid filters: " + ferr.Error()), nil
	}
	if len(filters) > 0 {
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
	if expand, ok := args["expand"].(bool); ok && expand {
		searchOpts = append(searchOpts, gleann.WithQueryExpander(s.queryExpander()))
	}

	var results []gleann.SearchResult
	var warnings []string // index problems that make the results unreliable

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
			searcher, release, err := s.acquireSearcher(name)
			if err != nil {
				continue
			}
			warnings = append(warnings, searcher.Warnings()...)
			res, err := searcher.Search(ctx, query, searchOpts...)
			release()
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
		searcher, release, err := s.acquireSearcher(indexName)
		if err != nil {
			return s.indexBusyError(indexName, err), nil
		}
		defer release()

		warnings = searcher.Warnings()
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

	if focus, ok := args["focus"].(bool); ok && focus {
		results = s.focusResults(ctx, indexName, query, results)
	}

	maxTokens := 0
	if mt, ok := args["max_tokens"].(float64); ok && mt > 0 {
		maxTokens = int(mt)
	}

	budgetOpts := gleann.TokenBudgetOptions{
		MaxTokens:  maxTokens,
		IncludeTip: true,
	}
	output, _ := gleann.FormatSearchResultsWithBudget(results, budgetOpts)

	// An agent reading this cannot see the server's log: tell it here when the
	// index is known to be damaged, so it does not trust the results blindly.
	if len(warnings) > 0 {
		output = "⚠ " + strings.Join(dedupStrings(warnings), "\n⚠ ") + "\n\n" + output
	}

	// Log to active session if one is running.
	s.sessionLog("search", indexName, query, len(results))

	return mcp.NewToolResultText(output), nil
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

	searcher, release, err := s.acquireSearcher(indexName)
	if err != nil {
		return s.indexBusyError(indexName, err), nil
	}
	defer release()

	chat := gleann.NewChat(searcher, s.chatConfig())

	var searchOpts []gleann.SearchOption
	filters, logic, ferr := parseFilters(args)
	if ferr != nil {
		return mcp.NewToolResultError("invalid filters: " + ferr.Error()), nil
	}
	if len(filters) > 0 {
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
		Description: "Find caller/callee relationships for a code symbol in the AST graph. PREFER this over grep \"symbol(\" to find call sites or dependencies: graph edges are language-aware and span files without matching comments or strings. Input a fully-qualified name (e.g. 'MyClass::method') or partial name. Returns who calls this symbol and what it calls. Use gleann_def or gleann_search first to discover the exact FQN, then gleann_impact for transitive blast radius.",
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

	searcher, release, err := s.acquireSearcher(indexName)
	if err != nil {
		return s.indexBusyError(indexName, err), nil
	}
	defer release()

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

	if notice := indexing.NonGoSymbolNotice(nodeFqn, db, gleann.DirectionBoth); notice != "" {
		sb.WriteString(fmt.Sprintf("\n%s\n", notice))
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

	searcher, release, err := s.acquireSearcher(indexName)
	if err != nil {
		return s.indexBusyError(indexName, err), nil
	}
	defer release()

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

	searcher, release, err := s.acquireSearcher(indexName)
	if err == nil && searcher != nil {
		db := searcher.GraphDB()
		if db != nil {
			if content, err := db.FullDocument(vpath); err == nil && content != "" {
				release()
				return mcp.NewToolResultText(content), nil
			}
		}
		release()
	}

	// Fallback to reading the file from disk, but only from inside the
	// index's source directory: vpath is client input and must not reach other
	// files. A refused path gets the same answer as a missing one.
	if meta, mErr := gleann.GetIndexMeta(s.config.IndexDir, indexName); mErr == nil {
		if full, ok := containedSourcePath(meta.SourceDir, vpath); ok {
			if data, err := os.ReadFile(full); err == nil && len(data) > 0 {
				return mcp.NewToolResultText(string(data)), nil
			}
		}
	}

	if err != nil {
		return s.indexBusyError(indexName, err), nil
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

	searcher, release, err := s.acquireSearcher(indexName)
	if err != nil {
		return s.indexBusyError(indexName, err), nil
	}
	defer release()
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
		Description: "Analyze the blast radius of changing a code symbol. PREFER this over grepping every reference: it walks transitive callers in the AST graph and ranks affected files (core files first, submodules last). Returns direct callers, transitive callers (BFS), the test callers that exercise the symbol (listed separately and never counted in the blast radius), and affected files. Use gleann_def or gleann_search first to find the exact symbol FQN, then gleann_graph_neighbors for one-hop detail.",
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

	searcher, release, err := s.acquireSearcher(indexName)
	if err != nil {
		return s.indexBusyError(indexName, err), nil
	}
	defer release()

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

	// Test callers are reported but never counted in the blast radius below:
	// changing the symbol breaks its tests, not the shipped call graph.
	sb.WriteString(fmt.Sprintf("\n=== Test Callers (%d total) ===\n", len(impact.TestCallers)))
	if len(impact.TestCallers) == 0 {
		sb.WriteString("None found.\n")
	} else {
		for i, c := range impact.TestCallers {
			if i >= maxItems {
				sb.WriteString(fmt.Sprintf("... and %d more test callers\n", len(impact.TestCallers)-maxItems))
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

	// Blast radius = production callers; the test callers listed above are
	// deliberately not part of it.
	total := len(impact.DirectCallers) + len(impact.TransitiveCallers)
	sb.WriteString(fmt.Sprintf("\nTotal blast radius: %d affected symbols across %d files\n", total, len(impact.AffectedFiles)))

	if notice := indexing.NonGoSymbolNotice(symbol, db, gleann.DirectionCallers); notice != "" {
		sb.WriteString(fmt.Sprintf("\n%s\n", notice))
	}

	if total == 0 && len(impact.TestCallers) == 0 {
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
		Description: "Code graph statistics for an index: file count, symbol count, edge counts by type, and the health record (which graph extractor wrote it, exact or syntactic Go resolution and why, tree-sitter, when, dirty files, edges by confidence) with a rebuild hint when the graph is older.",
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

	searcher, release, err := s.acquireSearcher(indexName)
	if err != nil {
		return s.indexBusyError(indexName, err), nil
	}
	defer release()

	db := searcher.GraphDB()
	if db == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Graph database not available for index %q", indexName)), nil
	}

	stats, err := db.Stats()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get graph stats: %v", err)), nil
	}
	// Stats() stays counters-only (v1.22 M1), so this health-aware tool fetches
	// the record explicitly through the SQLite view's GraphHealth method.
	var health *graphstore.GraphHealth
	if hq, ok := db.(interface {
		GraphHealth() (*graphstore.GraphHealth, error)
	}); ok {
		if h, err := hq.GraphHealth(); err == nil {
			health = h
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Graph Statistics — %s\n\n", indexName))
	sb.WriteString(fmt.Sprintf("  Files:           %d\n", stats.Files))
	sb.WriteString(fmt.Sprintf("  Symbols:         %d\n", stats.Symbols))
	sb.WriteString(fmt.Sprintf("  Call edges:      %d\n", stats.CallEdges))
	sb.WriteString(fmt.Sprintf("  Declare edges:   %d\n", stats.DeclareEdges))
	sb.WriteString(fmt.Sprintf("  Implements edges: %d\n", stats.ImplementsEdges))
	// Graph health (T3): how the graph was built, and the rebuild command when
	// the stored graph extractor is older than this binary.
	if lines := graphstore.HealthLines(health); len(lines) > 0 {
		sb.WriteString("\n  Graph health:\n")
		for _, line := range lines {
			sb.WriteString("    " + line + "\n")
		}
	}
	if hint := graphstore.VersionDriftHint(indexName, health); hint != "" {
		sb.WriteString("    ⚠️  " + hint + "\n")
	}

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

	searcher, release, err := s.acquireSearcher(indexName)
	if err != nil {
		return s.indexBusyError(indexName, err), nil
	}
	defer release()

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
		mcp.WithDescription("Synchronize, update, or initialize a Gleann index (vector search passages, AST code graph, and document outlines) with workspace files. Run this after editing code so gleann_search, gleann_def and the graph tools see the new state; it is incremental, so a repeated call with no changes is cheap. Defaults to fast 'code' mode. For large repositories, synchronization runs asynchronously in the background. If in_progress is returned, DO NOT sleep or block in a loop; immediately inform the user that background synchronization is in progress and continue."),
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

func (s *Server) indexBusyError(indexName string, err error) *mcp.CallToolResult {
	if err == nil {
		return nil
	}
	errMsg := err.Error()
	isTimeoutOrLock := strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "lock") ||
		strings.Contains(errMsg, "bbolt") ||
		strings.Contains(errMsg, "resource temporarily unavailable") ||
		strings.Contains(errMsg, "status 1")

	if s.isSyncRunning(indexName) || isTimeoutOrLock {
		s.syncMu.Lock()
		t := s.syncTasks[indexName]
		s.syncMu.Unlock()
		progressInfo := ""
		if t != nil {
			t.mu.RLock()
			if t.totalItems > 0 {
				progressInfo = fmt.Sprintf(" (%.1f%% complete, %d/%d items, ETA: %.0fs)", t.pct, t.itemsDone, t.totalItems, t.etaSec)
			}
			t.mu.RUnlock()
		}
		return mcp.NewToolResultError(fmt.Sprintf("Index %q is currently being built or synced in the background%s. Please wait a moment and retry shortly.", indexName, progressInfo))
	}
	return mcp.NewToolResultError(fmt.Sprintf("Error loading index %q: %v", indexName, err))
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
			indexName = sanitizeIndexName(strings.ToLower(filepath.Base(cwd)))
		}
	}
	if indexName == "" {
		return mcp.NewToolResultError("missing parameter: index (and could not auto-resolve from workspace directory)"), nil
	}
	// The name becomes a path under the index dir and a positional argument
	// of the build, so it must be a safe, non-flag name.
	if err := gleann.ValidateName(indexName); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	meta, metaErr := gleann.GetIndexMeta(s.config.IndexDir, indexName)
	isNew := (metaErr != nil)

	docsDir := request.GetString("docs_dir", "")
	// A docs_dir the client names is held to the allowed roots (allowed_roots,
	// GLEANN_ALLOWED_ROOTS, else the home directory), the index's own source
	// directory and the working directory.
	if docsDir != "" && !s.docsDirAllowed(docsDir, meta) {
		return mcp.NewToolResultError(fmt.Sprintf("docs_dir %q is not inside the allowed roots; add its parent to allowed_roots in config.json (or GLEANN_ALLOWED_ROOTS)", docsDir)), nil
	}
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

	// Explicit files are indexed as given, so they must stay inside docsDir.
	for _, f := range files {
		cand := f
		if !filepath.IsAbs(cand) {
			cand = filepath.Join(docsDir, cand)
		}
		if _, err := pathpolicy.Contain(cand, []string{docsDir}); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("file %q is not inside the index's source directory", f)), nil
		}
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

			existing.mu.RLock()
			phase := existing.phase
			pct := existing.pct
			itemsDone := existing.itemsDone
			totalItems := existing.totalItems
			rate := existing.rate
			etaSec := existing.etaSec
			existing.mu.RUnlock()

			progressDetail := ""
			if totalItems > 0 {
				progressDetail = fmt.Sprintf(": currently %.1f%% (%d/%d items, phase: %s, ETA: %.0fs)", pct, itemsDone, totalItems, phase, etaSec)
			}

			res := map[string]any{
				"status":                "in_progress",
				"index":                 indexName,
				"mode":                  existing.mode,
				"action":                "in_progress",
				"phase":                 phase,
				"pct":                   pct,
				"items_done":            itemsDone,
				"total_items":           totalItems,
				"rate":                  rate,
				"eta_seconds":           etaSec,
				"message":               fmt.Sprintf("%s for %q is actively running in the background%s (started %s ago). DO NOT sleep or block. The current index remains available for search. You must immediately inform the user that synchronization is actively progressing with this status and proceed with other tasks.", actionMsg, indexName, progressDetail, elapsed),
				"elapsed":               elapsed.String(),
				"instruction_for_agent": "CRITICAL: Do NOT execute sleep commands or poll repeatedly in a loop. Inform the user of current percentage and ETA, then continue.",
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
	opts.OnProgress = func(phase string, pct float64, done, total int, rate, etaSec float64) {
		task.mu.Lock()
		task.phase = phase
		task.pct = pct
		task.itemsDone = done
		task.totalItems = total
		task.rate = rate
		task.etaSec = etaSec
		task.mu.Unlock()
	}
	s.syncTasks[indexName] = task
	s.syncMu.Unlock()

	go func() {
		defer close(task.done)
		// Detach from the request context so a client timeout does not sever
		// indexing; the server lifetime context still cancels it on Close.
		out, runErr := runner(s.lifetime(), opts)
		// Drop handles a concurrent tool call may have re-opened mid-build.
		s.evictIndex(indexName)
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

		task.mu.RLock()
		phase := task.phase
		pct := task.pct
		itemsDone := task.itemsDone
		totalItems := task.totalItems
		rate := task.rate
		etaSec := task.etaSec
		task.mu.RUnlock()

		progressDetail := ""
		if totalItems > 0 {
			progressDetail = fmt.Sprintf(": currently %.1f%% (%d/%d items, phase: %s, ETA: %.0fs)", pct, itemsDone, totalItems, phase, etaSec)
		}

		res := map[string]any{
			"status":                "in_progress",
			"index":                 indexName,
			"mode":                  mode,
			"action":                actionMsg,
			"phase":                 phase,
			"pct":                   pct,
			"items_done":            itemsDone,
			"total_items":           totalItems,
			"rate":                  rate,
			"eta_seconds":           etaSec,
			"message":               fmt.Sprintf("%s for %q%s. Because this is a large codebase, indexing is progressing asynchronously in the background. The current index remains available for search. DO NOT sleep or block.", actionMsg, indexName, progressDetail),
			"elapsed":               elapsedStr,
			"instruction_for_agent": "CRITICAL: Do NOT execute sleep commands or poll in a loop. Inform the user of current progress and continue.",
		}
		resBytes, _ := json.MarshalIndent(res, "", "  ")
		return mcp.NewToolResultText(string(resBytes)), nil
	}
}

// defaultSyncRunner builds (new index) or syncs (existing index) in-process
// via internal/indexing. The returned output is the human-readable build
// log plus the plain-surface progress lines, i.e. what the former
// separate `gleann index build|sync` process printed.
func (s *Server) defaultSyncRunner(ctx context.Context, opts SyncOptions) (string, error) {
	// The graph database admits one writer process; release the pooled
	// handle so the update below can open it.
	s.evictGraph(opts.IndexName)
	out := &syncWriter{}
	pacer := gleann.NewPacer(gleann.SurfacePlain, out)

	cfg := s.config
	if s.buildConfig != nil {
		cfg = *s.buildConfig
	}
	if s.config.IndexDir != "" {
		cfg.IndexDir = s.config.IndexDir
	}

	mode := indexing.ParseMode(opts.Mode)

	common := indexing.Options{
		Config:    cfg,
		Mode:      mode,
		NoPlugins: opts.NoPlugins,
		Stdout:    out,
		Stderr:    out,
		Progress: func(p indexing.Progress) {
			pacer.Tick(p.Phase, p.ItemsDone, p.TotalItems, p.BytesDone, p.TotalBytes, p.Force)
			if opts.OnProgress != nil {
				opts.OnProgress(p.Phase, p.Pct, p.ItemsDone, p.TotalItems, p.Rate, p.ETASec)
			}
		},
	}

	var err error
	if opts.IsNew {
		var res *indexing.BuildResult
		buildOpts := indexing.BuildOptions{
			Options: common,
			Name:    opts.IndexName,
			DocsDir: opts.DocsDir,
			Graph:   true,
		}
		// New builds honour the specialised per-mode models from config
		// (code_embedding_model / doc_embedding_model). The model chosen here
		// is written into the index meta, so later syncs must keep it
		// (indexing.Sync rejects a different model). Mode "all" is left
		// untouched: setting both models makes Build a dual build that writes
		// "<name>-code" and "<name>-docs" instead of the index the caller asked for.
		switch mode {
		case indexing.ModeCode:
			buildOpts.CodeModel = cfg.CodeEmbeddingModel
		case indexing.ModeDocs:
			buildOpts.DocModel = cfg.DocEmbeddingModel
		}
		res, err = indexing.Build(ctx, buildOpts)
		if err == nil && res.Estimate != nil {
			pacer.Done(true, 0, res.Estimate.FileCount, res.Estimate.TotalBytes)
		}
	} else {
		_, err = indexing.Sync(ctx, indexing.SyncOptions{
			Options: common,
			Name:    opts.IndexName,
			DocsDir: opts.DocsDir,
			Files:   opts.Files,
			Graph:   true,
		})
	}
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
	}
	return out.String(), err
}

// syncWriter is a goroutine-safe in-memory log sink for in-process builds.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
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

// sanitizeIndexName turns an auto-derived name (e.g. a directory name) into
// one accepted by gleann.ValidateName by replacing disallowed characters
// with '-' and trimming leading non-alphanumerics. Returns "" if nothing
// usable remains.
func sanitizeIndexName(name string) string {
	if gleann.ValidateName(name) == nil {
		return name
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.TrimLeft(b.String(), "-._")
	if len(out) > gleann.MaxNameLength {
		out = out[:gleann.MaxNameLength]
	}
	if gleann.ValidateName(out) != nil {
		return ""
	}
	return out
}

// dedupStrings returns ss without repeats, in order.
func dedupStrings(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := ss[:0:0]
	for _, v := range ss {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// focusResults replaces each result's text with its most query-relevant
// segments (see gleann.ExplainResult). Results that cannot be explained
// (lexical-only index, embedder error) keep their full text.
func (s *Server) focusResults(ctx context.Context, indexName, query string, results []gleann.SearchResult) []gleann.SearchResult {
	out := make([]gleann.SearchResult, len(results))
	for i, r := range results {
		out[i] = r
		name := indexName
		if idx, ok := r.Metadata["_index"].(string); ok && idx != "" {
			name = idx
		}
		searcher, release, err := s.acquireSearcher(name)
		if err != nil {
			continue
		}
		exp, err := searcher.ExplainResult(ctx, query, r, gleann.ExplainOptions{})
		release()
		if err != nil || len(exp.Segments) < 4 {
			continue
		}
		if snip := exp.Snippet(max(1, len(exp.Segments)/4)); snip != "" {
			out[i].Text = snip
		}
	}
	return out
}

// containedSourcePath resolves p (absolute, or relative to sourceDir) and
// reports whether it lies inside sourceDir once symlinks are followed. An
// index without a recorded source directory contains no readable files.
func containedSourcePath(sourceDir, p string) (string, bool) {
	if sourceDir == "" || p == "" {
		return "", false
	}
	cand := p
	if !filepath.IsAbs(cand) {
		cand = filepath.Join(sourceDir, cand)
	}
	full, err := pathpolicy.Contain(cand, []string{sourceDir})
	if err != nil {
		return "", false
	}
	return full, true
}

// docsDirAllowed reports whether a client-supplied docs_dir may be built or
// synced: it must lie inside the allowed roots, the index's recorded source
// directory or the working directory.
func (s *Server) docsDirAllowed(docsDir string, meta *gleann.IndexMeta) bool {
	var configured []string
	if env := strings.TrimSpace(os.Getenv("GLEANN_ALLOWED_ROOTS")); env != "" {
		configured = filepath.SplitList(env)
	} else if s.buildConfig != nil {
		configured = s.buildConfig.AllowedRoots
	}
	roots := pathpolicy.DefaultRoots(configured)
	if meta != nil && meta.SourceDir != "" {
		roots = append(roots, meta.SourceDir)
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" && filepath.Dir(cwd) != cwd {
		roots = append(roots, cwd)
	}
	_, err := pathpolicy.Contain(docsDir, roots)
	return err == nil
}
