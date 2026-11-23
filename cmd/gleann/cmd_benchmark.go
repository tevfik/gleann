package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tevfik/gleann/pkg/benchmark"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/walker"
)

// cmdBenchmark implements `gleann benchmark` / `gleann bench`.
// It supports two modes:
// 1. ContextBench / SWE-Bench retrieval quality benchmark (--suite contextbench or when --docs is omitted)
// 2. Token reduction analysis (--docs <dir> without --suite)
func cmdBenchmark(args []string) {
	rejectUnknownFlags("benchmark", args, benchFlags)
	if wantsHelpIn(args, benchFlags) {
		printBenchmarkUsage()
		return
	}
	config := getConfig(args)
	applySavedConfig(&config, args)

	indexName := getFlag(args, "--index")
	docsDir := getFlag(args, "--docs")
	tasksFile := getFlag(args, "--tasks")
	suite := getFlag(args, "--suite")
	outputFile := getFlag(args, "--output")
	asJSON := hasFlag(args, "--json")
	compareSpec := getFlag(args, "--compare")
	perTaskFile := getFlag(args, "--per-task")

	topK := 10
	if v := getFlag(args, "--top-k"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topK = n
		}
	}

	// Mode G: generate a golden task set from a Go source tree.
	if genDir := getFlag(args, "--gen-tasks"); genDir != "" {
		runGenTasks(genDir, getFlag(args, "--kind"), getFlag(args, "--count"), outputFile)
		return
	}

	// Mode C: Compare mode without running an index (two per-task files from two binaries)
	if compareSpec != "" && indexName == "" && docsDir == "" && suite != "agent" && !hasFlag(args, "--agent") {
		runStandaloneCompare(compareSpec, asJSON, outputFile)
		return
	}

	// Mode 0: Agent-level task evaluation suite (T25)
	if suite == "agent" || hasFlag(args, "--agent") {
		runAgentBenchmark(asJSON, outputFile)
		return
	}

	// Mode 1: SWE-Bench / ContextBench evaluation mode.
	if suite != "" || tasksFile != "" || (indexName != "" && docsDir == "") {
		runContextBenchmark(config, indexName, tasksFile, topK, asJSON, outputFile, compareSpec, perTaskFile)
		return
	}

	// Mode 2: Legacy Token Reduction Analysis (requires --index and --docs).
	if indexName == "" || docsDir == "" {
		printBenchmarkUsage()
		exitFunc(1)
	}

	runTokenReductionAnalysis(config, indexName, docsDir, topK)
}

