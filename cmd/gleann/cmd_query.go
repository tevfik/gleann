package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/mattn/go-isatty"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
	"github.com/tevfik/gleann/pkg/roles"
	"github.com/tevfik/gleann/pkg/wordwrap"
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
	tui.PrintSetupHint()
	// If no args provided, show usage.
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann ask [name[,name2,...]] [question] [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Index name is optional. If omitted, will use:")
		fmt.Fprintln(os.Stderr, "  - Index from --continue/--continue-last conversation, or")
		fmt.Fprintln(os.Stderr, "  - Interactive selection if multiple indexes exist, or")
		fmt.Fprintln(os.Stderr, "  - The only index if exactly one exists")
		fmt.Fprintln(os.Stderr, "  - No index (pure LLM, no RAG) if none exist")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fmt.Fprintln(os.Stderr, "  --interactive        Start interactive chat session")
		fmt.Fprintln(os.Stderr, "  --agent              Use ReAct agent with read_full_document tool")
		fmt.Fprintln(os.Stderr, "  --continue ID        Continue a previous conversation")
		fmt.Fprintln(os.Stderr, "  --continue-last      Continue the most recent conversation")
		fmt.Fprintln(os.Stderr, "  --title TITLE        Set conversation title")
		fmt.Fprintln(os.Stderr, "  --role ROLE          Use a named role (e.g. code, shell, explain)")
		fmt.Fprintln(os.Stderr, "  --format FORMAT      Output format: json, markdown, raw")
		fmt.Fprintln(os.Stderr, "  --attach FILE        Attach image/audio file (can repeat; requires multimodal model)")
		fmt.Fprintln(os.Stderr, "  --raw                Output raw text (no formatting); auto-enabled when piped")
		fmt.Fprintln(os.Stderr, "  --quiet              Suppress status messages")
		fmt.Fprintln(os.Stderr, "  --word-wrap N        Wrap output at N columns (default: terminal width)")
		fmt.Fprintln(os.Stderr, "  --no-cache           Do not save conversation")
		fmt.Fprintln(os.Stderr, "  --no-limit           Remove token limit (unlimited output)")
		os.Exit(1)
	}

	config := getConfig(args)
	applySavedConfig(&config, args)

	// Collect all non-flag positional arguments.
	var positional []string
	var attachFiles []string
	flagsWithValue := map[string]bool{
		"--continue": true, "--title": true, "--role": true, "--format": true,
		"--word-wrap": true, "--llm-model": true, "--llm-provider": true,
		"--rerank-model": true, "--top-k": true, "--metric": true,
		"--model": true, "--provider": true, "--host": true,
		"--attach": true,
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--attach" && i+1 < len(args) {
			i++
			attachFiles = append(attachFiles, args[i])
			continue
		}
		if strings.HasPrefix(args[i], "--") {
			if flagsWithValue[args[i]] && i+1 < len(args) {
				i++ // skip flag value
			}
			continue
		}
		positional = append(positional, args[i])
	}

	// Determine index name vs question from positional args.
	// Strategy: check if first positional arg is a known index name.
	var name string
	var questionWords []string

	if len(positional) > 0 {
		candidate := positional[0]
		// Check if candidate matches an existing index.
		isIndex := false
		if indexes, err := gleann.ListIndexes(config.IndexDir); err == nil {
			for _, idx := range indexes {
				if idx.Name == candidate {
					isIndex = true
					break
				}
				// Also check comma-separated multi-index (e.g. "code,docs").
				for _, part := range strings.Split(candidate, ",") {
					if idx.Name == part {
						isIndex = true
						break
					}
				}
			}
		}

		if isIndex {
			name = candidate
			questionWords = positional[1:]
		} else {
			// First arg is not an index → treat all positional as question.
			questionWords = positional
		}
	}

	question := strings.Join(questionWords, " ")

	// Stdin/pipe support: if stdin is piped, read it and prepend to question.
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		piped, err := io.ReadAll(os.Stdin)
		if err == nil && len(piped) > 0 {
			pipeText := strings.TrimSpace(string(piped))
			if question == "" {
				question = pipeText
			} else {
				question = pipeText + "\n\n" + question
			}
		}
	}

	if question == "" && !hasFlag(args, "--interactive") && !hasFlag(args, "--continue") && !hasFlag(args, "--continue-last") {
		fmt.Fprintln(os.Stderr, "error: no question provided")
		os.Exit(1)
	}

	// Raw mode: explicit --raw flag or auto-detect when stdout is piped.
	rawMode := hasFlag(args, "--raw") || !isOutputTTY()
	quiet := hasFlag(args, "--quiet") || rawMode
	noCache := hasFlag(args, "--no-cache")
	noLimit := hasFlag(args, "--no-limit")

	interactive := hasFlag(args, "--interactive")
	agentMode := hasFlag(args, "--agent")
	useGraph := hasFlag(args, "--graph")

	// Resolve index name if not provided explicitly.
	convStore := conversations.DefaultStore()
	if name == "" {
		// Option 1: Load from --continue or --continue-last.
		if contID := getFlag(args, "--continue"); contID != "" {
			conv, err := convStore.Load(contID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error loading conversation: %v\n", err)
				os.Exit(1)
			}
			if len(conv.Indexes) > 0 {
				name = strings.Join(conv.Indexes, ",")
			}
		} else if hasFlag(args, "--continue-last") {
			conv, err := convStore.Latest()
			if err == nil && conv != nil && len(conv.Indexes) > 0 {
				name = strings.Join(conv.Indexes, ",")
			}
		}

		// Option 2: If still no index, list available indexes and pick.
		if name == "" {
			indexes, err := gleann.ListIndexes(config.IndexDir)
			if err == nil && len(indexes) == 1 {
				// Single index → auto-select.
				name = indexes[0].Name
				if !quiet {
					fmt.Fprintf(os.Stderr, "Using index: %s\n", name)
				}
			} else if err == nil && len(indexes) > 1 {
				// Multiple indexes → interactive selection.
				fmt.Fprintln(os.Stderr, "Available indexes:")
				for i, idx := range indexes {
					fmt.Fprintf(os.Stderr, "  %d. %s (%d passages, %s)\n", i+1, idx.Name, idx.NumPassages, idx.Backend)
				}
				fmt.Fprint(os.Stderr, "\nSelect index (1-", len(indexes), "): ")
				var choice int
				_, err := fmt.Scanf("%d", &choice)
				if err != nil || choice < 1 || choice > len(indexes) {
					fmt.Fprintln(os.Stderr, "error: invalid selection")
					os.Exit(1)
				}
				name = indexes[choice-1].Name
			}
			// No indexes → will run in pure LLM mode (no RAG context).
		}
	}

	if err := initLlamaCPP(context.Background(), &config); err != nil {
		fmt.Fprintf(os.Stderr, "error initializing llamacpp: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Create searcher: with index (RAG) or without (pure LLM).
	var searcher gleann.Searcher
	if name == "" {
		// Pure LLM mode — no RAG context.
		searcher = gleann.NullSearcher{}
		if !quiet {
			fmt.Fprintln(os.Stderr, "No index selected — running in pure LLM mode (no RAG context).")
		}
	} else {
		embedder := embedding.NewComputer(embedding.Options{
			Provider:    embedding.Provider(config.EmbeddingProvider),
			Model:       config.EmbeddingModel,
			BaseURL:     config.OllamaHost,
			BatchSize:   config.BatchSize,
			Concurrency: config.Concurrency,
		})

		// Parse index names (comma-separated for multi-index).
		indexNames := strings.Split(name, ",")

		if len(indexNames) == 1 {
			s := gleann.NewSearcher(config, embedder)
			if err := s.Load(ctx, indexNames[0]); err != nil {
				fmt.Fprintf(os.Stderr, "error loading index: %v\n", err)
				os.Exit(1)
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
				s.SetReranker(gleann.NewReranker(rerankerCfg))
				config.SearchConfig.UseReranker = true
			}
			// If --graph is set, enable graph context enrichment.
			if useGraph {
				config.SearchConfig.UseGraphContext = true
			}
			searcher = s
		} else {
			ms, err := gleann.LoadMultiSearcher(ctx, config, embedder, indexNames)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error loading indexes: %v\n", err)
				os.Exit(1)
			}
			searcher = ms
		}
	}
	defer searcher.Close()

	// Build chat config.
	chatConfig := gleann.DefaultChatConfig()
	// Apply saved config LLM settings.
	if config.LLMProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(config.LLMProvider)
	}
	if config.LLMModel != "" {
		chatConfig.Model = config.LLMModel
	}
	if config.OllamaHost != "" {
		chatConfig.BaseURL = config.OllamaHost
	}
	if config.OpenAIAPIKey != "" {
		chatConfig.APIKey = config.OpenAIAPIKey
	}
	if config.OpenAIBaseURL != "" && chatConfig.Provider == gleann.LLMOpenAI {
		chatConfig.BaseURL = config.OpenAIBaseURL
	}
	// CLI flags override saved config.
	if llmModel := getFlag(args, "--llm-model"); llmModel != "" {
		chatConfig.Model = llmModel
	}
	if llmProvider := getFlag(args, "--llm-provider"); llmProvider != "" {
		chatConfig.Provider = gleann.LLMProvider(llmProvider)
	}

	// Apply role system prompt.
	// Check saved config roles first, then fall back to built-in registry.
	savedCfg := tui.LoadSavedConfig()
	if roleName := getFlag(args, "--role"); roleName != "" {
		var prompt string
		if savedCfg != nil && savedCfg.Roles != nil {
			if lines, ok := savedCfg.Roles[roleName]; ok {
				prompt = strings.Join(lines, "\n")
			}
		}
		if prompt == "" {
			reg := roles.DefaultRegistry()
			p, err := reg.SystemPrompt(roleName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			prompt = p
		}
		chatConfig.SystemPrompt = prompt
	}

	// Apply format system message.
	// Check config format_text first, then fall back to built-in messages.
	if format := getFlag(args, "--format"); format != "" {
		formatKey := strings.ToLower(format)
		formatMsg := ""
		if savedCfg != nil && savedCfg.FormatText != nil {
			if msg, ok := savedCfg.FormatText[formatKey]; ok {
				formatMsg = msg
			}
		}
		if formatMsg == "" {
			switch formatKey {
			case "json":
				formatMsg = "Respond ONLY with valid JSON. No markdown, no explanation."
			case "markdown", "md":
				formatMsg = "Format your response as well-structured Markdown."
			case "raw", "text":
				formatMsg = "Respond in plain text with no formatting."
			default:
				formatMsg = fmt.Sprintf("Respond in %s format.", format)
			}
		}
		chatConfig.SystemPrompt = chatConfig.SystemPrompt + "\n\n" + formatMsg
	}

	applyLlamaChatOverride(&chatConfig)
	chat := gleann.NewChat(searcher, chatConfig)

	// Inject memory context.
	if memMgr, err := memory.DefaultManager(); err == nil {
		defer memMgr.Close()
		if cw, err := memMgr.BuildContext(); err == nil {
			if rendered := cw.Render(); rendered != "" {
				chat.SetMemoryContext(rendered)
			}
		}
	}

	// Apply --no-limit: remove token limit for unlimited output.
	if noLimit {
		chat.SetMaxTokens(0)
	}

	// Conversation management: --continue or --continue-last.
	// Note: convStore already initialized above when resolving index name.
	var activeConv *conversations.Conversation

	if contID := getFlag(args, "--continue"); contID != "" {
		conv, err := convStore.Load(contID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading conversation: %v\n", err)
			os.Exit(1)
		}
		activeConv = conv
		// Restore chat history from conversation.
		for _, m := range conv.Messages {
			chat.AppendHistory(gleann.ChatMessage{Role: m.Role, Content: m.Content})
		}
		stderrf(args, "📎 Continuing conversation %s (%q)\n", conversations.ShortID(conv.ID), conv.Title)
	} else if hasFlag(args, "--continue-last") {
		conv, err := convStore.Latest()
		if err != nil || conv == nil {
			fmt.Fprintln(os.Stderr, "no previous conversation found")
			os.Exit(1)
		}
		activeConv = conv
		for _, m := range conv.Messages {
			chat.AppendHistory(gleann.ChatMessage{Role: m.Role, Content: m.Content})
		}
		stderrf(args, "📎 Continuing conversation %s (%q)\n", conversations.ShortID(conv.ID), conv.Title)
	}

	// Save conversation helper.
	indexNames := strings.Split(name, ",")
	saveConversation := func() {
		if noCache {
			return // skip saving when --no-cache
		}
		if activeConv == nil {
			activeConv = &conversations.Conversation{
				Indexes: indexNames,
				Model:   chatConfig.Model,
			}
		}
		if title := getFlag(args, "--title"); title != "" {
			activeConv.Title = title
		}
		// Sync messages from chat history.
		activeConv.Messages = nil
		for _, m := range chat.History() {
			activeConv.Messages = append(activeConv.Messages, conversations.Message{
				Role: m.Role, Content: m.Content,
			})
		}

		// Auto-summarize title via LLM if no explicit title was set and conversation is new.
		if activeConv.Title == "" || activeConv.Title == autoTitleFallback(activeConv) {
			summarizer := buildSummarizer(chatConfig, config)
			if summarizer != nil {
				title := summarizer.SummarizeTitle(ctx, activeConv)
				if title != "" {
					activeConv.Title = title
				}
			}
		}

		if err := convStore.Save(activeConv); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to save conversation: %v\n", err)
		}
	}

	_ = quiet // used below

	// Word-wrap: parse --word-wrap N or use terminal width from saved config.
	wrapWidth := 0
	if ww := getFlag(args, "--word-wrap"); ww != "" {
		fmt.Sscanf(ww, "%d", &wrapWidth)
	} else {
		savedCfg2 := tui.LoadSavedConfig()
		if savedCfg2 != nil && savedCfg2.WordWrap > 0 {
			wrapWidth = savedCfg2.WordWrap
		}
	}
	// Create stream writer with word-wrap support.
	// In raw mode: stream directly to stdout.
	// In TTY mode: buffer output for markdown rendering after stream completes.
	var responseBuffer strings.Builder
	wrapper := wordwrap.NewStreamWriter(wrapWidth, func(s string) {
		if rawMode {
			fmt.Print(s)
		} else {
			responseBuffer.WriteString(s)
		}
	})

	// renderMarkdown renders the buffered response with glamour (terminal markdown).
	renderMarkdown := func() {
		content := responseBuffer.String()
		if content == "" {
			return
		}
		rendered, err := glamour.Render(content, "dark")
		if err != nil {
			// Fallback to plain output on render error.
			fmt.Print(content)
		} else {
			fmt.Print(rendered)
		}
		responseBuffer.Reset()
	}

	if interactive {
		stderrf(args, "💬 Interactive mode (index: %s, model: %s)\n", name, chatConfig.Model)
		stderrf(args, "   Type 'quit' or 'exit' to stop.\n")

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
				saveConversation()
				fmt.Println("Goodbye!")
				break
			}

			fmt.Print("\nAssistant: ")
			err := chat.AskStream(ctx, input, func(token string) {
				wrapper.Write(token)
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
				continue
			}
			if !rawMode {
				wrapper.Flush()
				renderMarkdown()
			}
			fmt.Print("\n\n")
		}
	} else if agentMode {
		// Agent mode: wire up the ReAct agent with read_full_document tool.
		var tools []gleann.ReActTool
		tools = append(tools, gleann.NewSearchTool(nil)) // search is baked into the chat
		if ls, ok := searcher.(*gleann.LeannSearcher); ok {
			tools = append(tools, gleann.NewReadDocumentTool(ls))
		}
		agent := gleann.NewReActAgent(chat, tools, 6)
		fmt.Fprintf(os.Stderr, "🤖 Agent mode (index: %s, model: %s)\n", name, chatConfig.Model)
		answer, steps, err := agent.Run(ctx, question)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
			os.Exit(1)
		}
		for i, s := range steps {
			fmt.Fprintf(os.Stderr, "  Step %d: Thought=%q Action=%q\n", i+1, s.Thought, s.Action)
		}
		fmt.Println(answer)
		saveConversation()
	} else {
		fmt.Print("")
		var err error
		if len(attachFiles) > 0 {
			err = chat.AskStreamWithMedia(ctx, question, attachFiles, func(token string) {
				wrapper.Write(token)
			})
		} else {
			err = chat.AskStream(ctx, question, func(token string) {
				wrapper.Write(token)
			})
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !rawMode {
			wrapper.Flush()
			renderMarkdown()
		}
		fmt.Println()
		saveConversation()
	}
}

