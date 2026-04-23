// Package autosetup provides zero-config auto-detection and bootstrap
// for gleann. When no config exists, it detects Ollama, picks optimal
// models, and creates a ready-to-use configuration automatically.
package autosetup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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

// ── Model Tier System ─────────────────────────────────────────────────────

// ModelTier defines embedding model priority tiers.
type ModelTier struct {
	Name string // e.g., "nomic-embed-text"
	Size string // human-readable, e.g., "270 MB"
	Tier int    // 1 = quick start, 2 = recommended, 3 = expert
}

// EmbeddingTiers returns the tiered list of embedding models.
// Tier 1: small/fast for first-run. Tier 2: recommended quality. Tier 3: expert.
func EmbeddingTiers() []ModelTier {
	return []ModelTier{
		{Name: "nomic-embed-text", Size: "270 MB", Tier: 1},
		{Name: "bge-m3", Size: "1.2 GB", Tier: 2},
		{Name: "snowflake-arctic-embed", Size: "670 MB", Tier: 2},
		{Name: "mxbai-embed-large", Size: "670 MB", Tier: 3},
		{Name: "jina/jina-embeddings-v3", Size: "1.2 GB", Tier: 3},
	}
}

// QuickStartEmbeddingModel returns the smallest embedding model for fast onboarding.
func QuickStartEmbeddingModel() string {
	return "nomic-embed-text"
}

// ── Ollama Model Pull ─────────────────────────────────────────────────────