// runContextBenchmark runs SWE-Bench / ContextBench retrieval evaluation against an index.
func runContextBenchmark(config gleann.Config, indexName, tasksFile string, topK int, asJSON bool, outputFile, compareSpec, perTaskFile string) {
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index <name> is required for benchmark suite")
		exitFunc(1)
	}

	ctx := context.Background()

	// Resolve the embedder from the index's own metadata (as `gleann search`
	// does). Without this a config/index model mismatch makes every query
	// fail to embed and silently fall back to BM25, so all strategies
	// report identical numbers.
	config.EmbedderResolver = defaultEmbedderResolver(config)
	embedder := newEmbedder(config)

	searcher := gleann.NewSearcher(config, embedder)

	if err := searcher.Load(ctx, indexName); err != nil {
		fmt.Fprintf(os.Stderr, "error loading index %q: %v\n", indexName, err)
		exitFunc(1)
	}
	defer searcher.Close()

	// Load tasks from file (enforce realistic task dataset)
	if tasksFile == "" {
		defaultTasks := filepath.Join("bench", "tasks", indexName+".json")
		if _, err := os.Stat(defaultTasks); err == nil {
			tasksFile = defaultTasks
		} else {
			defaultGleann := filepath.Join("bench", "tasks", "gleann.json")
			if _, err := os.Stat(defaultGleann); err == nil {
				tasksFile = defaultGleann
				fmt.Fprintf(os.Stderr, "note: %s not found; falling back to default tasks %s\n", defaultTasks, tasksFile)
			}
		}
	}

	if tasksFile == "" {
		fmt.Fprintln(os.Stderr, "error: --tasks <file.json> is required. See bench/tasks/gleann.json for example.")
		exitFunc(1)
	}

	var tasks []benchmark.Task
	data, err := os.ReadFile(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading tasks file %s: %v\n", tasksFile, err)
		exitFunc(1)
	}
	if err := json.Unmarshal(data, &tasks); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing tasks json: %v\n", err)
		exitFunc(1)
	}

	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "error: no benchmark tasks found in tasks file")
		exitFunc(1)
	}

	if !asJSON {
		fmt.Printf("🧪 Running ContextBench / SWE-Bench retrieval suite on index %q (%d tasks)...\n", indexName, len(tasks))
	}

	runner := benchmark.NewRunner(tasks)
	runner.PerTaskFile = perTaskFile
	// The tasks file holds every query verbatim; when it is in the index it
	// must not count as a result.
	runner.IgnorePaths(benchmark.TaskFileMatcher(tasksFile))

	sourceDir := searcher.Meta().SourceDir
	if sourceDir == "" {
		sourceDir = "."
	}
	if abs, err := filepath.Abs(sourceDir); err == nil {
		sourceDir = abs
	}

	// Strategy 1: Ripgrep (Lexical baseline)
	if _, err := exec.LookPath("rg"); err != nil {
		runner.RegisterUnavailable("Ripgrep", "ripgrep not installed")
	} else if info, err := os.Stat(sourceDir); err != nil {
		runner.RegisterUnavailable("Ripgrep", fmt.Sprintf("source directory unavailable: %v", err))
	} else if !info.IsDir() {
		runner.RegisterUnavailable("Ripgrep", fmt.Sprintf("source directory %q is not a directory", sourceDir))
	} else {
		runner.RegisterStrategy("Ripgrep", func(ctx context.Context, query string, k int) ([]string, int, error) {
			return runRipgrepSearch(sourceDir, query, k)
		})
	}

	// Strategy 2: BM25 (Pure lexical full-corpus retrieval)
	runner.RegisterStrategy("BM25", func(ctx context.Context, query string, k int) ([]string, int, error) {
		results, err := searcher.SearchBM25(ctx, query, k)
		if err != nil {
			// Fallback to alpha 0 if BM25 direct topK not supported
			results, err = searcher.Search(ctx, query, gleann.WithTopK(k), gleann.WithHybridAlpha(0.0))
		}
		if err != nil {
			return nil, 0, err
		}
		paths, tokens := extractResults(results)
		return paths, tokens, nil
	})

	// Strategy 3: Vector (DiskANN+PQ or HNSW)
	// The label names the backend of the index that is searched, not the
	// configured default (an HNSW index used to be reported as DiskANN+PQ).
	backend := searcher.Meta().Backend
	if backend == "" {
		backend = config.Backend
	}
	vectorLabel := fmt.Sprintf("Vector (%s)", strings.ToUpper(backend))
	if backend == "" || backend == "hnsw" {
		vectorLabel = "Vector (HNSW)"
	} else if backend == "diskann" {
		vectorLabel = "Vector (DiskANN+PQ)"
	}
	runner.RegisterStrategy(vectorLabel, func(ctx context.Context, query string, k int) ([]string, int, error) {
		results, err := searcher.Search(ctx, query, gleann.WithTopK(k), gleann.WithHybridAlpha(1.0))
		if err != nil {
			return nil, 0, err
		}
		paths, tokens := extractResults(results)
		return paths, tokens, nil
	})

	// Strategy 4: Hybrid (Vector + BM25).
	hybridWith := func(opts ...gleann.SearchOption) func(context.Context, string, int) ([]string, int, error) {
		return func(ctx context.Context, query string, k int) ([]string, int, error) {
			all := append([]gleann.SearchOption{gleann.WithTopK(k)}, opts...)
			results, err := searcher.Search(ctx, query, all...)
			if err != nil {
				return nil, 0, err
			}
			paths, tokens := extractResults(results)
			return paths, tokens, nil
		}
	}

	// The first hybrid row is the shipped default: the configured vector
	// weight, which is what `gleann search` uses (0.7 unless --hybrid changed
	// it). It used to be a fixed α=0.5 under the bare name "Hybrid", so the
	// row a reader took for the default measured another retriever.
	// A weight outside [0,1] is not what the searcher applies (the fusion only
	// branches on alpha < 1), so fall back to the default.
	// `--hybrid NaN` and a saved NaN both compare false against every bound, so
	// test for finiteness explicitly: otherwise the searcher sees `alpha < 1`
	// as false and silently skips the BM25 branch while the label claims a
	// hybrid measurement.
	defaultAlpha := config.SearchConfig.HybridAlpha
	if math.IsNaN(float64(defaultAlpha)) || math.IsInf(float64(defaultAlpha), 0) ||
		defaultAlpha < 0 || defaultAlpha > 1 {
		defaultAlpha = gleann.DefaultConfig().SearchConfig.HybridAlpha
	}
	runner.RegisterStrategy(fmt.Sprintf("Hybrid (default, α=%g)", defaultAlpha),
		hybridWith(gleann.WithHybridAlpha(defaultAlpha)))

	// Strategy 4b: the same fusion at fixed, BM25-leaning weights, so the
	// weight itself is measurable. A weight the default already uses is not
	// registered twice under two names.
	for _, alpha := range []float32{0.5, 0.3} {
		if alpha == defaultAlpha {
			continue
		}
		runner.RegisterStrategy(fmt.Sprintf("Hybrid (α=%g)", alpha),
			hybridWith(gleann.WithHybridAlpha(alpha)))
	}

	// Strategy 4f: two-stage with the configured cross-encoder reranker
	// (GLEANN_BENCH_RERANK=1; uses the saved reranker settings or
	// GLEANN_BENCH_RERANK_MODEL / _PROVIDER).
	var benchRR gleann.Reranker // reused by the HyDE + rerank strategy
	benchPool := 20
	if os.Getenv("GLEANN_BENCH_RERANK") == "1" {
		rcfg := gleann.ResolveRerankerConfig(config, os.Getenv("GLEANN_BENCH_RERANK_MODEL"))
		if p := os.Getenv("GLEANN_BENCH_RERANK_PROVIDER"); p != "" {
			rcfg.Provider = gleann.RerankerProvider(p)
		}
		if u := os.Getenv("GLEANN_BENCH_RERANK_URL"); u != "" { // llama-server --reranking
			rcfg.Provider, rcfg.BaseURL = gleann.RerankerJina, u
		}
		pool := 20
		if v, err := strconv.Atoi(os.Getenv("GLEANN_BENCH_RERANK_POOL")); err == nil && v > 0 {
			pool = v
		}
		chars := 500
		if v, err := strconv.Atoi(os.Getenv("GLEANN_BENCH_RERANK_CHARS")); err == nil && v >= 0 {
			chars = v
		}
		rcfg.MaxDocChars = chars
		rr := gleann.NewReranker(rcfg)
		benchRR, benchPool = rr, pool
		for _, gate := range []bool{false, true} {
			label := fmt.Sprintf("Rerank pool=%d chars=%d", pool, chars)
			if gate {
				label += " +skip-ident"
			}
			runner.RegisterStrategy(label, hybridWith(gleann.WithCustomReranker(rr), gleann.WithReranker(true),
				gleann.WithRerankPool(pool), gleann.WithRerankSkipIdentifiers(gate)))
		}
	}

	// Strategy 4g: HyDE query expansion (GLEANN_BENCH_EXPAND_MODEL=<ollama
	// model>), alone and followed by the reranker configured above.
	if m := os.Getenv("GLEANN_BENCH_EXPAND_MODEL"); m != "" {
		cc := gleann.DefaultChatConfig()
		cc.Provider, cc.Model, cc.BaseURL = gleann.LLMOllama, m, config.OllamaHost
		cc.MaxTokens = 120
		exp := gleann.NewLLMQueryExpander(searcher, cc)
		runner.RegisterStrategy("Hybrid + HyDE("+m+")", hybridWith(gleann.WithQueryExpander(exp)))
		if benchRR != nil {
			runner.RegisterStrategy(fmt.Sprintf("Hybrid + HyDE + rerank pool=%d +skip-ident", benchPool), hybridWith(gleann.WithQueryExpander(exp),
				gleann.WithCustomReranker(benchRR), gleann.WithReranker(true), gleann.WithRerankPool(benchPool), gleann.WithRerankSkipIdentifiers(true)))
		}
	}

	// Strategy 4c: keep only the best chunk per file, so several chunks of one
	// file do not use up top-k slots that other files could fill.
	runner.RegisterStrategy("Hybrid (1 chunk/file)", func(ctx context.Context, query string, k int) ([]string, int, error) {
		results, err := searcher.Search(ctx, query, gleann.WithTopK(k), gleann.WithOnePerFile(true))
		if err != nil {
			return nil, 0, err
		}
		paths, tokens := extractResults(results)
		return paths, tokens, nil
	})

	// Strategy 5: GraphRAG (if graph exists). SearchGraphRAG seeds retrieval
	// with α=0.5, so its row is comparable with "Hybrid (α=0.5)", not with the
	// configured default.
	if searcher.GraphDB() != nil {
		runner.RegisterStrategy("GraphRAG", func(ctx context.Context, query string, k int) ([]string, int, error) {
			results, err := searcher.SearchGraphRAG(ctx, query, k)
			if err != nil {
				return nil, 0, err
			}
			paths, tokens := extractResults(results)
			return paths, tokens, nil
		})
	}

	summaries, err := runner.Run(ctx, topK)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark run error: %v\n", err)
		exitFunc(1)
	}

	if n := runner.Ignored(); n > 0 {
		fmt.Fprintf(os.Stderr, "note: the index contains the tasks file %s; its %d hits were not counted as results. Keep task files out of the index (.gleannignore).\n", tasksFile, n)
	}

	var comparisons []*benchmark.ComparisonResult
	if compareSpec != "" {
		stratA, stratB, ok := resolveComparePair(compareSpec, runner.StrategyNames())
		if !ok {
			fmt.Fprintf(os.Stderr, "error: --compare requires two strategies separated by comma, e.g. --compare \"StratA,StratB\"\n")
			exitFunc(1)
		}
		comp, err := runner.Compare(stratA, stratB)
		if err != nil {
			fmt.Fprintf(os.Stderr, "comparison error: %v\n", err)
			exitFunc(1)
		}
		comparisons = append(comparisons, comp)
	}

	if asJSON {
		out, _ := benchmark.RenderJSON(summaries)
		if len(comparisons) > 0 {
			var raw map[string]any
			_ = json.Unmarshal([]byte(out), &raw)
			raw["comparison"] = comparisons[0]
			b, _ := json.MarshalIndent(raw, "", "  ")
			out = string(b)
		}
		fmt.Println(out)
		if outputFile != "" {
			_ = os.WriteFile(outputFile, []byte(out), 0644)
		}
		return
	}

	report := benchmark.RenderMarkdownReport(summaries, len(tasks), comparisons...)
	fmt.Println()
	fmt.Println(report)

	if outputFile != "" {
		if err := os.WriteFile(outputFile, []byte(report), 0644); err == nil {
			fmt.Printf("📄 Report saved to %s\n", outputFile)
		}
	}
}

