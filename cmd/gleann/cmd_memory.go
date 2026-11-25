package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
		// --help/-h is a request for usage, not a query word and not an
		// unknown flag: it is honoured before the subcommand runs and before
		// unknown flags are rejected, so it can never store, delete, clear or
		// export anything. A literal "--" still ends flag parsing, so
		// `memory forget -- --help` keeps "--help" as query text.
		if wantsHelpIn(subArgs, fs) {
			printMemoryUsage()
			return // exit 0: showing help is not an error
		}
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
  gleann memory remember <text> [--tag <tags>] [--label <label>] [--scope <scope>]  Store fact in long-term memory
  gleann memory forget   [--all] <query-or-id>   Remove memory by ID, or by content match (several matches need --all)
  gleann memory list     [--tier <tier>] [--scope <s>|all]  List memories (default: global + detected repo scope)
  gleann memory search   <query>                 Full-text search across all tiers
  gleann memory add      <tier> <text>            Add note to specific tier
  gleann memory clear    [<tier>|--tier <tier>]  Delete memories of one tier (no tier = every tier)
  gleann memory stats                             Storage statistics
  gleann memory summarize --last                 Summarize last conversation → memory
  gleann memory summarize --id <conv-id>          Summarize specific conversation
  gleann memory summarize --last --extract        Also extract individual facts
  gleann memory reflect  [--dry-run]              Run sleep-time reflection cycle
  gleann memory reflect  --list [--limit N]       List recorded reflection runs (newest first)
  gleann memory reflect  --revert <run-id>        Revert a reflection run change set
  gleann memory prune    [--age <duration>]       Remove old entries
  gleann memory compact  [--min-validity <0-1>]   Prune stale/unreliable blocks (default min validity: 0.2)
  gleann memory maintain                          Full maintenance pass
  gleann memory context [--scope <s>|all] [--max-bytes N]  Show compiled context sent to LLM (default: detected repo scope + global; --max-bytes N cuts the text on a character boundary, 0 = unlimited, plain text only (--json ignores it), the output ends with a newline)
  gleann memory context --path <p> --command <c>  Show context + failures matching files/command
  gleann memory context --symbol <s> [--scope <s>]  Show context + failures matching symbols
  gleann memory failure add --command <c> --error <text> --fix <text> [--path <p>]... [--symbol <s>]... [--scope <s>]
  gleann memory failure list [--scope <s>]        List recorded failures (default: repo scope; --scope all = all scopes)
  gleann memory export   --dir <dir> [--scope <s>|all]  Export memory blocks and summaries to markdown (skips short-term and expired blocks)
  gleann memory import   --dir <dir> [--dry-run]        Import memory blocks and summaries from markdown (--dry-run previews without writing)
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
  --scope <scope>         Block/failure/context/list scope ("repo" or "." = the detected repository scope). remember: default global; failure/context/list: default detected repo + global. --scope all = global on add, every scope on list/context/export
  --include-short         Also export the session-scoped short-term blocks (memory export)
  --include-expired       Also export blocks already past their expiry (memory export)
  --json                  Output as JSON (memory context includes the budget report)
  --verbose               Print the injection budget report to stderr (memory context)
  --extract               Also extract individual facts (with summarize)
  --dry-run               Preview without writing: reflection change set, or memory import plan
  --list                  List stored reflection runs (memory reflect; no LLM needed)
  --limit <n>             Cap the reflection run list (0 or unset = all runs)
  --revert <run-id>       Revert reflection run by ID

Injection budget:
  Injected memory is capped at 4000 tokens by default. GLEANN_MEMORY_MAX_TOKENS
  changes the cap (0 = unlimited). Over budget, short-term blocks are dropped
  first, then medium, then long; within a tier the older and less-accessed
  block goes first. Blocks are never cut in half.

