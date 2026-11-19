package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/memsync"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

func cmdMemory(args []string) {
	if len(args) == 0 {
		printMemoryUsage()
		return // exit 0: showing help is not an error
	}

	sub := args[0]
	subArgs := args[1:]
	if fs, ok := memoryFlagSets[sub]; ok {
		rejectUnknownFlags("memory "+sub, subArgs, fs)
	}

	switch sub {
	case "help", "--help", "-h":
		printMemoryUsage()
	case "remember":
		cmdMemoryRemember(subArgs)
	case "forget":
		cmdMemoryForget(subArgs)
	case "list":
		cmdMemoryList(subArgs)
	case "search":
		cmdMemorySearch(subArgs)
	case "clear":
		cmdMemoryClear(subArgs)
	case "add":
		cmdMemoryAdd(subArgs)
	case "stats":
		cmdMemoryStats()
	case "summarize":
		cmdMemorySummarize(subArgs)
	case "prune":
		cmdMemoryPrune(subArgs)
	case "compact":
		cmdMemoryCompact(subArgs)
	case "context":
		cmdMemoryContext(subArgs)
	case "maintain":
		cmdMemoryMaintain()
	case "reflect":
		cmdMemoryReflect(subArgs)
	case "export":
		cmdMemoryExport(subArgs)
	case "import":
		cmdMemoryImport(subArgs)
	case "import-graph":
		cmdMemoryImportGraph(subArgs)
	case "failure":
		cmdMemoryFailure(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown memory subcommand: %s\n", sub)
		printMemoryUsage()
		exitFunc(1)
	}
}

func printMemoryUsage() {
	fmt.Println(`gleann memory — Hierarchical long-term memory for agents and conversations

gleann memory stores knowledge across three tiers that persist between sessions.
All stored memory is automatically injected into every LLM query (ask, chat, mcp).

Usage:
  gleann memory remember <text>                  Store fact in long-term memory
  gleann memory forget   <query-or-id>           Remove memory by content match or ID
  gleann memory list     [--tier <tier>]          List memories (all tiers or specific)
  gleann memory search   <query>                 Full-text search across all tiers
  gleann memory add      <tier> <text>            Add note to specific tier
  gleann memory clear    [--tier <tier>]          Delete memories (tier or all)
  gleann memory stats                             Storage statistics
  gleann memory summarize --last                 Summarize last conversation → memory
  gleann memory summarize --id <conv-id>          Summarize specific conversation
  gleann memory summarize --last --extract        Also extract individual facts
  gleann memory reflect  [--dry-run]              Run sleep-time reflection cycle
  gleann memory reflect  --revert <run-id>        Revert a reflection run change set
  gleann memory prune    [--age <duration>]       Remove old entries
  gleann memory maintain                          Full maintenance pass
  gleann memory context                           Show compiled context sent to LLM
  gleann memory context --path <p> --command <c>  Show context + failures matching files/command
  gleann memory failure add --command <c> --error <text> --fix <text> [--path <p>]... [--symbol <s>]...
  gleann memory failure list                       List recorded failures
  gleann memory export   --dir <dir>             Export memory blocks and summaries to markdown
  gleann memory import   --dir <dir>             Import memory blocks and summaries from markdown
  gleann memory import-graph <file.json|dir>     Import entities exported from a v1.9 Kùzu store (kuzu-memory-export)

Memory Tiers:
  short    In-memory, session-scoped
           → auto-promoted to medium when chat session ends
  medium   BBolt, conversation digests, daily summaries
           → auto-archived to long after 30 days
  long     BBolt, permanent facts, user preferences
           → never automatically deleted

Options:
  --dir <dir>             Directory path for memory export/import
  --tier <tier>           Filter by tier: short | medium | long
  --tag <tags>            Comma-separated tags (e.g. "preference,project")
  --label <label>         Semantic label (default: "note" or "user_memory")
  --age <duration>        Duration for prune (e.g. 30d, 90d, 2w, 24h)
  --json                  Output as JSON (memory context includes the budget report)
  --verbose               Print the injection budget report to stderr (memory context)
  --extract               Also extract individual facts (with summarize)
  --dry-run               Preview reflection change set without writing
  --revert <run-id>       Revert reflection run by ID

Injection budget:
  Injected memory is capped at 4000 tokens by default. GLEANN_MEMORY_MAX_TOKENS
  changes the cap (0 = unlimited). Over budget, short-term blocks are dropped
  first, then medium, then long; within a tier the older and less-accessed
  block goes first. Blocks are never cut in half.

Chat Slash Commands (inside 'gleann chat'):
  /remember <text>        Store fact to long-term memory
  /forget <query>         Remove memories matching query
  /memories               Browse stored memories
  /new                    Start a fresh conversation thread (clears context)

Auto-injection:
  Memory context is compiled and injected as a system message before every
  LLM query. This gives agents persistent knowledge across sessions.
  Use 'gleann memory context' to inspect what the LLM currently receives.

Examples:
  gleann memory remember "Project uses hexagonal architecture"
  gleann memory remember "Prefer snake_case for DB columns" --tag "preference"
  gleann memory remember "Auth service owner: Alice" --tag "team" --label "contact"
  gleann memory add short "Current task: refactoring auth module"
  gleann memory add medium "Sprint 12 focus: performance improvements"
  gleann memory search "architecture"
  gleann memory search "snake_case" --json
  gleann memory list --tier long
  gleann memory forget "snake_case"
  gleann memory forget abc123ef
  gleann memory summarize --last
  gleann memory summarize --last --extract
  gleann memory summarize --id abc12345
  gleann memory prune --age 90d
  gleann memory stats
  gleann memory context
  gleann memory maintain
  gleann memory export --dir ./backup
  gleann memory import --dir ./backup

Related Commands:
  gleann chat --list                  Browse conversation history
  gleann chat --delete-older-than 30d Clean up old conversations
  gleann serve                        REST API also exposes memory engine endpoints
  gleann mcp                          MCP server exposes inject_knowledge_graph tool`)
}

func openMemoryManager() *memory.Manager {
	mgr, err := memory.DefaultManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening memory store: %v\n", err)
		if strings.Contains(err.Error(), "timeout") {
			fmt.Fprintln(os.Stderr, "hint: another process is holding the memory database lock.")
			fmt.Fprintln(os.Stderr, "      Common cause: 'gleann serve' is running. Most memory subcommands")
			fmt.Fprintln(os.Stderr, "      auto-route through the running server's REST API; if you see this")
			fmt.Fprintln(os.Stderr, "      message, check it is reachable: curl http://localhost:8080/health")
			fmt.Fprintln(os.Stderr, "      (override the discovery URL with GLEANN_REMOTE_ADDR=<url>, or set")
			fmt.Fprintln(os.Stderr, "      GLEANN_REMOTE_ADDR=off to force direct bbolt access).")
		}
		exitFunc(1)
	}
	return mgr
}

