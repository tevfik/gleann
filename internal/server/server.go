package server
// Package server provides a REST API server for gleann.
// This mirrors Python LEANN's FastAPI server with stdlib net/http.


import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/multimodal"

	"github.com/tevfik/gleann/internal/eventbus"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
	"github.com/tevfik/gleann/pkg/roles"

	// Register HNSW backend.
	_ "github.com/tevfik/gleann/pkg/backends"
)

//go:embed dist/*
var webFS embed.FS

// Server is the REST API server for gleann.
type Server struct {
	config      gleann.Config
	embedder    *embedding.Computer
	logBuffer   *LogBuffer
	searchers   map[string]*gleann.LeannSearcher
	mu          sync.RWMutex
	addr        string
	version     string
	server      *http.Server
	graphPool   *graphDBPool
	memoryPool  *memoryPool         // Memory Engine: generic Entity/RELATES_TO graph
	blockMem    *memory.Manager     // BBolt hierarchical memory blocks (pkg/memory)
	bgManager   *background.Manager // Background task manager
	autoIndexer *background.AutoIndexer
	bus         *eventbus.Bus // In-process pub/sub for lifecycle events
	stopCh      chan struct{} // closed on Stop() to signal background goroutines
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
	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(config.EmbeddingProvider),
		Model:    config.EmbeddingModel,
		BaseURL:  config.OllamaHost,
		APIKey:   config.OpenAIAPIKey,
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

// Start starts the server.
func (s *Server) Start() error {
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
func (s *Server) Stop(ctx context.Context) error {
	// Signal background goroutines to stop.
	if s.stopCh != nil {
		close(s.stopCh)
	}
	if s.autoIndexer != nil {
		s.autoIndexer.Stop()
	}
	if s.bgManager != nil {
		s.bgManager.Stop()
	}
	if s.graphPool != nil {
		s.graphPool.closeAll()
	}
	s.stopMemoryPool(ctx)
	s.closeBlockMem()
	if s.bus != nil {
		_ = s.bus.Close()
	}
	return s.server.Shutdown(ctx)
}

// startAutoIndexer initializes the background auto-indexer.
// It reads GLEANN_AUTO_INDEX_DIRS (format: "name1:dir1,name2:dir2") to
// determine which indexes to watch for file changes.
func (s *Server) startAutoIndexer() {
	ai, err := background.NewAutoIndexer(s.bgManager, background.AutoIndexConfig{
		IndexDir: s.config.IndexDir,
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
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
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
		s.autoIndexer = ai
	} else {
		// Keep the autoIndexer around so we can add watches dynamically later
		s.autoIndexer = ai
	}
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
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var embedderName string
	if s.embedder != nil {
		embedderName = s.embedder.ModelName()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"indexes":                 indexes,
		"count":                   len(indexes),
		"current_embedding_model": embedderName,
	})
}

func (s *Server) handleGetIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
		return
	}

	searcher, err := s.getSearcher(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found: %v", name, err))
		return
	}

	writeJSON(w, http.StatusOK, searcher.Meta())
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
}

