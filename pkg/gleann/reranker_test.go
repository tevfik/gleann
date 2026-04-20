package gleann

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ── Rerank dispatch ────────────────────────────────────────────

func TestRerank_EmptyResults(t *testing.T) {
	r := NewReranker(RerankerConfig{Provider: RerankerOllama})
	got, err := r.Rerank(context.Background(), "query", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestRerank_UnsupportedProvider(t *testing.T) {
	r := NewReranker(RerankerConfig{Provider: "unknown"})
	results := []SearchResult{{Text: "a", Score: 0.5}}
	_, err := r.Rerank(context.Background(), "query", results, 5)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestRerank_TopNFromConfig(t *testing.T) {
	// When topN=0, falls back to config.TopN, then to len(results).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return embeddings: 3 vectors (1 query + 2 docs), all dimension 2.
		json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: [][]float32{
				{1.0, 0.0}, // query
				{1.0, 0.0}, // doc0 — identical to query → score=1.0
				{0.0, 1.0}, // doc1 — orthogonal → score≈0.0
			},
		})
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerOllama,
		Model:    "test-model",
		BaseURL:  srv.URL,
		TopN:     1, // keep only top 1
	})
	results := []SearchResult{
		{Text: "doc0", Score: 0.1},
		{Text: "doc1", Score: 0.9},
	}
	got, err := r.Rerank(context.Background(), "query", results, 0 /* use config TopN */)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result (topN from config), got %d", len(got))
	}
	if got[0].Text != "doc0" {
		t.Errorf("expected doc0 (highest similarity), got %s", got[0].Text)
	}
}

// ── Ollama reranker ────────────────────────────────────────────

func TestRerankOllama_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req ollamaEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "test-model" {
			t.Errorf("expected model test-model, got %s", req.Model)
		}
		if len(req.Input) != 4 { // query + 3 docs
			t.Errorf("expected 4 inputs, got %d", len(req.Input))
		}
		// Return embeddings: query=[1,0], doc0=[0.9,0.1], doc1=[0,1], doc2=[0.5,0.5]
		json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: [][]float32{
				{1.0, 0.0}, // query
				{0.9, 0.1}, // doc0 — high similarity
				{0.0, 1.0}, // doc1 — low similarity
				{0.5, 0.5}, // doc2 — medium similarity
			},
		})
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerOllama,
		Model:    "test-model",
		BaseURL:  srv.URL,
	})
	results := []SearchResult{
		{ID: 1, Text: "doc0", Score: 0.1},
		{ID: 2, Text: "doc1", Score: 0.9},
		{ID: 3, Text: "doc2", Score: 0.5},
	}
	got, err := r.Rerank(context.Background(), "query", results, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	// doc0 should be first (highest cosine similarity to query)
	if got[0].Text != "doc0" {
		t.Errorf("expected doc0 first, got %s", got[0].Text)
	}
	if got[1].Text != "doc2" {
		t.Errorf("expected doc2 second, got %s", got[1].Text)
	}
}

func TestRerankOllama_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerOllama,
		Model:    "bad-model",
		BaseURL:  srv.URL,
	})
	results := []SearchResult{{Text: "doc0", Score: 0.5}}
	_, err := r.Rerank(context.Background(), "query", results, 5)
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestRerankOllama_EmbeddingCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return wrong number of embeddings.
		json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: [][]float32{{1.0, 0.0}}, // only 1 instead of 2
		})
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerOllama,
		Model:    "test-model",
		BaseURL:  srv.URL,
	})
	results := []SearchResult{{Text: "doc0", Score: 0.5}}
	_, err := r.Rerank(context.Background(), "query", results, 5)
	if err == nil {
		t.Fatal("expected error for embedding count mismatch")
	}
}

// ── Jina reranker ──────────────────────────────────────────────

func TestRerankJina_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or wrong auth header")
		}
		var req jinaRerankRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "jina-reranker-v2" {
			t.Errorf("expected model jina-reranker-v2, got %s", req.Model)
		}
		json.NewEncoder(w).Encode(jinaRerankResponse{
			Results: []jinaResult{
				{Index: 1, RelevanceScore: 0.95},
				{Index: 0, RelevanceScore: 0.80},
			},
		})
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerJina,
		Model:    "jina-reranker-v2",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
	})
	results := []SearchResult{
		{ID: 1, Text: "first doc", Score: 0.5},
		{ID: 2, Text: "second doc", Score: 0.3},
	}
	got, err := r.Rerank(context.Background(), "test query", results, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	// Second doc reranked higher (0.95 > 0.80)
	if got[0].Text != "second doc" {
		t.Errorf("expected second doc first, got %s", got[0].Text)
	}
}

