package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tevfik/gleann/internal/chunking"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/server"
	"github.com/tevfik/gleann/pkg/gleann"

	// Register HNSW backend.
	_ "github.com/tevfik/gleann/internal/backend/hnsw"
)

const version = "1.0.0"

func main() {
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
  gleann watch  <name> --docs <dir>    Watch & auto-rebuild on changes
  gleann list                           List all indexes
  gleann remove <name>                  Remove an index
  gleann info   <name>                  Show index info
  gleann serve  [--addr :8080]          Start REST API server
  gleann version                        Show version

Options:
  --model <model>       Embedding model (default: bge-m3)
  --provider <provider> Embedding provider: ollama, openai (default: ollama)
  --top-k <n>           Number of results (default: 10)
  --index-dir <dir>     Index storage directory (default: ~/.gleann/indexes)
  --metric <metric>     Distance metric: l2, cosine, ip (default: l2)
  --json                Output as JSON
  --interactive         Interactive chat mode (ask command)
  --llm-model <model>   LLM model for ask (default: llama3.2)
  --llm-provider <prov> LLM provider: ollama, openai, anthropic (default: ollama)

Examples:
  gleann build my-docs --docs ./documents/
  gleann search my-docs "How does caching work?"
  gleann ask my-docs "Explain the architecture" --interactive
  gleann serve --addr :8080`)
}

func getConfig(args []string) gleann.Config {
	config := gleann.DefaultConfig()

	homeDir, _ := os.UserHomeDir()
	config.IndexDir = filepath.Join(homeDir, ".gleann", "indexes")

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
				config.IndexDir = args[i+1]
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

	config := getConfig(args)

	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(config.EmbeddingProvider),
		Model:    config.EmbeddingModel,
		BaseURL:  config.OllamaHost,
	})

	builder, err := gleann.NewBuilder(config, embedder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Read documents from directory.
	fmt.Printf("📂 Reading documents from %s...\n", docsDir)
	items, err := readDocuments(docsDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap)
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
	fmt.Printf("✅ Index %q built: %d passages in %s\n", name, len(items), elapsed.Round(time.Millisecond))
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

	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(config.EmbeddingProvider),
		Model:    config.EmbeddingModel,
		BaseURL:  config.OllamaHost,
	})

	searcher := gleann.NewSearcher(config, embedder)

	ctx := context.Background()
	if err := searcher.Load(ctx, name); err != nil {
		fmt.Fprintf(os.Stderr, "error loading index: %v\n", err)
		os.Exit(1)
	}
	defer searcher.Close()

	start := time.Now()
	results, err := searcher.Search(ctx, query)
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
		fmt.Fprintln(os.Stderr, "usage: gleann remove <name>")
		os.Exit(1)
	}

	name := args[0]
	config := getConfig(args)

	if err := gleann.RemoveIndex(config.IndexDir, name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("🗑️  Index %q removed.\n", name)
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

	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(config.EmbeddingProvider),
		Model:    config.EmbeddingModel,
		BaseURL:  config.OllamaHost,
	})

	searcher := gleann.NewSearcher(config, embedder)
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

	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(config.EmbeddingProvider),
		Model:    config.EmbeddingModel,
		BaseURL:  config.OllamaHost,
	})

	fmt.Printf("👁️  Watching %s for changes (interval: %s)\n", docsDir, interval)
	fmt.Printf("   Index: %s\n", name)
	fmt.Println("   Press Ctrl+C to stop.")

	// Track file hashes.
	lastHashes := make(map[string][32]byte)

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Initial build.
	buildIndex(name, docsDir, config, embedder)
	lastHashes = computeHashes(docsDir)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			fmt.Println("\nStopping watcher...")
			return
		case <-ticker.C:
			currentHashes := computeHashes(docsDir)
			if hashesChanged(lastHashes, currentHashes) {
				fmt.Printf("🔄 Changes detected, rebuilding index %q...\n", name)
				buildIndex(name, docsDir, config, embedder)
				lastHashes = currentHashes
			}
		}
	}
}

func buildIndex(name, docsDir string, config gleann.Config, embedder gleann.EmbeddingComputer) {
	items, err := readDocuments(docsDir, config.ChunkConfig.ChunkSize, config.ChunkConfig.ChunkOverlap)
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

func computeHashes(dir string) map[string][32]byte {
	hashes := make(map[string][32]byte)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		hashes[path] = sha256.Sum256(data)
		return nil
	})
	return hashes
}

func hashesChanged(old, new map[string][32]byte) bool {
	if len(old) != len(new) {
		return true
	}
	for path, hash := range new {
		if oldHash, ok := old[path]; !ok || oldHash != hash {
			return true
		}
	}
	return false
}

func cmdServe(args []string) {
	config := getConfig(args)
	addr := getFlag(args, "--addr")
	if addr == "" {
		addr = ":8080"
	}

	srv := server.NewServer(config, addr)

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
	fmt.Println()
	fmt.Println("Endpoints:")
	fmt.Println("   GET  /health                    Health check")
	fmt.Println("   GET  /api/indexes               List indexes")
	fmt.Println("   GET  /api/indexes/{name}        Index info")
	fmt.Println("   POST /api/indexes/{name}/search Search")
	fmt.Println("   POST /api/indexes/{name}/build  Build index")
	fmt.Println("   DELETE /api/indexes/{name}       Delete index")
	fmt.Println()

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// --- Helpers ---

func readDocuments(dir string, chunkSize, chunkOverlap int) ([]gleann.Item, error) {
	var items []gleann.Item

	splitter := chunking.NewSentenceSplitter(chunkSize, chunkOverlap)
	codeSplitter := chunking.NewCodeChunker(chunkSize, chunkOverlap)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Skip hidden and binary files.
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip unreadable files.
		}

		text := string(data)
		if len(strings.TrimSpace(text)) == 0 {
			return nil
		}

		relPath, _ := filepath.Rel(dir, path)
		metadata := map[string]any{
			"source": relPath,
		}

		if chunking.IsCodeFile(path) {
			chunks := codeSplitter.ChunkWithMetadata(text, metadata)
			items = append(items, chunks...)
		} else {
			chunks := splitter.ChunkWithMetadata(text, metadata)
			items = append(items, chunks...)
		}

		return nil
	})

	return items, err
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
