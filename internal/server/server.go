package server

// Package server provides a REST API server for gleann.
// This mirrors Python LEANN's FastAPI server with stdlib net/http.

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/multimodal"

	"github.com/tevfik/gleann/internal/eventbus"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
	"github.com/tevfik/gleann/pkg/roles"
	"golang.org/x/sync/singleflight"

	// Register HNSW backend.
	_ "github.com/tevfik/gleann/pkg/backends"
)

//go:embed dist/*
var webFS embed.FS

// Server is the REST API server for gleann.
type Server struct {
	// config and embedder are replaced by handleUpdateConfig while requests
	// are in flight. Always read them through s.cfg() / s.getEmbedder(),
	// which take cfgMu; only write them while holding cfgMu.
	config   gleann.Config
	embedder *embedding.Computer
	cfgMu    sync.RWMutex

	logBuffer *LogBuffer

	// searchers caches loaded index searchers. mu guards searchers,
	// searcherRefs and searcherGen. Use acquireSearcher/invalidateSearcher
	// (searcher_cache.go) rather than touching the map directly.
	searchers    map[string]*gleann.LeannSearcher
	searcherRefs map[*gleann.LeannSearcher]*searcherRef
	searcherGen  uint64
	loadGroup    singleflight.Group
	mu           sync.RWMutex

	addr       string
	version    string
	server     *http.Server
	graphPool  *graphDBPool
	memoryPool *memoryPool         // Memory Engine: generic Entity/RELATES_TO graph
	blockMem   *memory.Manager     // BBolt hierarchical memory blocks (pkg/memory)
	bgManager  *background.Manager // Background task manager

	autoIndexer *background.AutoIndexer
	aiMu        sync.Mutex // guards autoIndexer

	bus    *eventbus.Bus // In-process pub/sub for lifecycle events
	stopCh chan struct{} // closed on Stop() to signal background goroutines

	// Server-lifetime context: cancelled by Stop so that background work
	// (self-exec'd index builds, model downloads, searcher loads) does not
	// outlive the server. bgWG tracks goroutines Stop must wait for.
	lifeOnce   sync.Once
	lifeCtx    context.Context
	lifeCancel context.CancelFunc
	bgWG       sync.WaitGroup
}

// cfg returns a snapshot of the current server configuration.
func (s *Server) cfg() gleann.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.config
}

// getEmbedder returns the current embedding computer (may be nil).
func (s *Server) getEmbedder() *embedding.Computer {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.embedder
}

// cfgAndEmbedder returns a consistent snapshot of config and embedder.
func (s *Server) cfgAndEmbedder() (gleann.Config, *embedding.Computer) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.config, s.embedder
}

// lifetime returns the server-lifetime context (cancelled by Stop).
func (s *Server) lifetime() context.Context {
	s.lifeOnce.Do(func() {
		s.lifeCtx, s.lifeCancel = context.WithCancel(context.Background())
	})
	return s.lifeCtx
}

// goBackground runs fn in a goroutine tracked by Stop.
func (s *Server) goBackground(fn func()) {
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		fn()
	}()
}

// getAutoIndexer returns the auto-indexer (may be nil).
func (s *Server) getAutoIndexer() *background.AutoIndexer {
	s.aiMu.Lock()
	defer s.aiMu.Unlock()
	return s.autoIndexer
}

// publish is a nil-safe helper that emits an event on the bus.
// Errors are silently ignored — eventbus is best-effort instrumentation,
// not a control-flow primitive.
func (s *Server) publish(topic string, payload map[string]any) {
	if s == nil || s.bus == nil {
		return
	}
	_ = s.bus.Publish(topic, payload)
}

// Bus returns the server's event bus (nil if not initialized).
// Callers may Subscribe to receive lifecycle events.
func (s *Server) Bus() *eventbus.Bus {
	if s == nil {
		return nil
	}
	return s.bus
}

// NewServer creates a new REST API server.
// version is the build-time version string (injected via -ldflags).
func NewServer(config gleann.Config, addr, version string) *Server {
	baseURL := config.OllamaHost
	if config.EmbeddingProvider == "openai" && config.OpenAIBaseURL != "" {
		baseURL = config.OpenAIBaseURL
	}
	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(config.EmbeddingProvider),
		Model:    config.EmbeddingModel,
		BaseURL:  baseURL,
		APIKey:   config.APIKeyForProvider(config.EmbeddingProvider),
	})

	if version == "" {
		version = "dev"
	}

	memPool := newMemoryPool(config.IndexDir)

	bgMgr := background.NewManager(2)
	if config.TaskEvictionAgeHours > 0 {
		bgMgr.SetEvictionAge(time.Duration(config.TaskEvictionAgeHours) * time.Hour)
	}

	s := &Server{
		config:     config,
		embedder:   embedder,
		searchers:  make(map[string]*gleann.LeannSearcher),
		addr:       addr,
		version:    version,
		graphPool:  newGraphDBPool(config.IndexDir),
		memoryPool: memPool,
		bgManager:  bgMgr,
		bus:        eventbus.New(64, nil),
	}

	// Wire the VectorSyncer factory so that Memory Engine entities are
	// automatically reflected in the HNSW+BM25 vector index.
	// This is a no-op in !treesitter builds.
	s.initMemorySyncer(config, embedder)

	s.logBuffer = NewLogBuffer(100)
	log.SetOutput(io.MultiWriter(os.Stderr, s.logBuffer))

	return s
}

