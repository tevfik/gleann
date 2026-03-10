package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

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

			fmt.Print("\nAssistant: ")
			err := chat.AskStream(ctx, input, func(token string) {
				fmt.Print(token)
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
				continue
			}
			fmt.Print("\n\n")
		}
	} else {
		fmt.Print("")
		err := chat.AskStream(ctx, question, func(token string) {
			fmt.Print(token)
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println()
	}
}

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
