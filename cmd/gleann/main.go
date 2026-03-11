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
	case "mcp":
		cmdMCP()
	case "tui":
		cmdTUI()
	case "setup":
		cmdSetup()
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
	fmt.Println(`gleann — Lightweight Vector Database with Graph-Based Recomputation

Usage:
  gleann ask    <name> <question>       Ask a question (RAG Q&A)
  gleann chat   [name]                  Interactive chat TUI / conversation management
  gleann search <name> <query>          Search an index
  gleann index  <sub> [args]            Manage indexes (list, build, remove, rebuild, info, watch)
  gleann graph  <deps|callers> <sym>    Query AST Graph in KuzuDB
  gleann serve  [--addr :8080]          Start REST API server
  gleann mcp                            Start MCP server (stdio, for AI editors)
  gleann tui                            Launch interactive TUI
  gleann setup                          Run configuration wizard
  gleann config  <show|path|edit|validate>  Manage configuration
  gleann completion <bash|zsh|fish>      Output shell completion script
  gleann version                        Show version

Index Management (gleann index <sub>):
  list                                  List all indexes
  build  <name> --docs <dir> [--graph]  Build index from documents
  remove <name>                         Remove an index
  rebuild <name> --docs <dir> [--graph] Remove & rebuild index from scratch
  info   <name>                         Show index metadata
  watch  <name> --docs <dir> [--graph]  Watch & auto-rebuild on changes

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
  --graph                 Enrich results with graph context (callers/callees)
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

Ask & Chat Options:
  --continue <id>         Continue a previous conversation
  --continue-last         Continue the most recent conversation
  --title <title>         Set conversation title
  --role <role>           Use a named role (e.g. code, shell, explain)
  --format <fmt>          Output format: json, markdown, raw
  --raw                   Output raw text (no markdown rendering); auto-enabled when piped
  --quiet                 Suppress status messages
  --word-wrap <n>         Wrap output at N columns (default: terminal width)
  --no-cache              Don't save conversation to history
  --no-limit              Remove token limit (unlimited output)

Chat Conversation Management (gleann chat ...):
  --list                  List saved conversations
  --pick                  Interactively pick a conversation (arrow keys)
  --show <id>             Show a specific conversation
  --show-last             Show the most recent conversation
  --delete <id> [id...]   Delete specific conversations
  --delete-older-than <d> Delete conversations older than duration (e.g. 7d, 2w)

Config Subcommands (gleann config <sub>):
  show                    Show current configuration (JSON)
  path                    Print config file path
  edit                    Open config in $EDITOR
  validate                Check config syntax and values

Graph Options:
  --index <name>          Index name for graph queries (required for graph command)

Examples:
  gleann index list
  gleann index build my-docs --docs ./documents/
  gleann index build my-code --docs ./src/ --graph
  gleann index watch my-code --docs ./src/ --graph
  gleann search my-docs "How does caching work?" --rerank
  gleann ask my-docs "Explain the architecture" --interactive
  gleann ask my-docs "Summarize this" --role code --format json
  gleann ask my-docs "long answer" --no-limit
  cat file.go | gleann ask my-code "Review this code"
  gleann ask my-code --continue-last "What about error handling?"
  gleann chat --list
  gleann chat --delete-older-than 30d
  gleann chat my-docs
  gleann graph deps main.handleRequest --index my-code
  gleann config show
  gleann config edit
  gleann tui
  gleann serve --addr :8080

Setup Options:
  --bootstrap             Quick setup with auto-detected defaults (no TUI)
  --check                 Check if gleann is configured
  --host <url>            Ollama host for bootstrap mode (default: http://localhost:11434)`)
}