// buildMux registers all routes on a new ServeMux.
func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Routes matching Python LEANN's FastAPI endpoints.
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/indexes", s.handleListIndexes)
	mux.HandleFunc("GET /api/indexes/{name}", s.handleGetIndex)
	mux.HandleFunc("POST /api/indexes/{name}/search", s.handleSearch)
	mux.HandleFunc("POST /api/indexes/{name}/ask", s.handleAsk)
	mux.HandleFunc("POST /api/indexes/{name}/build", s.handleBuild)
	mux.HandleFunc("POST /api/indexes/{name}/update", s.handleUpdateIndex)
	mux.HandleFunc("POST /api/indexes/{name}/index-path", s.handleIndexPath)
	mux.HandleFunc("POST /api/indexes/{name}/watch", s.handleWatch)
	mux.HandleFunc("POST /api/indexes/{name}/upload", s.handleUpload)
	mux.HandleFunc("PATCH /api/indexes/{name}", s.handlePatchIndex)
	mux.HandleFunc("DELETE /api/indexes/{name}", s.handleDeleteIndex)

	// Multi-index search.
	mux.HandleFunc("POST /api/search", s.handleMultiSearch)

	// Webhook configuration.
	mux.HandleFunc("GET /api/webhooks", s.handleListWebhooks)
	mux.HandleFunc("POST /api/webhooks", s.handleRegisterWebhook)
	mux.HandleFunc("DELETE /api/webhooks", s.handleDeleteWebhook)

	// Metrics (Prometheus/OpenTelemetry).
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Conversation management.
	mux.HandleFunc("GET /api/conversations", s.handleListConversations)
	mux.HandleFunc("GET /api/conversations/{id}", s.handleGetConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)
	mux.HandleFunc("DELETE /api/conversations", s.handleClearConversations)
	mux.HandleFunc("PUT /api/conversations/{id}", s.handleUpdateConversation)

	// Graph API endpoints (KuzuDB-backed code graph).
	mux.HandleFunc("GET /api/graph/{name}", s.handleGraphStats)
	mux.HandleFunc("GET /api/graph/{name}/toc", s.handleDocumentTOC)
	mux.HandleFunc("GET /api/graph/{name}/documents", s.handleListDocuments)
	mux.HandleFunc("POST /api/graph/{name}/query", s.handleGraphQuery)
	mux.HandleFunc("POST /api/graph/{name}/index", s.handleGraphIndex)

	// Memory Engine endpoints (generic Entity/RELATES_TO knowledge graph).
	mux.HandleFunc("POST /api/memory/{name}/inject", s.handleMemoryInject)
	mux.HandleFunc("DELETE /api/memory/{name}/nodes/{id}", s.handleMemoryDeleteNode)
	mux.HandleFunc("DELETE /api/memory/{name}/edges", s.handleMemoryDeleteEdge)
	mux.HandleFunc("POST /api/memory/{name}/traverse", s.handleMemoryTraverse)

	// Memory Block endpoints (BBolt hierarchical memory — pkg/memory).
	// Note: /api/blocks/search and /api/blocks/context must be registered before
	// /api/blocks/{id} so the router matches them as literals first.
	mux.HandleFunc("GET /api/blocks/search", s.handleSearchBlocks)
	mux.HandleFunc("GET /api/blocks/context", s.handleBlockContext)
	mux.HandleFunc("GET /api/blocks/stats", s.handleBlockStats)
	mux.HandleFunc("GET /api/blocks", s.handleListBlocks)
	mux.HandleFunc("POST /api/blocks", s.handleAddBlock)
	mux.HandleFunc("POST /api/blocks/compact", s.handleCompactBlocks)
	mux.HandleFunc("DELETE /api/blocks/{id}", s.handleDeleteBlock)
	mux.HandleFunc("DELETE /api/blocks", s.handleClearBlocks)

	// OpenAI-compatible proxy endpoints.
	mux.HandleFunc("GET /v1/models", s.handleListModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)

	// OpenAPI / Swagger documentation.
	mux.HandleFunc("GET /api/openapi.json", s.handleOpenAPISpec)
	mux.HandleFunc("GET /api/docs", s.handleSwaggerUI)

	// A2A Protocol endpoints (Agent-to-Agent discovery and communication).
	s.mountA2A(mux)

	// Background task management endpoints.
	s.mountBackgroundTasks(mux)

	// System & Config.
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/config", s.handleUpdateConfig)
	mux.HandleFunc("GET /api/logs", s.handleGetLogs)
	mux.HandleFunc("GET /api/proxy/models", s.handleProxyModels)

	// Plugins.
	mux.HandleFunc("GET /api/plugins", s.handleListPlugins)
	mux.HandleFunc("POST /api/plugins/{name}/install", s.handleInstallPlugin)
	mux.HandleFunc("DELETE /api/plugins/{name}", s.handleUninstallPlugin)

	// Models
	mux.HandleFunc("POST /api/models/download", s.handleDownloadModel)
	mux.HandleFunc("GET /api/models/local", s.handleListLocalModels)

	// Unified memory API (orchestrates blocks + graph + vector).
	s.mountUnifiedMemory(mux)

	// Knowledge Packs (curated reference data: crops, pests, regional facts...).
	s.mountPacks(mux)

	// Serve /assets from embedded "dist/assets"
	if sub, err := fs.Sub(webFS, "dist/assets"); err == nil {
		mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))
	}

	// Root Handler (SPA Fallback)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// If it's an API request that wasn't found, return 404
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/a2a/") {
			http.NotFound(w, r)
			return
		}

		// Otherwise serve the embedded index.html
		content, err := webFS.ReadFile("dist/index.html")
		if err != nil {
			http.Error(w, "Web UI not available", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
	})

	return mux
}

// Start starts the server.
func (s *Server) Start() error {
	mux := s.buildMux()

	s.server = &http.Server{
		Addr:         s.addr,
		Handler:      withMiddleware(mux),
		ReadTimeout:  30 * time.Minute,
		WriteTimeout: 30 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	// Start background maintenance scheduler for BBolt memory.
	s.stopCh = make(chan struct{})
	if mgr, err := s.blockManager(); err == nil {
		startMaintenanceScheduler(mgr, s.stopCh)
		startSleepTimeEngine(mgr, s.stopCh)
	}

	// Start auto-indexer for watched directories (env: GLEANN_AUTO_INDEX_DIRS).
	s.startAutoIndexer()

	log.Printf("gleann server starting on %s", s.addr)
	return s.server.ListenAndServe()
}

// Stop gracefully shuts down the server.
//
// Order matters: the HTTP server is shut down first so no new requests
// arrive and in-flight handlers drain; then background workers and pools
// are stopped, cached searchers closed, and finally the auto-indexer.
func (s *Server) Stop(ctx context.Context) error {
	var shutdownErr error
	if s.server != nil {
		shutdownErr = s.server.Shutdown(ctx)
	}

	// Signal background goroutines to stop and cancel lifetime-bound work
	// (self-exec'd builds, downloads, searcher loads).
	if s.stopCh != nil {
		close(s.stopCh)
	}
	s.lifetime()
	s.lifeCancel()

	if s.bgManager != nil {
		s.bgManager.Stop()
	}
	if s.graphPool != nil {
		s.graphPool.closeAll()
	}
	s.stopMemoryPool(ctx)
	s.closeBlockMem()

	// Wait (bounded by ctx) for tracked background goroutines.
	waitDone := make(chan struct{})
	go func() {
		s.bgWG.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-ctx.Done():
		log.Printf("server stop: background tasks did not finish: %v", ctx.Err())
	}

	s.invalidateAllSearchers()

	if ai := s.getAutoIndexer(); ai != nil {
		ai.Stop()
	}
	if s.bus != nil {
		_ = s.bus.Close()
	}
	return shutdownErr
}

// startAutoIndexer initializes the background auto-indexer.
// It reads GLEANN_AUTO_INDEX_DIRS (format: "name1:dir1,name2:dir2") to
// determine which indexes to watch for file changes.
func (s *Server) startAutoIndexer() {
	ai, err := background.NewAutoIndexer(s.bgManager, background.AutoIndexConfig{
		IndexDir: s.cfg().IndexDir,
	})
	if err != nil {
		log.Printf("auto-indexer: init failed: %v", err)
		return
	}

	dirsEnv := os.Getenv("GLEANN_AUTO_INDEX_DIRS")
	if dirsEnv != "" {
		for _, pair := range strings.Split(dirsEnv, ",") {
			parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				continue
			}
			if err := ai.Watch(parts[0], parts[1]); err != nil {
				log.Printf("auto-indexer: watch env %q→%q failed: %v", parts[0], parts[1], err)
			} else {
				log.Printf("auto-indexer: watching env %q → %s", parts[0], parts[1])
			}
		}
	}

	// Also watch indexes that have AutoWatch = true in their metadata
	indexes, err := gleann.ListIndexes(s.cfg().IndexDir)
	if err == nil {
		for _, idx := range indexes {
			if idx.AutoWatch && idx.SourceDir != "" {
				if err := ai.Watch(idx.Name, idx.SourceDir); err != nil {
					log.Printf("auto-indexer: persistent watch %q→%q failed: %v", idx.Name, idx.SourceDir, err)
				} else {
					log.Printf("auto-indexer: watching persistent %q → %s", idx.Name, idx.SourceDir)
				}
			}
		}
	}

	if len(ai.WatchedIndexes()) > 0 {
		ai.Start(context.Background())
	}
	// Keep the autoIndexer around (even with no watches) so we can add
	// watches dynamically later.
	s.aiMu.Lock()
	s.autoIndexer = ai
	s.aiMu.Unlock()
}

