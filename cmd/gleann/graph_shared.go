package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// The graph subcommands below read through the gleann.GraphDB interface, so
// they work with both backends: the Kùzu sidecar of a format-1 index (needs
// -tags treesitter) and the SQLite code graph of a format-2 index, which is
// served in every build including CGO_ENABLED=0 (RFC 003 step 7).

// openGraphDBForCLI opens the graph of an index for a CLI query: the format-2
// SQLite graph first (any build), then the Kùzu sidecar via GraphDBOpener
// (injected only in treesitter builds).
func openGraphDBForCLI(config gleann.Config, indexName string) (gleann.GraphDB, error) {
	db, err := gleann.OpenGraphDB(config.IndexDir, indexName)
	if err != nil {
		return nil, err
	}
	if db != nil {
		return db, nil
	}
	// A format-2 index keeps its graph in <name>.db; never fall back to (and
	// thereby create) a Kùzu <name>_graph directory for it.
	if gleann.StoreFormatOf(filepath.Join(config.IndexDir, indexName, indexName)) == 2 {
		return nil, fmt.Errorf("index %q has no code graph yet; build it with: gleann index build %s --docs <dir> --graph", indexName, indexName)
	}
	if gleann.GraphDBOpener == nil {
		return nil, fmt.Errorf("index %q has no format-2 graph data; graph queries on format-1 (Kùzu) indexes require a build with -tags treesitter", indexName)
	}
	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	return gleann.GraphDBOpener(dbPath)
}

// runGraphSymbolCommand dispatches the symbol-argument subcommands against
// any GraphDB backend. args is the full argument list after "graph".
func runGraphSymbolCommand(subCmd string, args []string, symbol string, db gleann.GraphDB) {
	switch subCmd {
	case "deps":
		callees, err := db.Callees(symbol)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🕸️  Dependencies for %s (%d):\n", symbol, len(callees))
		for _, c := range callees {
			fmt.Printf("  → [%s] %s\n", c.Kind, c.FQN)
		}
	case "callers":
		callers, err := db.Callers(symbol)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🕸️  Callers of %s (%d):\n", symbol, len(callers))
		for _, c := range callers {
			fmt.Printf("  ← [%s] %s\n", c.Kind, c.FQN)
		}
	case "explain":
		cmdGraphExplain(symbol, db)
	case "path":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "error: graph path requires two symbols: graph path <from> <to> --index <name>")
			os.Exit(1)
		}
		target := args[2]
		cmdGraphPath(symbol, target, db)
	case "query":
		depth := 2
		if d := getFlag(args, "--depth"); d != "" {
			fmt.Sscanf(d, "%d", &depth)
		}
		cmdGraphQuery(symbol, depth, db)
	default:
		fmt.Fprintf(os.Stderr, "unknown graph command: %s\n", subCmd)
		printGraphUsage()
		os.Exit(1)
	}
}

// cmdGraphBuild rebuilds the graph of an existing index (format-2 SQLite in
// every build; Kùzu only with -tags treesitter).
func cmdGraphBuild(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	docsDir := getFlag(args, "--docs")
	if indexName == "" {
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				indexName = a
				break
			}
		}
	}
	if docsDir == "" && indexName != "" {
		if meta, err := gleann.GetIndexMeta(config.IndexDir, indexName); err == nil && meta.SourceDir != "" {
			docsDir = meta.SourceDir
		}
	}
	if indexName == "" || docsDir == "" {
		fmt.Fprintln(os.Stderr, "usage: gleann graph build <name> [--docs <dir>] [--include-submodules]")
		os.Exit(1)
	}
	includeSubmodules := hasFlag(args, "--include-submodules")
	buildGraphIndex(indexName, docsDir, config.IndexDir, nil, nil, includeSubmodules)
}