// resolveComparePair splits a "--compare A,B" spec into two strategy names.
// A strategy name may itself contain a comma ("Hybrid (default, α=0.7)"), so
// the split is resolved against the names the run registered first. When only
// one side matches, the known name anchors the split and the other side is
// kept verbatim, which preserves the runner's "strategy not found" error for
// a mistyped name. A plain two-part spec is the last fallback.
func resolveComparePair(spec string, names []string) (string, string, bool) {
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	type pair struct{ a, b string }
	var exact, anchored []pair
	for i := 0; i < len(spec); i++ {
		if spec[i] != ',' {
			continue
		}
		a, b := strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:])
		if a == "" || b == "" {
			continue
		}
		switch {
		case known[a] && known[b]:
			exact = append(exact, pair{a, b})
		case known[a]:
			anchored = append(anchored, pair{a, b})
		case known[b]:
			anchored = append(anchored, pair{a, b})
		}
	}
	if len(exact) == 1 {
		return exact[0].a, exact[0].b, true
	}
	if len(exact) > 1 {
		return "", "", false
	}
	if len(anchored) == 1 {
		return anchored[0].a, anchored[0].b, true
	}
	if len(anchored) > 1 {
		return "", "", false
	}
	if parts := strings.Split(spec, ","); len(parts) == 2 {
		a, b := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if a != "" && b != "" {
			return a, b, true
		}
	}
	return "", "", false
}