// --- Handlers ---

// handleWatch toggles watch state for an index

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": s.version,
		"engine":  "gleann-go",
	})
}

func (s *Server) handleListIndexes(w http.ResponseWriter, r *http.Request) {
	indexes, err := gleann.ListIndexes(s.cfg().IndexDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

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
		if err := gleann.ValidateName(clientTarget); err != nil {
			writeError(w, http.StatusBadRequest, "invalid client target: "+err.Error())
			return
		}
	}

	tagFilter := r.URL.Query().Get("tag")
	exposedOnly := r.URL.Query().Get("exposed_only") == "true"
	tagEnv := os.Getenv("GLEANN_TAGS")
	isAdmin := r.URL.Query().Get("all") == "true"

	var filtered []gleann.IndexMeta
	for _, idx := range indexes {
		if clientTarget != "" && !strings.EqualFold(idx.Name, clientTarget) {
			continue
		}
		if tagFilter != "" && !idx.HasTag(tagFilter) {
			continue
		}
		if exposedOnly && !idx.IsMCPExposed() {
			continue
		}
		if tagEnv != "" && !isAdmin {
			matched := false
			for _, t := range strings.Split(tagEnv, ",") {
				t = strings.TrimSpace(t)
				if t != "" && idx.HasTag(t) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		filtered = append(filtered, idx)
	}

	cfg, emb := s.cfgAndEmbedder()
	var embedderName string
	if emb != nil {
		embedderName = emb.ModelName()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"indexes":                  filtered,
		"count":                    len(filtered),
		"current_embedding_model":  embedderName,
		"current_embedding_models": activeEmbeddingModels(embedderName, cfg),
	})
}

// activeEmbeddingModels lists every embedding model the server can search with:
// the general embedder plus the dedicated code/doc models, which are resolved
// per index. Clients use it to decide whether an index needs a rebuild.
func activeEmbeddingModels(general string, cfg gleann.Config) []string {
	var models []string
	seen := make(map[string]bool)
	for _, m := range []string{general, cfg.CodeEmbeddingModel, cfg.DocEmbeddingModel} {
		if m != "" && !seen[m] {
			seen[m] = true
			models = append(models, m)
		}
	}
	return models
}

func (s *Server) handleGetIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	searcher, release, err := s.acquireSearcher(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found: %v", name, err))
		return
	}
	defer release()

	writeJSON(w, http.StatusOK, searcher.Meta())
}

type patchIndexRequest struct {
	Tags        *[]string `json:"tags,omitempty"`
	Description *string   `json:"description,omitempty"`
	MCPExposed  *bool     `json:"mcp_exposed,omitempty"`
}