type searchResponse struct {
	Results []gleann.SearchResult `json:"results"`
	Count   int                   `json:"count"`
	QueryMs int64                 `json:"query_ms"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
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

	searcher, err := s.getSearcher(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found: %v", name, err))
		return
	}

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

	// Set up per-request reranker if requested.
	if req.Rerank || s.config.SearchConfig.UseReranker {
		opts = append(opts, gleann.WithReranker(true))
		// Ensure the searcher has a reranker configured.
		rerankModel := req.RerankModel
		if rerankModel == "" {
			rerankModel = s.config.SearchConfig.RerankerConfig.Model
		}
		if rerankModel == "" {
			rerankModel = "bge-reranker-v2-m3"
		}
		rerankerCfg := gleann.RerankerConfig{
			Provider: gleann.RerankerProvider(s.config.EmbeddingProvider),
			Model:    rerankModel,
			BaseURL:  s.config.OllamaHost,
			APIKey:   s.config.OpenAIAPIKey,
		}
		searcher.SetReranker(gleann.NewReranker(rerankerCfg))
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
		Results: results,
		Count:   len(results),
		QueryMs: time.Since(start).Milliseconds(),
	})
}

type askRequest struct {
	Question       string `json:"question"`
	TopK           int    `json:"top_k,omitempty"`
	LLMModel       string `json:"llm_model,omitempty"`
	LLMProvider    string `json:"llm_provider,omitempty"`
	SystemPrompt   string `json:"system_prompt,omitempty"`
	Role           string `json:"role,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
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
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
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

	searcher, err := s.getSearcher(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found: %v", name, err))
		return
	}

	chatConfig := gleann.DefaultChatConfig()
	// Apply server config defaults.
	if s.config.LLMModel != "" {
		chatConfig.Model = s.config.LLMModel
	}
	if s.config.LLMProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(s.config.LLMProvider)
	}
	if s.config.OllamaHost != "" {
		chatConfig.BaseURL = s.config.OllamaHost
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
		meta, err := gleann.GetIndexMeta(s.config.IndexDir, name)
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
		
		meta, err := gleann.GetIndexMeta(s.config.IndexDir, indexName)
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
	Texts    []string       `json:"texts"`
	Items    []gleann.Item  `json:"items,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
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

	if meta, err := gleann.GetIndexMeta(s.config.IndexDir, name); err == nil {
		if meta.EmbeddingModel != "" && meta.EmbeddingModel != s.embedder.ModelName() {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("model mismatch: index uses '%s', current is '%s'. please delete index first", meta.EmbeddingModel, s.embedder.ModelName()))
			return
		}
	}

	builder, err := gleann.NewBuilder(s.config, s.embedder)
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

	// Clear cached searcher.
	s.mu.Lock()
	delete(s.searchers, name)
	s.mu.Unlock()

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
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
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

	if meta, err := gleann.GetIndexMeta(s.config.IndexDir, name); err == nil {
		if meta.EmbeddingModel != "" && meta.EmbeddingModel != s.embedder.ModelName() {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("model mismatch: index uses '%s', current is '%s'. please delete index first", meta.EmbeddingModel, s.embedder.ModelName()))
			return
		}
	}

	builder, err := gleann.NewBuilder(s.config, s.embedder)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create builder: "+err.Error())
		return
	}

	start := time.Now()
	if err := builder.UpdateIndex(r.Context(), name, items, req.Sources); err != nil {
		writeError(w, http.StatusInternalServerError, "update index failed: "+err.Error())
		return
	}

	// Invalidate searcher cache for this index
	s.mu.Lock()
	if searcher, ok := s.searchers[name]; ok {
		searcher.Close()
		delete(s.searchers, name)
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"index":           name,
		"updated_sources": req.Sources,
		"items_count":     len(items),
		"duration_ms":     time.Since(start).Milliseconds(),
	})
}

func (s *Server) handleDeleteIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
		return
	}

	// Close cached searcher.
	s.mu.Lock()
	if searcher, ok := s.searchers[name]; ok {
		searcher.Close()
		delete(s.searchers, name)
	}
	s.mu.Unlock()

	if err := gleann.RemoveIndex(s.config.IndexDir, name); err != nil {
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

func (s *Server) getSearcher(ctx context.Context, name string) (*gleann.LeannSearcher, error) {
	s.mu.RLock()
	searcher, ok := s.searchers[name]
	s.mu.RUnlock()

	if ok {
		return searcher, nil
	}

	// Create and cache searcher.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check.
	if searcher, ok := s.searchers[name]; ok {
		return searcher, nil
	}

	// Check model compatibility before loading native FAISS bindings to prevent segfaults.
	if meta, err := gleann.GetIndexMeta(s.config.IndexDir, name); err == nil {
		if meta.EmbeddingModel != "" && meta.EmbeddingModel != s.embedder.ModelName() {
			return nil, fmt.Errorf("model mismatch: index uses '%s', current is '%s'. please delete and recreate index", meta.EmbeddingModel, s.embedder.ModelName())
		}
	}

	searcher = gleann.NewSearcher(s.config, s.embedder)
	if err := searcher.Load(ctx, name); err != nil {
		return nil, err
	}

	s.searchers[name] = searcher
	return searcher, nil
}

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

func withMiddleware(next http.Handler) http.Handler {
	// Chain: body-limit → rate limiter → timeout → CORS/logging.
	return bodyLimitMiddleware(rateLimitMiddleware(timeoutMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Logging.
		start := time.Now()
		next.ServeHTTP(w, r)

		// Skip health check logging.
		if !strings.Contains(r.URL.Path, "health") {
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

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.config)
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
	

	s.mu.Lock()
	s.config = newConfig
	s.embedder = embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(newConfig.EmbeddingProvider),
		Model:    newConfig.EmbeddingModel,
		BaseURL:  newConfig.OllamaHost,
		APIKey:   newConfig.OpenAIAPIKey,
	})
	s.mu.Unlock()
	
	// Save to config file — merge with "completed" marker so auto-setup
	// (EnsureConfig) won't overwrite user-saved settings on restart.
	if home, err := os.UserHomeDir(); err == nil {
		cfgPath := filepath.Join(home, ".gleann", "config.json")
		// Read existing config as raw map to preserve unknown fields.
		existing := make(map[string]any)
		if data, err := os.ReadFile(cfgPath); err == nil {
			_ = json.Unmarshal(data, &existing)
		}
		// Marshal the new config into a map and merge.
		raw, _ := json.Marshal(newConfig)
		_ = json.Unmarshal(raw, &existing)
		existing["completed"] = true
		b, _ := json.MarshalIndent(existing, "", "  ")
		_ = os.WriteFile(cfgPath, b, 0644)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
	Path string `json:"path"`
}

// handleIndexPath triggers indexing of a local directory or file path on the server.
func (s *Server) handleIndexPath(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
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

	if s.bgManager != nil {
		s.bgManager.Submit(background.TaskTypeAutoIndex, func(progress func(pct float64, msg string)) error {
			progress(0.1, fmt.Sprintf("Building index '%s' from '%s'", name, req.Path))
			cmd := exec.Command(os.Args[0], "index", "build", name, "--docs", req.Path)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("indexing failed: %w", err)
			}
			progress(1.0, fmt.Sprintf("Index '%s' built successfully from '%s'", name, req.Path))
			return nil
		})
	} else {
		go func() {
			cmd := exec.Command(os.Args[0], "index", "build", name, "--docs", req.Path)
			if err := cmd.Run(); err != nil {
				log.Printf("Background index build failed for %s: %v", req.Path, err)
			}
		}()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "indexing_started",
		"path":   req.Path,
		"index":  name,
	})
}

// handleUpload handles binary and text file uploads via multipart form data.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file upload")
		return
	}
	defer file.Close()

	tmpDir, err := os.MkdirTemp("", "gleann_upload_*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create temp dir")
		return
	}

	destPath := filepath.Join(tmpDir, header.Filename)
	dest, err := os.Create(destPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save file")
		return
	}
	io.Copy(dest, file)
	dest.Close()

	cmd := exec.Command(os.Args[0], "index", "build", name, "--docs", tmpDir)
	out, errCmd := cmd.CombinedOutput()
	if errCmd != nil {
		fmt.Fprintf(os.Stderr, "handleUpload index build failed: %v\nOutput: %s\n", errCmd, out)
		writeError(w, http.StatusInternalServerError, "failed to index file: "+errCmd.Error())
		return
	}
	os.RemoveAll(tmpDir) // cleanup after success

	writeJSON(w, http.StatusOK, map[string]string{"status": "indexed", "file": header.Filename})
}


func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "index name required")
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

	indexes, err := gleann.ListIndexes(s.config.IndexDir)
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

		if s.autoIndexer == nil {
			ai, err := background.NewAutoIndexer(s.bgManager, background.AutoIndexConfig{
				IndexDir: s.config.IndexDir,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to start auto-indexer")
				return
			}
			s.autoIndexer = ai
			s.autoIndexer.Start(context.Background())
		}

		if err := s.autoIndexer.Watch(name, dirToWatch); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to watch dir: "+err.Error())
			return
		}

		_ = gleann.UpdateIndexMeta(s.config.IndexDir, name, func(meta *gleann.IndexMeta) {
			meta.AutoWatch = true
			meta.SourceDir = dirToWatch
		})
	} else {
		if s.autoIndexer != nil {
			s.autoIndexer.Unwatch(name)
		}
		_ = gleann.UpdateIndexMeta(s.config.IndexDir, name, func(meta *gleann.IndexMeta) {
			meta.AutoWatch = false
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "watch": req.Enable})
}
