package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

func cmdSearch(args []string) {
	if len(args) < 2 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		if hasFlag(args, "--help") || hasFlag(args, "-h") {
			fmt.Println(`Usage: gleann search <name[,name2,...]|--all> <query> [options]

Semantic vector search across one or more indexes.

Arguments:
  <name>                  Name of the index (or comma-separated names, e.g. core,px4)
  <query>                 Search query text

Options:
  --all                   Search across all existing indexes
  --top-k <N>             Number of top results to return (default: 5)
  --rerank                Rerank results with cross-encoder
  --rerank-url <url>      Rerank through a /v1/rerank server (e.g. llama-server --reranking)
  --json                  Output results as JSON
  -h, --help              Show this help message

Examples:
  gleann search core "auth middleware"
  gleann search px4,core "ekf estimation" --rerank
  gleann search --all "PID controller"`)
			return
		}
		fmt.Fprintln(os.Stderr, "usage: gleann search <name[,name2,...]|--all> <query>")
		os.Exit(1)
	}

	name := args[0]
	query := strings.Join(args[1:], " ")

	// Remove flags from query.
	queryParts := []string{}
	skip := false
	for _, part := range strings.Fields(query) {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(part, "--") {
			skip = true
			continue
		}
		queryParts = append(queryParts, part)
	}
	query = strings.Join(queryParts, " ")

	config := getConfig(args)
	asJSON := hasFlag(args, "--json")
	searchAll := hasFlag(args, "--all")

	applySavedConfig(&config, args)
	config.EmbedderResolver = defaultEmbedderResolver(config)

	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		os.Exit(1)
	}

	embedder := newEmbedder(config)

	searcher := gleann.NewSearcher(config, embedder)

	// Set up BM25 hybrid scoring by default (disable with --no-hybrid).
	if hasFlag(args, "--no-hybrid") {
		searcher.SetScorer(nil)
	}

	// Set up reranker if --rerank is specified.
	if hasFlag(args, "--rerank") {
		rerankerCfg := rerankerConfigFromFlags(config, args)
		searcher.SetReranker(gleann.NewReranker(rerankerCfg))
		config.SearchConfig.UseReranker = true
	}

	ctx := context.Background()

	// Build common search options.
	var searchOpts []gleann.SearchOption
	if config.SearchConfig.UseReranker {
		searchOpts = append(searchOpts, gleann.WithReranker(true))
	}
	if hasFlag(args, "--graph") {
		searchOpts = append(searchOpts, gleann.WithGraphContext(true))
	}
	if hasFlag(args, "--no-hybrid") {
		searchOpts = append(searchOpts, gleann.WithHybridAlpha(1.0))
	} else if alphaStr := getFlag(args, "--hybrid-alpha"); alphaStr != "" {
		if alpha, err := strconv.ParseFloat(alphaStr, 32); err == nil {
			searchOpts = append(searchOpts, gleann.WithHybridAlpha(float32(alpha)))
		}
	}
	if hasFlag(args, "--one-per-file") {
		searchOpts = append(searchOpts, gleann.WithOnePerFile(true))
	}
	if hasFlag(args, "--include-tests") {
		searchOpts = append(searchOpts, gleann.WithIncludeTests(true))
	}
	if kind := getFlag(args, "--kind"); kind != "" {
		searchOpts = append(searchOpts, gleann.WithKind(kind))
	}

	// Multi-index search: comma-separated names, --all, or auto-expanded dual index (<name>-code, <name>-docs).
	names := strings.Split(name, ",")
	if !searchAll && len(names) == 1 {
		idxPath := filepath.Join(config.IndexDir, name)
		if _, err := os.Stat(idxPath); os.IsNotExist(err) {
			codeIdx := filepath.Join(config.IndexDir, name+"-code")
			docsIdx := filepath.Join(config.IndexDir, name+"-docs")
			var autoNames []string
			if _, err := os.Stat(codeIdx); err == nil {
				autoNames = append(autoNames, name+"-code")
			}
			if _, err := os.Stat(docsIdx); err == nil {
				autoNames = append(autoNames, name+"-docs")
			}
			if len(autoNames) > 0 {
				names = autoNames
			}
		}
	}
	if searchAll || len(names) > 1 {
		var indexNames []string
		if !searchAll {
			indexNames = names
		}
		// indexNames == nil means "all indexes"

		start := time.Now()
		multiResults, err := gleann.SearchMultiple(ctx, config, embedder, indexNames, query, searchOpts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error in multi-search: %v\n", err)
			os.Exit(1)
		}
		elapsed := time.Since(start)

		if asJSON {
			data, _ := json.MarshalIndent(multiResults, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Printf("🔍 Multi-index results for %q (%d results in %s):\n\n", query, len(multiResults), elapsed.Round(time.Millisecond))
		for i, result := range multiResults {
			text := result.Text
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			fmt.Printf("[%d] Score: %.4f  Index: %s\n", i+1, result.Score, result.Index)
			fmt.Printf("    %s\n\n", strings.ReplaceAll(text, "\n", "\n    "))
		}
		return
	}

	// Single-index search.
	if err := searcher.Load(ctx, name); err != nil {
		fmt.Fprintf(os.Stderr, "error loading index: %v\n", err)
		os.Exit(1)
	}
	defer searcher.Close()

	start := time.Now()
	results, err := searcher.Search(ctx, query, searchOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error searching: %v\n", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	if asJSON {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("🔍 Results for %q (%d results in %s):\n\n", query, len(results), elapsed.Round(time.Millisecond))
	for i, result := range results {
		text := result.Text
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		fmt.Printf("[%d] Score: %.4f\n", i+1, result.Score)
		fmt.Printf("    %s\n\n", strings.ReplaceAll(text, "\n", "\n    "))

		// Show graph context if available.
		if result.GraphContext != nil && len(result.GraphContext.Symbols) > 0 {
			fmt.Printf("    📊 Graph Context:\n")
			for _, sym := range result.GraphContext.Symbols {
				fmt.Printf("      • %s (%s)\n", sym.FQN, sym.Kind)
				if len(sym.Callers) > 0 {
					fmt.Printf("        ← callers: %s\n", strings.Join(sym.Callers, ", "))
				}
				if len(sym.Callees) > 0 {
					fmt.Printf("        → callees: %s\n", strings.Join(sym.Callees, ", "))
				}
			}
			fmt.Println()
		}
	}
}

// rerankerConfigFromFlags builds the reranker config for --rerank. With
// --rerank-url the reranker is any server speaking /v1/rerank (llama-server
// --reranking, Jina-compatible); otherwise the embedding provider is reused,
// which only works for providers that implement reranking.
func rerankerConfigFromFlags(config gleann.Config, args []string) gleann.RerankerConfig {
	model := getFlag(args, "--rerank-model")
	if model == "" {
		model = "bge-reranker-v2-m3"
	}
	if url := getFlag(args, "--rerank-url"); url != "" {
		return gleann.RerankerConfig{Provider: gleann.RerankerJina, Model: model, BaseURL: url}
	}
	cfg := gleann.RerankerConfig{
		Provider: gleann.RerankerProvider(config.EmbeddingProvider),
		Model:    model,
		BaseURL:  config.OllamaHost,
	}
	if cfg.Provider == gleann.RerankerOllama {
		cfg.APIKey = config.APIKeyForProvider("ollama")
	}
	return cfg
}
