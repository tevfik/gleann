package tui

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Bootstrap creates a default configuration without launching the interactive TUI.
// It auto-detects Ollama at localhost:11434 and picks sensible defaults.
//
// Usage from CLI:
//
//	gleann setup --bootstrap              # apply defaults, detect Ollama
//	gleann setup --bootstrap --host URL   # use specific Ollama host
//	gleann setup --bootstrap --code-model M1 --doc-model M2
func Bootstrap(ollamaHost string, dualModels ...string) (*OnboardResult, error) {
	return BootstrapWithAPIKey(ollamaHost, "", dualModels...)
}

// BootstrapWithAPIKey is like Bootstrap but also stores an optional Ollama
// API key (sent as a Bearer token for Ollama behind an auth proxy).
//
//	gleann setup --bootstrap --host URL --ollama-api-key KEY
func BootstrapWithAPIKey(ollamaHost, ollamaAPIKey string, dualModels ...string) (*OnboardResult, error) {
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}

	result := &OnboardResult{
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        ollamaHost,
		OllamaAPIKey:      ollamaAPIKey,
		LLMProvider:       "ollama",
		LLMModel:          "gemma3:4b",
		RerankEnabled:     false,
		IndexDir:          DefaultIndexDir(),
		MCPEnabled:        true,
		ServerEnabled:     false,
		Completed:         true,

		// Sensible chat defaults.
		Temperature: 0.7,
		MaxTokens:   4096,
		TopK:        10,
	}

	if len(dualModels) > 0 && dualModels[0] != "" {
		result.CodeEmbeddingModel = dualModels[0]
	}
	if len(dualModels) > 1 && dualModels[1] != "" {
		result.DocEmbeddingModel = dualModels[1]
	}

	// Try to detect Ollama and pick models.
	fmt.Printf("🔍 Detecting Ollama at %s...\n", ollamaHost)
	if ollamaReachable(ollamaHost, ollamaAPIKey) {
		fmt.Println("  ✓ Ollama is running")

		models, err := fetchModels("ollama", ollamaHost, ollamaAPIKey)
		if err == nil && len(models) > 0 {
			fmt.Printf("  ✓ Found %d models\n", len(models))
			pickBestModels(result, models)
		} else {
			fmt.Println("  ⚠ Could not list models, using defaults")
		}
	} else {
		fmt.Println("  ⚠ Ollama not reachable, using defaults")
		fmt.Println("    Start Ollama and run 'gleann setup --bootstrap' again to auto-detect models")
	}

	// Print summary.
	fmt.Println()
	fmt.Println("📋 Bootstrap Configuration:")
	fmt.Printf("  Embedding: %s (%s)\n", result.EmbeddingModel, result.EmbeddingProvider)
	if result.CodeEmbeddingModel != "" {
		fmt.Printf("  Code Model: %s\n", result.CodeEmbeddingModel)
	}
	if result.DocEmbeddingModel != "" {
		fmt.Printf("  Docs Model: %s\n", result.DocEmbeddingModel)
	}
	fmt.Printf("  LLM:       %s (%s)\n", result.LLMModel, result.LLMProvider)
	fmt.Printf("  Host:      %s\n", result.OllamaHost)
	fmt.Printf("  Indexes:   %s\n", result.IndexDir)
	fmt.Printf("  MCP:       %v\n", result.MCPEnabled)

	// Save.
	if err := SaveConfig(*result); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	fmt.Println()
	fmt.Println("✅ Configuration saved to ~/.gleann/config.json")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  gleann index build myindex --docs /path/to/docs   # index documents")
	fmt.Println("  gleann search myindex 'query'               # search")
	fmt.Println("  gleann chat myindex                         # interactive chat")
	fmt.Println("  gleann setup                                # interactive wizard for fine-tuning")

	return result, nil
}