// runGenTasks writes generated retrieval tasks as JSON (stdout, or --output).
func runGenTasks(dir, kind, count, outputFile string) {
	n, _ := strconv.Atoi(count)
	tasks, err := benchmark.GenerateGoTasks(dir, benchmark.GenOptions{Count: n, Kind: kind})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "error: no tasks could be generated (needs exported, documented Go declarations)")
		exitFunc(1)
	}
	data, _ := json.MarshalIndent(tasks, "", "  ")
	if outputFile == "" {
		fmt.Println(string(data))
		return
	}
	if err := os.WriteFile(outputFile, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
	fmt.Printf("wrote %d tasks to %s\n", len(tasks), outputFile)
}

func getMetaStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func extractResults(results []gleann.SearchResult) ([]string, int) {
	var paths []string
	tokens := 0
	for _, r := range results {
		filePath := getMetaStr(r.Metadata, "file")
		if filePath == "" {
			filePath = getMetaStr(r.Metadata, "source")
		}
		if filePath != "" {
			paths = append(paths, filePath)
		}
		tokens += len(r.Text) / 4 // approximate tokens
	}
	return paths, tokens
}

// isStopWord reports whether w is a common English stop word.
func isStopWord(w string) bool {
	switch strings.ToLower(w) {
	case "the", "is", "at", "which", "on",
		"a", "an", "and", "or", "but",
		"in", "to", "for", "of", "with",
		"by", "from", "as", "it", "that",
		"this", "be", "are", "was", "were",
		"been", "being", "have", "has", "had",
		"do", "does", "did", "will", "would",
		"could", "should", "may", "might",
		"not", "no", "so", "if", "then":
		return true
	}
	return false
}

