// Package embedding provides embedding computation via various providers
// (Ollama, OpenAI-compatible APIs).
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Provider is the embedding computation provider type.
type Provider string

const (
	ProviderOllama   Provider = "ollama"
	ProviderLlamaCPP Provider = "llamacpp"
	ProviderOpenAI   Provider = "openai"
	ProviderGemini   Provider = "gemini"
)

// Computer computes embeddings using a specified provider.
type Computer struct {
	provider       Provider
	model          string
	baseURL        string
	apiKey         string
	dimensions     int
	batchSize      int
	concurrency    int
	promptTemplate string
	client         *http.Client
	mu             sync.Mutex
	dimOnce        sync.Once
}

// Options configures the embedding computer.
type Options struct {
	Provider       Provider
	Model          string
	BaseURL        string
	APIKey         string
	BatchSize      int
	Concurrency    int    // Number of concurrent batches
	PromptTemplate string // Prepended to text before embedding
}

// NewComputer creates a new embedding computer.
func NewComputer(opts Options) *Computer {
	if opts.BatchSize <= 0 {
		if opts.Provider == ProviderOllama {
			opts.BatchSize = 256 // Stable medium-large batch for Ollama
		} else {
			opts.BatchSize = 100 // External APIs handle larger batches
		}
	}
	if opts.Model == "" {
		opts.Model = "bge-m3"
	}
	if opts.Provider == "" {
		opts.Provider = ProviderOllama
	}
	if opts.BaseURL == "" {
		switch opts.Provider {
		case ProviderOllama:
			opts.BaseURL = resolveOllamaHost()
		case ProviderLlamaCPP:
			opts.BaseURL = "http://localhost:8080"
		case ProviderOpenAI:
			opts.BaseURL = resolveOpenAIBaseURL()
		case ProviderGemini:
			opts.BaseURL = "https://generativelanguage.googleapis.com"
		}
	} else if opts.Provider == ProviderLlamaCPP && (strings.Contains(opts.BaseURL, "11434") || strings.Contains(opts.BaseURL, "(auto-scan")) {
		// Prevent cross-contamination from shared Config.OllamaHost defaults in callers
		opts.BaseURL = "http://localhost:8080"
	}

	if opts.APIKey == "" {
		switch opts.Provider {
		case ProviderOpenAI:
			opts.APIKey = os.Getenv("OPENAI_API_KEY")
		case ProviderGemini:
			opts.APIKey = os.Getenv("GEMINI_API_KEY")
			if opts.APIKey == "" {
				opts.APIKey = os.Getenv("GOOGLE_API_KEY")
			}
		}
	}
	if opts.Concurrency <= 0 {
		if opts.Provider == ProviderOllama {
			opts.Concurrency = 4 // Massive batches so lower concurrency
		} else {
			opts.Concurrency = 20 // External providers
		}
	}

	return &Computer{
		provider:       opts.Provider,
		model:          opts.Model,
		baseURL:        opts.BaseURL,
		apiKey:         opts.APIKey,
		batchSize:      opts.BatchSize,
		concurrency:    opts.Concurrency,
		promptTemplate: opts.PromptTemplate,
		client: &http.Client{
			// Give 60 minutes for massive GPU batch processing.
			Timeout: 60 * time.Minute,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 60 * time.Minute,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				ResponseHeaderTimeout: 60 * time.Minute, // The crucial part: waiting for Ollama to process the batch
			},
		},
	}
}

