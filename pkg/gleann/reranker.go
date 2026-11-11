package gleann

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// RerankerOllama is accepted for configuration compatibility, but Ollama
	// has no cross-encoder (rerank) API: embedding a reranker model and
	// comparing vectors is not reranking (it lowered MRR on the gleann
	// benchmark and crashes for bge-reranker-v2-m3). Rerank returns an
	// error, so search keeps the first-stage order. Use "eif" (in process)
	// or "llamacpp" with a llama-server --reranking URL.
	// Reranks by computing query-document cosine similarity with a
	// different (typically stronger) embedding model.
	RerankerOllama RerankerProvider = "ollama"

	// RerankerJina uses Jina AI's reranking API.
	RerankerJina RerankerProvider = "jina"

	// RerankerCohere uses Cohere's reranking API.
	RerankerCohere RerankerProvider = "cohere"

	// RerankerVoyage uses Voyage AI's reranking API.
	RerankerVoyage RerankerProvider = "voyage"

	// RerankerLlamacpp calls a llama-server started with --reranking
	// (POST {BaseURL}/v1/rerank, Jina-compatible).
	RerankerLlamacpp RerankerProvider = "llamacpp"

	// RerankerEIF runs a GGUF cross-encoder (bge-reranker-v2-m3,
	// mmarco-mMiniLMv2, ...) in process through bitllm.c. Needs a build with
	// the eif tag; Model is a path or a file name in ~/.gleann/models.
	RerankerEIF RerankerProvider = "eif"
)

// EIFRankFunc scores docs against query with an in-process cross-encoder and
// returns one relevance logit per doc (higher is more relevant).
type EIFRankFunc func(ctx context.Context, model, query string, docs []string) ([]float32, error)

var eifRank EIFRankFunc

// RegisterEIFRanker installs the in-process cross-encoder used by the "eif"
// reranker provider. The eif build of internal/embedding calls it from init.
func RegisterEIFRanker(fn EIFRankFunc) { eifRank = fn }

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

	// MaxDocChars truncates each candidate's text before scoring (0 = no
	// limit). Cross-encoders cost grows with input length, and on the
	// gleann benchmark the first ~128 tokens of a chunk ranked as well as
	// the whole chunk (see docs/retrieval-experiments.md).
	MaxDocChars int `json:"max_doc_chars,omitempty"`
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
		case RerankerOllama:
			cfg.APIKey = os.Getenv(OllamaAPIKeyEnv)
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

	if n := r.config.MaxDocChars; n > 0 {
		return r.rerankTruncated(ctx, query, results, topN, n)
	}

	switch r.config.Provider {
	case RerankerOllama:
		return nil, errOllamaNoRerank
	case RerankerLlamacpp, RerankerJina:
		return r.rerankJina(ctx, query, results, topN)
	case RerankerCohere:
		return r.rerankCohere(ctx, query, results, topN)
	case RerankerVoyage:
		return r.rerankVoyage(ctx, query, results, topN)
	case RerankerEIF:
		return r.rerankEIF(ctx, query, results, topN)
	default:
		return nil, fmt.Errorf("unsupported reranker provider: %s", r.config.Provider)
	}
}

// rerankTruncated scores truncated copies of the candidates and returns the
// reranked results with their full text restored.
func (r *CrossEncoderReranker) rerankTruncated(ctx context.Context, query string, results []SearchResult, topN, maxChars int) ([]SearchResult, error) {
	full := make(map[int64]string, len(results))
	short := make([]SearchResult, len(results))
	for i, res := range results {
		if _, dup := full[res.ID]; dup {
			// IDs collide (e.g. results from several indexes): score full texts.
			cfg := r.config
			cfg.MaxDocChars = 0
			return (&CrossEncoderReranker{config: cfg, client: r.client}).Rerank(ctx, query, results, topN)
		}
		full[res.ID] = res.Text
		short[i] = res
		short[i].Text = truncateRunes(res.Text, maxChars)
	}
	cfg := r.config
	cfg.MaxDocChars = 0
	out, err := (&CrossEncoderReranker{config: cfg, client: r.client}).Rerank(ctx, query, short, topN)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Text = full[out[i].ID]
	}
	return out, nil
}

// truncateRunes returns at most n runes of s.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// errOllamaNoRerank explains why the Ollama reranker is not used.
var errOllamaNoRerank = errors.New("ollama has no rerank API; set rerank_provider to \"eif\" (in-process GGUF cross-encoder) or \"llamacpp\" with rerank_url pointing at a llama-server --reranking")

// ── In-process cross-encoder (bitllm.c) ────────────────────────

func (r *CrossEncoderReranker) rerankEIF(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error) {
	if eifRank == nil {
		return nil, fmt.Errorf("eif reranker: this gleann build has no in-process engine (build with -tags eif), or use --rerank-url")
	}
	docs := make([]string, len(results))
	for i, res := range results {
		docs[i] = res.Text
	}
	scores, err := eifRank(ctx, r.config.Model, query, docs)
	if err != nil {
		return nil, fmt.Errorf("eif reranker: %w", err)
	}
	if len(scores) != len(results) {
		return nil, fmt.Errorf("eif reranker: got %d scores for %d documents", len(scores), len(results))
	}
	reranked := make([]SearchResult, len(results))
	copy(reranked, results)
	for i := range reranked {
		reranked[i].Score = scores[i]
	}
	sort.SliceStable(reranked, func(i, j int) bool { return reranked[i].Score > reranked[j].Score })
	if topN > 0 && len(reranked) > topN {
		reranked = reranked[:topN]
	}
	return reranked, nil
}

// ── Jina Reranker API ──────────────────────────────────────────

type jinaRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	TopN      int      `json:"top_n,omitempty"`
	Documents []string `json:"documents"` // plain strings: accepted by Jina and by llama-server
}

type jinaRerankResponse struct {
	Results []jinaResult `json:"results"`
}

type jinaResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

func (r *CrossEncoderReranker) rerankJina(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error) {
	docs := make([]string, len(results))
	for i, res := range results {
		docs[i] = res.Text
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
	// Self-hosted servers (llama-server --reranking) need no key.
	if r.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	}

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

	// Not every server honours top_n (llama-server returns every document).
	if topN > 0 && len(reranked) > topN {
		reranked = reranked[:topN]
	}
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
