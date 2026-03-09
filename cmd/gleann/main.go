package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/mcp"
	"github.com/tevfik/gleann/internal/server"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"

	_ "github.com/tevfik/gleann/pkg/backends"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	defer cleanupLlamaCPP()
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "build":
		cmdBuild(args)
	case "search":
		cmdSearch(args)
	case "ask":
		cmdAsk(args)
	case "watch":
		cmdWatch(args)
	case "list":
		cmdList(args)
	case "remove":
		cmdRemove(args)
	case "serve":
		cmdServe(args)
	case "info":
		cmdInfo(args)
	case "graph":
		cmdGraph(args)
	case "chat":
		cmdChat(args)
	case "mcp":
		cmdMCP()
	case "tui":
		cmdTUI()
	case "setup":
		cmdSetup()
	case "version":
		fmt.Printf("gleann %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`gleann — Lightweight Vector Database with Graph-Based Recomputation

Usage:
  gleann build  <name> --docs <dir>     Build index from documents
  gleann search <name> <query>          Search an index
  gleann ask    <name> <question>       Ask a question (RAG Q&A)
  gleann chat   [name]                  Interactive chat TUI
  gleann watch  <name> --docs <dir>     Watch & auto-rebuild on changes
  gleann list                           List all indexes
  gleann remove <name>                  Remove an index
  gleann info   <name>                  Show index info
  gleann graph  <deps|callers> <sym>    Query AST Graph in KuzuDB
  gleann serve  [--addr :8080]          Start REST API server
  gleann mcp                            Start MCP server (stdio, for AI editors)
  gleann tui                            Launch interactive TUI
  gleann setup                          Run configuration wizard
  gleann version                        Show version

Embedding Options:
  --model <model>         Embedding model (default: bge-m3)
  --provider <provider>   Embedding provider: ollama, openai (default: ollama)
  --host <url>            Ollama host URL (default: http://localhost:11434)
  --batch-size <n>        Embedding batch size (default: auto based on provider)
  --concurrency <n>       Max concurrent embedding batches (default: auto based on provider)

Search Options:
  --top-k <n>             Number of results (default: 10)
  --metric <metric>       Distance metric: l2, cosine, ip (default: l2)
  --json                  Output as JSON
  --rerank                Enable two-stage reranking for higher accuracy
  --rerank-model <model>  Reranker model (default: bge-reranker-v2-m3)
  --hybrid                Use hybrid search (vector + BM25)
  --ef-search <n>         HNSW ef_search parameter (higher = more accurate, slower)

Build Options:
  --index-dir <dir>       Index storage directory (default: ~/.gleann/indexes)
  --chunk-size <n>        Chunk size in tokens (default: 512)
  --chunk-overlap <n>     Chunk overlap in tokens (default: 50)
  --graph                 Build AST-based code graph (requires treesitter build tag)
  --prune                 Prune unchanged files during incremental builds
  --no-mmap               Disable memory-mapped file access

LLM Options:
  --llm-model <model>     LLM model for ask/chat (default: llama3.2)
  --llm-provider <prov>   LLM provider: ollama, openai, anthropic (default: ollama)
  --interactive           Interactive chat mode (ask command)
  --session <file>        Resume chat from a session file

Graph Options:
  --index <name>          Index name for graph queries (required for graph command)

Examples:
  gleann build my-docs --docs ./documents/
  gleann build my-code --docs ./src/ --graph
  gleann search my-docs "How does caching work?"
  gleann search my-docs "How does caching work?" --rerank
  gleann ask my-docs "Explain the architecture" --interactive
  gleann chat my-docs
  gleann graph deps main.handleRequest --index my-code
  gleann tui
  gleann serve --addr :8080`)
}

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
		}
	}

	return config
}

func getFlag(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func cmdBuild(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann build <name> --docs <dir>")
		os.Exit(1)
	}

	name := args[0]
	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		fmt.Fprintln(os.Stderr, "error: --docs flag required")
		os.Exit(1)
	}
	buildGraph := hasFlag(args, "--graph")

	config := getConfig(args)

	// Load saved config from ~/.gleann/config.json (TUI setup).
	// CLI flags take precedence over saved values.
	savedCfg := tui.LoadSavedConfig()
	if savedCfg != nil {
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
	}

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

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Initialize vault tracker
	tracker, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not initialize vault tracker: %v\n", err)
	} else {
		defer tracker.Close()
	}

	// Read documents from directory.
	fmt.Printf("📂 Reading documents from %s...\n", docsDir)
	items, pluginDocs, err := readDocuments(docsDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap, tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading documents: %v\n", err)
		os.Exit(1)
	}

	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "error: no documents found")
		os.Exit(1)
	}

	fmt.Printf("📝 Found %d text chunks\n", len(items))
	fmt.Printf("🔧 Building index %q with model %s...\n", name, config.EmbeddingModel)

	start := time.Now()
	ctx := context.Background()
	if err := builder.Build(ctx, name, items); err != nil {
		fmt.Fprintf(os.Stderr, "error building index: %v\n", err)
		os.Exit(1)
	}

	elapsed := time.Since(start)
	fmt.Printf("✅ Vector Index %q built: %d passages in %s\n", name, len(items), elapsed.Round(time.Millisecond))

	if buildGraph {
		buildGraphIndex(name, docsDir, config.IndexDir, pluginDocs)
	}
}

func cmdSearch(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gleann search <name> <query>")
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

	// Load saved config from ~/.gleann/config.json (TUI setup).
	// CLI flags take precedence over saved values.
	savedCfg := tui.LoadSavedConfig()
	if savedCfg != nil {
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
	}

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
	if err := searcher.Load(ctx, name); err != nil {
		fmt.Fprintf(os.Stderr, "error loading index: %v\n", err)
		os.Exit(1)
	}
	defer searcher.Close()

	start := time.Now()
	var searchOpts []gleann.SearchOption
	if config.SearchConfig.UseReranker {
		searchOpts = append(searchOpts, gleann.WithReranker(true))
	}
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
	}
}

func cmdList(args []string) {
	config := getConfig(args)
	asJSON := hasFlag(args, "--json")

	indexes, err := gleann.ListIndexes(config.IndexDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		data, _ := json.MarshalIndent(indexes, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(indexes) == 0 {
		fmt.Println("No indexes found.")
		return
	}

	fmt.Printf("📚 Indexes (%d):\n\n", len(indexes))
	for _, idx := range indexes {
		fmt.Printf("  %-20s  %d passages  backend=%s  model=%s\n",
			idx.Name, idx.NumPassages, idx.Backend, idx.EmbeddingModel)
	}
}

func cmdRemove(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann remove <name1> [name2] ... or gleann remove \"prefix*\"")
		os.Exit(1)
	}

	config := getConfig(args)
	// Filter out flags from args to get only index names/patterns
	patterns := []string{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			patterns = append(patterns, arg)
		}
	}

	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "error: no index name or pattern provided")
		os.Exit(1)
	}

	// 1. Get all existing indexes to match against patterns
	allIndexes, err := gleann.ListIndexes(config.IndexDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing indexes: %v\n", err)
		os.Exit(1)
	}

	// 2. Identify indexes to remove
	toBeRemoved := make(map[string]bool)
	for _, pattern := range patterns {
		matched := false
		for _, idx := range allIndexes {
			match, _ := filepath.Match(pattern, idx.Name)
			if match {
				toBeRemoved[idx.Name] = true
				matched = true
			}
		}
		// If it's not a wildcard and didn't match, assume it's a literal name that might exist even if metadata is missing
		if !strings.ContainsAny(pattern, "*?[]") && !matched {
			toBeRemoved[pattern] = true
		}
	}

	if len(toBeRemoved) == 0 {
		fmt.Println("No matching indexes found.")
		return
	}

	// 3. Confirmation for mass deletion
	if len(toBeRemoved) > 3 || (len(patterns) == 1 && patterns[0] == "*") {
		fmt.Printf("⚠️  Are you sure you want to remove %d indexes? (y/N): ", len(toBeRemoved))
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	// 4. Perform removal
	successCount := 0
	for name := range toBeRemoved {
		if err := gleann.RemoveIndex(config.IndexDir, name); err != nil {
			fmt.Fprintf(os.Stderr, "error removing %q: %v\n", name, err)
		} else {
			fmt.Printf("🗑️  Index %q removed.\n", name)
			successCount++
		}
	}

	if successCount > 1 {
		fmt.Printf("✅ Successfully removed %d indexes.\n", successCount)
	}
}

func cmdInfo(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann info <name>")
		os.Exit(1)
	}

	name := args[0]
	config := getConfig(args)
	asJSON := hasFlag(args, "--json")

	indexDir := filepath.Join(config.IndexDir, name)
	metaPath := filepath.Join(indexDir, name+".meta.json")

	data, err := os.ReadFile(metaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: index %q not found\n", name)
		os.Exit(1)
	}

	var meta gleann.IndexMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		fmt.Println(string(data))
		return
	}

	fmt.Printf("📊 Index: %s\n", meta.Name)
	fmt.Printf("   Backend:    %s\n", meta.Backend)
	fmt.Printf("   Model:      %s\n", meta.EmbeddingModel)
	fmt.Printf("   Dimensions: %d\n", meta.Dimensions)
	fmt.Printf("   Passages:   %d\n", meta.NumPassages)
	fmt.Printf("   Created:    %s\n", meta.CreatedAt.Format(time.RFC3339))
	fmt.Printf("   Updated:    %s\n", meta.UpdatedAt.Format(time.RFC3339))

	// Show file sizes.
	files := []string{".index", ".passages.jsonl", ".passages.idx", ".meta.json"}
	totalSize := int64(0)
	for _, ext := range files {
		path := filepath.Join(indexDir, name+ext)
		info, err := os.Stat(path)
		if err == nil {
			totalSize += info.Size()
			fmt.Printf("   %-25s %s\n", ext+":", formatSize(info.Size()))
		}
	}
	fmt.Printf("   %-25s %s\n", "Total:", formatSize(totalSize))
}

func cmdAsk(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gleann ask <name> <question> [--interactive]")
		os.Exit(1)
	}

	name := args[0]
	question := strings.Join(args[1:], " ")

	// Remove flags from question.
	questionParts := []string{}
	skip := false
	for _, part := range strings.Fields(question) {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(part, "--") {
			if part == "--interactive" || part == "--json" {
				continue
			}
			skip = true
			continue
		}
		questionParts = append(questionParts, part)
	}
	question = strings.Join(questionParts, " ")

	config := getConfig(args)
	interactive := hasFlag(args, "--interactive")

	// Load saved config from ~/.gleann/config.json (TUI setup).
	// CLI flags take precedence over saved values.
	savedCfg := tui.LoadSavedConfig()
	if savedCfg != nil {
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
	}

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
	if err := searcher.Load(ctx, name); err != nil {
		fmt.Fprintf(os.Stderr, "error loading index: %v\n", err)
		os.Exit(1)
	}
	defer searcher.Close()

	chatConfig := gleann.DefaultChatConfig()
	if llmModel := getFlag(args, "--llm-model"); llmModel != "" {
		chatConfig.Model = llmModel
	}
	if llmProvider := getFlag(args, "--llm-provider"); llmProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(llmProvider)
	}

	applyLlamaChatOverride(&chatConfig)
	chat := gleann.NewChat(searcher, chatConfig)

	if interactive {
		fmt.Printf("💬 Interactive mode (index: %s, model: %s)\n", name, chatConfig.Model)
		fmt.Println("   Type 'quit' or 'exit' to stop.")

		scanner := bufio.NewScanner(os.Stdin)
		for {
			fmt.Print("You: ")
			if !scanner.Scan() {
				break
			}
			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}
			if input == "quit" || input == "exit" {
				fmt.Println("Goodbye!")
				break
			}

			answer, err := chat.Ask(ctx, input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			fmt.Printf("\nAssistant: %s\n\n", answer)
		}
	} else {
		answer, err := chat.Ask(ctx, question)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(answer)
	}
}

func cmdWatch(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann watch <name> --docs <dir> [--interval 5]")
		os.Exit(1)
	}

	name := args[0]
	docsDir := getFlag(args, "--docs")
	if docsDir == "" {
		fmt.Fprintln(os.Stderr, "error: --docs flag required")
		os.Exit(1)
	}

	intervalStr := getFlag(args, "--interval")
	interval := 5 * time.Second
	if intervalStr != "" {
		var secs int
		fmt.Sscanf(intervalStr, "%d", &secs)
		if secs > 0 {
			interval = time.Duration(secs) * time.Second
		}
	}

	config := getConfig(args)

	// Load saved config from ~/.gleann/config.json (TUI setup).
	// CLI flags take precedence over saved values.
	savedCfg := tui.LoadSavedConfig()
	if savedCfg != nil {
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
	}

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

	fmt.Printf("👁️  Watching %s for changes via fsnotify (debounce: %s)\n", docsDir, interval)
	fmt.Printf("   Index: %s\n", name)
	fmt.Println("   Press Ctrl+C to stop.")

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Initialize Vault Tracker & Watcher
	tracker, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing vault tracker: %v\n", err)
		os.Exit(1)
	}
	defer tracker.Close()

	watcher, err := vault.NewWatcher(tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing vault watcher: %v\n", err)
		os.Exit(1)
	}
	defer watcher.Close()

	// Initial build.
	buildIndex(name, docsDir, config, embedder, tracker)

	buildRequested := make(chan struct{}, 1)
	watcher.OnChange = func(event fsnotify.Event) {
		select {
		case buildRequested <- struct{}{}:
		default:
		}
	}

	if err := watcher.AddDirectory(docsDir); err != nil {
		fmt.Fprintf(os.Stderr, "error adding watch dir: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher.Start(ctx)

	// Rate-limited rebuilder loop
	for {
		select {
		case <-stop:
			fmt.Println("\nStopping watcher...")
			return
		case <-buildRequested:
			// Wait for debounce interval to coalese changes
			time.Sleep(interval)
			fmt.Printf("🔄 Changes detected by fsnotify, rebuilding index %q...\n", name)
			buildIndex(name, docsDir, config, embedder, tracker)

			// drain any queued up builds during sleep
			select {
			case <-buildRequested:
			default:
			}
		}
	}
}

func buildIndex(name, docsDir string, config gleann.Config, embedder gleann.EmbeddingComputer, tracker *vault.Tracker) {
	items, _, err := readDocuments(docsDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap, tracker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading documents: %v\n", err)
		return
	}
	if len(items) == 0 {
		return
	}

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return
	}

	start := time.Now()
	ctx := context.Background()
	if err := builder.Build(ctx, name, items); err != nil {
		fmt.Fprintf(os.Stderr, "error building: %v\n", err)
		return
	}
	fmt.Printf("✅ Rebuilt %q: %d passages in %s\n", name, len(items), time.Since(start).Round(time.Millisecond))
}

func cmdServe(args []string) {
	config := getConfig(args)

	// Load saved config from ~/.gleann/config.json (TUI setup).
	// CLI flags take precedence over saved values.
	savedCfg := tui.LoadSavedConfig()
	if savedCfg != nil {
		if !hasFlag(args, "--provider") && savedCfg.EmbeddingProvider != "" {
			config.EmbeddingProvider = savedCfg.EmbeddingProvider
		}
		if !hasFlag(args, "--model") && savedCfg.EmbeddingModel != "" {
			config.EmbeddingModel = savedCfg.EmbeddingModel
		}
		if savedCfg.OllamaHost != "" && config.OllamaHost == "" {
			config.OllamaHost = savedCfg.OllamaHost
		}
		if savedCfg.OpenAIKey != "" && config.OpenAIAPIKey == "" {
			config.OpenAIAPIKey = savedCfg.OpenAIKey
		}
		if savedCfg.OpenAIBaseURL != "" && config.OpenAIBaseURL == "" {
			config.OpenAIBaseURL = savedCfg.OpenAIBaseURL
		}
		if !hasFlag(args, "--index-dir") && savedCfg.IndexDir != "" {
			config.IndexDir = savedCfg.IndexDir
		}
	}

	addr := getFlag(args, "--addr")
	if addr == "" {
		addr = gleann.DefaultServerAddr
	}

	// Validate port number.
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid address %q: %v\n", addr, err)
		os.Exit(1)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		fmt.Fprintf(os.Stderr, "error: invalid port number %q\n", portStr)
		os.Exit(1)
	}
	if port < 1024 {
		fmt.Fprintf(os.Stderr, "warning: port %d requires root privileges (did you mean :%d?)\n", port, port+8000)
	}

	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		os.Exit(1)
	}

	srv := server.NewServer(config, addr, version)

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-stop
		fmt.Println("\nShutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Stop(ctx)
	}()

	fmt.Printf("🚀 gleann server starting on %s\n", addr)
	fmt.Printf("   Index dir: %s\n", config.IndexDir)
	fmt.Printf("   Model:     %s (%s)\n", config.EmbeddingModel, config.EmbeddingProvider)
	if config.OllamaHost != "" {
		fmt.Printf("   Host:      %s\n", config.OllamaHost)
	}
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("   GET  /health                    Health check")
	fmt.Println("   GET  /api/indexes               List indexes")
	fmt.Println("   GET  /api/indexes/{name}        Index info")
	fmt.Println("   POST /api/indexes/{name}/search Search")
	fmt.Println("   POST /api/indexes/{name}/build  Build index")
	fmt.Println("   DELETE /api/indexes/{name}      Delete index")
	fmt.Println()
	fmt.Println("   GET  /api/graph/{name}          Graph stats")
	fmt.Println("   POST /api/graph/{name}/query    Graph query (callees, callers, symbols_in_file)")
	fmt.Println("   POST /api/graph/{name}/index    Trigger AST graph indexing")
	fmt.Println()

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// --- MCP Server ---

func cmdMCP() {
	savedCfg := tui.LoadSavedConfig()

	cfg := mcp.Config{
		EmbeddingProvider: DefaultProvider,
		EmbeddingModel:    DefaultEmbeddingModel,
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           version,
	}

	homeDir, _ := os.UserHomeDir()
	cfg.IndexDir = filepath.Join(homeDir, ".gleann", "indexes")

	if savedCfg != nil {
		if savedCfg.EmbeddingProvider != "" {
			cfg.EmbeddingProvider = savedCfg.EmbeddingProvider
		}
		if savedCfg.EmbeddingModel != "" {
			cfg.EmbeddingModel = savedCfg.EmbeddingModel
		}
		if savedCfg.OllamaHost != "" {
			cfg.OllamaHost = savedCfg.OllamaHost
		}
		if savedCfg.OpenAIKey != "" {
			cfg.OpenAIAPIKey = savedCfg.OpenAIKey
		}
		if savedCfg.OpenAIBaseURL != "" {
			cfg.OpenAIBaseURL = savedCfg.OpenAIBaseURL
		}
		if savedCfg.IndexDir != "" {
			cfg.IndexDir = tui.ExpandPath(savedCfg.IndexDir)
		}
	}

	server := mcp.NewServer(cfg)
	server.Run()
}

// --- TUI Commands ---

func cmdChat(args []string) {
	var indexName string
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		indexName = args[0]
	}

	cfg := getConfig(args)
	if cfg.IndexDir == "" {
		cfg.IndexDir = tui.DefaultIndexDir()
	}

	// Load saved TUI config for LLM settings.
	savedCfg := tui.LoadSavedConfig()
	if savedCfg != nil {
		if savedCfg.EmbeddingProvider != "" {
			cfg.EmbeddingProvider = savedCfg.EmbeddingProvider
		}
		if savedCfg.EmbeddingModel != "" {
			cfg.EmbeddingModel = savedCfg.EmbeddingModel
		}
		if savedCfg.OllamaHost != "" {
			cfg.OllamaHost = savedCfg.OllamaHost
		}
	}

	// If no index given, launch index picker.
	if indexName == "" {
		if err := tui.RunChatFlow(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Direct chat with given index.
	if err := initLlamaCPP(context.Background(), &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		os.Exit(1)
	}

	embedder := embedding.NewComputer(embedding.Options{
		Provider:    embedding.Provider(cfg.EmbeddingProvider),
		Model:       cfg.EmbeddingModel,
		BaseURL:     cfg.OllamaHost,
		BatchSize:   cfg.BatchSize,
		Concurrency: cfg.Concurrency,
	})
	searcher := gleann.NewSearcher(cfg, embedder)
	ctx := context.Background()
	fmt.Fprintf(os.Stderr, "Loading index %q...\n", indexName)
	if err := searcher.Load(ctx, indexName); err != nil {
		fmt.Fprintf(os.Stderr, "error loading index %q: %v\n", indexName, err)
		os.Exit(1)
	}
	defer searcher.Close()

	chatCfg := gleann.DefaultChatConfig()
	if savedCfg != nil {
		if savedCfg.LLMProvider != "" {
			chatCfg.Provider = gleann.LLMProvider(savedCfg.LLMProvider)
		}
		if savedCfg.LLMModel != "" {
			chatCfg.Model = savedCfg.LLMModel
		}
		if savedCfg.OllamaHost != "" {
			chatCfg.BaseURL = savedCfg.OllamaHost
		}
	}
	if chatCfg.Provider == gleann.LLMOllama && chatCfg.BaseURL == "" {
		chatCfg.BaseURL = cfg.OllamaHost
	}

	// Override from CLI flags.
	if llmModel := getFlag(args, "--llm-model"); llmModel != "" {
		chatCfg.Model = llmModel
	}
	if llmProvider := getFlag(args, "--llm-provider"); llmProvider != "" {
		chatCfg.Provider = gleann.LLMProvider(llmProvider)
	}

	applyLlamaChatOverride(&chatCfg)
	chat := gleann.NewChat(searcher, chatCfg)

	if sessionFile := getFlag(args, "--session"); sessionFile != "" {
		fmt.Fprintf(os.Stderr, "Loading session from %s...\n", sessionFile)
		if err := chat.LoadSession(sessionFile); err != nil {
			fmt.Fprintf(os.Stderr, "error loading session: %v\n", err)
			os.Exit(1)
		}
	}

	if err := tui.RunChat(chat, indexName, chatCfg.Model); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdTUI() {
	if err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdSetup() {
	for {
		result, openPlugins, err := tui.RunOnboardWithPlugins()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if result == nil {
			fmt.Println("Setup cancelled.")
			return
		}
		if openPlugins {
			// Save config so far, then open plugin manager.
			if result.Completed {
				_ = tui.SaveConfig(*result)
			}
			tui.RunPlugins()
			continue // return to setup after plugins
		}
		if result.Uninstall {
			tui.RunInstall(result)
			return
		}
		if err := tui.SaveConfig(*result); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
		}
		fmt.Println("✅ Configuration saved to ~/.gleann/config.json")
		tui.RunInstall(result)
		return
	}
}

// --- Helpers ---

// PluginDoc holds a plugin result for deferred graph indexing.
// readDocuments collects these during chunking; buildGraphIndex writes them to KuzuDB.
type PluginDoc struct {
	Result     *gleann.PluginResult
	SourcePath string
}

// pluginResultToDoc converts a PluginResult to a StructuredDocument for chunking.
// This is a pure-Go conversion (no CGo/KuzuDB dependency) — the chunking package
// handles all markdown splitting while preserving section hierarchy.
func pluginResultToDoc(result *gleann.PluginResult) *chunking.StructuredDocument {
	var doc chunking.DocumentMeta
	var sections []chunking.MarkdownSection

	for _, node := range result.Nodes {
		switch node.Type {
		case "Document":
			doc.Title, _ = node.Data["title"].(string)
			doc.Format, _ = node.Data["format"].(string)
			doc.Summary, _ = node.Data["summary"].(string)
			if wc, ok := node.Data["word_count"].(float64); ok {
				doc.WordCount = int(wc)
			}
			if pc, ok := node.Data["page_count"].(float64); ok {
				v := int(pc)
				doc.PageCount = &v
			}
		case "Section":
			sec := chunking.MarkdownSection{
				Heading: strVal(node.Data, "heading"),
				Content: strVal(node.Data, "content"),
				Summary: strVal(node.Data, "summary"),
			}
			sec.ID, _ = node.Data["id"].(string)
			if l, ok := node.Data["level"].(float64); ok {
				sec.Level = int(l)
			}
			sections = append(sections, sec)
		}
	}

	// Resolve ParentID from HAS_SUBSECTION edges.
	for _, edge := range result.Edges {
		if edge.Type == "HAS_SUBSECTION" {
			for i := range sections {
				if sections[i].ID == edge.To {
					sections[i].ParentID = edge.From
				}
			}
		}
	}

	return &chunking.StructuredDocument{Document: doc, Sections: sections}
}

// strVal extracts a string value from a map with a zero-value fallback.
func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func readDocuments(dir string, chunkSize, chunkOverlap int, tracker *vault.Tracker) ([]gleann.Item, []*PluginDoc, error) {
	type fileEntry struct {
		path string
		info os.FileInfo
	}

	binaryExts := map[string]bool{
		".pdf": true, ".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true,
		".exe": true, ".bin": true, ".dll": true, ".so": true, ".dylib": true, ".o": true, ".a": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true, ".svg": true, ".webp": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".mkv": true, ".flv": true, ".wav": true, ".flac": true, ".ogg": true,
		".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
		".db": true, ".sqlite": true, ".sqlite3": true,
		".pyc": true, ".class": true, ".jar": true, ".war": true,
		".iso": true, ".img": true, ".dmg": true, ".deb": true, ".rpm": true,
		".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	}

	// Load plugins once and manage their lifecycles
	pluginManager, _ := gleann.NewPluginManager()
	if pluginManager != nil {
		defer pluginManager.Close()
	}

	// Phase 1: collect eligible file paths (serial walk is fast — just syscalls).
	var files []fileEntry
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(path)

		if info.IsDir() {
			if strings.HasPrefix(base, ".") && path != dir {
				return filepath.SkipDir
			}
			if base == "node_modules" || base == "vendor" || base == "dist" || base == "build" || base == ".next" {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(base, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))

		// Check if a plugin can extract this document.
		hasPlugin := false
		if pluginManager != nil && pluginManager.FindDocumentExtractor(ext) != nil {
			hasPlugin = true
		}

		if !hasPlugin && binaryExts[ext] {
			return nil
		}
		if !hasPlugin && info.Size() > 1<<20 { // >1MB (plugins can bypass this if they want to handle big docs)
			return nil
		}

		files = append(files, fileEntry{path: path, info: info})
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}

	if len(files) == 0 {
		return nil, nil, nil
	}

	// Phase 2: parallel read + chunk.
	// Use min(nCPU, 16) workers to avoid overwhelming Ollama with too many
	// simultaneous requests from the pass-through (chunking is CPU-bound).
	nWorkers := runtime.NumCPU()
	if nWorkers > 16 {
		nWorkers = 16
	}
	if nWorkers < 1 {
		nWorkers = 1
	}

	type result struct {
		items []gleann.Item
		err   error
	}

	jobCh := make(chan fileEntry, len(files))
	resCh := make(chan result, len(files))

	// Collected plugin results for deferred graph indexing.
	var pluginDocs []*PluginDoc
	var pluginDocsMu sync.Mutex

	for i := 0; i < nWorkers; i++ {
		go func() {
			// Each worker gets its own splitter instances (they are not thread-safe).
			splitter := chunking.NewSentenceSplitter(chunkSize, chunkOverlap)
			codeSplitter := chunking.NewCodeChunker(chunkSize, chunkOverlap)
			mdChunker := chunking.NewMarkdownChunker(chunkSize, chunkOverlap)

			for fe := range jobCh {
				ext := strings.ToLower(filepath.Ext(fe.path))
				var data []byte
				var err error

				// If a plugin handles this extension, use structured extraction.
				if pluginManager != nil {
					if plugin := pluginManager.FindDocumentExtractor(ext); plugin != nil {
						pResult, perr := pluginManager.ProcessStructured(plugin, fe.path)
						if perr != nil {
							fmt.Fprintf(os.Stderr, "Warning: plugin %s failed to extract %s: %v\n", plugin.Name, filepath.Base(fe.path), perr)
							resCh <- result{err: nil}
							continue
						}

						relPath, _ := filepath.Rel(dir, fe.path)

						// Convert plugin result → StructuredDocument → context-aware chunks.
						doc := pluginResultToDoc(pResult)
						mdChunks := mdChunker.ChunkDocument(doc)

						// Fallback: if structured extraction produced no sections but
						// raw markdown is available (e.g. markitdown backend), use
						// the markdown chunker's heading-based parser instead.
						if len(mdChunks) == 0 && pResult.Markdown != "" {
							mdChunks = mdChunker.ChunkMarkdown(pResult.Markdown, relPath)
						}

						var items []gleann.Item
						for _, ch := range mdChunks {
							ch.Metadata["source"] = relPath
							items = append(items, gleann.Item{
								Text:     ch.Text,
								Metadata: ch.Metadata,
							})
						}

						// Save plugin result for graph indexing (if --graph is active).
						pluginDocsMu.Lock()
						pluginDocs = append(pluginDocs, &PluginDoc{
							Result:     pResult,
							SourcePath: relPath,
						})
						pluginDocsMu.Unlock()

						resCh <- result{items: items}
						continue
					}
				}

				if data == nil {
					data, err = os.ReadFile(fe.path)
					if err != nil {
						resCh <- result{err: nil} // skip unreadable
						continue
					}
				}

				// Skip binary content (null bytes).
				check := data
				if len(check) > 512 {
					check = check[:512]
				}
				if bytes.ContainsRune(check, 0) {
					resCh <- result{}
					continue
				}

				text := string(data)
				if len(strings.TrimSpace(text)) == 0 {
					resCh <- result{}
					continue
				}

				relPath, _ := filepath.Rel(dir, fe.path)
				metadata := map[string]any{"source": relPath}

				if tracker != nil {
					h := sha256.Sum256(data)
					hash := hex.EncodeToString(h[:])
					if err := tracker.UpsertRecord(context.Background(), hash, fe.path, fe.info.ModTime().Unix(), fe.info.Size()); err == nil {
						metadata["hash"] = hash
					}
				}

				var rawChunks []chunking.Chunk
				if chunking.IsCodeFile(fe.path) {
					rawChunks = codeSplitter.ChunkWithMetadata(text, metadata)
				} else {
					rawChunks = splitter.ChunkWithMetadata(text, metadata)
				}

				var chunks []gleann.Item
				for _, rc := range rawChunks {
					chunks = append(chunks, gleann.Item{
						Text:     rc.Text,
						Metadata: rc.Metadata,
					})
				}
				resCh <- result{items: chunks}
			}
		}()
	}

	// Send all files to workers.
	for _, f := range files {
		jobCh <- f
	}
	close(jobCh)

	// Collect results.
	var allItems []gleann.Item
	for range files {
		r := <-resCh
		allItems = append(allItems, r.items...)
	}

	return allItems, pluginDocs, nil
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
