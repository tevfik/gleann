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
case "rebuild":
cmdRebuild(args)
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
  gleann rebuild <name> --docs <dir>    Remove & rebuild index from scratch
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
  gleann serve --addr :8080

Setup Options:
  --bootstrap             Quick setup with auto-detected defaults (no TUI)
  --check                 Check if gleann is configured
  --host <url>            Ollama host for bootstrap mode (default: http://localhost:11434)`)
}
