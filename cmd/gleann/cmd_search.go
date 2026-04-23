package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

func cmdSearch(args []string) {
	if len(args) < 2 {
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

	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		os.Exit(1)
	}

	embedder := embedding.NewComputer(embedding.Options{
		Provider:    embedding.Provider(config.EmbeddingProvider),
		Model:       config.EmbeddingModel,
		BaseURL:     config.OllamaHost,
		BatchSize:   config.BatchSize,
		Concurrency: config.Concurrency,
	})

	searcher := gleann.NewSearcher(config, embedder)

	// Set up BM25 hybrid scoring if --hybrid is specified.
	if hasFlag(args, "--hybrid") {
		searcher.SetScorer(gleann.NewBM25Adapter())
	}

	// Set up reranker if --rerank is specified.
	if hasFlag(args, "--rerank") {
		rerankModel := getFlag(args, "--rerank-model")
		if rerankModel == "" {
			rerankModel = "bge-reranker-v2-m3"
		}
		rerankerCfg := gleann.RerankerConfig{
			Provider: gleann.RerankerProvider(config.EmbeddingProvider),
			Model:    rerankModel,
			BaseURL:  config.OllamaHost,
		}
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
	if alphaStr := getFlag(args, "--hybrid-alpha"); alphaStr != "" {
		if alpha, err := strconv.ParseFloat(alphaStr, 32); err == nil {
			searchOpts = append(searchOpts, gleann.WithHybridAlpha(float32(alpha)))
		}
	}

	// Multi-index search: comma-separated names or --all.
	names := strings.Split(name, ",")
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