func extractRipgrepTerms(query string) []string {
	words := strings.Fields(query)
	var terms []string
	seen := make(map[string]bool)
	for _, w := range words {
		cleaned := strings.Trim(w, `",':;()[]{}*?!`+"`")
		lower := strings.ToLower(cleaned)
		if len(cleaned) > 1 && !isStopWord(lower) && !seen[lower] {
			seen[lower] = true
			terms = append(terms, cleaned)
		}
	}
	return terms
}

func isRipgrepUnsupportedFlagError(stderr string) bool {
	lower := strings.ToLower(stderr)
	isFlagErr := strings.Contains(lower, "unexpected argument") ||
		strings.Contains(lower, "found argument") ||
		strings.Contains(lower, "unrecognized option") ||
		strings.Contains(lower, "unknown option") ||
		strings.Contains(lower, "unknown flag")
	if !isFlagErr {
		return false
	}
	return strings.Contains(stderr, "--no-require-git") || strings.Contains(stderr, "--no-ignore-parent")
}

// runRipgrepSearch executes a fast ripgrep lookup for key terms in the query.
func runRipgrepSearch(sourceDir, query string, topK int) ([]string, int, error) {
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, 0, fmt.Errorf("ripgrep not installed: %w", benchmark.ErrStrategyUnavailable)
	}

	if sourceDir == "" {
		sourceDir = "."
	}
	if abs, err := filepath.Abs(sourceDir); err == nil {
		sourceDir = abs
	}
	info, err := os.Stat(sourceDir)
	if err != nil {
		return nil, 0, fmt.Errorf("source directory %q: %w: %w", sourceDir, err, benchmark.ErrStrategyUnavailable)
	}
	if !info.IsDir() {
		return nil, 0, fmt.Errorf("source directory %q is not a directory: %w", sourceDir, benchmark.ErrStrategyUnavailable)
	}

	terms := extractRipgrepTerms(query)
	if len(terms) == 0 {
		return nil, 0, nil
	}

	args := []string{"-F", "-c", "-i", "--no-ignore-parent", "--no-require-git"}
	for _, t := range terms {
		args = append(args, "-e", t)
	}
	args = append(args, ".")

	cmd := exec.Command("rg", args...)
	cmd.Dir = sourceDir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 1 {
				// rg exit code 1 indicates no matches found.
				return nil, 0, nil
			}
			msg := strings.TrimSpace(string(exitErr.Stderr))
			// If exit code 2 is caused by an unsupported flag on older ripgrep (< 12.0.0),
			// retry once without the unsupported flags instead of aborting the benchmark.
			if exitErr.ExitCode() == 2 && isRipgrepUnsupportedFlagError(msg) {
				var retryArgs []string
				for _, a := range args {
					if (strings.Contains(msg, "--no-require-git") && a == "--no-require-git") ||
						(strings.Contains(msg, "--no-ignore-parent") && a == "--no-ignore-parent") {
						continue
					}
					retryArgs = append(retryArgs, a)
				}
				cmdRetry := exec.Command("rg", retryArgs...)
				cmdRetry.Dir = sourceDir
				out, err = cmdRetry.Output()
				if err != nil {
					var retryExitErr *exec.ExitError
					if errors.As(err, &retryExitErr) {
						if retryExitErr.ExitCode() == 1 {
							return nil, 0, nil
						}
						retryMsg := strings.TrimSpace(string(retryExitErr.Stderr))
						if retryExitErr.ExitCode() == 2 && isRipgrepUnsupportedFlagError(retryMsg) {
							var finalArgs []string
							for _, a := range retryArgs {
								if a == "--no-require-git" || a == "--no-ignore-parent" {
									continue
								}
								finalArgs = append(finalArgs, a)
							}
							cmdFinal := exec.Command("rg", finalArgs...)
							cmdFinal.Dir = sourceDir
							out, err = cmdFinal.Output()
							if err != nil {
								var finalExitErr *exec.ExitError
								if errors.As(err, &finalExitErr) {
									if finalExitErr.ExitCode() == 1 {
										return nil, 0, nil
									}
									finalMsg := strings.TrimSpace(string(finalExitErr.Stderr))
									if finalMsg != "" {
										return nil, 0, fmt.Errorf("running ripgrep: %w: %s", err, finalMsg)
									}
									return nil, 0, fmt.Errorf("running ripgrep: %w", err)
								}
								return nil, 0, fmt.Errorf("running ripgrep: %w", err)
							}
						} else {
							if retryMsg != "" {
								return nil, 0, fmt.Errorf("running ripgrep: %w: %s", err, retryMsg)
							}
							return nil, 0, fmt.Errorf("running ripgrep: %w", err)
						}
					} else {
						return nil, 0, fmt.Errorf("running ripgrep: %w", err)
					}
				}
			} else {
				if msg != "" {
					return nil, 0, fmt.Errorf("running ripgrep: %w: %s", err, msg)
				}
				return nil, 0, fmt.Errorf("running ripgrep: %w", err)
			}
		} else if errors.Is(err, exec.ErrNotFound) {
			return nil, 0, fmt.Errorf("ripgrep not installed: %w", benchmark.ErrStrategyUnavailable)
		} else {
			return nil, 0, fmt.Errorf("running ripgrep: %w", err)
		}
	}

	type match struct {
		path  string
		count int
	}
	var matches []match
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lastColon := strings.LastIndex(line, ":")
		if lastColon == -1 {
			continue
		}
		filePath := strings.TrimSpace(line[:lastColon])
		filePath = strings.TrimPrefix(filePath, "./")
		filePath = strings.TrimPrefix(filePath, ".\\")
		count, err := strconv.Atoi(strings.TrimSpace(line[lastColon+1:]))
		if err != nil || count <= 0 {
			continue
		}
		matches = append(matches, match{path: filePath, count: count})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].count != matches[j].count {
			return matches[i].count > matches[j].count
		}
		return matches[i].path < matches[j].path
	})

	var matchedFiles []string
	tokens := 0
	for i := 0; i < len(matches) && i < topK; i++ {
		p := matches[i].path
		matchedFiles = append(matchedFiles, p)
		fullPath := filepath.Join(sourceDir, p)
		data, err := os.ReadFile(fullPath)
		if err == nil {
			tokens += len(data) / 4
		} else if info, err := os.Stat(fullPath); err == nil {
			tokens += int(info.Size() / 4)
		}
	}
	return matchedFiles, tokens, nil
}

