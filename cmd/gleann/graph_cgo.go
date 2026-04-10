//go:build treesitter

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/internal/graph/indexer"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/internal/graph/report"
	"github.com/tevfik/gleann/internal/graph/viz"
	"github.com/tevfik/gleann/pkg/gleann"
)

func init() {
	gleann.GraphDBOpener = func(dir string) (gleann.GraphDB, error) {
		return kgraph.Open(dir)
	}
}

// buildGraphIndex builds the AST graph index for the given directory
// and writes document graph nodes/edges for plugin-extracted documents.
// Only available when built with -tags treesitter (requires CGo + KuzuDB).
//
// If changedFiles is non-empty, only those files are re-indexed (incremental mode).
// If changedFiles is nil or empty, a full re-index of the directory is performed.
func buildGraphIndex(name, docsDir, indexDir string, pluginDocs []*PluginDoc, changedFiles []string) {
	// Resolve docsDir to absolute path to avoid cwd-dependent issues.
	absDocsDir, err := filepath.Abs(docsDir)
	if err != nil {
		absDocsDir = docsDir
	}

	fmt.Printf("🕸️  Building API Graph Index from %s...\n", absDocsDir)
	graphStart := time.Now()

	dbPath := filepath.Join(indexDir, name+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not initialize kuzu graph db: %v\n", err)
		return
	}
	defer db.Close()

	// Detect Go module name from go.mod in the docs directory.
	module := indexer.DetectGoModule(absDocsDir)

	// 1. AST code indexing.
	idx := indexer.New(db, module, absDocsDir)
	if len(changedFiles) > 0 {
		// Incremental mode: only re-index changed files.
		fmt.Printf("🔄 Incremental graph update: %d changed files\n", len(changedFiles))
		if err := idx.IndexFiles(changedFiles); err != nil {
			fmt.Fprintf(os.Stderr, "warning: incremental graph indexing failed: %v\n", err)
		} else {
			fmt.Printf("✅ Incremental Graph Index updated in %s\n", time.Since(graphStart).Round(time.Millisecond))
		}
	} else {
		// Full re-index.
		if err := idx.IndexDir(absDocsDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: graph indexing failed: %v\n", err)
		} else {
			fmt.Printf("✅ Code Graph Index built in %s\n", time.Since(graphStart).Round(time.Millisecond))
		}
	}

	// 2. Document graph indexing (plugin-extracted documents).
	if len(pluginDocs) > 0 {
		docStart := time.Now()
		docIdx := indexer.NewDocIndexer(db, 512, 64)

		// Batch all documents into a single DB transaction for performance.
		inputs := make([]*indexer.DocGraphInput, len(pluginDocs))
		for i, pd := range pluginDocs {
			inputs[i] = &indexer.DocGraphInput{
				Result:     pd.Result,
				SourcePath: pd.SourcePath,
			}
		}
		if err := docIdx.WriteGraphBatch(inputs); err != nil {
			fmt.Fprintf(os.Stderr, "warning: doc graph batch indexing failed: %v\n", err)
		}
		fmt.Printf("📄 Document Graph: %d documents indexed in %s\n", len(pluginDocs), time.Since(docStart).Round(time.Millisecond))
	}
}

// cmdGraph implements the `gleann graph <subcommand>` command.
// Only available when built with -tags treesitter.
func cmdGraph(args []string) {
	if len(args) < 1 {
		printGraphUsage()
		os.Exit(1)
	}

	subCmd := args[0]
	config := getConfig(args)

	// Subcommands that don't need a symbol argument.
	switch subCmd {
	case "viz":
		cmdGraphViz(args[1:], config)
		return
	case "report":
		cmdGraphReport(args[1:], config)
		return
	case "communities":
		cmdGraphCommunities(args[1:], config)
		return
	case "help", "--help":
		printGraphUsage()
		return
	}

	// Subcommands that need a symbol argument.
	if len(args) < 2 {
		printGraphUsage()
		os.Exit(1)
	}
	symbol := args[1]

	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required for graph queries")
		os.Exit(1)
	}

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer db.Close()

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
	default:
		fmt.Fprintf(os.Stderr, "unknown graph command: %s\n", subCmd)
		printGraphUsage()
		os.Exit(1)
	}
}