func (s *Server) handlePatchIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	var req patchIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	err := gleann.UpdateIndexMeta(s.cfg().IndexDir, name, func(meta *gleann.IndexMeta) {
		if req.Tags != nil {
			meta.Tags = *req.Tags
		}
		if req.Description != nil {
			meta.Description = *req.Description
		}
		if req.MCPExposed != nil {
			meta.MCPExposed = req.MCPExposed
		}
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update index metadata: %v", err))
		return
	}

	meta, err := gleann.GetIndexMeta(s.cfg().IndexDir, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Drop the cached searcher so the next request reloads it with the new
	// metadata (mutating the shared searcher in place would race with
	// concurrent readers).
	s.invalidateSearcher(name)

	writeJSON(w, http.StatusOK, meta)
}

type searchRequest struct {
	Query               string                  `json:"query"`
	TopK                int                     `json:"top_k,omitempty"`
	HybridAlpha         float32                 `json:"hybrid_alpha,omitempty"`
	MinScore            float32                 `json:"min_score,omitempty"`
	EfSearch            int                     `json:"ef_search,omitempty"`
	RecomputeEmbeddings bool                    `json:"recompute_embeddings,omitempty"`
	Rerank              bool                    `json:"rerank,omitempty"`
	RerankModel         string                  `json:"rerank_model,omitempty"`
	MetadataFilters     []gleann.MetadataFilter `json:"metadata_filters,omitempty"`
	FilterLogic         string                  `json:"filter_logic,omitempty"`
	GraphContext        bool                    `json:"graph_context,omitempty"`
	IncludeTests        bool                    `json:"include_tests,omitempty"`
	Kind                string                  `json:"kind,omitempty"`
}

type searchResponse struct {
	Results []gleann.SearchResult `json:"results"`
	Count   int                   `json:"count"`
	QueryMs int64                 `json:"query_ms"`
	// Warnings lists problems with the index that make the results unreliable
	// (for example passage IDs damaged by an older version); see
	// `gleann index repair`.
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	searcher, release, err := s.acquireSearcher(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found: %v", name, err))
		return
	}
	defer release()

	start := time.Now()

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
	if len(req.MetadataFilters) > 0 {
		opts = append(opts, gleann.WithMetadataFilter(req.MetadataFilters...))
	}
	if req.FilterLogic != "" {
		opts = append(opts, gleann.WithFilterLogic(req.FilterLogic))
	}
	if req.GraphContext {
		opts = append(opts, gleann.WithGraphContext(true))
	}
	if req.IncludeTests {
		opts = append(opts, gleann.WithIncludeTests(true))
	}
	if req.Kind != "" {
		opts = append(opts, gleann.WithKind(req.Kind))
	}

	// Set up per-request reranker if requested.
	if cfg := s.cfg(); req.Rerank || cfg.SearchConfig.UseReranker {
		rerankModel := req.RerankModel
		if rerankModel == "" {
			rerankModel = cfg.SearchConfig.RerankerConfig.Model
		}
		if rerankModel == "" {
			rerankModel = "bge-reranker-v2-m3"
		}
		rerankerCfg := gleann.RerankerConfig{
			Provider: gleann.RerankerProvider(cfg.EmbeddingProvider),
			Model:    rerankModel,
			BaseURL:  cfg.OllamaHost,
			APIKey:   cfg.APIKeyForProvider(cfg.EmbeddingProvider),
		}
		opts = append(opts, gleann.WithCustomReranker(gleann.NewReranker(rerankerCfg)))
	}

	results, err := searcher.Search(r.Context(), req.Query, opts...)
	if err != nil {
		serverMetrics.RecordSearch(time.Since(start), true)
		writeError(w, http.StatusInternalServerError, "search failed: "+err.Error())
		return
	}

	serverMetrics.RecordSearch(time.Since(start), false)
	s.publish(eventbus.TopicSearchCompleted, map[string]any{
		"index":    name,
		"query":    req.Query,
		"top_k":    req.TopK,
		"results":  len(results),
		"query_ms": time.Since(start).Milliseconds(),
	})

	writeJSON(w, http.StatusOK, searchResponse{
		Results:  results,
		Count:    len(results),
		QueryMs:  time.Since(start).Milliseconds(),
		Warnings: searcher.Warnings(),
	})
}

type askRequest struct {
	Question       string   `json:"question"`
	TopK           int      `json:"top_k,omitempty"`
	LLMModel       string   `json:"llm_model,omitempty"`
	LLMProvider    string   `json:"llm_provider,omitempty"`
	SystemPrompt   string   `json:"system_prompt,omitempty"`
	Role           string   `json:"role,omitempty"`
	ConversationID string   `json:"conversation_id,omitempty"`
	Stream         bool     `json:"stream,omitempty"`
	VisionRAG      bool     `json:"vision_rag,omitempty"`
	Images         []string `json:"images,omitempty"`
	Format         any      `json:"format,omitempty"`
}

type askResponse struct {
	Answer  string                `json:"answer"`
	Sources []gleann.SearchResult `json:"sources"`
	QueryMs int64                 `json:"query_ms"`
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	if err := s.checkIndexAccess(r, name); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var req askRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Question == "" {
		writeError(w, http.StatusBadRequest, "question is required")
		return
	}

	// Also support ?stream=true query param.
	if r.URL.Query().Get("stream") == "true" {
		req.Stream = true
	}

	searcher, release, err := s.acquireSearcher(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found: %v", name, err))
		return
	}
	defer release()

	chatConfig := gleann.DefaultChatConfig()
	// Apply server config defaults.
	cfg := s.cfg()
	if cfg.LLMModel != "" {
		chatConfig.Model = cfg.LLMModel
	}
	if cfg.LLMProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(cfg.LLMProvider)
	}
	if cfg.OllamaHost != "" {
		chatConfig.BaseURL = cfg.OllamaHost
	}

	if req.LLMModel != "" {
		chatConfig.Model = req.LLMModel
	}
	if req.LLMProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(req.LLMProvider)
	}

	// Remove default max tokens limit for server endpoints to prevent cutting off long responses
	chatConfig.MaxTokens = -1

	if req.SystemPrompt != "" {
		chatConfig.SystemPrompt = req.SystemPrompt
	}
	if req.Format != nil {
		chatConfig.Format = req.Format
	}

	// Resolve named role to system prompt.
	if req.Role != "" && req.SystemPrompt == "" {
		reg := roles.DefaultRegistry()
		if prompt, err := reg.SystemPrompt(req.Role); err == nil {
			chatConfig.SystemPrompt = prompt
		}
	}

	if chatConfig.Provider == gleann.LLMOllama {
		chatConfig.APIKey = s.cfg().APIKeyForProvider("ollama")
	}
	chat := gleann.NewChat(searcher, chatConfig)

	// Restore conversation history if continuing.
	if req.ConversationID != "" {
		convStore := conversations.DefaultStore()
		conv, err := convStore.Load(req.ConversationID)
		if err == nil {
			for _, m := range conv.Messages {
				chat.AppendHistory(gleann.ChatMessage{Role: m.Role, Content: m.Content})
			}
		}
	}

	var opts []gleann.SearchOption
	if req.TopK > 0 {
		opts = append(opts, gleann.WithTopK(req.TopK))
	}

	serverMetrics.RecordAsk()

	if req.Stream {
		s.handleAskStream(w, r, chat, req, name, opts)
		return
	}

	visionImages := req.Images
	if visionImages == nil {
		visionImages = []string{}
	}
	if req.VisionRAG {
		meta, err := gleann.GetIndexMeta(s.cfg().IndexDir, name)
		if err == nil && meta.SourceDir != "" {
			sources, _ := searcher.Search(r.Context(), req.Question, opts...)
			renderedPages := make(map[string]bool)

			for _, src := range sources {
				hasImg, _ := src.Metadata["has_image"].(bool)
				if !hasImg {
					continue
				}
				page, ok := src.Metadata["page"].(float64)
				if !ok {
					continue
				}
				relPath, _ := src.Metadata["source"].(string)
				if relPath != "" && strings.HasSuffix(strings.ToLower(relPath), ".pdf") {
					pageKey := fmt.Sprintf("%s:%d", relPath, int(page))
					if !renderedPages[pageKey] {
						absPath := filepath.Join(meta.SourceDir, relPath)
						b64, err := multimodal.RenderPDFPageToBase64(absPath, int(page), 150)
						if err == nil && b64 != "" {
							visionImages = append(visionImages, b64)
							renderedPages[pageKey] = true
						} else {
							fmt.Fprintf(os.Stderr, "VisionRAG: failed to render %s page %d: %v\n", relPath, int(page), err)
						}
					}
				}
			}
		}
	}

	start := time.Now()

	var answer string
	var errAsk error
	if len(visionImages) > 0 {
		answer, errAsk = chat.AskWithImages(r.Context(), req.Question, visionImages, opts...)
	} else {
		answer, errAsk = chat.Ask(r.Context(), req.Question, opts...)
	}

	if errAsk != nil {
		writeError(w, http.StatusInternalServerError, "ask failed: "+errAsk.Error())
		return
	}

	// Get sources for transparency.
	sources, _ := searcher.Search(r.Context(), req.Question, opts...)

	writeJSON(w, http.StatusOK, askResponse{
		Answer:  answer,
		Sources: sources,
		QueryMs: time.Since(start).Milliseconds(),
	})
}