Chat Slash Commands (inside 'gleann chat'):
  /remember <text>        Store fact to long-term memory
  /forget [--all] <query> Remove memories matching query (several matches need --all)
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
  gleann memory reflect --list
  gleann memory reflect --list --limit 5 --json
  gleann memory stats
  gleann memory context
  gleann memory maintain
  gleann memory export --dir ./backup
  gleann memory export --dir ./backup --scope repo   # only this repository plus global
  gleann memory import --dir ./backup
  gleann memory import --dir ./backup --dry-run      # preview, writes nothing
  gleann memory list --scope all                     # every scope, not just this repo

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
		fmt.Fprintln(os.Stderr, "usage: gleann memory remember <text> [--tag <tags>] [--label <label>] [--scope <scope>]")
		exitFunc(1)
	}

	var tags []string
	label := ""
	rawScope := ""

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
		case "--scope":
			if i+1 < len(args) {
				i++
				rawScope = args[i]
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

	// Normalize the scope; a sentinel means global, never a literal "all"
	// block. Without --scope, remember stays global (v3 repair, finding 1).
	scope := resolveRepoScopeAlias(memory.NormalizeScope(rawScope))
	if memory.IsAllScopesSentinel(scope) {
		scope = ""
	}

	// Prefer running server (avoids bbolt lock collision with `gleann serve`).
	if rc := remoteMemoryClient(); rc != nil {
		if label == "" {
			label = "note"
		}
		var block *memory.Block
		var err error
		if scope != "" {
			block, err = rc.AddBlock(&memory.Block{
				Tier: memory.TierLong, Label: label, Content: content,
				Tags: tags, Source: "cli", Scope: scope,
			})
		} else {
			block, err = rc.Add(memory.TierLong, label, content, tags)
		}
		if err == nil {
			fmt.Printf("✅ Remembered [%s]: %s  (via running server)\n", block.ID[:8], truncateStr(content, 80))
			printContradictionWarning(block)
			return
		}
		// Fallback to local store if remote call failed
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	var block *memory.Block
	var err error
	if scope != "" {
		block, err = mgr.RememberBlock(&memory.Block{
			Tier: memory.TierLong, Label: "user_memory", Content: content,
			Source: "user", Tags: tags, Scope: scope,
		})
	} else {
		block, err = mgr.Remember(content, tags...)
	}
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
	printContradictionWarning(block)
}

// printContradictionWarning tells the user which stored blocks a remembered
// fact conflicts with, when the write was flagged as contradicting.
func printContradictionWarning(block *memory.Block) {
	if block != nil && block.Metadata["has_contradictions"] == "true" {
		fmt.Printf("warning: contradicts existing memory [%s]\n", block.Metadata["contradicts"])
	}
}

func cmdMemoryForget(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann memory forget [--all] <id-or-query>")
		exitFunc(1)
	}

	query := freeText(args, memoryFlagSets["forget"])
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann memory forget [--all] <id-or-query>")
		exitFunc(1)
	}

	all := hasFlag(args, "--all")

	if rc := remoteMemoryClient(); rc != nil {
		count, err := rc.Forget(query, all)
		if err == nil {
			fmt.Printf("🗑  Forgot %d memory block(s) matching %q  (via running server)\n", count, query)
			return
		}
		var amb *memory.ForgetAmbiguousError
		if errors.As(err, &amb) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(2)
		}
		// Fallback to local store if remote call failed
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	var count int
	var err error
	if all {
		count, err = mgr.ForgetAll(query)
	} else {
		count, err = mgr.Forget(query)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		var amb *memory.ForgetAmbiguousError
		if errors.As(err, &amb) {
			exitFunc(2)
		}
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

	// Same scope default as `memory context`: the detected repository plus
	// global. --scope all (or *) lists every scope; --scope <name> lists one.
	rawScope := getFlag(args, "--scope")
	scope := resolveRepoScopeAlias(memory.NormalizeScope(rawScope))
	if memory.IsAllScopesSentinel(scope) {
		scope = ""
	} else if scope == "" {
		scope = memory.DetectRepoScope()
	}
	scopeLabel := scope
	if scopeLabel == "" {
		scopeLabel = "all"
	}

	var blocks []memory.Block
	var err error
	var remoteOk bool
	if rc := remoteMemoryClient(); rc != nil {
		blocks, err = rc.ListScoped(scope, tier)
		if err == nil {
			remoteOk = true
		}
	}
	if !remoteOk {
		mgr := openMemoryManager()
		defer mgr.Close()
		blocks, err = mgr.ListScoped(scope, tier)
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
		fmt.Printf("Scope %q: pass --scope all for every scope or --scope <scope> for one.\n", scopeLabel)
		return
	}

	tierLabel := "all tiers"
	if tier != "" {
		tierLabel = string(tier)
	}
	fmt.Printf("🧠 Memories (%d, %s):\n", len(blocks), tierLabel)
	fmt.Printf("Scope %s (--scope all lists every scope)\n\n", scopeLabel)

	for _, b := range blocks {
		age := time.Since(b.CreatedAt).Round(time.Second)
		tags := ""
		if len(b.Tags) > 0 {
			tags = " [" + strings.Join(b.Tags, ",") + "]"
		}
		scope := b.Scope
		if scope == "" {
			scope = "global"
		}
		fmt.Printf("  %s  %-8s %-12s %s%s  [scope: %s]  (%s ago)\n",
			b.ID[:8], b.Tier, b.Label, truncateStr(b.Content, 50), tags, scope, formatMemAge(age))
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

// parseClearTier reads the tier of `memory clear`: a positional tier name, or
// --tier/-t <tier> (also --tier=<tier>). No tier means every tier, but only a
// bare `clear`: a malformed form (missing value, unknown tier, stray flag, two
// tiers) is an error, never "clear everything".
func parseClearTier(args []string) (memory.Tier, error) {
	var names []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			names = append(names, args[i+1:]...)
			i = len(args)
		case a == "--tier" || a == "-t":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return "", fmt.Errorf("%s needs a tier (short, medium or long); run 'gleann memory clear' with no tier to clear every tier", a)
			}
			names = append(names, args[i+1])
			i++
		case strings.HasPrefix(a, "--tier="):
			names = append(names, strings.TrimPrefix(a, "--tier="))
		case strings.HasPrefix(a, "-"):
			return "", fmt.Errorf("unknown option %q for 'memory clear' (use --tier <tier>)", a)
		default:
			names = append(names, a)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	if len(names) > 1 {
		return "", fmt.Errorf("memory clear takes one tier, got %d", len(names))
	}
	t, err := memory.ParseTier(names[0])
	if err != nil {
		return "", err
	}
	return t, nil
}

func cmdMemoryClear(args []string) {
	tier, err := parseClearTier(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(2)
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

// flagPresent reports whether --flag appears before a literal "--", as
// "--flag", "--flag value" or "--flag=value", whatever its value is.
func flagPresent(args []string, flag string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

func cmdMemoryReflect(args []string) {
	dryRun := hasFlag(args, "--dry-run")
	revertID := getFlag(args, "--revert")
	listRuns := hasFlag(args, "--list")

	if dryRun && revertID != "" {
		fmt.Fprintln(os.Stderr, "error: cannot use --dry-run and --revert together")
		exitFunc(1)
	}
	if listRuns && (dryRun || revertID != "") {
		fmt.Fprintln(os.Stderr, "error: cannot use --list with --dry-run or --revert")
		exitFunc(1)
	}

	if !listRuns {
		for _, fl := range []string{"--limit", "--json"} {
			if flagPresent(args, fl) {
				fmt.Fprintf(os.Stderr, "error: %s only applies to 'memory reflect --list'; refusing to run a reflection cycle\n", fl)
				exitFunc(2)
			}
		}
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	if listRuns {
		limit := 0
		if raw := getFlag(args, "--limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				fmt.Fprintf(os.Stderr, "error: invalid --limit %q\n", raw)
				exitFunc(1)
			}
			limit = n
		}
		printReflectionRuns(mgr, limit, hasFlag(args, "--json"))
		return
	}

	if revertID != "" {
		cs, err := mgr.RevertReflection(revertID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
		fmt.Printf("Reverted reflection run %s (removed %d blocks, undid %d confirmations)\n", revertID, len(cs.BlocksAdded), len(cs.BlocksConfirmed))
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
		if len(cs.BlocksAdded) == 0 && len(cs.BlocksSuperseded) == 0 && len(cs.BlocksConfirmed) == 0 {
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
		if len(cs.BlocksConfirmed) > 0 {
			fmt.Printf("  Blocks to confirm (%d):\n", len(cs.BlocksConfirmed))
			for _, c := range cs.BlocksConfirmed {
				fmt.Printf("    = ID: %s (confirms %d -> %d)\n", c.ID, c.PriorConfirms, c.PriorConfirms+1)
			}
		}
		return
	}

	if len(cs.BlocksAdded) == 0 && len(cs.BlocksSuperseded) == 0 && len(cs.BlocksConfirmed) == 0 {
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
	if len(cs.BlocksConfirmed) > 0 {
		fmt.Printf("  Blocks confirmed (%d):\n", len(cs.BlocksConfirmed))
		for _, c := range cs.BlocksConfirmed {
			fmt.Printf("    = ID: %s (confirms %d -> %d)\n", c.ID, c.PriorConfirms, c.PriorConfirms+1)
		}
	}
}

// reflectionRunStatus summarizes what a persisted run did, so `reflect --list`
// can show an outcome without re-reading the blocks.
func reflectionRunStatus(cs *memory.ChangeSet) string {
	if len(cs.BlocksAdded) == 0 && len(cs.BlocksSuperseded) == 0 && len(cs.BlocksConfirmed) == 0 {
		return "no changes"
	}
	return "applied"
}

// printReflectionRuns lists persisted reflection runs, newest first. It reads
// only the stored change sets, so it never needs an LLM.
func printReflectionRuns(mgr *memory.Manager, limit int, asJSON bool) {
	runs, err := mgr.ListReflections(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	if asJSON {
		if runs == nil {
			runs = []*memory.ChangeSet{}
		}
		data, err := json.MarshalIndent(runs, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: encoding reflection runs: %v\n", err)
			exitFunc(1)
		}
		fmt.Println(string(data))
		return
	}

	if len(runs) == 0 {
		fmt.Println("No reflection runs recorded.")
		return
	}

	fmt.Printf("Reflection runs (%d, newest first):\n", len(runs))
	for _, cs := range runs {
		fmt.Printf("  %-12s  %s  added %d, superseded %d, confirmed %d  %s\n",
			cs.RunID, cs.Timestamp.Format("2006-01-02 15:04:05"),
			len(cs.BlocksAdded), len(cs.BlocksSuperseded), len(cs.BlocksConfirmed),
			reflectionRunStatus(cs))
	}
	fmt.Println("\nRevert a run with: gleann memory reflect --revert <run-id>")
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
	symbols := getAllFlags(args, "--symbol")
	// Same default as MCP memory_context: the detected repository scope (plus
	// the global blocks); outside a repository that is the directory name.
	// --scope all shows every scope.
	scope := resolveRepoScopeAlias(memory.NormalizeScope(getFlag(args, "--scope")))
	if scope == "" {
		scope = memory.DetectRepoScope()
	} else if memory.IsAllScopesSentinel(scope) {
		scope = ""
	}
	// --max-bytes caps the printed text on a character boundary (hooks use
	// it instead of `head -c`, which can split a multi-byte character).
	maxBytes := 0
	if v := getFlag(args, "--max-bytes"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fmt.Fprintf(os.Stderr, "error: --max-bytes needs a non-negative integer, got %q\n", v)
			exitFunc(2)
		}
		maxBytes = n
	}
	if rc := remoteMemoryClient(); rc != nil && command == "" && len(paths) == 0 && len(symbols) == 0 {
		rendered, err := rc.Context(scope)
		if err == nil {
			if rendered == "" {
				fmt.Println("(empty memory context)")
				return
			}
			fmt.Println(truncateOnRune(rendered, maxBytes))
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
	if command != "" || len(paths) > 0 || len(symbols) > 0 {
		cw, err = mgr.BuildTaskContextFor(scope, command, paths, symbols)
	} else if scope != "" {
		cw, err = mgr.BuildScopedContext(scope)
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
	fmt.Println(truncateOnRune(rendered, maxBytes))
}

// truncateOnRune cuts s to at most max bytes without splitting a UTF-8
// character; max <= 0 means no limit.
func truncateOnRune(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// resolveRepoScopeAlias maps the scope words "repo" and "." to the detected
// repository scope (the default of memory context and failure); any other
// scope passes through. A scope literally named "repo" is not addressable
// from the CLI that way, which is the price of the shorthand.
func resolveRepoScopeAlias(scope string) string {
	if scope == "repo" || scope == "." {
		return memory.DetectRepoScope()
	}
	return scope
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
	scope := resolveRepoScopeAlias(memory.NormalizeScope(getFlag(args, "--scope")))

	if command == "" || errText == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann memory failure add --command <c> --error <text> --fix <text> [--path <p>]... [--symbol <s>]... [--scope <s>]")
		exitFunc(1)
	}

	scope = memory.NormalizeScope(scope)
	if memory.IsAllScopesSentinel(scope) {
		scope = "" // --scope all records a global failure, never a literal "all" scope
	} else if scope == "" {
		scope = memory.DetectRepoScope()
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	block, err := mgr.RecordFailureScoped(scope, "", memory.Failure{
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
	scope := resolveRepoScopeAlias(memory.NormalizeScope(getFlag(args, "--scope")))

	if scope == "" {
		scope = memory.DetectRepoScope()
	} else if memory.IsAllScopesSentinel(scope) {
		scope = ""
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	failures, err := mgr.ListFailuresScoped(scope)
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
		fmt.Fprintln(os.Stderr, "usage: gleann memory export --dir <dir> [--scope <s>|all] [--include-short] [--include-expired]")
		exitFunc(1)
		return
	}

	// Default: every scope, skipping the session-scoped short-term blocks and
	// the ones already past their expiry. --scope narrows the run, --include-*
	// widen it back to the full dump.
	scope := resolveRepoScopeAlias(memory.NormalizeScope(getFlag(args, "--scope")))
	if memory.IsAllScopesSentinel(scope) {
		scope = ""
	}
	opts := memory.ExportOptions{
		Scope:          scope,
		IncludeShort:   hasFlag(args, "--include-short"),
		IncludeExpired: hasFlag(args, "--include-expired"),
	}

	mgr := openMemoryManager()
	defer mgr.Close()

	stats, err := mgr.ExportWithOptions(dir, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error exporting memory: %v\n", err)
		exitFunc(1)
		return
	}

	scopeLabel := scope
	if scopeLabel == "" {
		scopeLabel = "all"
	}
	line := fmt.Sprintf("📦 Exported %d memory block(s) and %d summary(ies) to %s  [scope: %s]",
		stats.Blocks, stats.Summaries, dir, scopeLabel)
	if stats.Pruned > 0 {
		line += fmt.Sprintf(", pruned %d stale file(s)", stats.Pruned)
	}
	fmt.Println(line)
}

func cmdMemoryImport(args []string) {
	dir := getFlag(args, "--dir")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann memory import --dir <dir> [--dry-run]")
		exitFunc(1)
		return
	}
	dryRun := hasFlag(args, "--dry-run")

	mgr := openMemoryManager()
	defer mgr.Close()

	stats, err := mgr.ImportWithOptions(dir, memory.ImportOptions{DryRun: dryRun})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error importing memory: %v\n", err)
		exitFunc(1)
		return
	}

	var parts []string
	if dryRun {
		parts = append(parts, fmt.Sprintf("%d would import", stats.BlocksImported))
		if stats.BlocksUpdated > 0 {
			parts = append(parts, fmt.Sprintf("%d would update", stats.BlocksUpdated))
		}
		if stats.BlocksUnchanged > 0 {
			parts = append(parts, fmt.Sprintf("%d unchanged", stats.BlocksUnchanged))
		}
		if stats.SummariesImported > 0 {
			parts = append(parts, fmt.Sprintf("%d summary(ies)", stats.SummariesImported))
		}
		fmt.Printf("🔍 Dry run: %d block file(s) from %s: %s (nothing written)\n",
			stats.BlocksTotal, dir, strings.Join(parts, ", "))
	} else {
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

	// A hand-edited export file is not applied: the stored block is newer or
	// equal, so the edit would be lost without a word about it.
	if len(stats.HandEditedFiles) > 0 {
		fmt.Fprintf(os.Stderr, "⚠  %d exported file(s) were edited by hand but their updated_at did not move forward, so they were kept as stored:\n", len(stats.HandEditedFiles))
		for _, f := range stats.HandEditedFiles {
			fmt.Fprintf(os.Stderr, "   %s\n", f)
		}
		fmt.Fprintln(os.Stderr, "   raise `updated_at` in the file's front matter (or re-export) to import the edit.")
	}
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