func cmdChat(args []string) {
	// Conversation management flags route to the conversation handler.
	if hasFlag(args, "--list") || hasFlag(args, "--pick") || hasFlag(args, "--show-last") ||
		getFlag(args, "--show") != "" || len(getDeleteArgs(args)) > 0 ||
		getFlag(args, "--delete-older-than") != "" {
		cmdConversations(args)
		return
	}

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

	// Direct chat with given index (supports comma-separated multi-index).
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

	ctx := context.Background()

	// Parse index names (comma-separated for multi-index).
	indexNames := strings.Split(indexName, ",")
	displayName := indexName // keep original for display

	var searcher gleann.Searcher
	if len(indexNames) == 1 {
		s := gleann.NewSearcher(cfg, embedder)
		fmt.Fprintf(os.Stderr, "Loading index %q...\n", indexNames[0])
		if err := s.Load(ctx, indexNames[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error loading index %q: %v\n", indexNames[0], err)
			os.Exit(1)
		}
		searcher = s
	} else {
		fmt.Fprintf(os.Stderr, "Loading %d indexes: %s...\n", len(indexNames), indexName)
		ms, err := gleann.LoadMultiSearcher(ctx, cfg, embedder, indexNames)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading indexes: %v\n", err)
			os.Exit(1)
		}
		searcher = ms
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

	if err := tui.RunChat(chat, displayName, chatCfg.Model); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// autoTitleFallback returns what autoTitle would produce — used to detect if
// a title was auto-generated (and thus should be replaced by LLM summary).
func autoTitleFallback(conv *conversations.Conversation) string {
	for _, m := range conv.Messages {
		if m.Role == "user" {
			t := m.Content
			if len(t) > 60 {
				t = t[:57] + "..."
			}
			return t
		}
	}
	return "untitled"
}

// buildSummarizer creates a Summarizer from the current LLM config.
// Returns nil if no LLM config is usable.
func buildSummarizer(chatCfg gleann.ChatConfig, config gleann.Config) *conversations.Summarizer {
	model := chatCfg.Model
	if model == "" {
		return nil
	}

	return &conversations.Summarizer{
		Provider: string(chatCfg.Provider),
		Model:    model,
		BaseURL:  chatCfg.BaseURL,
		APIKey:   chatCfg.APIKey,
	}
}
