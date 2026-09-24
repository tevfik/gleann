package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/benchmark"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/walker"
)

// cmdBenchmark implements `gleann benchmark` / `gleann bench`.
// It supports two modes:
// 1. ContextBench / SWE-Bench retrieval quality benchmark (--suite contextbench or when --docs is omitted)
// 2. Token reduction analysis (--docs <dir> without --suite)
func cmdBenchmark(args []string) {
	config := getConfig(args)
	applySavedConfig(&config, args)

	indexName := getFlag(args, "--index")
	docsDir := getFlag(args, "--docs")
	tasksFile := getFlag(args, "--tasks")
	suite := getFlag(args, "--suite")
	outputFile := getFlag(args, "--output")
	asJSON := hasFlag(args, "--json")

	topK := 10
	if v := getFlag(args, "--top-k"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topK = n
		}
	}

	// Mode 0: Agent-level task evaluation suite (T25)
	if suite == "agent" || hasFlag(args, "--agent") {
		runAgentBenchmark(asJSON, outputFile)
		return
	}

	// Mode 1: SWE-Bench / ContextBench evaluation mode.
	if suite != "" || tasksFile != "" || (indexName != "" && docsDir == "") {
		runContextBenchmark(config, indexName, tasksFile, topK, asJSON, outputFile)
		return
	}

	// Mode 2: Legacy Token Reduction Analysis (requires --index and --docs).
	if indexName == "" || docsDir == "" {
		printBenchmarkUsage()
		os.Exit(1)
	}

	runTokenReductionAnalysis(config, indexName, docsDir, topK)
}

// runContextBenchmark runs SWE-Bench / ContextBench retrieval evaluation against an index.
func runContextBenchmark(config gleann.Config, indexName, tasksFile string, topK int, asJSON bool, outputFile string) {
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index <name> is required for benchmark suite")
		os.Exit(1)
	}

	ctx := context.Background()

	embedder := embedding.NewComputer(embedding.Options{
		Provider:    embedding.Provider(config.EmbeddingProvider),
		Model:       config.EmbeddingModel,
		BaseURL:     config.OllamaHost,
		BatchSize:   config.BatchSize,
		Concurrency: config.Concurrency,
	})

	searcher := gleann.NewSearcher(config, embedder)
	searcher.SetScorer(gleann.NewBM25Adapter())

	if err := searcher.Load(ctx, indexName); err != nil {
		fmt.Fprintf(os.Stderr, "error loading index %q: %v\n", indexName, err)
		os.Exit(1)
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
			}
		}
	}

	if tasksFile == "" {
		fmt.Fprintln(os.Stderr, "error: --tasks <file.json> is required. See bench/tasks/gleann.json for example.")
		os.Exit(1)
	}

	var tasks []benchmark.Task
	data, err := os.ReadFile(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading tasks file %s: %v\n", tasksFile, err)
		os.Exit(1)
	}
	if err := json.Unmarshal(data, &tasks); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing tasks json: %v\n", err)
		os.Exit(1)
	}

	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "error: no benchmark tasks found in tasks file")
		os.Exit(1)
	}

	if !asJSON {
		fmt.Printf("🧪 Running ContextBench / SWE-Bench retrieval suite on index %q (%d tasks)...\n", indexName, len(tasks))
	}

	runner := benchmark.NewRunner(tasks)

	// Strategy 1: Ripgrep (Lexical baseline)
	runner.RegisterStrategy("Ripgrep", func(ctx context.Context, query string, k int) ([]string, int, error) {
		return runRipgrepSearch(query, k)
	})

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
	vectorLabel := fmt.Sprintf("Vector (%s)", strings.ToUpper(config.Backend))
	if config.Backend == "" || config.Backend == "diskann" {
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

	// Strategy 4: Hybrid (Vector + BM25)
	runner.RegisterStrategy("Hybrid", func(ctx context.Context, query string, k int) ([]string, int, error) {
		results, err := searcher.Search(ctx, query, gleann.WithTopK(k), gleann.WithHybridAlpha(0.5))
		if err != nil {
			return nil, 0, err
		}
		paths, tokens := extractResults(results)
		return paths, tokens, nil
	})

	// Strategy 5: GraphRAG (if graph exists)
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
		os.Exit(1)
	}

	if asJSON {
		out, _ := benchmark.RenderJSON(summaries)
		fmt.Println(out)
		if outputFile != "" {
			_ = os.WriteFile(outputFile, []byte(out), 0644)
		}
		return
	}

	report := benchmark.RenderMarkdownReport(summaries, len(tasks))
	fmt.Println()
	fmt.Println(report)

	if outputFile != "" {
		if err := os.WriteFile(outputFile, []byte(report), 0644); err == nil {
			fmt.Printf("📄 Report saved to %s\n", outputFile)
		}
	}
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