// cmdGraphExplain shows full context for a symbol: edges, community, impact.
func cmdGraphExplain(fqn string, db gleann.GraphDB) {
	// Search for exact or partial match
	results, err := db.SymbolSearch(fqn)
	if err != nil || len(results) == 0 {
		// Try exact match
		callees, _ := db.Callees(fqn)
		callers, _ := db.Callers(fqn)
		if len(callees) == 0 && len(callers) == 0 {
			fmt.Fprintf(os.Stderr, "Symbol %q not found. Try 'gleann graph query %q --index <name>' to search.\n", fqn, fqn)
			os.Exit(1)
		}
		// Print what we have
		fmt.Printf("🔍 Explain: %s\n\n", fqn)
		if len(callees) > 0 {
			fmt.Printf("  Calls (%d):\n", len(callees))
			for _, c := range callees {
				fmt.Printf("    → [%s] %s\n", c.Kind, c.FQN)
			}
		}
		if len(callers) > 0 {
			fmt.Printf("  Called by (%d):\n", len(callers))
			for _, c := range callers {
				fmt.Printf("    ← [%s] %s\n", c.Kind, c.FQN)
			}
		}
		memories := searchLinkedMemories(fqn)
		if len(memories) > 0 {
			fmt.Printf("\n  🧠 Linked Memory & Architectural Context (%d):\n", len(memories))
			for _, m := range memories {
				content := strings.ReplaceAll(m.Content, "\n", " ")
				if len(content) > 100 {
					content = content[:97] + "..."
				}
				fmt.Printf("    • [%s] %s\n", m.Tier, content)
			}
		}
		return
	}

	// Use first exact match or closest
	target := results[0]
	for _, r := range results {
		if r.FQN == fqn {
			target = r
			break
		}
	}
	fmt.Printf("🔍 Explain: %s\n", target.FQN)
	fmt.Printf("   Kind: %s\n", target.Kind)

	// Callees
	callees, _ := db.Callees(target.FQN)
	fmt.Printf("\n  Calls (%d):\n", len(callees))
	for _, c := range callees {
		fmt.Printf("    → [%s] %s\n", c.Kind, c.FQN)
	}

	// Callers
	callers, _ := db.Callers(target.FQN)
	fmt.Printf("\n  Called by (%d):\n", len(callers))
	for _, c := range callers {
		fmt.Printf("    ← [%s] %s\n", c.Kind, c.FQN)
	}

	// Impact
	impact, err := db.Impact(target.FQN, 3)
	if err == nil && len(impact.AffectedFiles) > 0 {
		fmt.Printf("\n  Impact (blast radius, depth=3):\n")
		fmt.Printf("    Direct callers:     %d\n", len(impact.DirectCallers))
		fmt.Printf("    Transitive callers: %d\n", len(impact.TransitiveCallers))
		fmt.Printf("    Affected files:     %d\n", len(impact.AffectedFiles))
		maxShow := 20
		for i, f := range impact.AffectedFiles {
			if i >= maxShow {
				fmt.Printf("      ... and %d more files\n", len(impact.AffectedFiles)-maxShow)
				break
			}
			fmt.Printf("      📄 %s\n", f)
		}
	}

	// Linked Memory Context
	memories := searchLinkedMemories(target.FQN)
	if len(memories) > 0 {
		fmt.Printf("\n  🧠 Linked Memory & Architectural Context (%d):\n", len(memories))
		for _, m := range memories {
			content := strings.ReplaceAll(m.Content, "\n", " ")
			if len(content) > 100 {
				content = content[:97] + "..."
			}
			fmt.Printf("    • [%s] %s\n", m.Tier, content)
		}
	}
}

// searchLinkedMemories queries the memory engine for notes, decisions, or rules related to a symbol.
func searchLinkedMemories(fqn string) []memory.Block {
	parts := strings.Split(fqn, ".")
	searchTerm := parts[len(parts)-1]
	if len(parts) > 1 && len(searchTerm) < 4 {
		searchTerm = parts[len(parts)-2] + " " + searchTerm
	}

	if rc := remoteMemoryClient(); rc != nil {
		blocks, err := rc.Search(searchTerm)
		if err == nil && len(blocks) > 0 {
			return blocks
		}
	}

	mgr, err := memory.DefaultManager()
	if err != nil {
		return nil
	}
	defer mgr.Close()
	blocks, _ := mgr.Search(searchTerm)
	return blocks
}

