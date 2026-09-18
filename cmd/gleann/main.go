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
		return
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
		cmdMCP(args)
	case "multimodal":
		cmdMultimodal(args)
	case "tui":
		cmdTUI()
	case "install":
		cmdInstall(args)
	case "uninstall":
		cmdUninstall(args)
	case "plugin", "plugins":
		cmdPlugin(args)
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
	case "benchmark", "bench":
		cmdBenchmark(args)
	case "tokens":
		cmdTokens(args)
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
	fmt.Println(`gleann — High-Performance AI Search, Code Intelligence & Governance

Usage:
  gleann <command> [arguments] [flags]

Core Commands:
  search <name> <query>        Semantic vector search across an index
  ask    <name> <question>     RAG question-answering with LLM synthesis
  chat   [name]                Interactive terminal chat session
  serve  [--addr :8080]        Start REST API & Web UI server
  mcp                          Start MCP server (stdio transport for AI agents)

Index Management & Governance:
  index build <name> --docs <dir> [--graph] [--multimodal-model <m>]
                               Index documents or code (optionally with AST graph)
  index list [--tag <t>] [--mcp]  List indexes with stats, tags, and MCP status
  index tag  <name> --add/--remove <tag>   Assign governance tags (e.g. work, private)
  index set  <name> [--mcp=true|false] [--desc "..."]
                               Configure MCP visibility and semantic description
  index watch <name> --docs <dir>  Auto-sync and rebuild index on file changes
  index remove <name>          Delete an index and all associated data

Code Intelligence (AST Graph):
  graph deps    <fqn> --index <name>  Show downstream dependencies of a symbol
  graph callers <fqn> --index <name>  Show callers / upstream references
  graph explain <fqn> --index <name>  Full symbol context and blast radius
  graph viz           --index <name>  Generate interactive HTML graph visualization
  graph report        --index <name>  Export comprehensive GRAPH_REPORT.md

Long-term Memory Engine:
  memory remember <text>       Store persistent facts into hierarchical memory
  memory search   <query>      Retrieve relevant memories across all tiers
  memory list [--tier <tier>]  Browse stored memories (short, medium, long)
  memory stats                 Show memory database statistics

Service & Setup:
  setup [--auto]               Interactive setup wizard (or zero-config auto)
  doctor                       System health and dependencies check
  service install|start|status Manage Gleann as a background system service

Examples:
  # 1. Index source code with call graph
  gleann index build core --docs ./src --graph

  # 2. Tag index and expose to MCP AI agents
  gleann index tag core --add backend --add work
  gleann index set core --mcp=true --desc "Core API server"

  # 3. Search and Ask
  gleann search core "rate limiting middleware" --rerank
  gleann ask core "Explain error handling patterns"

  # 4. Long-term memory
  gleann memory remember "Database migrations run via golang-migrate"
  gleann memory search "migration"

  # 5. Run Web UI and MCP server
  gleann serve --addr :8080
  gleann mcp

Run 'gleann <command> --help' for detailed subcommand flags and options.`)
}