// Compute computes embeddings for the given texts.
func (c *Computer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Apply prompt template if set.
	processedTexts := texts
	if c.promptTemplate != "" {
		processedTexts = make([]string, len(texts))
		for i, t := range texts {
			processedTexts[i] = c.promptTemplate + t
		}
	}

	// Truncate texts that exceed model's token limit.
	tokenLimit := GetModelTokenLimit(c.model)
	for i, t := range processedTexts {
		processedTexts[i] = TruncateToTokenLimit(t, tokenLimit)
	}

	allEmbeddings := make([][]float32, len(processedTexts))

	sem := make(chan struct{}, c.concurrency)
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	// Calculate number of batches to avoid math.Ceil overhead
	numBatches := (len(processedTexts) + c.batchSize - 1) / c.batchSize
	var processedBatches atomic.Int32

	if len(processedTexts) > 50 {
		fmt.Printf("🚀 Starting embedding computation for %d items over %d batches (concurrency: %d)\n", len(processedTexts), numBatches, c.concurrency)
	}

	for i := 0; i < numBatches; i++ {
		start := i * c.batchSize
		end := start + c.batchSize
		if end > len(processedTexts) {
			end = len(processedTexts)
		}

		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(startIdx, endIdx int, batch []string) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			// Don't start new work if there's an error
			if firstErr != nil {
				return
			}

			var embeddings [][]float32
			var err error

			switch c.provider {
			case ProviderOllama, ProviderLlamaCPP:
				embeddings, err = c.computeOllama(ctx, batch)
			case ProviderOpenAI:
				embeddings, err = c.computeOpenAI(ctx, batch)
			case ProviderGemini:
				embeddings, err = c.computeGemini(ctx, batch)
			default:
				err = fmt.Errorf("unsupported provider: %s", c.provider)
			}

			if err != nil {
				// Retry one-by-one if batch fails (e.g., one text too long).
				fmt.Printf("⚠️ Batch %d failed, retrying one-by-one: %v\n", startIdx, err)
				if len(batch) > 1 {
					var singleRetryEmbeddings [][]float32
					for j, text := range batch {
						var single [][]float32
						var singleErr error
						// Aggressively truncate on retry.
						truncated := TruncateToTokenLimit(text, GetModelTokenLimit(c.model)/2)
						switch c.provider {
						case ProviderOllama, ProviderLlamaCPP:
							single, singleErr = c.computeOllama(ctx, []string{truncated})
						case ProviderOpenAI:
							single, singleErr = c.computeOpenAI(ctx, []string{truncated})
						case ProviderGemini:
							single, singleErr = c.computeGemini(ctx, []string{truncated})
						}

						if singleErr != nil {
							errOnce.Do(func() {
								firstErr = fmt.Errorf("compute text %d (retry): %w", startIdx+j, singleErr)
							})
							return
						}
						singleRetryEmbeddings = append(singleRetryEmbeddings, single...)
					}
					embeddings = singleRetryEmbeddings
				} else {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("compute batch %d-%d: %w", startIdx, endIdx, err)
					})
					return
				}
			}

			// Wait until embeddings are ready, then copy to master array thread-safe
			for i, emb := range embeddings {
				allEmbeddings[startIdx+i] = emb
			}

			// Atomic progress tracking — no mutex needed
			current := processedBatches.Add(1)
			if len(processedTexts) > 50 {
				if current%50 == 0 || current == int32(numBatches) {
					fmt.Printf("⏳ Embeddings progress: %d / %d batches complete...\n", current, numBatches)
				}
			}
		}(start, end, processedTexts[start:end])
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	// Cache dimensions from first result.
	if len(allEmbeddings) > 0 && c.dimensions == 0 {
		c.mu.Lock()
		c.dimensions = len(allEmbeddings[0])
		c.mu.Unlock()
	}

	return allEmbeddings, nil
}

// ComputeSingle computes an embedding for a single text.
func (c *Computer) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	results, err := c.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return results[0], nil
}

// Dimensions returns the embedding dimensions (0 if not yet computed).
func (c *Computer) Dimensions() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dimensions
}

// ModelName returns the model name.
func (c *Computer) ModelName() string {
	return c.model
}

// SetPromptTemplate updates the prompt template (e.g., switch between build/query templates).
func (c *Computer) SetPromptTemplate(template string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.promptTemplate = template
}