// cmdGraphPath finds and prints the shortest path between two symbols.
func cmdGraphPath(from, to string, db gleann.GraphDB) {
	path, err := db.ShortestPath(from, to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("🛤️  Shortest path: %s → %s (%d hops)\n\n", from, to, len(path))
	for i, step := range path {
		arrow := "→"
		if step.Relation == "CALLED_BY" {
			arrow = "←"
		}
		fmt.Printf("  %d. %s %s [%s] %s\n", i+1, step.From, arrow, step.Relation, step.To)
	}
}

// cmdGraphQuery searches for symbols and shows their neighborhood.
func cmdGraphQuery(pattern string, depth int, db gleann.GraphDB) {
	results, err := db.SymbolSearch(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Printf("No symbols matching %q found.\n", pattern)
		return
	}

	fmt.Printf("🔎 Symbols matching %q (%d results):\n\n", pattern, len(results))
	for _, r := range results {
		fmt.Printf("  [%s] %s\n", r.Kind, r.FQN)
	}

	// Show neighborhood for the first result
	if len(results) > 0 {
		target := results[0]
		edges, err := db.Neighbors(target.FQN, depth)
		if err == nil && len(edges) > 0 {
			fmt.Printf("\n  Neighborhood of %s (depth=%d, %d edges):\n", target.FQN, depth, len(edges))
			for _, e := range edges {
				fmt.Printf("    %s -[%s]-> %s\n", shortFQN(e.From), e.Relation, shortFQN(e.To))
			}
		}
	}
}

// shortFQN extracts the last segment of an FQN for compact display.
func shortFQN(fqn string) string {
	if i := lastIndex(fqn, '/'); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

func lastIndex(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// cmdGraphTOC inspects hierarchical Table of Contents (TOC) or lists indexed documents.
func cmdGraphTOC(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required for graph toc")
		os.Exit(1)
	}

	jsonOutput := hasFlag(args, "--json")

	// Extract positional document path if any (skipping known flag values)
	var docPath string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if arg == "--index" || arg == "--config" || arg == "--output" {
				i++
			}
			continue
		}
		docPath = arg
		break
	}

	db, err := openGraphDBForCLI(config, indexName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if docPath != "" {
		toc, err := db.DocumentTOC(docPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error getting document TOC: %v\n", err)
			os.Exit(1)
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(toc, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Println()
		fmt.Printf("📄 Document: %s\n", toc.Title)
		fmt.Printf("📍 Path:     %s\n", toc.VPath)
		if toc.Folder != "" {
			fmt.Printf("📁 Folder:   %s\n", toc.Folder)
		}
		if toc.Summary != "" {
			fmt.Printf("📝 Summary:  %s\n", toc.Summary)
		}
		fmt.Printf("🔢 Headings: %d\n\n", toc.TotalNodes)
		fmt.Println("Hierarchical Outline:")
		if len(toc.Headings) == 0 {
			fmt.Println("  (No headings indexed for this document)")
		} else {
			printHeadingTree(toc.Headings, "")
		}
		fmt.Println()
		return
	}

	// List all documents
	docs, err := db.ListDocuments()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing documents: %v\n", err)
		os.Exit(1)
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(docs, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Println()
	fmt.Printf("📚 Indexed Documents in %q (%d total):\n\n", indexName, len(docs))
	if len(docs) == 0 {
		fmt.Println("  (No documents found in graph index)")
		fmt.Println()
		return
	}

	for i, d := range docs {
		isLast := i == len(docs)-1
		connector := "├── "
		subPrefix := "│   "
		if isLast {
			connector = "└── "
			subPrefix = "    "
		}
		folder := ""
		if d.Folder != "" {
			folder = fmt.Sprintf(" [%s]", d.Folder)
		}
		fmt.Printf("%s📄 %s (%d headings)%s\n", connector, d.VPath, d.TotalNodes, folder)
		if d.Summary != "" {
			fmt.Printf("%s📝 %s\n", subPrefix, truncStr(d.Summary, 90))
		}
	}
	fmt.Println()
}

func printHeadingTree(headings []gleann.DocumentHeadingItem, prefix string) {
	for i, h := range headings {
		isLast := i == len(headings)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}
		icon := "📌"
		if h.Level == 2 {
			icon = "📎"
		} else if h.Level >= 3 {
			icon = "▫️"
		}
		fmt.Printf("%s%s%s %s (H%d)\n", prefix, connector, icon, h.Title, h.Level)
		if len(h.Children) > 0 {
			printHeadingTree(h.Children, childPrefix)
		}
	}
}

func printGraphUsage() {
	fmt.Println(`gleann graph — Code graph analysis & visualization

Usage:
  gleann graph deps    <fqn> --index <name>    What does this symbol call?
  gleann graph callers <fqn> --index <name>    Who calls this symbol?
  gleann graph explain <fqn> --index <name>    Full node context (edges, community, impact)
  gleann graph path    <from> <to> --index <name>  Shortest path between two symbols
  gleann graph query   <pattern> --index <name> [--depth N]
      Neighborhood traversal (BFS) around a symbol

  gleann graph toc     [doc_path] --index <name> [--json]
      Inspect hierarchical Table of Contents (TOC) & heading tree for documents

  gleann graph build   <name> [--docs <dir>] [--include-submodules]
      Rebuild the graph of an existing index

  gleann graph viz         --index <name> [--output <file.html>]
      Generate interactive HTML graph visualization (vis.js)

  gleann graph report      --index <name> [--output <file.md>] [--force]
      Generate Markdown graph report with communities, god nodes.
      Default output is ./GRAPH_REPORT.md; the report of another index
      is not overwritten unless --force is given.

  gleann graph communities --index <name>
      Print community detection results to stdout

  gleann graph export      --index <name> --format <graphml|cypher> [--output <file>]
      Export graph to GraphML (Gephi/yEd) or Neo4j Cypher

  gleann graph wiki        --index <name> [--output <dir>]
      Generate per-community wiki articles (Markdown)

  gleann graph hook install|uninstall|status
      Manage git hooks for automatic graph rebuild on commit

  gleann graph map         --index <name> [--top N]
      Compact repo map: most important symbols by PageRank, grouped by file

  gleann graph risk        --index <name> [--top N] [--by-file]
      Risk analysis: centrality × coupling × blast radius scoring

Build requirements:
  deps/callers/explain/path/query/toc/build work in EVERY build (including
  CGO_ENABLED=0) for format-2 indexes (GLEANN_STORE_FORMAT=2), whose graph
  lives in the index's own SQLite database. Format-1 (Kùzu) indexes and the
  viz/report/communities/export/wiki/hook/map/risk commands require
  -tags treesitter.

Requires: gleann index build <name> --docs <dir> --graph

Examples:
  gleann graph toc --index teydep
  gleann graph toc docs/architecture.md --index my-docs
  gleann graph deps "pkg.Handler" --index my-code
  gleann graph callers "pkg.Handler" --index my-code
  gleann graph explain "pkg.Handler" --index my-code
  gleann graph path "main.main" "pkg.Handler" --index my-code
  gleann graph query "Handler" --index my-code --depth 3
  gleann graph viz --index my-code
  gleann graph report --index my-code
  gleann graph export --index my-code --format graphml --output graph.graphml
  gleann graph export --index my-code --format cypher --output cypher.txt
  gleann graph wiki --index my-code --output wiki/
  gleann graph hook install`)
}