// runRipgrepSearch executes a fast ripgrep lookup for key terms in the query.
func runRipgrepSearch(query string, topK int) ([]string, int, error) {
	words := strings.Fields(query)
	if len(words) == 0 {
		return nil, 0, nil
	}

	// Pick the most specific query terms (longest word or keyword)
	var term string
	for _, w := range words {
		cleaned := strings.Trim(w, `",':;()[]{}*`)
		if len(cleaned) > len(term) {
			term = cleaned
		}
	}

	if term == "" {
		return nil, 0, nil
	}

	cmd := exec.Command("rg", "-l", "-i", "--max-count", "1", term)
	out, err := cmd.Output()
	if err != nil {
		// Fallback to standard grep
		cmd = exec.Command("grep", "-l", "-r", "-i", "-m", "1", "--exclude-dir=.git", term, ".")
		out, err = cmd.Output()
	}

	var matchedFiles []string
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "./")
			if line != "" {
				matchedFiles = append(matchedFiles, line)
				if len(matchedFiles) >= topK {
					break
				}
			}
		}
	}
	return matchedFiles, 0, nil
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
	graphDir := filepath.Join(config.IndexDir, indexName+"_graph")
	if info, err := os.Stat(graphDir); err == nil && info.IsDir() {
		fmt.Println()
		printGraphBenchmark(graphDir)
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

	pm := gleann.NewPassageManager(basePath)
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

func printGraphBenchmark(graphDir string) {
	fmt.Println("Phase 4: AST Graph Stats")
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
	ctx := context.Background()
	summary, err := benchmark.RunAgentEvaluation(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error running agent benchmark: %v\n", err)
		os.Exit(1)
	}

	if outputFile != "" {
		if err := summary.WriteReport(outputFile, asJSON); err != nil {
			fmt.Fprintf(os.Stderr, "error writing report to %s: %v\n", outputFile, err)
			os.Exit(1)
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
  # 1. Agent-Level Task Evaluation Suite (T25):
  gleann bench --agent [--output <report.md>] [--json]
  gleann bench --suite agent [--output <report.md>] [--json]

  # 2. SWE-Bench / ContextBench Retrieval Quality Evaluation:
  gleann bench --index <name> [--tasks <file.json>] [--top-k <n>] [--output <report.md>] [--json]

  # 3. Token Reduction Analysis:
  gleann benchmark --index <name> --docs <dir> [--top-k <n>]

Options:
  --agent             Run agent-level 4-task evaluation suite (T25)
  --suite <name>      Evaluation suite: agent, contextbench
  --index <name>      Index name (required for retrieval benchmarks)
  --tasks <file>      SWE-Bench/ContextBench task file (optional, auto-generates if omitted)
  --docs <dir>        Source directory for token reduction analysis
  --top-k <n>         Number of retrieved passages (default: 10)
  --output <file>     Write markdown report to file
  --json              Output raw JSON metrics

Examples:
  gleann bench --agent
  gleann bench --agent --output docs/evaluation.md
  gleann bench --index my-code
  gleann bench --index my-code --output report.md
  gleann benchmark --index my-code --docs ./src/`)
}