// modelTokenLimits maps model names to their maximum token limits.
var modelTokenLimits = map[string]int{
	// OpenAI
	"text-embedding-3-small": 8191,
	"text-embedding-3-large": 8191,
	"text-embedding-ada-002": 8191,
	// Ollama / open-source
	"bge-m3":                 8192,
	"bge-large-en-v1.5":      512,
	"bge-small-en-v1.5":      512,
	"nomic-embed-text":       8192,
	"mxbai-embed-large":      512,
	"all-minilm":             256,
	"snowflake-arctic-embed": 512,
	// Gemini
	"text-embedding-004": 2048,
	// Default fallback
	"default": 512,
}

// GetModelTokenLimit returns the token limit for a model.
func GetModelTokenLimit(model string) int {
	if limit, ok := modelTokenLimits[model]; ok {
		return limit
	}
	return modelTokenLimits["default"]
}

// TruncateToTokenLimit approximates token count and truncates if needed.
func TruncateToTokenLimit(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return text
	}
	// Very conservative: assume 1 token per character (safe for CJK, code, URLs).
	// Most tokenizers average ~3-4 chars/token for English, but edge cases can be 1:1.
	maxChars := maxTokens
	if len(text) <= maxChars {
		return text
	}
	// Truncate at word boundary.
	truncated := text[:maxChars]
	lastSpace := len(truncated) - 1
	for lastSpace > 0 && truncated[lastSpace] != ' ' {
		lastSpace--
	}
	if lastSpace > 0 {
		return truncated[:lastSpace]
	}
	return truncated
}

// --- Ollama Provider ---

type ollamaEmbedRequest struct {
	Model    string `json:"model"`
	Input    any    `json:"input"`    // string or []string
	Truncate bool   `json:"truncate"` // let Ollama truncate if too long
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (c *Computer) computeOllama(ctx context.Context, texts []string) ([][]float32, error) {
	// Ollama /api/embed supports batch input.
	reqBody := ollamaEmbedRequest{
		Model:    c.model,
		Input:    texts,
		Truncate: true,
	}

	// Use Encoder to avoid escaping <, >, & in HTML content.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(reqBody); err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body := buf.Bytes()

	url := c.baseURL + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Embeddings))
	}

	return result.Embeddings, nil
}

// --- OpenAI Provider ---

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *Computer) computeOpenAI(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := openAIEmbedRequest{
		Model: c.model,
		Input: texts,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	var result openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Sort by index to preserve order.
	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}

	return embeddings, nil
}

// --- Settings ---

func resolveOllamaHost() string {
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		return host
	}
	return "http://localhost:11434"
}

func resolveOpenAIBaseURL() string {
	if url := os.Getenv("OPENAI_BASE_URL"); url != "" {
		return url
	}
	return "https://api.openai.com"
}

// --- Gemini Provider ---

type geminiEmbedRequest struct {
	Requests []geminiEmbedPart `json:"requests"`
}

type geminiEmbedPart struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
}

type geminiContent struct {
	Parts []geminiTextPart `json:"parts"`
}

type geminiTextPart struct {
	Text string `json:"text"`
}

type geminiEmbedResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

func (c *Computer) computeGemini(ctx context.Context, texts []string) ([][]float32, error) {
	model := c.model
	if model == "" {
		model = "text-embedding-004"
	}

	// Gemini batch embed endpoint.
	requests := make([]geminiEmbedPart, len(texts))
	for i, text := range texts {
		requests[i] = geminiEmbedPart{
			Model: "models/" + model,
			Content: geminiContent{
				Parts: []geminiTextPart{{Text: text}},
			},
		}
	}

	reqBody := geminiEmbedRequest{Requests: requests}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:batchEmbedContents?key=%s",
		c.baseURL, model, c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST gemini: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result geminiEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	embeddings := make([][]float32, len(result.Embeddings))
	for i, emb := range result.Embeddings {
		embeddings[i] = emb.Values
	}

	return embeddings, nil
}