// handleAskStream streams LLM tokens via Server-Sent Events (SSE).
// Event format:
//
//	data: {"token": "partial text"}\n\n     (for each token)
//	data: [DONE]\n\n                         (final event)
func (s *Server) handleAskStream(w http.ResponseWriter, r *http.Request, chat *gleann.LeannChat, req askRequest, indexName string, opts []gleann.SearchOption) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering.
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	callback := func(token string) {
		data, _ := json.Marshal(map[string]string{"token": token})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	sendStatus := func(status string) {
		data, _ := json.Marshal(map[string]string{"status": status})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	visionImages := req.Images
	if visionImages == nil {
		visionImages = []string{}
	}
	if req.VisionRAG {
		sendStatus("Analysing documents for visual context...")

		meta, err := gleann.GetIndexMeta(s.cfg().IndexDir, indexName)
		if err == nil && meta.SourceDir != "" {
			sources, _ := chat.GetSearcher().Search(r.Context(), req.Question, opts...)
			renderedPages := make(map[string]bool)
			for _, src := range sources {
				hasImg, _ := src.Metadata["has_image"].(bool)
				if !hasImg {
					continue
				}
				page, ok := src.Metadata["page"].(float64)
				if !ok {
					continue
				}
				relPath, _ := src.Metadata["source"].(string)
				if relPath != "" && strings.HasSuffix(strings.ToLower(relPath), ".pdf") {
					pageKey := fmt.Sprintf("%s:%d", relPath, int(page))
					if !renderedPages[pageKey] {
						sendStatus(fmt.Sprintf("Extracting image from %s (Page %d)...", filepath.Base(relPath), int(page)))
						absPath := filepath.Join(meta.SourceDir, relPath)
						b64, err := multimodal.RenderPDFPageToBase64(absPath, int(page), 150)
						if err == nil && b64 != "" {
							visionImages = append(visionImages, b64)
							renderedPages[pageKey] = true
						}
					}
				}
			}
		}

		if len(visionImages) > 0 {
			sendStatus(fmt.Sprintf("Sending %d image(s) to multimodal model...", len(visionImages)))
		} else {
			sendStatus("No relevant images found, falling back to text search.")
		}
	}

	var sources []gleann.SearchResult
	var err error
	if len(visionImages) > 0 {
		sources, err = chat.AskStreamWithImages(r.Context(), req.Question, visionImages, callback, opts...)
	} else {
		sources, err = chat.AskStream(r.Context(), req.Question, callback, opts...)
	}

	if err != nil {
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
	} else if len(sources) > 0 {
		srcData, _ := json.Marshal(map[string]any{"sources": sources})
		fmt.Fprintf(w, "data: %s\n\n", srcData)
		flusher.Flush()
	}

	// Save the conversation history.
	convStore := conversations.DefaultStore()
	var convID string
	var title string
	if req.ConversationID != "" {
		convID = req.ConversationID
		// Try to get title from existing
		if existing, err := convStore.Load(convID); err == nil {
			title = existing.Title
		}
	} else {
		title = req.Question
		if len(title) > 50 {
			title = title[:47] + "..."
		}
	}

	// Convert chat history to conversation format.
	chatHistory := chat.History()
	var msgs []conversations.Message
	for _, m := range chatHistory {
		msgs = append(msgs, conversations.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	conv := &conversations.Conversation{
		ID:        convID,
		Title:     title,
		Indexes:   []string{indexName},
		Model:     chat.Config().Model,
		Messages:  msgs,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := convStore.Save(conv); err == nil {
		convData, _ := json.Marshal(map[string]string{"conversation_id": conv.ID})
		fmt.Fprintf(w, "data: %s\n\n", convData)
		flusher.Flush()
	} else {
		log.Printf("Failed to save conversation: %v", err)
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

type buildRequest struct {
	Texts       []string       `json:"texts"`
	Items       []gleann.Item  `json:"items,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Description string         `json:"description,omitempty"`
	MCPExposed  *bool          `json:"mcp_exposed,omitempty"`
}

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	var req buildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Build items from texts or use provided items.
	var items []gleann.Item
	if len(req.Items) > 0 {
		items = req.Items
	} else if len(req.Texts) > 0 {
		items = make([]gleann.Item, len(req.Texts))
		for i, text := range req.Texts {
			items[i] = gleann.Item{Text: text, Metadata: req.Metadata}
		}
	} else {
		writeError(w, http.StatusBadRequest, "texts or items required")
		return
	}

	cfg, emb := s.cfgAndEmbedder()
	if meta, err := gleann.GetIndexMeta(cfg.IndexDir, name); err == nil {
		if meta.EmbeddingModel != "" && meta.EmbeddingModel != emb.ModelName() {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("model mismatch: index uses '%s', current is '%s'. please delete index first", meta.EmbeddingModel, emb.ModelName()))
			return
		}
	}

	builder, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create builder: "+err.Error())
		return
	}

	start := time.Now()
	s.publish(eventbus.TopicIndexStarted, map[string]any{
		"index": name,
		"count": len(items),
	})
	if err := builder.Build(r.Context(), name, items); err != nil {
		serverMetrics.RecordBuild(time.Since(start), true)
		s.publish(eventbus.TopicIndexFailed, map[string]any{
			"index": name,
			"error": err.Error(),
		})
		writeError(w, http.StatusInternalServerError, "build failed: "+err.Error())
		return
	}
	buildDuration := time.Since(start)
	serverMetrics.RecordBuild(buildDuration, false)
	s.publish(eventbus.TopicIndexCompleted, map[string]any{
		"index":    name,
		"count":    len(items),
		"build_ms": buildDuration.Milliseconds(),
	})

	// Update governance metadata if provided.
	if len(req.Tags) > 0 || req.Description != "" || req.MCPExposed != nil {
		_ = gleann.UpdateIndexMeta(s.cfg().IndexDir, name, func(meta *gleann.IndexMeta) {
			if len(req.Tags) > 0 {
				meta.Tags = req.Tags
			}
			if req.Description != "" {
				meta.Description = req.Description
			}
			if req.MCPExposed != nil {
				meta.MCPExposed = req.MCPExposed
			}
		})
	}

	// Clear cached searcher (closed once in-flight requests release it).
	s.invalidateSearcher(name)

	// Notify webhooks.
	notifyWebhooks("build_complete", map[string]any{
		"index":   name,
		"count":   len(items),
		"buildMs": buildDuration.Milliseconds(),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"name":    name,
		"count":   len(items),
		"buildMs": buildDuration.Milliseconds(),
	})
}

type updateIndexRequest struct {
	Sources  []string      `json:"sources"`
	DocsRoot string        `json:"docs_root,omitempty"`
	Items    []gleann.Item `json:"items,omitempty"`
}

func (s *Server) handleUpdateIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	var req updateIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if len(req.Sources) == 0 && len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "sources or items required for update")
		return
	}

	items := req.Items

	// If no explicit items were provided, read sources from disk under DocsRoot
	if len(items) == 0 && req.DocsRoot != "" {
		for _, src := range req.Sources {
			fullPath := filepath.Join(req.DocsRoot, src)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("read source file %s: %v", src, err))
				return
			}
			meta := map[string]any{"source": src}
			items = append(items, gleann.Item{
				Text:     string(content),
				Metadata: meta,
			})
		}
	}

	cfg, emb := s.cfgAndEmbedder()
	if meta, err := gleann.GetIndexMeta(cfg.IndexDir, name); err == nil {
		if meta.EmbeddingModel != "" && meta.EmbeddingModel != emb.ModelName() {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("model mismatch: index uses '%s', current is '%s'. please delete index first", meta.EmbeddingModel, emb.ModelName()))
			return
		}
	}

	builder, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create builder: "+err.Error())
		return
	}

	start := time.Now()
	stats, err := builder.UpdateIndexWithStats(r.Context(), name, items, req.Sources)
	serverMetrics.RecordUpdate(stats, err != nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update index failed: "+err.Error())
		return
	}

	// Invalidate searcher cache for this index (closed once in-flight
	// requests release it).
	s.invalidateSearcher(name)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"index":           name,
		"updated_sources": req.Sources,
		"items_count":     len(items),
		"duration_ms":     time.Since(start).Milliseconds(),
		// How the update was applied and what it cost: "compact" means the index
		// was rebuilt from its remaining passages (every one re-embedded).
		"mode":           string(stats.Mode),
		"passages":       stats.Passages,
		"removed":        stats.Removed,
		"embedded_texts": stats.Embedded,
	})
}

// removeIndexTimeout bounds how long a delete waits for in-flight searches to
// let go of the index files.
const removeIndexTimeout = 5 * time.Second

// removeIndexRetrying removes the index, retrying briefly while it fails.
// invalidateSearcher only closes a searcher once the requests still using it
// have released it, so for a moment after the cache drop the index files can
// still be open. Linux unlinks open files without complaint; Windows refuses
// ("being used by another process"). Each retry drops anything a concurrent
// request re-loaded in the meantime. The first attempt succeeding is the
// normal case and costs nothing extra.
func (s *Server) removeIndexRetrying(name string) error {
	deadline := time.Now().Add(removeIndexTimeout)
	for {
		err := gleann.RemoveIndex(s.cfg().IndexDir, name)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
		s.invalidateSearcher(name)
	}
}

func (s *Server) handleDeleteIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	// Drop the cached searcher; it is closed once in-flight requests that
	// still use it have released it.
	s.invalidateSearcher(name)

	err := s.removeIndexRetrying(name)
	// A request may have re-loaded the index while it was being removed;
	// drop anything cached in the meantime.
	s.invalidateSearcher(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed: "+err.Error())
		return
	}

	serverMetrics.RecordDelete()

	// Notify webhooks.
	notifyWebhooks("index_deleted", map[string]any{
		"index": name,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "deleted",
		"name":   name,
	})
}

// --- Conversation Handlers ---

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	store := conversations.DefaultStore()
	convs, err := store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list conversations: "+err.Error())
		return
	}

	type convSummary struct {
		ID        string `json:"id"`
		ShortID   string `json:"short_id"`
		Title     string `json:"title"`
		Model     string `json:"model"`
		Indexes   string `json:"indexes"`
		Messages  int    `json:"message_count"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}

	var items []convSummary
	for _, c := range convs {
		items = append(items, convSummary{
			ID:        c.ID,
			ShortID:   conversations.ShortID(c.ID),
			Title:     c.Title,
			Model:     c.Model,
			Indexes:   c.IndexLabel(),
			Messages:  c.MessageCount(),
			CreatedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt: c.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"conversations": items,
		"count":         len(items),
	})
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "conversation ID required")
		return
	}

	store := conversations.DefaultStore()
	conv, err := store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "conversation not found: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, conv)
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "conversation ID required")
		return
	}

	store := conversations.DefaultStore()
	if err := store.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, "conversation not found: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
		"id":     id,
	})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// checkClientTarget validates whether a request with a client target constraint
// (via X-Gleann-Target / X-Gleann-Index header or ?target= / ?index= query parameter)
// is permitted to access the requested index.
func (s *Server) checkClientTarget(r *http.Request, indexName string) error {
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
	if clientTarget != "" && !strings.EqualFold(clientTarget, indexName) {
		return fmt.Errorf("access denied: client target is restricted to %q, cannot access index %q", clientTarget, indexName)
	}
	return nil
}

// isIndexAccessibleMeta returns true if an index is public (MCP exposed) and matches GLEANN_TAGS if configured.
func (s *Server) isIndexAccessibleMeta(meta *gleann.IndexMeta) bool {
	if meta == nil {
		return false
	}
	if !meta.IsMCPExposed() {
		return false
	}
	tagEnv := os.Getenv("GLEANN_TAGS")
	if tagEnv != "" {
		matched := false
		for _, t := range strings.Split(tagEnv, ",") {
			t = strings.TrimSpace(t)
			if t != "" && meta.HasTag(t) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// isIndexAccessible checks if the named index exists and satisfies accessibility/governance rules.
func (s *Server) isIndexAccessible(name string) bool {
	if gleann.ValidateName(name) != nil {
		return false
	}
	meta, err := gleann.GetIndexMeta(s.cfg().IndexDir, name)
	if err != nil {
		// Fallback for mock or test indexes where directory exists or graphPool has it
		if s.graphPool != nil {
			if _, ok := s.graphPool.dbs[name]; ok {
				return true
			}
		}
		indexPath := filepath.Join(s.cfg().IndexDir, name)
		if fi, serr := os.Stat(indexPath); serr == nil && fi.IsDir() {
			return true
		}
		return false
	}
	return s.isIndexAccessibleMeta(meta)
}

// firstAccessibleIndexName returns the first index that is public and satisfies GLEANN_TAGS.
func (s *Server) firstAccessibleIndexName() string {
	indexes, err := gleann.ListIndexes(s.cfg().IndexDir)
	if err != nil {
		return ""
	}
	for _, idx := range indexes {
		if s.isIndexAccessibleMeta(&idx) {
			return idx.Name
		}
	}
	return ""
}

// checkIndexAccess verifies both client target constraints and server-side tag isolation rules.
func (s *Server) checkIndexAccess(r *http.Request, indexName string) error {
	if err := s.checkClientTarget(r, indexName); err != nil {
		return err
	}
	tagEnv := os.Getenv("GLEANN_TAGS")
	if tagEnv != "" {
		meta, err := gleann.GetIndexMeta(s.cfg().IndexDir, indexName)
		if err == nil {
			var allowed []string
			for _, t := range strings.Split(tagEnv, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					allowed = append(allowed, t)
				}
			}
			if len(allowed) > 0 && !meta.HasAnyTag(allowed) {
				return fmt.Errorf("access denied: index %q does not match required tags (%s)", indexName, tagEnv)
			}
		}
	}
	return nil
}

func withMiddleware(next http.Handler) http.Handler {
	// Chain: body-limit → rate limiter → timeout → CORS/logging.
	return bodyLimitMiddleware(rateLimitMiddleware(timeoutMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Gleann-Target, X-Gleann-Index, X-Gleann-Top-K, X-Gleann-Min-Score")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Logging.
		start := time.Now()
		next.ServeHTTP(w, r)

		// Logging: skip noisy high-frequency read-only polling requests in general logs.
		isPolling := r.Method == http.MethodGet && (r.URL.Path == "/health" ||
			r.URL.Path == "/api/tasks" ||
			r.URL.Path == "/api/blocks/stats" ||
			r.URL.Path == "/api/logs" ||
			r.URL.Path == "/api/config" ||
			r.URL.Path == "/api/plugins" ||
			r.URL.Path == "/api/models/local")
		if !isPolling {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
		}
	}))))
}