func runStandaloneCompare(compareSpec string, asJSON bool, outputFile string) {
	targetA, targetB, ok := splitStandaloneCompareSpec(compareSpec)
	if !ok {
		fmt.Fprintf(os.Stderr, "error: --compare requires two targets separated by comma, e.g. --compare fileA.jsonl,fileB.jsonl\n")
		exitFunc(1)
	}

	fileA, stratA := parseTarget(targetA)
	fileB, stratB := parseTarget(targetB)

	recsA, err := benchmark.ReadPerTaskRecords(fileA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", fileA, err)
		exitFunc(1)
	}
	recsB, err := benchmark.ReadPerTaskRecords(fileB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", fileB, err)
		exitFunc(1)
	}

	var errA, errB error
	recsA, stratA, errA = resolveTargetRecords(fileA, stratA, recsA)
	if errA != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", errA)
		exitFunc(1)
	}
	recsB, stratB, errB = resolveTargetRecords(fileB, stratB, recsB)
	if errB != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", errB)
		exitFunc(1)
	}

	comp, err := benchmark.CompareRecords(recsA, recsB, stratA, stratB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "comparison error: %v\n", err)
		exitFunc(1)
	}

	if asJSON {
		data, _ := json.MarshalIndent(comp, "", "  ")
		fmt.Println(string(data))
		if outputFile != "" {
			_ = os.WriteFile(outputFile, data, 0o644)
		}
		return
	}

	md := comp.FormatMarkdown()
	fmt.Println(md)
	if outputFile != "" {
		if err := os.WriteFile(outputFile, []byte(md), 0o644); err == nil {
			fmt.Printf("📄 Comparison saved to %s\n", outputFile)
		}
	}
}

func parseTarget(target string) (file, strategy string) {
	if idx := strings.Index(target, ".jsonl:"); idx != -1 {
		return target[:idx+6], target[idx+7:]
	}
	return target, ""
}

// splitStandaloneCompareSpec splits the offline "--compare A,B" form into its
// two "<file>[:<strategy>]" targets. A strategy qualifier may itself contain a
// comma ("Hybrid (default, α=0.7)"), so a comma is a boundary only when both
// sides parse as plausible targets. A plain two-target spec is the fallback.
func splitStandaloneCompareSpec(spec string) (string, string, bool) {
	var boundaries [][2]string
	for i := 0; i < len(spec); i++ {
		if spec[i] != ',' {
			continue
		}
		a, b := strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:])
		if a == "" || b == "" {
			continue
		}
		if !isTargetFile(a) || !isTargetFile(b) {
			continue
		}
		boundaries = append(boundaries, [2]string{a, b})
	}
	if len(boundaries) == 1 {
		return boundaries[0][0], boundaries[0][1], true
	}
	if len(boundaries) > 1 {
		return "", "", false
	}
	if parts := strings.Split(spec, ","); len(parts) == 2 {
		a, b := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if a != "" && b != "" {
			return a, b, true
		}
	}
	return "", "", false
}

