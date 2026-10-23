package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

// isOutputTTY returns true if stdout is connected to a terminal.
func isOutputTTY() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// isInputTTY returns true if stdin is connected to a terminal.
// Interactive subcommands (gleann tui, setup wizard) require both stdin and
// stdout to be TTYs — bubbletea cannot allocate a raw terminal otherwise.
func isInputTTY() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// stderrf prints to stderr unless --quiet is set.
func stderrf(args []string, format string, a ...any) {
	if hasFlag(args, "--quiet") {
		return
	}
	fmt.Fprintf(os.Stderr, format, a...)
}

// getConfig parses CLI flags into a gleann.Config.
func getConfig(args []string) gleann.Config {
	config := gleann.DefaultConfig()

	if hasFlag(args, "--no-mmap") {
		config.HNSWConfig.UseMmap = false
	}

	config.IndexDir = tui.DefaultIndexDir()
	if envDir := os.Getenv("GLEANN_INDEX_DIR"); envDir != "" {
		config.IndexDir = tui.ExpandPath(envDir)
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				config.EmbeddingModel = args[i+1]
				i++
			}
		case "--code-model", "--code-embedding-model":
			if i+1 < len(args) {
				config.CodeEmbeddingModel = args[i+1]
				i++
			}
		case "--doc-model", "--docs-model", "--doc-embedding-model":
			if i+1 < len(args) {
				config.DocEmbeddingModel = args[i+1]
				i++
			}
		case "--provider":
			if i+1 < len(args) {
				config.EmbeddingProvider = args[i+1]
				i++
			}
		case "--index-dir":
			if i+1 < len(args) {
				config.IndexDir = tui.ExpandPath(args[i+1])
				i++
			}
		case "--top-k":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.SearchConfig.TopK)
				i++
			}
		case "--ef-search":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.HNSWConfig.EfSearch)
				i++
			}
		case "--chunk-size":
			if i+1 < len(args) {
				var cs int
				fmt.Sscanf(args[i+1], "%d", &cs)
				if cs > 0 {
					config.ChunkConfig.ChunkSize = cs
				}
				i++
			}
		case "--chunk-overlap":
			if i+1 < len(args) {
				var co int
				fmt.Sscanf(args[i+1], "%d", &co)
				config.ChunkConfig.ChunkOverlap = co
				i++
			}
		case "--signatures", "--signatures-only":
			config.ChunkConfig.SignaturesOnly = true
		case "--batch-size":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.BatchSize)
				i++
			}
		case "--concurrency":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.Concurrency)
				i++
			}
		case "--hybrid":
			if i+1 < len(args) {
				var alpha float32
				fmt.Sscanf(args[i+1], "%f", &alpha)
				config.SearchConfig.HybridAlpha = alpha
				i++
			}
		case "--metric":
			if i+1 < len(args) {
				config.HNSWConfig.DistanceMetric = gleann.DistanceMetric(args[i+1])
				i++
			}
		case "--prune":
			if i+1 < len(args) {
				var fraction float64
				fmt.Sscanf(args[i+1], "%f", &fraction)
				config.HNSWConfig.PruneKeepFraction = fraction
				config.HNSWConfig.PruneEmbeddings = true
				i++
			}
		case "--backend":
			if i+1 < len(args) {
				config.Backend = args[i+1]
				i++
			}
		case "--pq-dim":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.DiskANNConfig.PQDim)
				i++
			}
		case "--pq-centroids":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.DiskANNConfig.PQCentroids)
				i++
			}
		case "--search-l":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.DiskANNConfig.SearchL)
				i++
			}
		case "--search-pq-rerank":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.DiskANNConfig.SearchPQRerank)
				i++
			}
		case "--diskann-r":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.DiskANNConfig.R)
				i++
			}
		case "--diskann-l":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &config.DiskANNConfig.L)
				i++
			}
		case "--diskann-alpha":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%f", &config.DiskANNConfig.Alpha)
				i++
			}
		case "--host":
			if i+1 < len(args) {
				host := args[i+1]
				if !strings.Contains(host, "://") {
					if !strings.Contains(host, ":") {
						host = "http://localhost:" + host
					} else {
						host = "http://" + host
					}
				}
				config.OllamaHost = host
				i++
			}
		case "--ollama-api-key":
			if i+1 < len(args) {
				config.OllamaAPIKey = args[i+1]
				i++
			}
		case "--openai-base-url":
			// Endpoint for --provider openai/llamacpp embeddings. Without
			// this, index build/search silently fell back to the saved
			// config and then api.openai.com.
			if i+1 < len(args) {
				config.OpenAIBaseURL = strings.TrimRight(args[i+1], "/")
				i++
			}
		}
	}

	return config
}