// ── LogBuffer ────────────────────────────────────────────────────────────────

type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func NewLogBuffer(max int) *LogBuffer {
	return &LogBuffer{max: max, lines: make([]string, 0, max)}
}

func (b *LogBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, string(p))
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	return len(p), nil
}

func (b *LogBuffer) GetLogs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	res := make([]string, len(b.lines))
	copy(res, b.lines)
	return res
}

// handleGetConfig returns the server configuration with secrets redacted
// (see gleann.Config.Redacted).
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg().Redacted())
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var newConfig gleann.Config
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config JSON")
		return
	}
	if newConfig.MultimodalModel != "" {
		caps := multimodal.DetectCapabilities(newConfig.OllamaHost, newConfig.MultimodalModel)
		if !caps.Vision {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Model '%s' does not support vision capabilities. Please select a valid multimodal model (e.g., minicpm-v, llava).", newConfig.MultimodalModel))
			return
		}
	}

	// Serialise concurrent updates so the sentinel merge below always sees
	// the config it is replacing.
	configUpdateMu.Lock()
	defer configUpdateMu.Unlock()

	// The web UI round-trips redacted secrets: the sentinel means "keep the
	// current value", an empty string means "clear".
	prev := s.cfg()
	newConfig.RestoreRedactedSecrets(prev)
	// Non-serialisable fields cannot come from the client; keep them.
	newConfig.EmbedderResolver = prev.EmbedderResolver

	// Persist first so the in-memory config never diverges from what a
	// restart would load.
	if err := saveServerConfig(newConfig); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config: "+err.Error())
		return
	}

	baseURL := newConfig.OllamaHost
	if newConfig.EmbeddingProvider == "openai" && newConfig.OpenAIBaseURL != "" {
		baseURL = newConfig.OpenAIBaseURL
	}
	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(newConfig.EmbeddingProvider),
		Model:    newConfig.EmbeddingModel,
		BaseURL:  baseURL,
		APIKey:   newConfig.APIKeyForProvider(newConfig.EmbeddingProvider),
	})

	s.cfgMu.Lock()
	s.config = newConfig
	s.embedder = embedder
	s.cfgMu.Unlock()

	// Cached searchers hold the old config/embedder; drop them (each is
	// closed once in-flight requests release it).
	s.invalidateAllSearchers()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// configUpdateMu serialises handleUpdateConfig (read-merge-write of the
