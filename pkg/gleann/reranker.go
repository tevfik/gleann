package gleann

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// RerankerProvider specifies which reranking service to use.
type RerankerProvider string

const (
	// RerankerOllama uses Ollama's embedding model in cross-encoder mode.
	// Reranks by computing query-document cosine similarity with a
	// different (typically stronger) embedding model.
	RerankerOllama RerankerProvider = "ollama"

	// RerankerJina uses Jina AI's reranking API.
	RerankerJina RerankerProvider = "jina"

	// RerankerCohere uses Cohere's reranking API.
	RerankerCohere RerankerProvider = "cohere"

	// RerankerVoyage uses Voyage AI's reranking API.
	RerankerVoyage RerankerProvider = "voyage"

	// RerankerLlamacpp uses standard llama.cpp /api/embed functionality
	RerankerLlamacpp RerankerProvider = "llamacpp"
)

// RerankerConfig configures the reranker.
type RerankerConfig struct {
	// Provider selects the reranking service.
	Provider RerankerProvider `json:"provider"`

	// Model is the reranking model name.
	//   - ollama/llamacpp:  e.g. "bge-reranker-v2-m3" or "bge-reranker-v2-gemma-Q4_K_M-GGUF"
	//   - jina:             e.g. "jina-reranker-v2-base-multilingual"
	//   - cohere:           e.g. "rerank-v3.5"
	//   - voyage:           e.g. "rerank-2"
	Model string `json:"model"`

	// BaseURL is the API base URL (required for ollama, optional for others).
	BaseURL string `json:"base_url,omitempty"`

	// APIKey is the API key (required for jina, cohere, voyage).
	APIKey string `json:"api_key,omitempty"`

	// TopN is how many results to keep after reranking (0 = keep all).
	TopN int `json:"top_n,omitempty"`
}

// DefaultRerankerConfig returns a default config using Ollama with bge-reranker.
func DefaultRerankerConfig() RerankerConfig {
	return RerankerConfig{
		Provider: RerankerOllama,
		Model:    "bge-reranker-v2-m3",
		BaseURL:  DefaultOllamaHost,
		TopN:     0,
	}
}

// ── Cross-Encoder Reranker ─────────────────────────────────────

// CrossEncoderReranker implements Reranker using a cross-encoder or
// dedicated reranking API.
type CrossEncoderReranker struct {
	config RerankerConfig
	client *http.Client
}

// NewReranker creates a Reranker from the given config.
func NewReranker(cfg RerankerConfig) *CrossEncoderReranker {
	if cfg.BaseURL == "" {
		switch cfg.Provider {
		case RerankerOllama:
			if host := os.Getenv("OLLAMA_HOST"); host != "" {
				cfg.BaseURL = host
			} else {
				cfg.BaseURL = DefaultOllamaHost
			}
		case RerankerLlamacpp:
			cfg.BaseURL = DefaultLlamaCPPHost
		case RerankerJina:
			cfg.BaseURL = "https://api.jina.ai"
		case RerankerCohere:
			cfg.BaseURL = "https://api.cohere.com"
		case RerankerVoyage:
			cfg.BaseURL = "https://api.voyageai.com"
		}
	}
	if cfg.APIKey == "" {
		switch cfg.Provider {
		case RerankerJina:
			cfg.APIKey = os.Getenv("JINA_API_KEY")
		case RerankerCohere:
			cfg.APIKey = os.Getenv("COHERE_API_KEY")
			if cfg.APIKey == "" {
				cfg.APIKey = os.Getenv("CO_API_KEY")
			}
		case RerankerVoyage:
			cfg.APIKey = os.Getenv("VOYAGE_API_KEY")
		}
	}

	return &CrossEncoderReranker{
		config: cfg,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Rerank implements the Reranker interface.
func (r *CrossEncoderReranker) Rerank(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error) {
	if len(results) == 0 {
		return results, nil
	}

	if topN <= 0 {
		if r.config.TopN > 0 {
			topN = r.config.TopN
		} else {
			topN = len(results)
		}
	}

	switch r.config.Provider {
	case RerankerOllama, RerankerLlamacpp:
		return r.rerankOllama(ctx, query, results, topN)
	case RerankerJina:
		return r.rerankJina(ctx, query, results, topN)
	case RerankerCohere:
		return r.rerankCohere(ctx, query, results, topN)
	case RerankerVoyage:
		return r.rerankVoyage(ctx, query, results, topN)
	default:
		return nil, fmt.Errorf("unsupported reranker provider: %s", r.config.Provider)
	}
}

// ── Ollama (cross-encoder via embedding similarity) ────────────

// Ollama doesn't have a native rerank API, so we simulate cross-encoder
// by computing embeddings for query and each document, then taking
// cosine similarity. This works well with models like bge-reranker-v2-m3
// run through ollama's /api/embed endpoint.
func (r *CrossEncoderReranker) rerankOllama(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error) {
	// Build all texts: [query, doc1, doc2, ...]
	texts := make([]string, 0, len(results)+1)
	texts = append(texts, query)
	for _, res := range results {
		texts = append(texts, res.Text)
	}

	// Compute embeddings for all texts in one batch.
	embeddings, err := r.ollamaEmbed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("ollama embed for reranking: %w", err)
	}

	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embeddings))
	}

	// Score each document against query by cosine similarity.
	queryEmb := embeddings[0]
	scored := make([]SearchResult, len(results))
	copy(scored, results)

	for i := range scored {
		scored[i].Score = cosineSimilarity(queryEmb, embeddings[i+1])
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if topN < len(scored) {
		scored = scored[:topN]
	}
	return scored, nil
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (r *CrossEncoderReranker) ollamaEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{
		Model: r.config.Model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ollama request: %w", err)
	}

	url := strings.TrimRight(r.config.BaseURL, "/") + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			errBody = []byte("(body unreadable)")
		}
		return nil, fmt.Errorf("ollama error %d: %s", resp.StatusCode, string(errBody))
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}
	return result.Embeddings, nil
}