// isTargetFile reports whether target looks like a "<file>[:<strategy>]"
// target: its file part is a plausible path, not the tail of a strategy name.
// A comma in the file part marks such a tail ("α=0.7),fileB.jsonl"); parentheses
// are legal in directory names and stay allowed.
func isTargetFile(target string) bool {
	file, _ := parseTarget(target)
	if file == "" || strings.Contains(file, ",") {
		return false
	}
	if strings.HasSuffix(file, ".jsonl") {
		return true
	}
	if info, err := os.Stat(file); err == nil && !info.IsDir() {
		return true
	}
	return false
}

func filterRecordsByStrategy(records []benchmark.PerTaskRecord, strategy string) []benchmark.PerTaskRecord {
	var out []benchmark.PerTaskRecord
	for _, r := range records {
		if r.Strategy == strategy {
			out = append(out, r)
		}
	}
	return out
}

func resolveTargetRecords(filePath, strategy string, records []benchmark.PerTaskRecord) ([]benchmark.PerTaskRecord, string, error) {
	if len(records) == 0 {
		return nil, "", fmt.Errorf("file %s is empty", filePath)
	}
	if strategy != "" {
		filtered := filterRecordsByStrategy(records, strategy)
		if len(filtered) == 0 {
			return nil, "", fmt.Errorf("strategy %q not found in %s", strategy, filePath)
		}
		return filtered, strategy, nil
	}
	// No strategy suffix provided. Inspect distinct strategies in the file.
	seen := make(map[string]bool)
	var distinct []string
	for _, r := range records {
		if !seen[r.Strategy] {
			seen[r.Strategy] = true
			distinct = append(distinct, r.Strategy)
		}
	}
	if len(distinct) > 1 {
		return nil, "", fmt.Errorf("%s contains multiple strategies (%s); specify target as %s:<strategy>", filePath, strings.Join(distinct, ", "), filePath)
	}
	strat := distinct[0]
	filtered := filterRecordsByStrategy(records, strat)
	return filtered, strat, nil
}

// runTokenReductionAnalysis runs the legacy token reduction benchmark.
func runTokenReductionAnalysis(config gleann.Config, indexName, docsDir string, topK int) {
	fmt.Println("📊 gleann benchmark — Token Reduction Analysis")
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("Index: %s\nDocs:  %s\n\n", indexName, docsDir)

	// Phase 1: Count raw corpus tokens.
	fmt.Println("Phase 1: Counting raw corpus tokens...")
	start := time.Now()
	rawTokens, rawFiles, rawBytes := countCorpusTokens(docsDir)
	phase1 := time.Since(start)
	fmt.Printf("  Files: %d\n", rawFiles)
	fmt.Printf("  Bytes: %s\n", benchFormatBytes(rawBytes))
	fmt.Printf("  Tokens (est): %d\n", rawTokens)
	fmt.Printf("  Time: %s\n\n", phase1.Round(time.Millisecond))

	// Phase 2: Simulate RAG context size.
	fmt.Println("Phase 2: Calculating RAG context size...")
	ragTokens := estimateRAGTokens(config, indexName, topK)
	fmt.Printf("  Top-K: %d\n", topK)
	fmt.Printf("  RAG context tokens (est): %d\n\n", ragTokens)

	// Phase 3: Results.
	fmt.Println(strings.Repeat("─", 60))
	if ragTokens > 0 && rawTokens > 0 {
		reduction := float64(rawTokens) / float64(ragTokens)
		fmt.Printf("📈 Token Reduction: %.1fx\n", reduction)
		fmt.Printf("   Raw corpus:  %d tokens\n", rawTokens)
		fmt.Printf("   RAG context: %d tokens (top-%d passages)\n", ragTokens, topK)
		fmt.Printf("   Savings:     %.1f%% fewer tokens\n", (1-1/reduction)*100)
	} else {
		fmt.Println("⚠️  Could not calculate reduction (empty corpus or index)")
	}

	// Phase 4: Graph stats if available.
	if gleann.HasCodeGraph(config.IndexDir, indexName) {
		fmt.Println()
		printGraphBenchmark(config.IndexDir, indexName)
	}
}

func countCorpusTokens(dir string) (tokens, files int, bytes int64) {
	opts := walker.Options{
		IncludeSubmodules: false,
		FollowSymlinks:    true,
	}
	_ = walker.Walk(dir, opts, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if isBinaryExt(ext) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !utf8.Valid(data) {
			return nil
		}

		files++
		bytes += int64(len(data))
		tokens += len(data) / 4
		return nil
	})
	return
}

func estimateRAGTokens(config gleann.Config, indexName string, topK int) int {
	indexDir := filepath.Join(config.IndexDir, indexName)
	basePath := filepath.Join(indexDir, indexName)

	pm, err := gleann.OpenPassageStore(basePath, true)
	if err != nil {
		return 0
	}
	defer pm.Close()

	total := 0
	count := topK
	maxCount := pm.Count()
	if maxCount < count {
		count = maxCount
	}

	for i := 0; i < count; i++ {
		p, err := pm.Get(int64(i))
		if err != nil {
			continue
		}
		total += len(p.Text) / 4
	}

	return total
}

