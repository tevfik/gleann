package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ModelInfo holds info about a model fetched from a provider.
type ModelInfo struct {
	Name string
	Size string // human-readable size (if available)
	Tag  string // extra detail
}

// fetchModels queries the provider API and returns available models.
func fetchModels(provider, host, apiKey string) ([]ModelInfo, error) {
	switch provider {
	case "ollama":
		return fetchOllamaModels(host)
	case "openai":
		return fetchOpenAIModels(host, apiKey)
	case "llamacpp":
		return fetchLlamaCPPModels(host)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

// --- Ollama: GET /api/tags ---

type ollamaTagsResponse struct {
	Models []ollamaModel `json:"models"`
}

type ollamaModel struct {
	Name       string            `json:"name"`
	Size       int64             `json:"size"`
	ModifiedAt string            `json:"modified_at"`
	Details    ollamaModelDetail `json:"details"`
}

type ollamaModelDetail struct {
	Family            string `json:"family"`
	ParameterSize     string `json:"parameter_size"`
	QuantizationLevel string `json:"quantization_level"`
}

func fetchOllamaModels(host string) ([]ModelInfo, error) {
	if host == "" {
		host = gleann.DefaultOllamaHost
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(host + "/api/tags")
	if err != nil {
		return nil, fmt.Errorf("connect to Ollama at %s: %w", host, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			body = []byte("(body unreadable)")
		}
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
	}

	var result ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Ollama response: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		info := ModelInfo{
			Name: m.Name,
			Size: formatModelSize(m.Size),
		}
		if m.Details.ParameterSize != "" {
			info.Tag = m.Details.ParameterSize
			if m.Details.QuantizationLevel != "" {
				info.Tag += " " + m.Details.QuantizationLevel
			}
		}
		models = append(models, info)
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Name < models[j].Name
	})

	return models, nil
}

// --- OpenAI: GET /v1/models ---

type openAIModelsResponse struct {
	Data []openAIModel `json:"data"`
}

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

func fetchOpenAIModels(host, apiKey string) ([]ModelInfo, error) {
	if host == "" {
		host = "https://api.openai.com"
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", host+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to OpenAI at %s: %w", host, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			body = []byte("(body unreadable)")
		}
		return nil, fmt.Errorf("OpenAI returned %d: %s", resp.StatusCode, string(body))
	}

	var result openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode OpenAI response: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		// Filter to embedding models for embedding selection.
		info := ModelInfo{
			Name: m.ID,
			Tag:  m.OwnedBy,
		}
		models = append(models, info)
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Name < models[j].Name
	})

	return models, nil
}

// filterEmbeddingModels returns models likely useful for embeddings.
func filterEmbeddingModels(models []ModelInfo) []ModelInfo {
	var result []ModelInfo
	embedKeywords := []string{"embed", "bge", "nomic", "e5", "gte", "minilm", "instructor"}
	for _, m := range models {
		lower := strings.ToLower(m.Name)
		for _, kw := range embedKeywords {
			if strings.Contains(lower, kw) {
				result = append(result, m)
				break
			}
		}
	}
	if len(result) == 0 {
		return models // Return all if no embedding-specific models found.
	}
	return result
}

// filterLLMModels returns models likely useful for chat/LLM.
func filterLLMModels(models []ModelInfo) []ModelInfo {
	var result []ModelInfo
	embedKeywords := []string{"embed", "bge", "nomic-embed", "e5-", "gte-", "minilm"}
	for _, m := range models {
		lower := strings.ToLower(m.Name)
		isEmbed := false
		for _, kw := range embedKeywords {
			if strings.Contains(lower, kw) {
				isEmbed = true
				break
			}
		}
		if !isEmbed {
			result = append(result, m)
		}
	}
	if len(result) == 0 {
		return models
	}
	return result
}

// filterRerankerModels returns models that are dedicated reranker models.
// Only models with reranker-specific names are returned (e.g. bge-reranker,
// jina-reranker, mxbai-rerank). Returns nil if no reranker models are found.
func filterRerankerModels(models []ModelInfo) []ModelInfo {
	rerankKW := []string{"rerank", "reranker", "cross-encoder"}
	var rerankers []ModelInfo
	for _, m := range models {
		lower := strings.ToLower(m.Name)
		for _, kw := range rerankKW {
			if strings.Contains(lower, kw) {
				rerankers = append(rerankers, m)
				break
			}
		}
	}
	return rerankers
}

func formatModelSize(bytes int64) string {
	const (
		MB = 1024 * 1024
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.0fMB", float64(bytes)/float64(MB))
	default:
		return ""
	}
}

// --- llama.cpp: local .gguf scanning ---

func fetchLlamaCPPModels(host string) ([]ModelInfo, error) {
	var searchDirs []string

	// host can be a specific directory for scanning models.
	if host != "" && !strings.Contains(host, "auto-scan") && !strings.HasPrefix(host, "http://") {
		searchDirs = []string{host}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		searchDirs = []string{
			DefaultModelsDir(),
			filepath.Join(home, "models"), // legacy/fallback
			filepath.Join(home, ".cache", "lm-studio", "models"),
			filepath.Join(home, ".cache", "huggingface", "hub"),
		}
	}

	var models []ModelInfo

	for _, dir := range searchDirs {
		if _, err := os.Stat(dir); err != nil {
			continue // skip missing dirs
		}

		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".gguf") {
				models = append(models, ModelInfo{
					Name: info.Name(),
					Size: formatModelSize(info.Size()),
					Tag:  path, // Store full path in Tag for later loading
				})
			}
			return nil
		})
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no .gguf models found in %v", searchDirs)
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Name < models[j].Name
	})

	return models, nil
}
