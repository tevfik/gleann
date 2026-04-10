package main

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

// isOutputTTY returns true if stdout is connected to a terminal.
func isOutputTTY() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
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

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				config.EmbeddingModel = args[i+1]
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
		case "--host":
			if i+1 < len(args) {
				config.OllamaHost = args[i+1]
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
		return
	}
	if getFlag(args, "--provider") == "" && savedCfg.EmbeddingProvider != "" {
		config.EmbeddingProvider = savedCfg.EmbeddingProvider
	}
	if getFlag(args, "--model") == "" && savedCfg.EmbeddingModel != "" {
		config.EmbeddingModel = savedCfg.EmbeddingModel
	}
	if getFlag(args, "--host") == "" && savedCfg.OllamaHost != "" {
		config.OllamaHost = savedCfg.OllamaHost
	}
	if savedCfg.OpenAIKey != "" && config.OpenAIAPIKey == "" {
		config.OpenAIAPIKey = savedCfg.OpenAIKey
	}
	if savedCfg.OpenAIBaseURL != "" && config.OpenAIBaseURL == "" {
		config.OpenAIBaseURL = savedCfg.OpenAIBaseURL
	}
	if getFlag(args, "--index-dir") == "" && savedCfg.IndexDir != "" {
		config.IndexDir = savedCfg.IndexDir
	}
	// LLM settings from saved config.
	if getFlag(args, "--llm-provider") == "" && savedCfg.LLMProvider != "" {
		config.LLMProvider = savedCfg.LLMProvider
	}
	if getFlag(args, "--llm-model") == "" && savedCfg.LLMModel != "" {
		config.LLMModel = savedCfg.LLMModel
	}
}
