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
  --rerank-pool <N>       Candidates sent to the reranker (default: max(4*top-k, 50))
  --expand                HyDE: an LLM sketches the code for the question and the
                          search uses it too (+1-3 s; best for "how/where" questions)
  --expand-model <m>      LLM for --expand (default: expand_model setting or chat model)
  --explain               Show why each result matched: most important segments
                          and BM25 term weights (~70 ms per result)
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
	query := stripSearchFlags(args[1:])

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

	// Rerank with --rerank or when enabled in the settings (--no-rerank turns it off).
	if wantRerank(config, args) {
		rerankerCfg := rerankerConfigFromFlags(config, args)
		searcher.SetReranker(gleann.NewReranker(rerankerCfg))
		config.SearchConfig.UseReranker = true
	}

	ctx := context.Background()

	// Build common search options.
	var searchOpts []gleann.SearchOption
	if wantExpand(config, args) {
		// Per-request expander so multi-index searches use it too.
		searchOpts = append(searchOpts, gleann.WithQueryExpander(newQueryExpander(searcher, args, config)))
	}
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
	if v := getFlag(args, "--rerank-pool"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			searchOpts = append(searchOpts, gleann.WithRerankPool(n))
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

	// --explain: occlusion-based segment importance + BM25 term breakdown.
	var explanations []*gleann.Explanation
	if hasFlag(args, "--explain") {
		explanations = make([]*gleann.Explanation, len(results))
		for i, r := range results {
			exp, err := searcher.ExplainResult(ctx, query, r, gleann.ExplainOptions{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "explain: %v\n", err)
				break
			}
			explanations[i] = exp
		}
	}

	if asJSON {
		var data []byte
		if explanations != nil {
			type explained struct {
				gleann.SearchResult
				Explanation *gleann.Explanation `json:"explanation,omitempty"`
			}
			out := make([]explained, len(results))
			for i, r := range results {
				out[i] = explained{SearchResult: r, Explanation: explanations[i]}
			}
			data, _ = json.MarshalIndent(out, "", "  ")
		} else {
			data, _ = json.MarshalIndent(results, "", "  ")
		}
		fmt.Println(string(data))
		return
	}

	fmt.Printf("🔍 Results for %q (%d results in %s):\n\n", query, len(results), elapsed.Round(time.Millisecond))
	for i, result := range results {
		fmt.Printf("[%d] Score: %.4f\n", i+1, result.Score)
		if explanations != nil && explanations[i] != nil {
			printExplanation(result, explanations[i])
			continue
		}
		text := result.Text
		if len(text) > 200 {
			text = text[:200] + "..."
		}
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

// printExplanation shows where a result matches: the passage's source, the
// most important segments (by drop in similarity when removed) and the BM25
// contribution of each query term.
func printExplanation(r gleann.SearchResult, e *gleann.Explanation) {
	src := getMetaStr(r.Metadata, "source")
	if src == "" {
		src = getMetaStr(r.Metadata, "file")
	}
	fmt.Printf("    %s  (similarity %.3f)\n", src, e.Similarity)
	for i, sg := range e.Segments {
		if i == 2 || (i > 0 && sg.Delta <= 0) {
			break
		}
		// Passage text carries its own source line numbers ("12|..."), so
		// only the importance is printed here.
		fmt.Printf("    ▸ Δ%+.3f\n", sg.Delta)
		text := sg.Text
		if len(text) > 300 {
			text = text[:300] + "..."
		}
		fmt.Printf("      %s\n", strings.ReplaceAll(text, "\n", "\n      "))
	}
	var hit, miss []string
	for _, t := range e.Terms {
		if t.TF > 0 {
			hit = append(hit, fmt.Sprintf("%s ×%d (%.2f)", t.Term, t.TF, t.Score))
		} else {
			miss = append(miss, t.Term)
		}
	}
	if len(hit) > 0 {
		fmt.Printf("    BM25 terms: %s\n", strings.Join(hit, ", "))
	}
	if len(miss) > 0 {
		fmt.Printf("    missing terms: %s\n", strings.Join(miss, ", "))
	}
	fmt.Println()
}

// wantRerank reports whether a search reranks: --rerank, or the saved
// rerank_enabled setting unless --no-rerank is given.
func wantRerank(config gleann.Config, args []string) bool {
	if hasFlag(args, "--no-rerank") {
		return false
	}
	return hasFlag(args, "--rerank") || config.SearchConfig.UseReranker
}

// rerankerConfigFromFlags builds the reranker config for --rerank. With
// --rerank-url the reranker is any server speaking /v1/rerank (llama-server
// --reranking, Jina-compatible); otherwise the saved reranker settings apply
// (gleann.ResolveRerankerConfig), with --rerank-model and --rerank-provider
// overriding them.
func rerankerConfigFromFlags(config gleann.Config, args []string) gleann.RerankerConfig {
	model := getFlag(args, "--rerank-model")
	if url := getFlag(args, "--rerank-url"); url != "" {
		if model == "" {
			model = gleann.DefaultServerRerankModel
		}
		return gleann.RerankerConfig{Provider: gleann.RerankerJina, Model: model, BaseURL: url}
	}
	if p := getFlag(args, "--rerank-provider"); p != "" {
		config.SearchConfig.RerankerConfig.Provider = gleann.RerankerProvider(p)
	}
	return gleann.ResolveRerankerConfig(config, model)
}

// searchValueFlags are the search/config flags that consume the next argument.
// Every other --flag is a boolean switch and must not swallow a query word.
var searchValueFlags = map[string]bool{
	"--top-k": true, "--top": true, "--model": true, "--code-model": true,
	"--code-embedding-model": true, "--docs-model": true, "--provider": true,
	"--host": true, "--base-url": true, "--openai-base-url": true,
	"--api-key": true, "--ollama-api-key": true, "--index-dir": true,
	"--kind": true, "--llm-model": true, "--llm-provider": true,
	"--rerank-model": true, "--rerank-url": true, "--rerank-pool": true, "--rerank-provider": true,
	"--expand-model": true,
	"--hybrid":       true, "--hybrid-alpha": true, "--prune": true,
}

// stripSearchFlags returns the query text from the arguments after the index
// name, dropping flags and (only for value-taking flags) their values.
// "--" ends flag parsing; "--flag=value" is a single token.
func stripSearchFlags(args []string) string {
	parts := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			parts = append(parts, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "--") {
			if !strings.Contains(a, "=") && searchValueFlags[a] {
				i++
			}
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
