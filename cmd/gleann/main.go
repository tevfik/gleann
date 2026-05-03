package main

import (
	"fmt"
	"os"

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
	case "index":
		cmdIndex(args)
	case "search":
		cmdSearch(args)
	case "ask":
		cmdAsk(args)
	case "serve":
		cmdServe(args)
	case "graph":
		cmdGraph(args)
	case "chat":
		cmdChat(args)
	case "memory":
		cmdMemory(args)
	case "mcp":
		cmdMCP()
	case "multimodal":
		cmdMultimodal(args)
	case "tui":
		cmdTUI()
	case "install":
		cmdInstall(args)
	case "setup":
		cmdSetup(args)
	case "quickstart", "go":
		fmt.Fprintln(os.Stderr, "Warning: 'gleann go' and 'gleann quickstart' are deprecated.")
		fmt.Fprintln(os.Stderr, "Please use 'gleann setup --auto' for zero-friction onboarding.")
		os.Exit(1)
	case "service":
		cmdService(args)
	case "doctor":
		cmdDoctor()
	case "tasks":
		cmdTasks(args)
	case "benchmark":
		cmdBenchmark(args)
	case "config":
		cmdConfig(args)
	case "completion":
		cmdCompletion(args)
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
	fmt.Println(`gleann — AI-powered search, code analysis & long-term memory

gleann has three intelligence pillars that work together:

  ┌─ Document & Code Search ──────────────────────────────────────────┐
  │  Index any docs or source code, then search or ask questions.     │
  │  Memory context is automatically injected into every LLM query.  │
  └───────────────────────────────────────────────────────────────────┘
  ┌─ Code Intelligence (AST Graph) ───────────────────────────────────┐
  │  Build a call graph alongside your index. Trace dependencies,     │
  │  callers, and blast-radius across your codebase.                  │
  └───────────────────────────────────────────────────────────────────┘
  ┌─ Long-term Memory ────────────────────────────────────────────────┐
  │  Store persistent facts, preferences, and conversation summaries. │
  │  Injected automatically as context into ask, chat, and agents.   │
  └───────────────────────────────────────────────────────────────────┘

── Getting Started ───────────────────────────────────────────────────

  gleann setup --auto [--docs <dir>]     Zero to working in 90 seconds
  gleann setup                           Interactive configuration wizard
  gleann doctor                          System health check

── Document & Code Search ────────────────────────────────────────────

  gleann index  <sub> [args]            Manage indexes
  gleann search <name> <query>          Semantic search
  gleann ask    <name> <question>       RAG-powered Q&A (LLM answer from docs)
  gleann chat   [name]                  Interactive chat TUI

  gleann index subcommands:
    list                                List all indexes
    build  <name> --docs <dir>          Build index from documents
    build  <name> --docs <dir> --graph  Also build AST code graph
    rebuild <name> --docs <dir>         Remove & rebuild from scratch
    remove <name>                       Delete an index
    info   <name>                       Show index metadata
    watch  <name> --docs <dir>          Watch directory & auto-rebuild

── Code Intelligence ─────────────────────────────────────────────────

  Symbol navigation:
  gleann graph deps    <fqn> --index <name>         What does this symbol call?
  gleann graph callers <fqn> --index <name>         Who calls this symbol?
  gleann graph explain <fqn> --index <name>         Full context: edges, community, blast radius
  gleann graph path    <from> <to> --index <name>   Shortest dependency path between two symbols
  gleann graph query   <pattern> --index <name>     BFS neighborhood around a symbol (pattern match)

  Analysis & output:
  gleann graph viz         --index <name>           Interactive HTML call-graph (vis.js)
  gleann graph report      --index <name>           Markdown report (god nodes, communities)
  gleann graph communities --index <name>           Community detection results (stdout)
  gleann graph export      --index <name> --format <graphml|cypher>  Export for Gephi / Neo4j
  gleann graph wiki        --index <name>           Per-community wiki articles (Markdown)
  gleann graph hook        install|uninstall|status Git hook: auto-rebuild on commit

  Requires: gleann index build <name> --docs <dir> --graph

── Platform Integration ──────────────────────────────────────────────

  gleann install                    Auto-detect & install for AI platforms
  gleann install --platform <name>  Install for a specific platform
  gleann install --list             List supported platforms
  gleann install uninstall          Remove platform integration files

  Platforms: opencode, claude, cursor, codex, gemini, claw, aider, copilot

── Long-term Memory ──────────────────────────────────────────────────

  gleann memory remember <text>               Store important knowledge (long-term)
  gleann memory forget   <query-or-id>        Remove a memory
  gleann memory list     [--tier short|medium|long]  Browse stored memories
  gleann memory search   <query>              Full-text search across all tiers
  gleann memory add      <tier> <text>        Add a note to a specific tier
  gleann memory clear    [--tier <tier>]      Clear memories (tier or all)
  gleann memory stats                         Storage statistics
  gleann memory summarize --last              Auto-summarize last conversation into memory
  gleann memory summarize --id <conv-id>      Summarize a specific conversation
  gleann memory prune    [--age <duration>]   Remove old entries (e.g. 30d, 90d)
  gleann memory maintain                      Full maintenance pass (prune + archive)
  gleann memory context                       Show current compiled memory context

  Memory tiers:
    short   In-memory, session-scoped → auto-promoted to medium on chat exit
    medium  BBolt, daily summaries → auto-archived to long after 30 days
    long    BBolt, permanent facts, user preferences (never auto-deleted)

  Chat slash commands:
    /remember <text>   Store fact to long-term memory mid-conversation
    /forget <query>    Remove matching memories mid-conversation
    /memories          Browse stored memories
    /new               Start a fresh conversation thread

  Memory is automatically injected into every: ask, chat, mcp

── Conversation Management ───────────────────────────────────────────

  gleann chat --list                    List saved conversations
  gleann chat --pick                    Interactively pick a conversation
  gleann chat --show <id>               Show a conversation
  gleann chat --show-last               Show most recent conversation
  gleann chat --delete <id> [id...]     Delete conversations
  gleann chat --delete-older-than <d>   Delete by age (e.g. 7d, 2w, 30d)

── Infrastructure ────────────────────────────────────────────────────

  gleann serve  [--addr :8080]          REST API server (rate limiting, timeouts)
  gleann tasks                          View background tasks (requires serve)
  gleann benchmark --index <n> --docs <d>  Token reduction analysis
  gleann mcp                            MCP server (stdio, for AI editors)
  gleann tui                            Interactive TUI launcher
  gleann config <show|path|edit|validate>  Manage configuration
  gleann completion <bash|zsh|fish>     Shell completion script
  gleann version                        Show version

── Service Management ────────────────────────────────────────────────

  gleann service install                Install as OS service (auto-start on login)
  gleann service uninstall              Remove OS service
  gleann service start [--addr :8080]   Start server in background
  gleann service stop                   Stop running server
  gleann service restart                Restart server
  gleann service status                 Show server status
  gleann service logs [--lines 50]      Show server logs

  Platforms: Linux (systemd), macOS (launchd), Windows (Task Scheduler)

── Multimodal Analysis ───────────────────────────────────────────────

  gleann multimodal analyze <file>      Analyze a PDF, image, or video with vision LLM
  gleann multimodal analyze <dir>       Batch analyze all multimodal files in a directory
    --model <model>                     Ollama model (default: auto-detect or gemma4)
    --host <url>                        Ollama host (default: http://localhost:11434)

  Server env vars:
    GLEANN_RATE_LIMIT=60     Requests/sec per IP (token bucket)
    GLEANN_RATE_BURST=120    Per-IP burst capacity
    GLEANN_TIMEOUT_ASK_S=300 Timeout for /ask endpoints (seconds)
    GLEANN_MAINTENANCE_ENABLED=true  Background memory maintenance

── Common Options ────────────────────────────────────────────────────

  Embedding:
    --model <model>         Embedding model (default: bge-m3)
    --provider <provider>   ollama | openai (default: ollama)
    --host <url>            Ollama host (default: http://localhost:11434)

  Search:
    --top-k <n>             Results to retrieve (default: 10)
    --rerank                Two-stage reranking for higher accuracy
    --hybrid                Vector + BM25 hybrid search
    --graph                 Enrich results with code graph context

  LLM:
    --llm-model <model>     LLM model (default: llama3.2)
    --llm-provider <prov>   ollama | openai | anthropic
    --role <role>           System prompt role (code, shell, explain, ...)
    --format <fmt>          Output format: json | markdown | raw
    --no-limit              Remove output token limit

  Multimodal:
    --attach <file>         Attach image/audio for analysis (repeatable)
    --multimodal-model <m>  Model for media indexing (default: auto-detect)

  Ask/Chat:
    --continue <id>         Continue a previous conversation
    --continue-last         Continue most recent conversation
    --no-cache              Don't save conversation to history
    --quiet                 Suppress status messages

── Examples ──────────────────────────────────────────────────────────

  # Index and search documents
  gleann index build my-docs --docs ./documents/
  gleann index build my-docs --docs ./media/ --multimodal-model gemma4:e4b
  gleann search my-docs "How does authentication work?" --rerank
  gleann ask my-docs "Explain the architecture"

  # Index source code with call graph
  gleann index build my-code --docs ./src/ --graph
  gleann graph deps "github.com/org/pkg.Handler" --index my-code
  gleann graph callers "github.com/org/pkg.Handler" --index my-code

  # Graph analysis & visualization
  gleann graph viz --index my-code                     # interactive HTML
  gleann graph report --index my-code                  # GRAPH_REPORT.md
  gleann graph communities --index my-code             # community detection

  # Token reduction benchmark
  gleann benchmark --index my-code --docs ./src/

  # Long-term memory
  gleann memory remember "Project uses hexagonal architecture"
  gleann memory remember "Prefer snake_case for DB columns" --tag "preference"
  gleann memory search "architecture"
  gleann memory summarize --last
  gleann memory stats

  # Chat with memory-augmented context
  gleann chat my-docs       # memory auto-injected
  cat file.go | gleann ask my-code "Review this code"
  gleann ask my-code --continue-last "What about error handling?"

  # Multi-index search
  gleann search code,docs "rate limiter" --rerank

  # Multimodal (image/audio analysis during RAG)
  gleann ask my-docs "What's in this diagram?" --attach diagram.png
  gleann ask my-docs "Summarize this recording" --attach meeting.wav

  # Background tasks
  gleann tasks                          # list running tasks
  gleann tasks --status running         # filter by status

  # REST API / MCP
  gleann serve --addr :8080
  gleann mcp`)
}
