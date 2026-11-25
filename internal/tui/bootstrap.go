package tui

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tevfik/gleann/internal/autosetup"
	"github.com/tevfik/gleann/pkg/gleann"
)

// BootstrapOptions are the inputs of `gleann setup --bootstrap`.
type BootstrapOptions struct {
	OllamaHost   string
	OllamaAPIKey string
	// Embedding provider and model, and the chat provider and model. An empty
	// value keeps the default or the model picked from Ollama's list; a set
	// value always wins.
	Provider, Model, LLMProvider, LLMModel string
	// CodeModel and DocModel are the dual embedding models.
	CodeModel, DocModel string
	// Force overwrites a completed configuration (the previous file is kept as
	// config.json.bak); without it a completed configuration is refused.
	Force bool
}

// ErrConfigExists is returned by Bootstrap when a completed configuration is
// already saved and Force is not set.
var ErrConfigExists = errors.New("a completed configuration already exists")

// Bootstrap creates a default configuration without launching the TUI. It
// auto-detects Ollama, picks sensible defaults, lets explicit options win and
// refuses to overwrite a completed configuration unless opts.Force is set.
func Bootstrap(opts BootstrapOptions) (*OnboardResult, error) {
	if !opts.Force {
		if existing := LoadSavedConfig(); existing != nil && existing.Completed {
			return nil, fmt.Errorf("%w at %s: re-run with --force to replace it (the old file is kept as %s, or a timestamped copy if that exists), or edit it with 'gleann setup'",
				ErrConfigExists, configPath(), filepath.Base(configPath())+".bak")
		}
	} else if err := backupConfig(); err != nil {
		return nil, err
	}
	return bootstrap(opts)
}

// backupConfig copies an existing config.json to config.json.bak (mode 0600,
// as the file may hold API keys). A backup that already exists is kept: the
// new one gets a timestamped name (config.json.bak-YYYYMMDD-HHMMSS), with a
// counter suffix (-1, -2, ...) when that name is taken too.
func backupConfig() error {
	return backupConfigAt(configPath(), time.Now())
}

func backupConfigAt(path string, now time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		stamped := path + ".bak-" + now.Format("20060102-150405")
		bak = stamped
		for i := 1; ; i++ {
			if _, err := os.Lstat(bak); os.IsNotExist(err) {
				break
			}
			bak = fmt.Sprintf("%s-%d", stamped, i)
		}
	}
	f, err := os.OpenFile(bak, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("backing up %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("backing up %s: %w", path, err)
	}
	return f.Close()
}

// BootstrapWithAPIKey is Bootstrap with only the host, key and dual models,
// replacing an existing configuration without asking (the pre-v1.21.1
// behaviour, kept for library callers).
//
// Usage from CLI:
//
//	gleann setup --bootstrap              # apply defaults, detect Ollama
//	gleann setup --bootstrap --host URL   # use specific Ollama host
//	gleann setup --bootstrap --code-model M1 --doc-model M2
//	gleann setup --bootstrap --host URL --ollama-api-key KEY
func BootstrapWithAPIKey(ollamaHost, ollamaAPIKey string, dualModels ...string) (*OnboardResult, error) {
	opts := BootstrapOptions{OllamaHost: ollamaHost, OllamaAPIKey: ollamaAPIKey}
	if len(dualModels) > 0 {
		opts.CodeModel = dualModels[0]
	}
	if len(dualModels) > 1 {
		opts.DocModel = dualModels[1]
	}
	return bootstrap(opts)
}

func bootstrap(opts BootstrapOptions) (*OnboardResult, error) {
	ollamaHost, ollamaAPIKey := opts.OllamaHost, opts.OllamaAPIKey
	ollamaHost = gleann.ResolveOllamaHost(ollamaHost, "")

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

	result.CodeEmbeddingModel = opts.CodeModel
	result.DocEmbeddingModel = opts.DocModel
	if opts.Provider != "" {
		result.EmbeddingProvider = opts.Provider
	}
	if opts.LLMProvider != "" {
		result.LLMProvider = opts.LLMProvider
	}

	// Try to detect Ollama and pick models.
	fmt.Printf("🔍 Detecting Ollama at %s...\n", ollamaHost)
	if ollamaReachable(ollamaHost, ollamaAPIKey) {
		fmt.Println("  ✓ Ollama is running")

		models, err := fetchModels("ollama", ollamaHost, ollamaAPIKey)
		if err == nil && len(models) > 0 {
			fmt.Printf("  ✓ Found %d models\n", len(models))
			pickBestModels(result, models)
			applyBootstrapOverrides(result, opts)
		} else {
			fmt.Println("  ⚠ Could not list models, using defaults")
		}
	} else {
		fmt.Println("  ⚠ Ollama not reachable, using defaults")
		// T1: inform, never download in the background; the same guidance
		// is persisted as setup_notice for the user to act on later.
		fmt.Println(autosetup.NoOllamaGuidance())
		result.SetupNotice = autosetup.NoOllamaNotice(ollamaHost)
	}

	applyBootstrapOverrides(result, opts)

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

// applyBootstrapOverrides puts the explicit options over what pickBestModels
// chose from Ollama's list.
func applyBootstrapOverrides(result *OnboardResult, opts BootstrapOptions) {
	if opts.Model != "" {
		result.EmbeddingModel = opts.Model
	}
	if opts.LLMModel != "" {
		result.LLMModel = opts.LLMModel
	}
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
	host := gleann.ResolveOllamaHost("", cfg.OllamaHost)
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

	// Reranker: Ollama has no rerank API, so a reranker model it lists is not
	// usable through Ollama; leave reranking off and say how to get it.
	if rrModels := filterRerankerModels(models); len(rrModels) > 0 {
		fmt.Printf("  ⚠ Reranker model %s is listed but Ollama has no rerank API; reranking stays off\n", rrModels[0].Name)
		fmt.Println("    Enable it with rerank_provider \"eif\" (in-process GGUF) or \"llamacpp\" (llama-server --reranking)")
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
