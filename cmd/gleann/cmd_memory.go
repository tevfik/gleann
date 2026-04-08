package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/memory"
)

func cmdMemory(args []string) {
	if len(args) == 0 {
		printMemoryUsage()
		return // exit 0: showing help is not an error
	}

	sub := args[0]
	subArgs := args[1:]

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
	case "context":
		cmdMemoryContext()
	case "maintain":
		cmdMemoryMaintain()
	default:
		fmt.Fprintf(os.Stderr, "unknown memory subcommand: %s\n", sub)
		printMemoryUsage()
		os.Exit(1)
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
  gleann memory prune    [--age <duration>]       Remove old entries
  gleann memory maintain                          Full maintenance pass
  gleann memory context                           Show compiled context sent to LLM

Memory Tiers:
  short    In-memory, session-scoped
           → auto-promoted to medium when chat session ends
  medium   BBolt, conversation digests, daily summaries
           → auto-archived to long after 30 days
  long     BBolt, permanent facts, user preferences
           → never automatically deleted

Options:
  --tier <tier>           Filter by tier: short | medium | long
  --tag <tags>            Comma-separated tags (e.g. "preference,project")
  --label <label>         Semantic label (default: "note" or "user_memory")
  --age <duration>        Duration for prune (e.g. 30d, 90d, 2w, 24h)
  --json                  Output as JSON
  --extract               Also extract individual facts (with summarize)

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
		os.Exit(1)
	}
	return mgr
}

func cmdMemoryRemember(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory remember <text> [--tag <tags>] [--label <label>]")
		os.Exit(1)
	}

	var tags []string
	label := ""

	// Parse content (everything that's not a flag).
	var contentParts []string
	for i := 0; i < len(args); i++ {
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
		os.Exit(1)
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	block, err := mgr.Remember(content, tags...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if label != "" {
		// Update label.
		b, _ := mgr.Store().Get(block.ID)
		if b != nil {
			b.Label = label
			b.UpdatedAt = time.Now()
		}
	}

	fmt.Printf("✅ Remembered [%s]: %s\n", block.ID[:8], truncateStr(content, 80))
}

func cmdMemoryForget(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory forget <id-or-query>")
		os.Exit(1)
	}

	query := strings.Join(args, " ")

	mgr := openMemoryManager()
	defer mgr.Close()

	count, err := mgr.Forget(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
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
			os.Exit(1)
		}
		tier = t
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	blocks, err := mgr.List(tier)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
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
		os.Exit(1)
	}

	query := strings.Join(args, " ")
	asJSON := hasFlag(args, "--json")

	mgr := openMemoryManager()
	defer mgr.Close()

	blocks, err := mgr.Search(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
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
	mgr := openMemoryManager()
	defer mgr.Close()

	if tierStr := getFlag(args, "--tier"); tierStr != "" {
		tier, err := memory.ParseTier(tierStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		count, err := mgr.Clear(tier)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🗑  Cleared %d memory block(s) from tier %q\n", count, tier)
	} else {
		count, err := mgr.ClearAll()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🗑  Cleared %d memory block(s) from all tiers\n", count)
	}
}

func cmdMemoryAdd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory add <tier> <text> [--tag <tags>] [--label <label>]")
		os.Exit(1)
	}

	tier, err := memory.ParseTier(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	remaining := args[1:]
	var tags []string
	label := "note"

	var contentParts []string
	for i := 0; i < len(remaining); i++ {
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
		os.Exit(1)
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	block, err := mgr.AddNote(tier, label, content, tags...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Added to %s [%s]: %s\n", tier, block.ID[:8], truncateStr(content, 80))
}

func cmdMemoryStats() {
	mgr := openMemoryManager()
	defer mgr.Close()

	stats, err := mgr.Stats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
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
			os.Exit(1)
		}
	} else if id := getFlag(args, "--id"); id != "" {
		conv, err = convStore.Load(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading conversation: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintln(os.Stderr, "usage: gleann memory summarize [--last | --id <ID>]")
		os.Exit(1)
	}

	// Build summarizer config from saved config.
	savedCfg := tui.LoadSavedConfig()
	sumCfg := memory.SummarizerConfig{
		Provider: "ollama",
		Model:    "llama3.2:3b-instruct-q4_K_M",
		BaseURL:  "http://localhost:11434",
	}
	if savedCfg != nil {
		if savedCfg.LLMProvider != "" {
			sumCfg.Provider = savedCfg.LLMProvider
		}
		if savedCfg.LLMModel != "" {
			sumCfg.Model = savedCfg.LLMModel
		}
		if savedCfg.OllamaHost != "" {
			sumCfg.BaseURL = savedCfg.OllamaHost
		}
		if savedCfg.OpenAIKey != "" {
			sumCfg.APIKey = savedCfg.OpenAIKey
		}
		if savedCfg.AnthropicKey != "" && savedCfg.LLMProvider == "anthropic" {
			sumCfg.APIKey = savedCfg.AnthropicKey
		}
	}

	summarizer := memory.NewSummarizer(sumCfg)

	fmt.Fprintf(os.Stderr, "Summarizing conversation %q...\n", conv.Title)

	ctx := context.Background()
	summary, err := summarizer.SummarizeConversation(ctx, conv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	if err := mgr.Store().SaveSummary(summary); err != nil {
		fmt.Fprintf(os.Stderr, "error saving summary: %v\n", err)
		os.Exit(1)
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
			os.Exit(1)
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

func cmdMemoryContext() {
	mgr := openMemoryManager()
	defer mgr.Close()

	cw, err := mgr.BuildContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if hasFlag(os.Args, "--json") {
		data, _ := json.MarshalIndent(cw, "", "  ")
		fmt.Println(string(data))
		return
	}

	rendered := cw.Render()
	if rendered == "" {
		fmt.Println("(empty memory context)")
		return
	}
	fmt.Println(rendered)
}

func cmdMemoryMaintain() {
	mgr := openMemoryManager()
	defer mgr.Close()

	fmt.Println("Running memory maintenance...")
	if err := mgr.RunMaintenance(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
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
