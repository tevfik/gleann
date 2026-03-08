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

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"

	// Register HNSW backend.
	_ "github.com/tevfik/gleann/pkg/backends"
)

// Server is the REST API server for gleann.
type Server struct {
	config    gleann.Config
	embedder  *embedding.Computer
	searchers map[string]*gleann.LeannSearcher
	mu        sync.RWMutex
	addr      string
	version   string
	server    *http.Server
	graphPool *graphDBPool
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
		config:    config,
		embedder:  embedder,
		searchers: make(map[string]*gleann.LeannSearcher),
		addr:      addr,
		version:   version,
		graphPool: newGraphDBPool(config.IndexDir),
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

	// Graph API endpoints (KuzuDB-backed code graph).
	mux.HandleFunc("GET /api/graph/{name}", s.handleGraphStats)
	mux.HandleFunc("POST /api/graph/{name}/query", s.handleGraphQuery)
	mux.HandleFunc("POST /api/graph/{name}/index", s.handleGraphIndex)

	s.server = &http.Server{
		Addr:         s.addr,
		Handler:      withMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("gleann server starting on %s", s.addr)
	return s.server.ListenAndServe()
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	if s.graphPool != nil {
		s.graphPool.closeAll()
	}
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
		writeError(w, http.StatusInternalServerError, "search failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, searchResponse{
		Results: results,
		Count:   len(results),
		QueryMs: time.Since(start).Milliseconds(),
	})
}

type askRequest struct {
	Question    string `json:"question"`
	TopK        int    `json:"top_k,omitempty"`
	LLMModel    string `json:"llm_model,omitempty"`
	LLMProvider string `json:"llm_provider,omitempty"`
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

	chat := gleann.NewChat(searcher, chatConfig)

	start := time.Now()

	var opts []gleann.SearchOption
	if req.TopK > 0 {
		opts = append(opts, gleann.WithTopK(req.TopK))
	}

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
		writeError(w, http.StatusInternalServerError, "build failed: "+err.Error())
		return
	}

	// Clear cached searcher.
	s.mu.Lock()
	delete(s.searchers, name)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"name":    name,
		"count":   len(items),
		"buildMs": time.Since(start).Milliseconds(),
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

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "deleted",
		"name":   name,
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
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})
}