// CheckSetup verifies that gleann is configured and optionally checks Ollama availability.
func CheckSetup() bool {
	cfg := LoadSavedConfig()
	if cfg == nil || !cfg.Completed {
		fmt.Println("⚠ gleann is not configured")
		fmt.Println("  Run 'gleann setup' for interactive wizard")
		fmt.Println("  Run 'gleann setup --bootstrap' for quick defaults")
		return false
	}

	fmt.Println("✅ gleann is configured")
	fmt.Printf("  Config:    ~/.gleann/config.json\n")
	fmt.Printf("  Embedding: %s (%s)\n", cfg.EmbeddingModel, cfg.EmbeddingProvider)
	if cfg.CodeEmbeddingModel != "" {
		fmt.Printf("  Code Model: %s\n", cfg.CodeEmbeddingModel)
	}
	if cfg.DocEmbeddingModel != "" {
		fmt.Printf("  Docs Model: %s\n", cfg.DocEmbeddingModel)
	}
	fmt.Printf("  LLM:       %s (%s)\n", cfg.LLMModel, cfg.LLMProvider)
	fmt.Printf("  Host:      %s\n", cfg.OllamaHost)
	fmt.Printf("  Indexes:   %s\n", cfg.IndexDir)

	// Check Ollama connectivity.
	host := cfg.OllamaHost
	if host == "" {
		host = "http://localhost:11434"
	}
	if ollamaReachable(host, cfg.OllamaAPIKey) {
		fmt.Printf("  Ollama:    ✅ reachable at %s\n", host)
	} else {
		fmt.Printf("  Ollama:    ⚠ not reachable at %s\n", host)
	}

	return true
}

// ollamaReachable checks if Ollama is up at the given host.
func ollamaReachable(host, apiKey string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := gleann.OllamaGet(client, host+"/api/tags", gleann.ResolveOllamaAPIKey(apiKey))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// pickBestModels selects the best embedding and LLM models from available models.
func pickBestModels(result *OnboardResult, models []ModelInfo) {
	// Embedding: prefer bge-m3, snowflake-arctic-embed, nomic-embed-text
	embModels := filterEmbeddingModels(models)
	if len(embModels) > 0 {
		result.EmbeddingModel = pickPreferred(embModels, []string{
			"bge-m3", "snowflake-arctic-embed", "nomic-embed-text",
		})
		fmt.Printf("  → Embedding model: %s\n", result.EmbeddingModel)
	}

	// LLM: prefer gemma3, qwen2.5, llama3, phi-4
	llmModels := filterLLMModels(models)
	if len(llmModels) > 0 {
		result.LLMModel = pickPreferred(llmModels, []string{
			"gemma3", "qwen2.5", "llama3", "phi-4", "mistral",
		})
		fmt.Printf("  → LLM model: %s\n", result.LLMModel)
	}

	// Reranker: enable if available.
	rrModels := filterRerankerModels(models)
	if len(rrModels) > 0 {
		result.RerankEnabled = true
		result.RerankModel = rrModels[0].Name
		fmt.Printf("  → Reranker model: %s (enabled)\n", result.RerankModel)
	}
}

// pickPreferred picks the first model that matches a preferred prefix, or falls back
// to the first model in the list.
func pickPreferred(models []ModelInfo, preferred []string) string {
	for _, prefix := range preferred {
		for _, m := range models {
			if len(m.Name) >= len(prefix) && m.Name[:len(prefix)] == prefix {
				return m.Name
			}
		}
	}
	return models[0].Name
}

// IsSetupNeeded returns true if config is missing or incomplete.
func IsSetupNeeded() bool {
	cfg := LoadSavedConfig()
	return cfg == nil || !cfg.Completed
}

// PrintSetupHint prints a one-liner hint when config is missing.
func PrintSetupHint() {
	if IsSetupNeeded() {
		fmt.Fprintln(os.Stderr, "💡 Tip: run 'gleann setup --bootstrap' for quick configuration or 'gleann setup' for interactive wizard")
	}
}
