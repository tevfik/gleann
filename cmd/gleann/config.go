package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
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
	args = expandFlagValues(args, configFlags)
	config := gleann.DefaultConfig()

	if hasFlag(args, "--no-mmap") {
		f := false
		config.UseMmap = &f
		config.HNSWConfig.UseMmap = false
		config.DiskANNConfig.UseMmap = false
	}
	if env := os.Getenv("GLEANN_MMAP"); env != "" {
		if strings.EqualFold(env, "0") || strings.EqualFold(env, "false") || strings.EqualFold(env, "off") || strings.EqualFold(env, "no") {
			f := false
			config.UseMmap = &f
			config.HNSWConfig.UseMmap = false
			config.DiskANNConfig.UseMmap = false
		} else if strings.EqualFold(env, "1") || strings.EqualFold(env, "true") || strings.EqualFold(env, "on") || strings.EqualFold(env, "yes") {
			t := true
			config.UseMmap = &t
			config.HNSWConfig.UseMmap = true
			config.DiskANNConfig.UseMmap = true
		}
	}
	if env := os.Getenv("GLEANN_SEARCHER_CACHE_BUDGET"); env != "" {
		if b, err := gleann.ParseByteSize(env); err == nil {
			config.SearcherCacheBudget = b
		}
	}

	config.IndexDir = tui.DefaultIndexDir()
	if envDir := os.Getenv("GLEANN_INDEX_DIR"); envDir != "" {
		config.IndexDir = tui.ExpandPath(envDir)
	}

	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break // the rest is question text, not flags
		}
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
				scanNumberFlag("--top-k", args[i+1], &config.SearchConfig.TopK)
				i++
			}
		case "--ef-search":
			if i+1 < len(args) {
				scanNumberFlag("--ef-search", args[i+1], &config.HNSWConfig.EfSearch)
				i++
			}
		case "--chunk-size":
			if i+1 < len(args) {
				var cs int
				scanNumberFlag("--chunk-size", args[i+1], &cs)
				if cs > 0 {
					config.ChunkConfig.ChunkSize = cs
				}
				i++
			}
		case "--chunk-overlap":
			if i+1 < len(args) {
				var co int
				scanNumberFlag("--chunk-overlap", args[i+1], &co)
				config.ChunkConfig.ChunkOverlap = co
				i++
			}
		case "--signatures", "--signatures-only":
			config.ChunkConfig.SignaturesOnly = true
		case "--batch-size":
			if i+1 < len(args) {
				scanNumberFlag("--batch-size", args[i+1], &config.BatchSize)
				i++
			}
		case "--concurrency":
			if i+1 < len(args) {
				scanNumberFlag("--concurrency", args[i+1], &config.Concurrency)
				i++
			}
		case "--hybrid":
			if i+1 < len(args) {
				var alpha float32
				scanNumberFlag("--hybrid", args[i+1], &alpha)
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
				scanNumberFlag("--prune", args[i+1], &fraction)
				config.HNSWConfig.PruneKeepFraction = fraction
				config.HNSWConfig.PruneEmbeddings = true
				i++
			}
		case "--storage":
			if i+1 < len(args) {
				config.StorageMode = args[i+1]
				if err := config.ApplyStorageMode(); err != nil {
					fmt.Fprintf(os.Stderr, "error: --storage: %v\n", err)
					exitFunc(1)
				}
				i++
			}
		case "--backend":
			if i+1 < len(args) {
				config.Backend = args[i+1]
				i++
			}
		case "--pq-dim":
			if i+1 < len(args) {
				scanNumberFlag("--pq-dim", args[i+1], &config.DiskANNConfig.PQDim)
				i++
			}
		case "--pq-centroids":
			if i+1 < len(args) {
				scanNumberFlag("--pq-centroids", args[i+1], &config.DiskANNConfig.PQCentroids)
				i++
			}
		case "--search-l":
			if i+1 < len(args) {
				scanNumberFlag("--search-l", args[i+1], &config.DiskANNConfig.SearchL)
				i++
			}
		case "--search-pq-rerank":
			if i+1 < len(args) {
				scanNumberFlag("--search-pq-rerank", args[i+1], &config.DiskANNConfig.SearchPQRerank)
				i++
			}
		case "--diskann-r":
			if i+1 < len(args) {
				scanNumberFlag("--diskann-r", args[i+1], &config.DiskANNConfig.R)
				i++
			}
		case "--diskann-l":
			if i+1 < len(args) {
				scanNumberFlag("--diskann-l", args[i+1], &config.DiskANNConfig.L)
				i++
			}
		case "--diskann-alpha":
			if i+1 < len(args) {
				scanNumberFlag("--diskann-alpha", args[i+1], &config.DiskANNConfig.Alpha)
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

// scanNumberFlag parses the value of a numeric flag into dst. A value that is
// not a number is an error (exit 2): it used to be dropped silently, so
// "--hybrid abc" searched lexical only.
func scanNumberFlag[T int | float32 | float64](flag, val string, dst *T) {
	var err error
	switch d := any(dst).(type) {
	case *int:
		var n int64
		n, err = strconv.ParseInt(val, 10, 0)
		*d = int(n)
	case *float32:
		var f float64
		f, err = strconv.ParseFloat(val, 32)
		*d = float32(f)
	case *float64:
		*d, err = strconv.ParseFloat(val, 64)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s needs a number, got %q\n", flag, val)
		exitFunc(2)
	}
}

// getFlag returns the value of a --flag in args, or "" if not found.
func getFlag(args []string, flag string) string {
	for i, a := range args {
		if a == "--" {
			break
		}
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

// hasFlag reports whether the boolean flag is set in args: true for a bare
// "--flag" or a "--flag=<true-ish value>", false for a "--flag=<false-ish
// value>" (see parseBoolFlag). Nothing after a literal "--" is a flag. When the
// flag is repeated with contradictory spellings the last one wins, so
// "--quiet=false --quiet" is quiet and "--quiet --quiet=false" is not.
func hasFlag(args []string, flag string) bool {
	set := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == flag {
			set = true
			continue
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			set = parseBoolFlag(v)
		}
	}
	return set
}

// getAllFlags returns every value of a repeatable --flag in args, in order.
// Nothing after a literal "--" is a flag.
func getAllFlags(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
			i++
			continue
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
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
		// Same normalisation as --openai-base-url: a saved key ending in "/"
		// must not produce "…/v1//embeddings".
		config.OpenAIBaseURL = strings.TrimRight(savedCfg.OpenAIBaseURL, "/")
	}
	if getFlag(args, "--index-dir") == "" && os.Getenv("GLEANN_INDEX_DIR") == "" && savedCfg.IndexDir != "" {
		// LoadSavedConfig already expands "~", but expanding here keeps the
		// mapping honest if the value ever arrives from another loader.
		config.IndexDir = tui.ExpandPath(savedCfg.IndexDir)
	}
	// LLM settings from saved config.
	if getFlag(args, "--llm-provider") == "" && savedCfg.LLMProvider != "" {
		config.LLMProvider = savedCfg.LLMProvider
	}
	if getFlag(args, "--llm-model") == "" && savedCfg.LLMModel != "" {
		config.LLMModel = savedCfg.LLMModel
	}
	// Result count from saved config (an explicit --top-k wins).
	if getFlag(args, "--top-k") == "" && savedCfg.TopK > 0 {
		config.SearchConfig.TopK = savedCfg.TopK
	}
	// Vector storage mode from saved config (an explicit --prune/--storage wins).
	if getFlag(args, "--storage") == "" && getFlag(args, "--prune") == "" && savedCfg.StorageMode != "" {
		config.StorageMode = savedCfg.StorageMode
		_ = config.ApplyStorageMode()
	}
	// Backend from saved config.
	if getFlag(args, "--backend") == "" && savedCfg.Backend != "" {
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
	// Reranker: the TUI, the web UI (/api/settings) and `gleann config` all
	// write rerank_enabled / rerank_model / rerank_provider; every command
	// reads them here into SearchConfig.
	applySavedReranker(config, savedCfg)
	// REST server policy (auth, CORS, rate limit); GLEANN_AUTH,
	// GLEANN_CORS_ORIGINS and GLEANN_RATE_* still win where the server reads them.
	if savedCfg.Auth != "" {
		config.Auth = savedCfg.Auth
	}
	if len(savedCfg.CORSOrigins) > 0 {
		config.CORSOrigins = savedCfg.CORSOrigins
	}
	if len(savedCfg.AllowedRoots) > 0 {
		config.AllowedRoots = savedCfg.AllowedRoots
	}
	if savedCfg.RateLimit > 0 {
		config.RateLimit = savedCfg.RateLimit
	}
	if savedCfg.RateBurst > 0 {
		config.RateBurst = savedCfg.RateBurst
	}
	if savedCfg.ProxyMode != "" {
		config.ProxyMode = savedCfg.ProxyMode
	}
	if savedCfg.ProxyContextIntro != "" {
		config.ProxyContextIntro = savedCfg.ProxyContextIntro
	}
	if savedCfg.ProxyCover {
		config.ProxyCover = true
	}
	if savedCfg.ProxyCoverMargin > 0 {
		config.ProxyCoverMargin = float32(savedCfg.ProxyCoverMargin)
	}
	if savedCfg.ProxyCoverShare > 0 {
		config.ProxyCoverShare = float32(savedCfg.ProxyCoverShare)
	}
	if savedCfg.ProxyCoverFloor > 0 {
		config.ProxyCoverFloor = float32(savedCfg.ProxyCoverFloor)
	}
	// A2A is a *bool so that "unset" keeps the server default (on) while an
	// explicit false from the settings screen is honoured: a saved
	// "a2a_enabled": false used to be dropped here, leaving the A2A routes on.
	if savedCfg.A2AEnabled != nil {
		v := *savedCfg.A2AEnabled
		config.A2AEnabled = &v
	}
	// The documented a2a_base_url key was never copied, so it had no effect.
	if savedCfg.A2ABaseURL != "" {
		config.A2ABaseURL = savedCfg.A2ABaseURL
	}
	// Embedded llama.cpp engine settings (model storage directory, HF repos)
	// were persisted by the wizard but never read back by the CLI/serve path.
	applySavedLlamaCPP(config, savedCfg.LlamaCPPConfig)
}

// applySavedLlamaCPP merges the saved local-engine settings into config. Only
// non-zero saved values win, so a partial config.json keeps the
// gleann.DefaultConfig defaults (in particular ModelStorageDir) instead of
// blanking them.
func applySavedLlamaCPP(config *gleann.Config, saved gleann.LlamaCPPConfig) {
	if saved.AutoDownload {
		config.LlamaCPPConfig.AutoDownload = true
	}
	if saved.ModelStorageDir != "" {
		config.LlamaCPPConfig.ModelStorageDir = saved.ModelStorageDir
	}
	if saved.RerankerHFRepo != "" {
		config.LlamaCPPConfig.RerankerHFRepo = saved.RerankerHFRepo
	}
	if saved.RerankerHFFile != "" {
		config.LlamaCPPConfig.RerankerHFFile = saved.RerankerHFFile
	}
	if saved.LLMHFRepo != "" {
		config.LlamaCPPConfig.LLMHFRepo = saved.LLMHFRepo
	}
	if saved.LLMHFFile != "" {
		config.LlamaCPPConfig.LLMHFFile = saved.LLMHFFile
	}
}

// applySavedReranker copies the saved reranker settings into config. A
// --rerank-model or --rerank-provider flag still wins (rerankerConfigFromFlags).
func applySavedReranker(config *gleann.Config, saved *tui.OnboardResult) {
	if saved == nil {
		return
	}
	config.SearchConfig.UseReranker = saved.RerankEnabled
	rc := &config.SearchConfig.RerankerConfig
	if saved.RerankModel != "" {
		rc.Model = saved.RerankModel
	}
	if saved.RerankProvider != "" {
		rc.Provider = gleann.RerankerProvider(saved.RerankProvider)
	}
	config.SearchConfig.Expand = saved.QueryExpand
	config.SearchConfig.ExpandModel = saved.ExpandModel
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
