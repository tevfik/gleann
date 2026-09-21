package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

// getAgentsMDContent returns standardized, English AGENTS.md instructions for AI agents.
// If indexName is provided, it is woven into the instructions, tools, and command examples.
func getAgentsMDContent(indexName string) string {
	idxDisplay := indexName
	if idxDisplay == "" {
		idxDisplay = "<name>"
	}

	var sb strings.Builder
	sb.WriteString("\n## gleann: Code Intelligence, Search & Long-term Memory\n\n")
	if indexName != "" {
		sb.WriteString(fmt.Sprintf("This project is indexed in [gleann](https://github.com/tevfik/gleann) under index name **`%s`**.\n\n", indexName))
	} else {
		sb.WriteString("This project uses [gleann](https://github.com/tevfik/gleann) for AI-powered codebase\nnavigation and persistent cross-session memory.\n\n")
	}

	sb.WriteString("### 1 — Before exploring source files\n\n")
	sb.WriteString("Read **GRAPH_REPORT.md** (if present) — contains god nodes (high-degree hub symbols),\ncommunity structure, and cross-cutting dependency edges.\n")
	sb.WriteString(fmt.Sprintf("Generate it with: `gleann graph report --index %s`\n\n", idxDisplay))

	sb.WriteString("Key graph / search commands:\n")
	sb.WriteString(fmt.Sprintf("- `gleann search %s <query>` — semantic search\n", idxDisplay))
	sb.WriteString("- `gleann search idx1,idx2 <query>` — multi-index search (comma-separated)\n")
	sb.WriteString("- `gleann search --all <query>` — search across all indexes\n")
	sb.WriteString(fmt.Sprintf("- `gleann search %s <query> --rerank` — add cross-encoder reranking\n", idxDisplay))
	sb.WriteString(fmt.Sprintf("- `gleann ask %s <question>` — RAG-powered Q&A from indexed content\n", idxDisplay))
	sb.WriteString("- `gleann index list` — list available indexes\n")
	sb.WriteString(fmt.Sprintf("- `gleann index sync %s [--docs <dir>]` — incrementally sync index after code changes\n", idxDisplay))
	sb.WriteString(fmt.Sprintf("- `gleann index watch %s --docs <dir>` — auto-rebuild on file changes\n", idxDisplay))
	sb.WriteString(fmt.Sprintf("- `gleann graph explain <symbol> --index %s` — callers, callees, blast radius\n", idxDisplay))
	sb.WriteString(fmt.Sprintf("- `gleann graph query <pattern> --index %s` — find symbols by pattern\n", idxDisplay))
	sb.WriteString(fmt.Sprintf("- `gleann graph path <from> <to> --index %s` — shortest dependency path\n\n", idxDisplay))

	sb.WriteString("### 2 — Long-term memory (always ON)\n\n")
	sb.WriteString("gleann maintains **persistent, tiered memory** that survives across sessions.  Use it\nactively to build up institutional knowledge about this codebase:\n\n")
	sb.WriteString("| Tier | Lifetime | Use for |\n")
	sb.WriteString("|------|----------|---------|\n")
	sb.WriteString("| **short** | session only | current task state, transient notes |\n")
	sb.WriteString("| **medium** | days / sprints | sprint goals, active decisions, pending TODOs |\n")
	sb.WriteString("| **long** | permanent | architecture decisions, conventions, team contacts |\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("gleann memory remember \"Hexagonal architecture: adapters live in internal/adapters\"\n")
	sb.WriteString("gleann memory remember \"DB columns use snake_case\" --tag \"convention\"\n")
	sb.WriteString("gleann memory add short \"Current task: refactor auth module\"\n")
	sb.WriteString("gleann memory add medium \"Sprint 14: focus on latency improvements\"\n")
	sb.WriteString("gleann memory search \"architecture\"\n")
	sb.WriteString("gleann memory list --tier long\n")
	sb.WriteString("gleann memory stats\n")
	sb.WriteString("```\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("gleann memory summarize --last           # compress last conversation → long-term memory\n")
	sb.WriteString("gleann memory summarize --last --extract # also extract individual facts into blocks\n")
	sb.WriteString("gleann memory prune --age 90d            # remove entries older than 90 days\n")
	sb.WriteString("gleann memory forget \"outdated fact\"     # remove by content match or block ID\n")
	sb.WriteString("gleann memory clear --tier short         # wipe session-tier blocks\n")
	sb.WriteString("```\n\n")
	sb.WriteString("### 3 — MCP tools (when gleann mcp is running)\n\n")
	sb.WriteString("**Search & graph:**\n")
	sb.WriteString("`gleann_search` · `gleann_search_multi` · `gleann_ask` · `gleann_graph_neighbors` · `gleann_impact` · `gleann_read_full_document` · `gleann_document_toc` · `gleann_sync`\n")
	sb.WriteString("`inject_knowledge_graph` · `delete_graph_entity` · `traverse_knowledge_graph`\n\n")
	if indexName != "" {
		sb.WriteString(fmt.Sprintf("- `gleann_search` — semantic search. Pass `{\"query\": \"...\"}` (the index automatically defaults to `%s`).\n", indexName))
		sb.WriteString(fmt.Sprintf("- `gleann_sync` — **CRITICAL:** call this (`{\"index\": \"%s\"}`) after creating, editing, or deleting files to incrementally refresh vector passages and AST graph! Default mode is `\"code\"` for fast indexing. Use `mode: \"docs\"` to index office documents on-demand.\n", indexName))
	} else {
		sb.WriteString("- `gleann_search` — semantic search across indexes. Can omit index to auto-resolve.\n")
		sb.WriteString("- `gleann_sync` — call this after creating, editing, or deleting files to incrementally refresh vector passages and AST graph! Default mode is `\"code\"` for fast indexing. Use `mode: \"docs\"` for office documents.\n")
	}
	sb.WriteString("- `gleann_impact` — inspect callers and blast radius before modifying symbols.\n\n")
	sb.WriteString("**Memory (always-available, no build tag):**\n")
	sb.WriteString("- `memory_remember` — store fact with tier/label/tags/scope\n")
	sb.WriteString("- `memory_forget` — remove block by ID or content match\n")
	sb.WriteString("- `memory_search` — full-text search across all tiers\n")
	sb.WriteString("- `memory_list` — browse blocks, filter by tier\n")
	sb.WriteString("- `memory_context` — returns the compiled `<memory_context>` window that gleann\n")
	sb.WriteString("  injects into LLM system prompts — call this at session start to recall everything\n\n")
	sb.WriteString("**Workflow:** call `memory_context` at the start of every session, use `gleann_sync` after modifying files so code intelligence tools immediately reflect changes, then call `memory_remember` whenever you learn something important about the codebase.\n")

	return sb.String()
}

// cmdAgents generates or dumps AGENTS.md for AI coding agents.
//
// Usage:
//   gleann agents [dump] [--dir <dir>] [--index <name>] [--stdout] [--force]
func cmdAgents(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		printAgentsUsage()
		return
	}

	stdout := hasFlag(args, "--stdout")
	force := hasFlag(args, "--force")
	indexName := getFlag(args, "--index")
	dir := getFlag(args, "--docs")
	if dir == "" {
		dir = getFlag(args, "--dir")
	}
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot determine working directory: %v\n", err)
			os.Exit(1)
		}
	}
	absDir, err := filepath.Abs(dir)
	if err == nil {
		dir = absDir
	}

	// Auto-detect index name if not explicitly passed
	if indexName == "" {
		indexName = os.Getenv("GLEANN_INDEX")
	}
	if indexName == "" {
		config := getConfig(args)
		applySavedConfig(&config, args)
		indexes, _ := gleann.ListIndexes(config.IndexDir)
		dirBase := strings.ToLower(filepath.Base(dir))
		for _, idx := range indexes {
			if strings.EqualFold(idx.Name, dirBase) {
				indexName = idx.Name
				break
			}
		}
		if indexName == "" && len(indexes) == 1 {
			indexName = indexes[0].Name
		}
	}

	content := getAgentsMDContent(indexName)

	if stdout {
		fmt.Print(strings.TrimPrefix(content, "\n"))
		return
	}

	targetPath := filepath.Join(dir, "AGENTS.md")
	if force {
		if err := os.WriteFile(targetPath, []byte(strings.TrimPrefix(content, "\n")), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", targetPath, err)
			os.Exit(1)
		}
		fmt.Printf("✅ AGENTS.md overwritten in %s\n", targetPath)
		return
	}

	if err := appendOrCreateFile(targetPath, content, "gleann: Code Intelligence"); err != nil {
		fmt.Fprintf(os.Stderr, "error updating %s: %v\n", targetPath, err)
		os.Exit(1)
	}
	fmt.Printf("✅ AGENTS.md generated/updated in %s\n", targetPath)
}

func printAgentsUsage() {
	fmt.Println(`gleann agents — generate or dump AGENTS.md for AI coding agents

Usage:
  gleann agents [dump] [--dir <dir>] [--index <name>] [--stdout] [--force]

Options:
  --dir <dir>      Target directory where AGENTS.md will be written (default: current directory)
  --index <name>   Index name to embed in instructions (default: auto-detected from folder name)
  --stdout         Output markdown directly to stdout instead of writing to file
  --force          Overwrite existing AGENTS.md file completely

Examples:
  # Generate AGENTS.md in current directory
  gleann agents

  # Generate AGENTS.md for a specific index and directory
  gleann agents dump --dir /path/to/project --index myproject

  # Print AGENTS.md to stdout
  gleann agents --stdout`)
}