func printGraphBenchmark(indexDir, name string) {
	fmt.Println("Phase 4: AST Graph Stats")
	// A format-2 graph lives in the index database: report its row counts.
	if g, err := gleann.OpenGraphStore(indexDir, name); err == nil && g != nil {
		defer g.Close()
		if st, err := g.Stats(); err == nil {
			fmt.Printf("  Graph storage: %s.db (%d symbols, %d CALLS edges)\n", name, st.Symbols, st.CallEdges)
			return
		}
	}
	graphDir := filepath.Join(indexDir, name+"_graph")
	var totalSize int64
	var fileCount int
	filepath.WalkDir(graphDir, func(_ string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				totalSize += info.Size()
				fileCount++
			}
		}
		return nil
	})
	fmt.Printf("  Graph storage: %s (%d files)\n", benchFormatBytes(totalSize), fileCount)
}

func isBinaryExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico",
		".mp3", ".wav", ".flac", ".ogg", ".m4a",
		".mp4", ".avi", ".mkv", ".mov", ".webm",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z",
		".exe", ".dll", ".so", ".dylib",
		".pdf", ".doc", ".docx", ".xls", ".xlsx",
		".woff", ".woff2", ".ttf", ".eot",
		".sqlite", ".db":
		return true
	}
	return false
}

func benchFormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func runAgentBenchmark(asJSON bool, outputFile string) {
	fmt.Fprintln(os.Stderr, benchmark.SimulationNotice)
	ctx := context.Background()
	summary, err := benchmark.RunAgentEvaluation(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error running agent benchmark: %v\n", err)
		exitFunc(1)
	}

	if outputFile != "" {
		if err := summary.WriteReport(outputFile, asJSON); err != nil {
			fmt.Fprintf(os.Stderr, "error writing report to %s: %v\n", outputFile, err)
			exitFunc(1)
		}
		fmt.Printf("Report saved to %s\n", outputFile)
	}

	if asJSON {
		data, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Println(summary.FormatMarkdown())
	}
}

func printBenchmarkUsage() {
	fmt.Println(`gleann benchmark / gleann bench — Retrieval & token reduction evaluation

Usage:
  # 1. SWE-Bench / ContextBench Retrieval Quality Evaluation:
  gleann bench --index <name> [--tasks <file.json>] [--top-k <n>] [--output <report.md>] [--json]

  # 2. Generate a golden task set from a Go source tree:
  gleann bench --gen-tasks <dir> [--kind doc|symbol] [--count <n>] [--output <tasks.json>]

  # 3. Token Reduction Analysis:
  gleann benchmark --index <name> --docs <dir> [--top-k <n>]

  # 4. Compare mode (two per-task files from two binaries, or two strategies):
  gleann bench --compare <fileA.jsonl,fileB.jsonl> [--output <report.md>] [--json]

  # 5. Agent-Level Task Evaluation Suite (simulated scenario):
  gleann bench --agent [--output <report.md>] [--json]
  gleann bench --suite agent [--output <report.md>] [--json]

Options:
  --index <name>      Index name (required for retrieval benchmarks)
  --tasks <file>      SWE-Bench/ContextBench task file (default: bench/tasks/<index>.json, fallback: bench/tasks/gleann.json)
  --top-k <n>         Number of retrieved passages (default: 10)
  --output <file>     Write markdown report to file
  --json              Output raw JSON metrics
  --gen-tasks <dir>   Generate tasks from exported, documented Go declarations
  --kind <k>          doc (doc-sentence query, default) or symbol (identifier query)
  --count <n>         Number of generated tasks (default: 100)
  --docs <dir>        Source directory for token reduction analysis
  --compare <spec>    Compare two strategies ("A,B") or two per-task JSONL files
  --per-task <file>   Write per-task metrics to JSONL file (or set GLEANN_BENCH_PERTASK)
  --agent             Run the simulated agent-level 4-task scenario (assumed numbers, not a measurement)
  --suite <name>      Evaluation suite: contextbench, agent

Examples:
  gleann bench --index my-code --tasks bench/tasks/gleann_nl.json
  gleann bench --index my-code --tasks bench/tasks/gleann_nl.json --output report.md
  gleann bench --index my-code --compare "Hybrid (default, α=0.7),Hybrid (1 chunk/file)"
  gleann bench --compare runA.jsonl,runB.jsonl
  gleann benchmark --index my-code --docs ./src/
  gleann bench --agent
  gleann bench --agent --output report.md`)
}