func TestRerankJina_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid api key"))
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerJina,
		Model:    "jina-reranker-v2",
		BaseURL:  srv.URL,
		APIKey:   "bad-key",
	})
	results := []SearchResult{{Text: "doc", Score: 0.5}}
	_, err := r.Rerank(context.Background(), "query", results, 5)
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

// ── Cohere reranker ────────────────────────────────────────────

func TestRerankCohere_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/rerank" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer cohere-key" {
			t.Errorf("missing or wrong auth header")
		}
		var req cohereRerankRequest
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Documents) != 3 {
			t.Errorf("expected 3 docs, got %d", len(req.Documents))
		}
		json.NewEncoder(w).Encode(cohereRerankResponse{
			Results: []cohereResult{
				{Index: 2, RelevanceScore: 0.99},
				{Index: 0, RelevanceScore: 0.70},
				{Index: 1, RelevanceScore: 0.50},
			},
		})
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerCohere,
		Model:    "rerank-v3.5",
		BaseURL:  srv.URL,
		APIKey:   "cohere-key",
	})
	results := []SearchResult{
		{ID: 1, Text: "alpha", Score: 0.1},
		{ID: 2, Text: "beta", Score: 0.2},
		{ID: 3, Text: "gamma", Score: 0.3},
	}
	got, err := r.Rerank(context.Background(), "query", results, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	if got[0].Text != "gamma" {
		t.Errorf("expected gamma first (score 0.99), got %s", got[0].Text)
	}
}

func TestRerankCohere_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerCohere,
		Model:    "rerank-v3.5",
		BaseURL:  srv.URL,
		APIKey:   "key",
	})
	results := []SearchResult{{Text: "doc", Score: 0.5}}
	_, err := r.Rerank(context.Background(), "query", results, 5)
	if err == nil {
		t.Fatal("expected error for 400")
	}
}

// ── Voyage reranker ────────────────────────────────────────────

func TestRerankVoyage_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer voyage-key" {
			t.Errorf("missing or wrong auth header")
		}
		var req voyageRerankRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.TopK != 2 {
			t.Errorf("expected top_k=2, got %d", req.TopK)
		}
		json.NewEncoder(w).Encode(voyageRerankResponse{
			Data: []voyageResult{
				{Index: 1, RelevanceScore: 0.88},
				{Index: 0, RelevanceScore: 0.75},
			},
		})
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerVoyage,
		Model:    "rerank-2",
		BaseURL:  srv.URL,
		APIKey:   "voyage-key",
	})
	results := []SearchResult{
		{ID: 1, Text: "first", Score: 0.5},
		{ID: 2, Text: "second", Score: 0.3},
	}
	got, err := r.Rerank(context.Background(), "query", results, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].Text != "second" {
		t.Errorf("expected second first (score 0.88), got %s", got[0].Text)
	}
}

func TestRerankVoyage_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerVoyage,
		Model:    "rerank-2",
		BaseURL:  srv.URL,
		APIKey:   "key",
	})
	results := []SearchResult{{Text: "doc", Score: 0.5}}
	_, err := r.Rerank(context.Background(), "query", results, 5)
	if err == nil {
		t.Fatal("expected error for 429")
	}
}

// ── Voyage with out-of-range index ─────────────────────────────

func TestRerankVoyage_OutOfRangeIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(voyageRerankResponse{
			Data: []voyageResult{
				{Index: 999, RelevanceScore: 0.99}, // out of range
				{Index: 0, RelevanceScore: 0.50},
			},
		})
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerVoyage,
		Model:    "rerank-2",
		BaseURL:  srv.URL,
		APIKey:   "key",
	})
	results := []SearchResult{{Text: "doc0", Score: 0.5}}
	got, err := r.Rerank(context.Background(), "query", results, 5)
	if err != nil {
		t.Fatal(err)
	}
	// Only 1 result (the in-range one)
	if len(got) != 1 {
		t.Errorf("expected 1 result (skip out-of-range), got %d", len(got))
	}
}

// ── LlamaCPP provider routes to rerankOllama ───────────────────

func TestRerank_LlamaCPP_RoutesToOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: [][]float32{
				{1.0, 0.0}, // query
				{0.8, 0.2}, // doc0
			},
		})
	}))
	defer srv.Close()

	r := NewReranker(RerankerConfig{
		Provider: RerankerLlamacpp,
		Model:    "test.gguf",
		BaseURL:  srv.URL,
	})
	results := []SearchResult{{Text: "doc0", Score: 0.1}}
	got, err := r.Rerank(context.Background(), "query", results, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
}