// ── Jina Reranker API ──────────────────────────────────────────

type jinaRerankRequest struct {
	Model     string         `json:"model"`
	Query     string         `json:"query"`
	TopN      int            `json:"top_n,omitempty"`
	Documents []jinaDocument `json:"documents"`
}

type jinaDocument struct {
	Text string `json:"text"`
}

type jinaRerankResponse struct {
	Results []jinaResult `json:"results"`
}

type jinaResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

func (r *CrossEncoderReranker) rerankJina(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error) {
	docs := make([]jinaDocument, len(results))
	for i, res := range results {
		docs[i] = jinaDocument{Text: res.Text}
	}

	reqBody := jinaRerankRequest{
		Model:     r.config.Model,
		Query:     query,
		TopN:      topN,
		Documents: docs,
	}

	body, _ := json.Marshal(reqBody)
	url := strings.TrimRight(r.config.BaseURL, "/") + "/v1/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.config.APIKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jina request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			errBody = []byte("(body unreadable)")
		}
		return nil, fmt.Errorf("jina error %d: %s", resp.StatusCode, string(errBody))
	}

	var jinaResp jinaRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&jinaResp); err != nil {
		return nil, fmt.Errorf("decode jina response: %w", err)
	}

	reranked := make([]SearchResult, 0, len(jinaResp.Results))
	for _, jr := range jinaResp.Results {
		if jr.Index < len(results) {
			res := results[jr.Index]
			res.Score = float32(jr.RelevanceScore)
			reranked = append(reranked, res)
		}
	}

	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Score > reranked[j].Score
	})

	return reranked, nil
}

// ── Cohere Reranker API ────────────────────────────────────────

type cohereRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	TopN      int      `json:"top_n,omitempty"`
	Documents []string `json:"documents"`
}

type cohereRerankResponse struct {
	Results []cohereResult `json:"results"`
}

type cohereResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

func (r *CrossEncoderReranker) rerankCohere(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error) {
	docs := make([]string, len(results))
	for i, res := range results {
		docs[i] = res.Text
	}

	reqBody := cohereRerankRequest{
		Model:     r.config.Model,
		Query:     query,
		TopN:      topN,
		Documents: docs,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal cohere request: %w", err)
	}
	url := strings.TrimRight(r.config.BaseURL, "/") + "/v2/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.config.APIKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cohere request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			errBody = []byte("(body unreadable)")
		}
		return nil, fmt.Errorf("cohere error %d: %s", resp.StatusCode, string(errBody))
	}

	var cohereResp cohereRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&cohereResp); err != nil {
		return nil, fmt.Errorf("decode cohere response: %w", err)
	}

	reranked := make([]SearchResult, 0, len(cohereResp.Results))
	for _, cr := range cohereResp.Results {
		if cr.Index < len(results) {
			res := results[cr.Index]
			res.Score = float32(cr.RelevanceScore)
			reranked = append(reranked, res)
		}
	}

	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Score > reranked[j].Score
	})

	return reranked, nil
}

// ── Voyage AI Reranker API ─────────────────────────────────────

type voyageRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	TopK      int      `json:"top_k,omitempty"`
	Documents []string `json:"documents"`
}

type voyageRerankResponse struct {
	Data []voyageResult `json:"data"`
}

type voyageResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

func (r *CrossEncoderReranker) rerankVoyage(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error) {
	docs := make([]string, len(results))
	for i, res := range results {
		docs[i] = res.Text
	}

	reqBody := voyageRerankRequest{
		Model:     r.config.Model,
		Query:     query,
		TopK:      topN,
		Documents: docs,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal voyage request: %w", err)
	}
	url := strings.TrimRight(r.config.BaseURL, "/") + "/v1/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.config.APIKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			errBody = []byte("(body unreadable)")
		}
		return nil, fmt.Errorf("voyage error %d: %s", resp.StatusCode, string(errBody))
	}

	var voyageResp voyageRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&voyageResp); err != nil {
		return nil, fmt.Errorf("decode voyage response: %w", err)
	}

	reranked := make([]SearchResult, 0, len(voyageResp.Data))
	for _, vr := range voyageResp.Data {
		if vr.Index < len(results) {
			res := results[vr.Index]
			res.Score = float32(vr.RelevanceScore)
			reranked = append(reranked, res)
		}
	}

	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Score > reranked[j].Score
	})

	return reranked, nil
}