// config file and the in-memory config).
var configUpdateMu sync.Mutex

// serverConfigPath returns ~/.gleann/config.json.
func serverConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".gleann", "config.json"), nil
}

// saveServerConfig merges cfg into ~/.gleann/config.json, preserving keys
// gleann.Config does not know about (e.g. roles, TUI-only settings) and
// setting the "completed" marker so auto-setup (EnsureConfig) won't
// overwrite user-saved settings on restart. The file contains API keys, so
// it is written atomically with mode 0600.
func saveServerConfig(cfg gleann.Config) error {
	cfgPath, err := serverConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Read existing config as raw map to preserve unknown fields.
	existing := make(map[string]any)
	if data, err := os.ReadFile(cfgPath); err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &existing); err != nil {
				return fmt.Errorf("existing %s is not valid JSON (fix or remove it): %w", cfgPath, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", cfgPath, err)
	}

	// Marshal the new config into a map and merge.
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	incoming := make(map[string]any)
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return fmt.Errorf("re-decode config: %w", err)
	}
	// Keys owned by gleann.Config that were omitted (omitempty, i.e. now
	// empty) must be removed, otherwise clearing a value such as an API
	// key would silently keep the old one on disk.
	for _, k := range configJSONKeys() {
		if _, ok := incoming[k]; !ok {
			delete(existing, k)
		}
	}
	for k, v := range incoming {
		existing[k] = v
	}
	existing["completed"] = true

	b, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config file: %w", err)
	}
	return writeFileAtomic(cfgPath, b, 0o600)
}

// writeFileAtomic writes data to a temp file in the target directory with
// the given mode and renames it over path, so readers never observe a
// partially written file and the final mode is perm even if path existed
// with looser permissions.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// configJSONKeys returns the top-level JSON keys of gleann.Config.
func configJSONKeys() []string {
	t := reflect.TypeOf(gleann.Config{})
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"logs": s.logBuffer.GetLogs()})
}

// handleClearConversations clears all chat history.
func (s *Server) handleClearConversations(w http.ResponseWriter, r *http.Request) {
	store := conversations.DefaultStore()
	convs, err := store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	count := 0
	for _, c := range convs {
		if store.Delete(c.ID) == nil {
			count++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "count": count})
}

type updateConversationRequest struct {
	Title string `json:"title"`
}