func cmdMemoryRemember(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory remember <text> [--tag <tags>] [--label <label>]")
		exitFunc(1)
	}

	var tags []string
	label := ""

	// Parse content (everything that's not a flag).
	var contentParts []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			contentParts = append(contentParts, args[i+1:]...)
			break
		}
		switch args[i] {
		case "--tag":
			if i+1 < len(args) {
				i++
				tags = strings.Split(args[i], ",")
			}
		case "--label":
			if i+1 < len(args) {
				i++
				label = args[i]
			}
		default:
			contentParts = append(contentParts, args[i])
		}
	}

	content := strings.Join(contentParts, " ")
	if content == "" {
		fmt.Fprintln(os.Stderr, "error: no content provided")
		exitFunc(1)
	}

	// Prefer running server (avoids bbolt lock collision with `gleann serve`).
	if rc := remoteMemoryClient(); rc != nil {
		if label == "" {
			label = "note"
		}
		block, err := rc.Add(memory.TierLong, label, content, tags)
		if err == nil {
			fmt.Printf("✅ Remembered [%s]: %s  (via running server)\n", block.ID[:8], truncateStr(content, 80))
			return
		}
		// Fallback to local store if remote call failed
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	block, err := mgr.Remember(content, tags...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	if label != "" {
		// Update label.
		b, _ := mgr.Store().Get(block.ID)
		if b != nil {
			b.Label = label
			b.UpdatedAt = time.Now()
			_ = mgr.Store().Update(b)
		}
	}

	fmt.Printf("✅ Remembered [%s]: %s\n", block.ID[:8], truncateStr(content, 80))
}

func cmdMemoryForget(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory forget <id-or-query>")
		exitFunc(1)
	}

	query := freeText(args, memoryFlagSets["forget"])
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann memory forget <id-or-query>")
		exitFunc(1)
	}

	if rc := remoteMemoryClient(); rc != nil {
		count, err := rc.Forget(query)
		if err == nil {
			fmt.Printf("🗑  Forgot %d memory block(s) matching %q  (via running server)\n", count, query)
			return
		}
		// Fallback to local store if remote call failed
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	count, err := mgr.Forget(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	fmt.Printf("🗑  Forgot %d memory block(s) matching %q\n", count, query)
}

func cmdMemoryList(args []string) {
	tier := memory.Tier("")
	asJSON := hasFlag(args, "--json")

	if tierStr := getFlag(args, "--tier"); tierStr != "" {
		t, err := memory.ParseTier(tierStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		tier = t
	}

	var blocks []memory.Block
	var err error
	var remoteOk bool
	if rc := remoteMemoryClient(); rc != nil {
		blocks, err = rc.List(tier)
		if err == nil {
			remoteOk = true
		}
	}
	if !remoteOk {
		mgr := openMemoryManager()
		defer mgr.Close()
		blocks, err = mgr.List(tier)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	if asJSON {
		data, _ := json.MarshalIndent(blocks, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(blocks) == 0 {
		if tier != "" {
			fmt.Printf("No memories in tier %q.\n", tier)
		} else {
			fmt.Println("No memories stored.")
		}
		return
	}

	tierLabel := "all tiers"
	if tier != "" {
		tierLabel = string(tier)
	}
	fmt.Printf("🧠 Memories (%d, %s):\n\n", len(blocks), tierLabel)

	for _, b := range blocks {
		age := time.Since(b.CreatedAt).Round(time.Second)
		tags := ""
		if len(b.Tags) > 0 {
			tags = " [" + strings.Join(b.Tags, ",") + "]"
		}
		fmt.Printf("  %s  %-8s %-12s %s%s  (%s ago)\n",
			b.ID[:8], b.Tier, b.Label, truncateStr(b.Content, 50), tags, formatMemAge(age))
	}
}

func cmdMemorySearch(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory search <query>")
		exitFunc(1)
	}

	query := freeText(args, memoryFlagSets["search"])
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann memory search <query>")
		exitFunc(1)
	}
	asJSON := hasFlag(args, "--json")

	var blocks []memory.Block
	var err error
	var remoteOk bool
	if rc := remoteMemoryClient(); rc != nil {
		blocks, err = rc.Search(query)
		if err == nil {
			remoteOk = true
		}
	}
	if !remoteOk {
		mgr := openMemoryManager()
		defer mgr.Close()
		blocks, err = mgr.Search(query)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	if asJSON {
		data, _ := json.MarshalIndent(blocks, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(blocks) == 0 {
		fmt.Printf("No memories matching %q.\n", query)
		return
	}

	fmt.Printf("🔍 Found %d memory block(s) matching %q:\n\n", len(blocks), query)

	for _, b := range blocks {
		age := time.Since(b.CreatedAt).Round(time.Second)
		fmt.Printf("  %s  %-8s %-12s %s  (%s ago)\n",
			b.ID[:8], b.Tier, b.Label, truncateStr(b.Content, 60), formatMemAge(age))
	}
}

func cmdMemoryClear(args []string) {
	var tier memory.Tier
	if tierStr := getFlag(args, "--tier"); tierStr != "" {
		t, err := memory.ParseTier(tierStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		tier = t
	}

	if rc := remoteMemoryClient(); rc != nil {
		count, err := rc.Clear(tier)
		if err == nil {
			if tier != "" {
				fmt.Printf("🗑  Cleared %d memory block(s) from tier %q  (via running server)\n", count, tier)
			} else {
				fmt.Printf("🗑  Cleared %d memory block(s) from all tiers  (via running server)\n", count)
			}
			return
		}
		// Fallback to local store if remote call failed
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	if tier != "" {
		count, err := mgr.Clear(tier)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		fmt.Printf("🗑  Cleared %d memory block(s) from tier %q\n", count, tier)
	} else {
		count, err := mgr.ClearAll()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		fmt.Printf("🗑  Cleared %d memory block(s) from all tiers\n", count)
	}
}

func cmdMemoryAdd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory add <tier> <text> [--tag <tags>] [--label <label>]")
		exitFunc(1)
	}

	tier, err := memory.ParseTier(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	remaining := args[1:]
	var tags []string
	label := "note"

	var contentParts []string
	for i := 0; i < len(remaining); i++ {
		if remaining[i] == "--" {
			contentParts = append(contentParts, remaining[i+1:]...)
			break
		}
		switch remaining[i] {
		case "--tag":
			if i+1 < len(remaining) {
				i++
				tags = strings.Split(remaining[i], ",")
			}
		case "--label":
			if i+1 < len(remaining) {
				i++
				label = remaining[i]
			}
		default:
			contentParts = append(contentParts, remaining[i])
		}
	}

	content := strings.Join(contentParts, " ")
	if content == "" {
		fmt.Fprintln(os.Stderr, "error: no content provided")
		exitFunc(1)
	}

	if rc := remoteMemoryClient(); rc != nil {
		block, err := rc.Add(tier, label, content, tags)
		if err == nil {
			fmt.Printf("✅ Added to %s [%s]: %s  (via running server)\n", tier, block.ID[:8], truncateStr(content, 80))
			return
		}
		// Fallback to local store if remote call failed
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	block, err := mgr.AddNote(tier, label, content, tags...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	fmt.Printf("✅ Added to %s [%s]: %s\n", tier, block.ID[:8], truncateStr(content, 80))
}

func cmdMemoryStats() {
	if rc := remoteMemoryClient(); rc != nil {
		stats, err := rc.Stats()
		if err == nil {
			fmt.Println("🧠 Memory Statistics (via running server):")
			fmt.Printf("  Short-term:  %d blocks (in-memory)\n", stats.ShortTermCount)
			fmt.Printf("  Medium-term: %d blocks (BBolt)\n", stats.MediumTermCount)
			fmt.Printf("  Long-term:   %d blocks (BBolt)\n", stats.LongTermCount)
			fmt.Printf("  Total:       %d blocks\n", stats.TotalCount)
			fmt.Printf("  Disk size:   %s\n", formatMemSize(stats.DiskSizeBytes))
			return
		}
		// Fallback to local store if remote call failed
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	stats, err := mgr.Stats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	fmt.Println("🧠 Memory Statistics:")
	fmt.Printf("  Short-term:  %d blocks (in-memory)\n", stats.ShortTermCount)
	fmt.Printf("  Medium-term: %d blocks (BBolt)\n", stats.MediumTermCount)
	fmt.Printf("  Long-term:   %d blocks (BBolt)\n", stats.LongTermCount)
	fmt.Printf("  Total:       %d blocks\n", stats.TotalCount)
	fmt.Printf("  Disk size:   %s\n", formatMemSize(stats.DiskSizeBytes))

	// Show summaries count.
	summaries, err := mgr.Store().ListSummaries()
	if err == nil {
		fmt.Printf("  Summaries:   %d\n", len(summaries))
	}

	fmt.Printf("  Store path:  %s\n", mgr.Store().Path())
}

func cmdMemorySummarize(args []string) {
	convStore := conversations.DefaultStore()

	var conv *conversations.Conversation
	var err error

	if hasFlag(args, "--last") {
		conv, err = convStore.Latest()
		if err != nil || conv == nil {
			fmt.Fprintln(os.Stderr, "no saved conversations")
			exitFunc(1)
		}
	} else if id := getFlag(args, "--id"); id != "" {
		conv, err = convStore.Load(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading conversation: %v\n", err)
			exitFunc(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "usage: gleann memory summarize [--last | --id <ID>]")
		exitFunc(1)
	}

	// Build summarizer config from saved config.
	sumCfg := defaultSummarizerConfig()

	summarizer := memory.NewSummarizer(sumCfg)

	fmt.Fprintf(os.Stderr, "Summarizing conversation %q...\n", conv.Title)

	ctx := context.Background()
	summary, err := summarizer.SummarizeConversation(ctx, conv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	if err := mgr.Store().SaveSummary(summary); err != nil {
		fmt.Fprintf(os.Stderr, "error saving summary: %v\n", err)
		exitFunc(1)
	}

	fmt.Printf("✅ Summary saved for %q:\n   %s\n", summary.Title, summary.Content)

	// Extract memories if requested.
	if hasFlag(args, "--extract") {
		fmt.Fprintf(os.Stderr, "Extracting memories...\n")
		blocks, err := summarizer.ExtractMemories(ctx, conv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not extract memories: %v\n", err)
			return
		}
		if len(blocks) == 0 {
			fmt.Println("   No important facts extracted.")
			return
		}
		for _, b := range blocks {
			if err := mgr.Store().Add(&b); err == nil {
				fmt.Printf("   📝 [%s] %s\n", b.Label, truncateStr(b.Content, 70))
			}
		}
	}
}

func defaultSummarizerConfig() memory.SummarizerConfig {
	savedCfg := tui.LoadSavedConfig()
	sumCfg := memory.SummarizerConfig{
		Provider: "ollama",
		Model:    DefaultLLMModel,
		BaseURL:  "http://localhost:11434",
	}
	if envHost := os.Getenv("OLLAMA_HOST"); envHost != "" {
		sumCfg.BaseURL = envHost
	}
	if savedCfg != nil {
		if savedCfg.LLMProvider != "" {
			sumCfg.Provider = savedCfg.LLMProvider
		}
		if savedCfg.LLMModel != "" {
			sumCfg.Model = savedCfg.LLMModel
		}
		// Each provider gets only its own endpoint and key: openai uses
		// openai_base_url (empty fails in the summarizer, never OpenAI's
		// cloud by default), anthropic its default endpoint and key.
		switch sumCfg.Provider {
		case "ollama":
			if savedCfg.OllamaHost != "" && os.Getenv("OLLAMA_HOST") == "" {
				sumCfg.BaseURL = savedCfg.OllamaHost
			}
			sumCfg.APIKey = gleann.ResolveOllamaAPIKey(savedCfg.OllamaAPIKey)
		case "openai":
			sumCfg.BaseURL = savedCfg.OpenAIBaseURL
			sumCfg.APIKey = savedCfg.OpenAIKey
		case "anthropic":
			sumCfg.BaseURL = ""
			sumCfg.APIKey = savedCfg.AnthropicKey
		default:
			sumCfg.APIKey = ""
		}
	}
	return sumCfg
}

func cmdMemoryReflect(args []string) {
	dryRun := hasFlag(args, "--dry-run")
	revertID := getFlag(args, "--revert")

	if dryRun && revertID != "" {
		fmt.Fprintln(os.Stderr, "error: cannot use --dry-run and --revert together")
		exitFunc(1)
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	if revertID != "" {
		cs, err := mgr.RevertReflection(revertID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		fmt.Printf("Reverted reflection run %s (removed %d blocks)\n", revertID, len(cs.BlocksAdded))
		return
	}

	convStore := conversations.DefaultStore()
	sumCfg := defaultSummarizerConfig()

	cfg := memory.SleepTimeConfig{
		Enabled:       true,
		MaxConvs:      5,
		ConvStore:     convStore,
		SummarizerCfg: sumCfg,
	}

	engine := memory.NewSleepTimeEngine(mgr, cfg)
	ctx := context.Background()
	cs, err := engine.Reflect(ctx, dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	if dryRun {
		fmt.Printf("Reflection change set (run id: %s):\n", cs.RunID)
		fmt.Printf("  Timestamp: %s\n", cs.Timestamp.Format("2006-01-02 15:04:05"))
		if len(cs.BlocksAdded) == 0 && len(cs.BlocksSuperseded) == 0 {
			fmt.Println("  No changes to apply.")
			return
		}
		if len(cs.BlocksAdded) > 0 {
			fmt.Printf("  Blocks to add (%d):\n", len(cs.BlocksAdded))
			for _, b := range cs.AddedDetails {
				fmt.Printf("    + [%s] %s (ID: %s)\n", b.Label, truncateStr(b.Content, 70), b.ID)
			}
		}
		if len(cs.BlocksSuperseded) > 0 {
			fmt.Printf("  Blocks to supersede (%d):\n", len(cs.BlocksSuperseded))
			for _, b := range cs.SupersededDetails {
				fmt.Printf("    ~ [%s] %s (ID: %s)\n", b.Label, truncateStr(b.Content, 70), b.ID)
			}
		}
		return
	}

	if len(cs.BlocksAdded) == 0 && len(cs.BlocksSuperseded) == 0 {
		fmt.Printf("Reflection run %s completed: no new memories found.\n", cs.RunID)
		return
	}
	fmt.Printf("Reflection run %s applied:\n", cs.RunID)
	if len(cs.BlocksAdded) > 0 {
		fmt.Printf("  Blocks added (%d):\n", len(cs.BlocksAdded))
		for _, b := range cs.AddedDetails {
			fmt.Printf("    + [%s] %s (ID: %s)\n", b.Label, truncateStr(b.Content, 70), b.ID)
		}
	}
	if len(cs.BlocksSuperseded) > 0 {
		fmt.Printf("  Blocks superseded (%d):\n", len(cs.BlocksSuperseded))
		for _, b := range cs.SupersededDetails {
			fmt.Printf("    ~ [%s] %s (ID: %s)\n", b.Label, truncateStr(b.Content, 70), b.ID)
		}
	}
}

func cmdMemoryPrune(args []string) {
	ageStr := getFlag(args, "--age")
	if ageStr == "" {
		ageStr = "90d"
	}

	d, err := parseFriendlyDuration(ageStr)
	if err != nil {
		d2, err2 := time.ParseDuration(ageStr)
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "error: invalid duration %q\n", ageStr)
			exitFunc(1)
		}
		d = d2
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	total := 0

	// Prune short-term.
	c, _ := mgr.Store().DeleteOlderThan(memory.TierShort, d)
	total += c

	// Prune medium-term.
	c, _ = mgr.Store().DeleteOlderThan(memory.TierMedium, d)
	total += c

	// Prune expired.
	c, _ = mgr.Store().PruneExpired()
	total += c

	// Prune old summaries.
	c, _ = mgr.Store().DeleteSummariesOlderThan(d)
	total += c

	fmt.Printf("🗑  Pruned %d item(s) older than %s\n", total, ageStr)
}

func cmdMemoryContext(args []string) {
	verbose := hasFlag(args, "--verbose")
	command := getFlag(args, "--command")
	paths := getAllFlags(args, "--path")
	if rc := remoteMemoryClient(); rc != nil && command == "" && len(paths) == 0 {
		rendered, err := rc.Context("")
		if err == nil {
			if rendered == "" {
				fmt.Println("(empty memory context)")
				return
			}
			fmt.Println(rendered)
			if verbose {
				fmt.Fprintln(os.Stderr, "memory budget: not reported by a running server")
			}
			return
		}
		// Fallback to local store if remote call failed
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	var cw *memory.ContextWindow
	var err error
	if command != "" || len(paths) > 0 {
		cw, err = mgr.BuildTaskContext("", command, paths)
	} else {
		cw, err = mgr.BuildContext()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	if hasFlag(args, "--json") {
		data, _ := json.MarshalIndent(cw, "", "  ")
		fmt.Println(string(data))
		return
	}

	rendered := cw.Render()
	if verbose {
		if cw.Budget != nil {
			fmt.Fprintln(os.Stderr, cw.Budget.Summary())
		}
	}
	if rendered == "" {
		fmt.Println("(empty memory context)")
		return
	}
	fmt.Println(rendered)
}

func cmdMemoryFailure(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory failure add|list ...")
		exitFunc(1)
	}
	switch args[0] {
	case "add":
		cmdMemoryFailureAdd(args[1:])
	case "list":
		cmdMemoryFailureList(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown memory failure subcommand: %s\n", args[0])
		exitFunc(1)
	}
}

func cmdMemoryFailureAdd(args []string) {
	command := getFlag(args, "--command")
	errText := getFlag(args, "--error")
	fix := getFlag(args, "--fix")
	paths := getAllFlags(args, "--path")
	symbols := getAllFlags(args, "--symbol")

	if command == "" || errText == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann memory failure add --command <c> --error <text> --fix <text> [--path <p>]... [--symbol <s>]...")
		exitFunc(1)
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	block, err := mgr.RecordFailure(memory.Failure{
		Command: command,
		Error:   errText,
		Fix:     fix,
		Paths:   paths,
		Symbols: symbols,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	if block.Confirms > 1 {
		fmt.Printf("⚠️  Failure confirmed (%d×) for %q: %s\n", block.Confirms, command, truncateStr(block.Content, 80))
	} else {
		fmt.Printf("⚠️  Recorded failure [%s] for %q: %s\n", block.ID[:8], command, truncateStr(block.Content, 80))
	}
}

func cmdMemoryFailureList(args []string) {
	asJSON := hasFlag(args, "--json")

	mgr := openMemoryManager()
	defer mgr.Close()

	failures, err := mgr.ListFailures()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	if asJSON {
		data, _ := json.MarshalIndent(failures, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(failures) == 0 {
		fmt.Println("No failures recorded.")
		return
	}

	fmt.Printf("⚠️  Recorded failures (%d):\n\n", len(failures))
	for i, b := range failures {
		fmt.Printf("  %d. [%s] command: %s\n", i+1, b.ID[:8], b.Metadata[memory.FailureMetaCommand])
		fmt.Printf("     error: %s\n", truncateStr(b.Content, 100))
		if fix := b.Metadata[memory.FailureMetaFix]; fix != "" {
			fmt.Printf("     fix: %s\n", truncateStr(fix, 100))
		}
		if len(b.Paths) > 0 {
			fmt.Printf("     files: %s\n", strings.Join(b.Paths, ", "))
		}
	}
}

func cmdMemoryMaintain() {
	mgr := openMemoryManager()
	defer mgr.Close()

	fmt.Println("Running memory maintenance...")
	if err := mgr.RunMaintenance(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
	fmt.Println("✅ Maintenance complete.")
}

// ── Helpers ───────────────────────────────────────────────────────

func truncateStr(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func formatMemAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func formatMemSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	case bytes < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
	}
}

func cmdMemoryCompact(args []string) {
	minVal := 0.2
	for i := 0; i < len(args); i++ {
		if args[i] == "--min-validity" && i+1 < len(args) {
			if v, err := strconv.ParseFloat(args[i+1], 64); err == nil {
				minVal = v
			}
			i++
		}
	}

	if rc := remoteMemoryClient(); rc != nil {
		pruned, err := rc.Compact(minVal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error (remote): %v\n", err)
			exitFunc(1)
		}
		fmt.Printf("🧹 Memory compacted: %d stale/unreliable block(s) pruned (min validity: %.2f) (via running server)\n", pruned, minVal)
		return
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	pruned, err := mgr.Compact(minVal)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	fmt.Printf("🧹 Memory compacted: %d stale/unreliable block(s) pruned (min validity: %.2f)\n", pruned, minVal)
}

func cmdMemoryExport(args []string) {
	dir := getFlag(args, "--dir")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann memory export --dir <dir>")
		exitFunc(1)
		return
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	stats, err := mgr.Export(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error exporting memory: %v\n", err)
		exitFunc(1)
		return
	}

	fmt.Printf("📦 Exported %d memory block(s) and %d summary(ies) to %s\n",
		stats.Blocks, stats.Summaries, dir)
}

func cmdMemoryImport(args []string) {
	dir := getFlag(args, "--dir")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann memory import --dir <dir>")
		exitFunc(1)
		return
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	stats, err := mgr.Import(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error importing memory: %v\n", err)
		exitFunc(1)
		return
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("%d imported", stats.BlocksImported))
	if stats.BlocksUpdated > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", stats.BlocksUpdated))
	}
	if stats.BlocksUnchanged > 0 {
		parts = append(parts, fmt.Sprintf("%d unchanged", stats.BlocksUnchanged))
	}
	if stats.Conflicts > 0 {
		parts = append(parts, fmt.Sprintf("%d conflict(s) resolved", stats.Conflicts))
	}
	if stats.SummariesImported > 0 {
		parts = append(parts, fmt.Sprintf("%d summary(ies)", stats.SummariesImported))
	}
	fmt.Printf("📥 Imported memory blocks from %s: %s\n", dir, strings.Join(parts, ", "))
}

// cmdMemoryImportGraph loads the export files of tools/kuzu-memory-export
// (entities, relations and archive of a gleann v1.9 Memory Engine store).
func cmdMemoryImportGraph(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory import-graph <file.json|dir>")
		exitFunc(1)
		return
	}
	mgr := openMemoryManager()
	defer mgr.Close()

	results, err := mgr.Store().ImportGraphPath(args[0])
	for _, r := range results {
		if r.AlreadyImported {
			fmt.Printf("= %s: already imported from this file, nothing changed\n", r.Namespace)
			continue
		}
		fmt.Printf("📥 %s: %d entities, %d relations, %d archive rows added\n",
			r.Namespace, r.Entities, r.Relations, r.Archive)
		syncImportedEntities(r)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error importing graph: %v\n", err)
		exitFunc(1)
	}
}

// syncImportedEntities adds the entities an import-graph run inserted to the
// namespace's memory vector index (<ns>_memory_vectors), as the server and
// MCP memory syncer do for injected entities. It runs when an embedding model
// is configured (the condition under which the syncer is enabled); a failure
// is a warning, the entities stay imported.
func syncImportedEntities(r memory.GraphImportResult) {
	if len(r.AddedEntities) == 0 {
		return
	}
	config := getConfig(nil)
	applySavedConfig(&config, nil)
	if config.EmbeddingModel == "" || gleann.ValidateName(r.Namespace) != nil {
		return
	}
	builder, err := gleann.NewBuilder(config, indexing.NewEmbedder(config))
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠  %s: entities imported, memory vector index not updated: %v\n", r.Namespace, err)
		return
	}
	contents := make([]memsync.Content, 0, len(r.AddedEntities))
	for _, e := range r.AddedEntities {
		var attrs map[string]any
		_ = json.Unmarshal([]byte(e.Attributes), &attrs)
		contents = append(contents, memsync.Content{NodeID: e.ID, Content: e.Content, Attrs: attrs})
	}
	name := r.Namespace + "_memory_vectors"
	if err := memsync.NewLeannSyncer(builder, name, nil).AddContents(context.Background(), contents); err != nil {
		fmt.Fprintf(os.Stderr, "⚠  %s: entities imported, memory vector index %s not updated: %v\n", r.Namespace, name, err)
		return
	}
	fmt.Printf("🧠 %s: %d entities added to the memory vector index %s\n", r.Namespace, len(contents), name)
}