// ollamaPullResponse is a line of the streaming /api/pull response.
type ollamaPullResponse struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// PullModel downloads a model via Ollama's POST /api/pull endpoint.
// It streams progress and calls progressFn for each update.
// If progressFn is nil, progress is printed to stdout.
func PullModel(host, model string, progressFn func(status string, completed, total int64)) error {
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")

	body, _ := json.Marshal(map[string]any{
		"name":   model,
		"stream": true,
	})

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Post(host+"/api/pull", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("cannot reach Ollama at %s: %w", host, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Ollama pull failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB line limit
	var lastStatus string

	for scanner.Scan() {
		var line ollamaPullResponse
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}

		if progressFn != nil {
			progressFn(line.Status, line.Completed, line.Total)
		} else if line.Status != lastStatus || line.Total > 0 {
			if line.Total > 0 && line.Total != line.Completed {
				pct := float64(line.Completed) / float64(line.Total) * 100
				fmt.Printf("\r   %-30s %5.1f%%", line.Status, pct)
			} else {
				fmt.Printf("\r   %-50s", line.Status)
			}
			lastStatus = line.Status
		}
	}

	if progressFn == nil {
		fmt.Println() // final newline
	}

	return scanner.Err()
}

// HasModel checks if a model is available in Ollama.
func HasModel(host, model string) bool {
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(host + "/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return false
	}

	// Match by prefix (e.g., "bge-m3" matches "bge-m3:latest").
	for _, m := range tags.Models {
		if m.Name == model || strings.HasPrefix(m.Name, model+":") || strings.HasPrefix(m.Name, model) {
			return true
		}
	}
	return false
}

// EnsureModels checks that all required models are available in Ollama.
// Missing models are automatically pulled. Returns the list of models that were pulled.
func EnsureModels(host string, quiet bool, models ...string) ([]string, error) {
	if host == "" {
		host = "http://localhost:11434"
	}

	var pulled []string
	for _, model := range models {
		if model == "" {
			continue
		}
		if HasModel(host, model) {
			if !quiet {
				fmt.Printf("   ✓ %s (available)\n", model)
			}
			continue
		}

		if !quiet {
			fmt.Printf("   ↓ Pulling %s...\n", model)
		}
		if err := PullModel(host, model, nil); err != nil {
			return pulled, fmt.Errorf("failed to pull %s: %w", model, err)
		}
		pulled = append(pulled, model)
		if !quiet {
			fmt.Printf("   ✓ %s (installed)\n", model)
		}
	}
	return pulled, nil
}

// ── Detect + Show + Confirm ───────────────────────────────────────────────

// DetectedConfig holds a fully resolved configuration ready for user confirmation.
type DetectedConfig struct {
	OllamaHost     string `json:"ollama_host"`
	OllamaRunning  bool   `json:"ollama_running"`
	EmbeddingModel string `json:"embedding_model"`
	LLMModel       string `json:"llm_model"`
	RerankModel    string `json:"rerank_model"`
	IndexDir       string `json:"index_dir"`
	MCPEnabled     bool   `json:"mcp_enabled"`
	ServerEnabled  bool   `json:"server_enabled"`
}

// DetectAll performs comprehensive auto-detection and returns a
// DetectedConfig suitable for user confirmation.
// If quickStart is true, prefers smaller models (tier 1) for faster onboarding.
func DetectAll(ollamaHost string, quickStart ...bool) DetectedConfig {
	det := DetectOllama(ollamaHost)

	// In quick-start mode, if no embedding model was found in the available
	// models, prefer the small tier-1 model for faster first experience.
	if len(quickStart) > 0 && quickStart[0] {
		hasEmbed := false
		for _, m := range det.ModelsFound {
			for _, prefix := range []string{"bge-m3", "snowflake-arctic-embed", "nomic-embed-text", "mxbai-embed"} {
				if strings.HasPrefix(m, prefix) {
					hasEmbed = true
					break
				}
			}
			if hasEmbed {
				break
			}
		}
		if !hasEmbed {
			det.EmbeddingModel = QuickStartEmbeddingModel() // nomic-embed-text (270 MB)
		}
	}

	home, _ := os.UserHomeDir()
	indexDir := home + "/.gleann/indexes"

	return DetectedConfig{
		OllamaHost:     det.OllamaHost,
		OllamaRunning:  det.OllamaFound,
		EmbeddingModel: det.EmbeddingModel,
		LLMModel:       det.LLMModel,
		RerankModel:    det.RerankModel,
		IndexDir:       indexDir,
		MCPEnabled:     true,
		ServerEnabled:  false,
	}
}

// FormatDetectedConfig returns a human-readable summary of the detected config.
func FormatDetectedConfig(dc DetectedConfig) string {
	var sb strings.Builder
	sb.WriteString("  ┌─────────────────────────────────────────────────────┐\n")
	sb.WriteString("  │  gleann — auto-detected configuration               │\n")
	sb.WriteString("  │                                                     │\n")

	status := "✓ running"
	if !dc.OllamaRunning {
		status = "✗ not found"
	}
	sb.WriteString(fmt.Sprintf("  │  Ollama     : %-20s (%s)     │\n", dc.OllamaHost, status))
	sb.WriteString(fmt.Sprintf("  │  Embedding  : %-37s │\n", dc.EmbeddingModel))
	sb.WriteString(fmt.Sprintf("  │  LLM        : %-37s │\n", dc.LLMModel))

	rerank := "(none)"
	if dc.RerankModel != "" {
		rerank = dc.RerankModel
	}
	sb.WriteString(fmt.Sprintf("  │  Reranker   : %-37s │\n", rerank))
	sb.WriteString(fmt.Sprintf("  │  Index dir  : %-37s │\n", dc.IndexDir))

	mcp := "disabled"
	if dc.MCPEnabled {
		mcp = "enabled"
	}
	sb.WriteString(fmt.Sprintf("  │  MCP server : %-37s │\n", mcp))

	srv := "disabled"
	if dc.ServerEnabled {
		srv = "enabled"
	}
	sb.WriteString(fmt.Sprintf("  │  REST API   : %-37s │\n", srv))

	sb.WriteString("  │                                                     │\n")
	sb.WriteString("  │  [Enter] Accept & continue                          │\n")
	sb.WriteString("  │  [e] Edit a setting       [a] Advanced setup        │\n")
	sb.WriteString("  └─────────────────────────────────────────────────────┘\n")
	return sb.String()
}

// ApplyDetectedConfig writes the DetectedConfig to ~/.gleann/config.json.
func ApplyDetectedConfig(dc DetectedConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cfg := map[string]any{
		"embedding_provider": "ollama",
		"embedding_model":    dc.EmbeddingModel,
		"ollama_host":        dc.OllamaHost,
		"llm_provider":       "ollama",
		"llm_model":          dc.LLMModel,
		"index_dir":          dc.IndexDir,
		"mcp_enabled":        dc.MCPEnabled,
		"server_enabled":     dc.ServerEnabled,
		"completed":          true,
		"temperature":        0.7,
		"max_tokens":         4096,
		"top_k":              10,
	}
	if dc.RerankModel != "" {
		cfg["rerank_enabled"] = true
		cfg["rerank_model"] = dc.RerankModel
	}

	if err := os.MkdirAll(home+"/.gleann", 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(home+"/.gleann/config.json", data, 0o644)
}
