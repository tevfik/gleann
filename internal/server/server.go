// Package server provides a REST API server for gleann.
// This mirrors Python LEANN's FastAPI server with stdlib net/http.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
	"github.com/tevfik/gleann/pkg/roles"

	// Register HNSW backend.
	_ "github.com/tevfik/gleann/pkg/backends"
)

// Server is the REST API server for gleann.
type Server struct {
	config     gleann.Config
	embedder   *embedding.Computer
	searchers  map[string]*gleann.LeannSearcher
	mu         sync.RWMutex
	addr       string
	version    string
	server     *http.Server
	graphPool  *graphDBPool
	memoryPool *memoryPool         // Memory Engine: generic Entity/RELATES_TO graph
	blockMem   *memory.Manager     // BBolt hierarchical memory blocks (pkg/memory)
	bgManager  *background.Manager // Background task manager
	stopCh     chan struct{}       // closed on Stop() to signal background goroutines
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

	return &Server{
		config:     config,
		embedder:   embedder,
		searchers:  make(map[string]*gleann.LeannSearcher),
		addr:       addr,
		version:    version,
		graphPool:  newGraphDBPool(config.IndexDir),
		memoryPool: newMemoryPool(config.IndexDir),
		bgManager:  background.NewManager(2),
	}
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

	// Unified memory API (orchestrates blocks + graph + vector).
	s.mountUnifiedMemory(mux)

	s.server = &http.Server{
		Addr:         s.addr,
		Handler:      withMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start background maintenance scheduler for BBolt memory.
	s.stopCh = make(chan struct{})
	if mgr, err := s.blockManager(); err == nil {
		startMaintenanceScheduler(mgr, s.stopCh)
		startSleepTimeEngine(mgr, s.stopCh)
	}

	log.Printf("gleann server starting on %s", s.addr)
	return s.server.ListenAndServe()
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	// Signal background goroutines to stop.
	if s.stopCh != nil {
		close(s.stopCh)
	}
	if s.bgManager != nil {
		s.bgManager.Stop()
	}
	if s.graphPool != nil {
		s.graphPool.closeAll()
	}
	s.stopMemoryPool(ctx)
	s.closeBlockMem()
	return s.server.Shutdown(ctx)
}

// --- Handlers ---

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
	writeJSON(w, http.StatusOK, map[string]any{
		"indexes": indexes,
		"count":   len(indexes),
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
	Stream         bool   `json:"stream,omitempty"`
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
	if req.LLMModel != "" {
		chatConfig.Model = req.LLMModel
	}
	if req.LLMProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(req.LLMProvider)
	}
	if req.SystemPrompt != "" {
		chatConfig.SystemPrompt = req.SystemPrompt
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
		s.handleAskStream(w, r, chat, req.Question, opts)
		return
	}

	start := time.Now()

	answer, err := chat.Ask(r.Context(), req.Question, opts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ask failed: "+err.Error())
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
func (s *Server) handleAskStream(w http.ResponseWriter, r *http.Request, chat *gleann.LeannChat, question string, opts []gleann.SearchOption) {
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

	err := chat.AskStream(r.Context(), question, callback, opts...)
	if err != nil {
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
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

	builder, err := gleann.NewBuilder(s.config, s.embedder)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create builder: "+err.Error())
		return
	}

	start := time.Now()
	if err := builder.Build(r.Context(), name, items); err != nil {
		serverMetrics.RecordBuild(time.Since(start), true)
		writeError(w, http.StatusInternalServerError, "build failed: "+err.Error())
		return
	}
	buildDuration := time.Since(start)
	serverMetrics.RecordBuild(buildDuration, false)

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
	// Chain: rate limiter → timeout → CORS/logging.
	return rateLimitMiddleware(timeoutMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})))
}