// cmdGraphViz generates an interactive HTML visualization.
func cmdGraphViz(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		os.Exit(1)
	}

	output := getFlag(args, "--output")
	if output == "" {
		output = indexName + "_graph.html"
	}

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("🕸️  Running community detection...")
	start := time.Now()
	result, err := community.FromKuzu(db, 5, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   %d communities, %d god nodes, modularity=%.3f (%s)\n",
		len(result.Communities), len(result.GodNodes), result.Modularity,
		time.Since(start).Round(time.Millisecond))

	// Re-load graph for viz (community.FromKuzu doesn't expose the graph).
	g := community.NewGraph()
	loadGraphForViz(db, g)

	f, err := os.Create(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	opts := viz.DefaultOptions()
	opts.Title = fmt.Sprintf("gleann — %s", indexName)
	if err := viz.RenderHTML(f, g, result, opts); err != nil {
		fmt.Fprintf(os.Stderr, "error rendering HTML: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Graph visualization saved to %s\n", output)
}

// cmdGraphReport generates a Markdown graph report.
func cmdGraphReport(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		os.Exit(1)
	}

	output := getFlag(args, "--output")
	if output == "" {
		output = "GRAPH_REPORT.md"
	}

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("📊 Running community detection for report...")
	result, err := community.FromKuzu(db, 5, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	f, err := os.Create(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	opts := report.Options{
		IndexName: indexName,
		DocsDir:   getFlag(args, "--docs"),
	}
	if err := report.WriteMarkdown(f, result, opts); err != nil {
		fmt.Fprintf(os.Stderr, "error writing report: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Graph report saved to %s\n", output)
}

// cmdGraphCommunities prints community detection results to stdout.
func cmdGraphCommunities(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		os.Exit(1)
	}

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	result, err := community.FromKuzu(db, 5, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("🕸️  Community Detection Results\n")
	fmt.Printf("   Nodes: %d | Edges: %d | Communities: %d | Modularity: %.4f\n\n",
		result.NodeCount, result.EdgeCount, len(result.Communities), result.Modularity)

	for _, c := range result.Communities {
		fmt.Printf("Community %d: %s (%d nodes, cohesion=%.3f)\n", c.ID, c.Label, c.NodeCount, c.Cohesion)
	}

	if len(result.GodNodes) > 0 {
		fmt.Printf("\n🔴 God Nodes (high-degree hubs):\n")
		for _, g := range result.GodNodes {
			fmt.Printf("  [%s] %s (in=%d, out=%d, total=%d)\n", g.Kind, g.Name, g.InDeg, g.OutDeg, g.TotalDeg)
		}
	}

	if len(result.SurprisingEdges) > 0 {
		fmt.Printf("\n⚡ Surprising cross-community edges:\n")
		for _, e := range result.SurprisingEdges {
			fmt.Printf("  %s → %s (community %d → %d)\n", e.From, e.To, e.FromCommunity, e.ToCommunity)
		}
	}
}

// loadGraphForViz reloads graph data from KuzuDB into the community.Graph for visualization.
func loadGraphForViz(db *kgraph.DB, g *community.Graph) {
	// Load symbols.
	res, err := db.Conn().Query(`MATCH (s:Symbol) RETURN s.fqn AS fqn, s.name AS name, s.kind AS kind, s.file AS file`)
	if err == nil {
		for res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			g.AddNode(community.Node{
				ID:   fmt.Sprint(m["fqn"]),
				Name: fmt.Sprint(m["name"]),
				Kind: fmt.Sprint(m["kind"]),
				File: fmt.Sprint(m["file"]),
			})
		}
		res.Close()
	}
	// Load calls.
	res2, err := db.Conn().Query(`MATCH (a:Symbol)-[:CALLS]->(b:Symbol) RETURN a.fqn AS from, b.fqn AS to`)
	if err == nil {
		for res2.HasNext() {
			row, _ := res2.Next()
			m, _ := row.GetAsMap()
			g.AddEdge(fmt.Sprint(m["from"]), fmt.Sprint(m["to"]), 1.0)
		}
		res2.Close()
	}
}

func printGraphUsage() {
	fmt.Println(`gleann graph — Code graph analysis & visualization

Usage:
  gleann graph deps    <fqn> --index <name>    What does this symbol call?
  gleann graph callers <fqn> --index <name>    Who calls this symbol?

  gleann graph viz         --index <name> [--output <file.html>]
      Generate interactive HTML graph visualization (vis.js)

  gleann graph report      --index <name> [--output <file.md>]
      Generate Markdown graph report with communities, god nodes

  gleann graph communities --index <name>
      Print community detection results to stdout

Requires: gleann index build <name> --docs <dir> --graph

Examples:
  gleann graph deps "pkg.Handler" --index my-code
  gleann graph callers "pkg.Handler" --index my-code
  gleann graph viz --index my-code
  gleann graph viz --index my-code --output project_graph.html
  gleann graph report --index my-code
  gleann graph communities --index my-code`)
}