// handleUpdateConversation updates a conversation's title.
func (s *Server) handleUpdateConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "conversation ID required")
		return
	}

	var req updateConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	store := conversations.DefaultStore()
	conv, err := store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}

	conv.Title = req.Title
	if err := store.Save(conv); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save conversation: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, conv)
}

// indexPathRequest is the body for POST /api/indexes/{name}/index-path
type indexPathRequest struct {
	Path        string   `json:"path"`
	Tags        []string `json:"tags,omitempty"`
	Description string   `json:"description,omitempty"`
	MCPExposed  *bool    `json:"mcp_exposed,omitempty"`
	CodeModel   string   `json:"code_model,omitempty"`
	DocModel    string   `json:"doc_model,omitempty"`
}

// handleIndexPath triggers indexing of a local directory or file path on the server.
func (s *Server) handleIndexPath(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	var req indexPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}

	opts := s.buildOptions(name, req.Path)
	opts.Tags = req.Tags
	opts.Description = req.Description
	opts.MCPExposed = req.MCPExposed
	opts.CodeModel = req.CodeModel
	opts.DocModel = req.DocModel

	// The build runs in-process and is bound to the server lifetime: Stop
	// cancels it instead of leaving it running behind.
	ctx := s.lifetime()
	if s.bgManager != nil {
		s.bgManager.Submit(background.TaskTypeAutoIndex, func(progress func(pct float64, msg string)) error {
			progress(0.1, fmt.Sprintf("Building index '%s' from '%s'", name, req.Path))
			if err := s.runBuild(ctx, opts); err != nil {
				return fmt.Errorf("indexing failed: %w", err)
			}
			progress(1.0, fmt.Sprintf("Index '%s' built successfully from '%s'", name, req.Path))
			return nil
		})
	} else {
		s.goBackground(func() {
			if err := s.runBuild(ctx, opts); err != nil {
				log.Printf("Background index build failed for %s: %v", req.Path, err)
			}
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "indexing_started",
		"path":   req.Path,
		"index":  name,
	})
}

// buildOptions returns in-process build options for an index using the
// server's current configuration.
func (s *Server) buildOptions(name, docsDir string) indexing.BuildOptions {
	return indexing.BuildOptions{
		Options: indexing.Options{Config: s.cfg()},
		Name:    name,
		DocsDir: docsDir,
	}
}

// runBuild builds an index in-process and, on success, drops any cached
// searcher(s) for it so the next query sees the new data. For dual-model
// builds both "<name>-code" and "<name>-docs" are invalidated.
func (s *Server) runBuild(ctx context.Context, opts indexing.BuildOptions) error {
	// The graph database admits one writer; release the pooled handles so the
	// build can write the graph (they reopen on the next query).
	if s.graphPool != nil {
		for _, n := range []string{opts.Name, opts.Name + "-code", opts.Name + "-docs"} {
			s.graphPool.close(n)
		}
	}
	var out bytes.Buffer
	opts.Stdout = &out
	opts.Stderr = &out
	res, err := indexing.Build(ctx, opts)
	if err != nil {
		if tail := strings.TrimSpace(lastLines(out.String(), 20)); tail != "" {
			return fmt.Errorf("%w: %s", err, tail)
		}
		return err
	}
	s.invalidateSearcher(opts.Name)
	for _, part := range res.Parts {
		s.invalidateSearcher(part.Name)
	}
	return nil
}

// lastLines returns at most n trailing lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// handleUpload handles binary and text file uploads via multipart form data.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file upload")
		return
	}
	defer file.Close()

	// Never trust the client-supplied filename as a path.
	fileName := filepath.Base(filepath.Clean("/" + strings.ReplaceAll(header.Filename, `\`, "/")))
	if fileName == "" || fileName == "." || fileName == ".." || fileName == "/" {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}

	tmpDir, err := os.MkdirTemp("", "gleann_upload_*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create temp dir")
		return
	}
	// The build below runs synchronously, so the upload can always be
	// cleaned up when the handler returns (success or failure).
	defer os.RemoveAll(tmpDir)

	destPath := filepath.Join(tmpDir, fileName)
	dest, err := os.Create(destPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save file")
		return
	}
	if _, err := io.Copy(dest, file); err != nil {
		dest.Close()
		writeError(w, http.StatusInternalServerError, "failed to save file: "+err.Error())
		return
	}
	if err := dest.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save file: "+err.Error())
		return
	}

	// Build in-process, bound to the server lifetime (Stop cancels it).
	if errBuild := s.runBuild(s.lifetime(), s.buildOptions(name, tmpDir)); errBuild != nil {
		log.Printf("handleUpload index build failed: %v", errBuild)
		writeError(w, http.StatusInternalServerError, "failed to index file: "+errBuild.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "indexed", "file": fileName})
}

func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
		return
	}

	var req struct {
		Enable bool   `json:"enable"`
		Dir    string `json:"dir"` // Required if enable=true and not already set
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	indexes, err := gleann.ListIndexes(s.cfg().IndexDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list indexes")
		return
	}
	var targetMeta *gleann.IndexMeta
	for i := range indexes {
		if indexes[i].Name == name {
			targetMeta = &indexes[i]
			break
		}
	}
	if targetMeta == nil {
		writeError(w, http.StatusNotFound, "index not found")
		return
	}

	if req.Enable {
		dirToWatch := req.Dir
		if dirToWatch == "" {
			dirToWatch = targetMeta.SourceDir
		}
		if dirToWatch == "" {
			writeError(w, http.StatusBadRequest, "no directory provided or configured to watch")
			return
		}

		s.aiMu.Lock()
		ai := s.autoIndexer
		if ai == nil {
			var err error
			ai, err = background.NewAutoIndexer(s.bgManager, background.AutoIndexConfig{
				IndexDir: s.cfg().IndexDir,
			})
			if err != nil {
				s.aiMu.Unlock()
				writeError(w, http.StatusInternalServerError, "failed to start auto-indexer")
				return
			}
			s.autoIndexer = ai
			ai.Start(context.Background())
		}
		s.aiMu.Unlock()

		if err := ai.Watch(name, dirToWatch); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to watch dir: "+err.Error())
			return
		}

		_ = gleann.UpdateIndexMeta(s.cfg().IndexDir, name, func(meta *gleann.IndexMeta) {
			meta.AutoWatch = true
			meta.SourceDir = dirToWatch
		})
	} else {
		if ai := s.getAutoIndexer(); ai != nil {
			ai.Unwatch(name)
		}
		_ = gleann.UpdateIndexMeta(s.cfg().IndexDir, name, func(meta *gleann.IndexMeta) {
			meta.AutoWatch = false
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "watch": req.Enable})
}
