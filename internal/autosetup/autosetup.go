// Package autosetup provides zero-config auto-detection and bootstrap
// for gleann. When no config exists, it detects Ollama, picks optimal
// models, and creates a ready-to-use configuration automatically.
package autosetup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Result holds the auto-detected configuration.
type Result struct {
	OllamaHost     string
	OllamaFound    bool
	EmbeddingModel string
	LLMModel       string
	RerankModel    string
	ModelsFound    []string
}

// ollamaTagsResponse is the JSON shape returned by GET /api/tags.
type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// DetectOllama probes the given host (or localhost:11434) and returns
// the available model list. It is safe to call even when Ollama is down.
func DetectOllama(host string) Result {
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")

	res := Result{
		OllamaHost:     host,
		EmbeddingModel: "bge-m3",
		LLMModel:       "gemma3:4b",
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(host + "/api/tags")
	if err != nil {
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res
	}

	res.OllamaFound = true

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return res
	}

	for _, m := range tags.Models {
		res.ModelsFound = append(res.ModelsFound, m.Name)
	}

	res.EmbeddingModel = pickModel(res.ModelsFound, []string{
		"bge-m3", "snowflake-arctic-embed", "nomic-embed-text",
	}, "bge-m3")

	res.LLMModel = pickModel(res.ModelsFound, []string{
		"gemma3", "gemma4", "qwen2.5", "qwen3", "llama3", "phi-4", "mistral",
	}, "gemma3:4b")

	res.RerankModel = pickModel(res.ModelsFound, []string{
		"bge-reranker", "jina-reranker",
	}, "")

	return res
}

// pickModel searches available models for a preferred prefix. Returns
// fallback if nothing matches.
func pickModel(available []string, prefixes []string, fallback string) string {
	for _, prefix := range prefixes {
		for _, m := range available {
			if strings.HasPrefix(m, prefix) {
				return m
			}
		}
	}
	return fallback
}

// EnsureConfig checks whether ~/.gleann/config.json exists and is
// complete. If not, it auto-detects Ollama and writes minimal defaults.
// Returns true if bootstrap was performed, false if config already existed.
func EnsureConfig(ollamaHost string, quiet bool) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}

	cfgPath := home + "/.gleann/config.json"
	if _, err := os.Stat(cfgPath); err == nil {
		// Config exists — check if it's complete.
		data, _ := os.ReadFile(cfgPath)
		var cfg map[string]any
		if json.Unmarshal(data, &cfg) == nil {
			if completed, ok := cfg["completed"].(bool); ok && completed {
				return false, nil
			}
		}
	}

	// Auto-detect.
	if !quiet {
		fmt.Println("🔧 No configuration found — auto-detecting...")
	}
	det := DetectOllama(ollamaHost)

	if !quiet {
		if det.OllamaFound {
			fmt.Printf("   ✓ Ollama found at %s (%d models)\n", det.OllamaHost, len(det.ModelsFound))
			fmt.Printf("   → Embedding: %s\n", det.EmbeddingModel)
			fmt.Printf("   → LLM: %s\n", det.LLMModel)
		} else {
			fmt.Printf("   ⚠ Ollama not found at %s — using defaults\n", det.OllamaHost)
		}
	}

	// Build minimal config.
	cfg := map[string]any{
		"embedding_provider": "ollama",
		"embedding_model":    det.EmbeddingModel,
		"ollama_host":        det.OllamaHost,
		"llm_provider":       "ollama",
		"llm_model":          det.LLMModel,
		"index_dir":          home + "/.gleann/indexes",
		"mcp_enabled":        true,
		"server_enabled":     true,
		"completed":          true,
		"temperature":        0.7,
		"max_tokens":         4096,
		"top_k":              10,
	}
	if det.RerankModel != "" {
		cfg["rerank_enabled"] = true
		cfg["rerank_model"] = det.RerankModel
	}

	// Write.
	if err := os.MkdirAll(home+"/.gleann", 0o755); err != nil {
		return false, err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		return false, err
	}

	if !quiet {
		fmt.Println("   ✓ Config saved to ~/.gleann/config.json")
	}
	return true, nil
}