// getFlag returns the value of a --flag in args, or "" if not found.
func getFlag(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// hasFlag returns true if flag appears in args.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// applySavedConfig merges saved TUI config into a gleann.Config.
// CLI flags (already in config) take precedence over saved values.
func applySavedConfig(config *gleann.Config, args []string) {
	savedCfg := tui.LoadSavedConfig()
	if savedCfg == nil {
		if getFlag(args, "--host") == "" {
			if envHost := os.Getenv("OLLAMA_HOST"); envHost != "" {
				if !strings.Contains(envHost, "://") {
					if !strings.Contains(envHost, ":") {
						envHost = "http://localhost:" + envHost
					} else {
						envHost = "http://" + envHost
					}
				}
				config.OllamaHost = envHost
			}
		}
		return
	}
	if getFlag(args, "--provider") == "" && savedCfg.EmbeddingProvider != "" {
		config.EmbeddingProvider = savedCfg.EmbeddingProvider
	}
	if getFlag(args, "--model") == "" && savedCfg.EmbeddingModel != "" {
		config.EmbeddingModel = savedCfg.EmbeddingModel
	}
	if getFlag(args, "--host") == "" {
		if savedCfg.OllamaHost != "" {
			host := savedCfg.OllamaHost
			if !strings.Contains(host, "://") {
				if !strings.Contains(host, ":") {
					host = "http://localhost:" + host
				} else {
					host = "http://" + host
				}
			}
			config.OllamaHost = host
		} else if envHost := os.Getenv("OLLAMA_HOST"); envHost != "" {
			if !strings.Contains(envHost, "://") {
				if !strings.Contains(envHost, ":") {
					envHost = "http://localhost:" + envHost
				} else {
					envHost = "http://" + envHost
				}
			}
			config.OllamaHost = envHost
		}
	}
	if savedCfg.OpenAIKey != "" && config.OpenAIAPIKey == "" {
		config.OpenAIAPIKey = savedCfg.OpenAIKey
	}
	if savedCfg.OllamaAPIKey != "" && config.OllamaAPIKey == "" {
		config.OllamaAPIKey = savedCfg.OllamaAPIKey
	}
	if savedCfg.OpenAIBaseURL != "" && config.OpenAIBaseURL == "" {
		config.OpenAIBaseURL = savedCfg.OpenAIBaseURL
	}
	if getFlag(args, "--index-dir") == "" && os.Getenv("GLEANN_INDEX_DIR") == "" && savedCfg.IndexDir != "" {
		config.IndexDir = savedCfg.IndexDir
	}
	// LLM settings from saved config.
	if getFlag(args, "--llm-provider") == "" && savedCfg.LLMProvider != "" {
		config.LLMProvider = savedCfg.LLMProvider
	}
	if getFlag(args, "--llm-model") == "" && savedCfg.LLMModel != "" {
		config.LLMModel = savedCfg.LLMModel
	}
	// Backend from saved config.
	if savedCfg.Backend != "" {
		config.Backend = savedCfg.Backend
	}
	// Dual embedding models from saved config.
	if config.CodeEmbeddingModel == "" && savedCfg.CodeEmbeddingModel != "" {
		config.CodeEmbeddingModel = savedCfg.CodeEmbeddingModel
	}
	if config.DocEmbeddingModel == "" && savedCfg.DocEmbeddingModel != "" {
		config.DocEmbeddingModel = savedCfg.DocEmbeddingModel
	}
	// Multimodal model — stored in OnboardResult via the shared JSON.
	if savedCfg.MultimodalModel != "" {
		config.MultimodalModel = savedCfg.MultimodalModel
	}
}

// resolveOllamaAPIKey returns the Ollama API key to use: the value in cfg
// (from --ollama-api-key or applySavedConfig), then the saved config's
// ollama_api_key, then the OLLAMA_API_KEY environment variable.
func resolveOllamaAPIKey(cfg gleann.Config) string {
	if cfg.OllamaAPIKey != "" {
		return cfg.OllamaAPIKey
	}
	if saved := tui.LoadSavedConfig(); saved != nil && saved.OllamaAPIKey != "" {
		return saved.OllamaAPIKey
	}
	return gleann.ResolveOllamaAPIKey("")
}

// newEmbedder initializes an embedding.Computer from gleann.Config, properly
// routing the base URL and API key based on the configured provider.
func newEmbedder(cfg gleann.Config) *embedding.Computer {
	return indexing.NewEmbedder(cfg)
}

// defaultEmbedderResolver returns an EmbedderResolver that dynamically provides
// specialized embedding computers per model (e.g. for multi-index code vs doc search).
func defaultEmbedderResolver(cfg gleann.Config) gleann.EmbedderResolver {
	var mu sync.Mutex
	cache := make(map[string]gleann.EmbeddingComputer)
	return gleann.EmbedderResolverFunc(func(ctx context.Context, modelName string, dims int) (gleann.EmbeddingComputer, error) {
		mu.Lock()
		defer mu.Unlock()
		if c, ok := cache[modelName]; ok {
			return c, nil
		}
		subCfg := cfg
		subCfg.EmbeddingModel = modelName
		emb := newEmbedder(subCfg)
		cache[modelName] = emb
		return emb, nil
	})
}

// withModeModel maps the mode's model flag onto --model, as `index build` does:
// --code-model for a code index, --doc-model for a docs index.
func withModeModel(args []string, mode IndexMode) []string {
	if getFlag(args, "--model") != "" {
		return args
	}
	pick := func(names ...string) string {
		for _, n := range names {
			if v := getFlag(args, n); v != "" {
				return v
			}
		}
		return ""
	}
	switch mode {
	case IndexModeCode:
		if m := pick("--code-model", "--code-embedding-model"); m != "" {
			return append(args[:len(args):len(args)], "--model", m)
		}
	case IndexModeDocs:
		if m := pick("--doc-model", "--docs-model"); m != "" {
			return append(args[:len(args):len(args)], "--model", m)
		}
	}
	return args
}

// modelExplicit reports whether the command line names the embedding model
// (after withModeModel), as opposed to inheriting the saved or built-in default.
func modelExplicit(args []string) bool {
	return getFlag(args, "--model") != ""
}
