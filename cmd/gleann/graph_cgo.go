//go:build treesitter

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	case "export":
		cmdGraphExport(args[1:], config)
		return
	case "wiki":
		cmdGraphWiki(args[1:], config)
		return
	case "hook":
		cmdGraphHook(args[1:], config)
		return
	case "map":
		cmdGraphMap(args[1:], config)
		return
	case "risk":
		cmdGraphRisk(args[1:], config)
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

// cmdGraphExplain shows full context for a symbol: edges, community, impact.
func cmdGraphExplain(fqn string, db *kgraph.DB) {
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
		for _, f := range impact.AffectedFiles {
			fmt.Printf("      📄 %s\n", f)
		}
	}
}

// cmdGraphPath finds and prints the shortest path between two symbols.
func cmdGraphPath(from, to string, db *kgraph.DB) {
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
func cmdGraphQuery(pattern string, depth int, db *kgraph.DB) {
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

// cmdGraphExport exports the graph to GraphML or Neo4j Cypher format.
func cmdGraphExport(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		os.Exit(1)
	}
	format := getFlag(args, "--format")
	if format == "" {
		fmt.Fprintln(os.Stderr, "error: --format flag required (graphml or cypher)")
		os.Exit(1)
	}

	output := getFlag(args, "--output")

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	g := community.NewGraph()
	loadGraphForViz(db, g)

	switch format {
	case "graphml":
		if output == "" {
			output = indexName + "_graph.graphml"
		}
		f, err := os.Create(output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		exportGraphML(f, g)
		fmt.Printf("✅ GraphML exported to %s\n", output)

	case "cypher":
		if output == "" {
			output = indexName + "_cypher.txt"
		}
		f, err := os.Create(output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		exportCypher(f, g)
		fmt.Printf("✅ Neo4j Cypher exported to %s\n", output)

	default:
		fmt.Fprintf(os.Stderr, "error: unsupported format %q (use graphml or cypher)\n", format)
		os.Exit(1)
	}
}

// exportGraphML writes the graph in GraphML format (compatible with Gephi, yEd).
func exportGraphML(w io.Writer, g *community.Graph) {
	fmt.Fprintln(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintln(w, `<graphml xmlns="http://graphml.graphstruct.org/graphml"`)
	fmt.Fprintln(w, `  xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`)
	fmt.Fprintln(w, `  xsi:schemaLocation="http://graphml.graphstruct.org/graphml http://graphml.graphstruct.org/xmlns/1.0/graphml.xsd">`)
	fmt.Fprintln(w, `  <key id="d0" for="node" attr.name="label" attr.type="string"/>`)
	fmt.Fprintln(w, `  <key id="d1" for="node" attr.name="kind" attr.type="string"/>`)
	fmt.Fprintln(w, `  <key id="d2" for="node" attr.name="file" attr.type="string"/>`)
	fmt.Fprintln(w, `  <key id="d3" for="edge" attr.name="relation" attr.type="string"/>`)
	fmt.Fprintln(w, `  <graph id="G" edgemode="directed">`)

	for _, id := range g.NodeIDs() {
		n := g.GetNode(id)
		if n == nil {
			continue
		}
		fmt.Fprintf(w, "    <node id=%q>\n", id)
		fmt.Fprintf(w, "      <data key=\"d0\">%s</data>\n", xmlEscape(n.Name))
		fmt.Fprintf(w, "      <data key=\"d1\">%s</data>\n", xmlEscape(n.Kind))
		fmt.Fprintf(w, "      <data key=\"d2\">%s</data>\n", xmlEscape(n.File))
		fmt.Fprintln(w, "    </node>")
	}

	for i, e := range g.Edges() {
		fmt.Fprintf(w, "    <edge id=\"e%d\" source=%q target=%q>\n", i, e.From, e.To)
		fmt.Fprintf(w, "      <data key=\"d3\">CALLS</data>\n")
		fmt.Fprintln(w, "    </edge>")
	}

	fmt.Fprintln(w, `  </graph>`)
	fmt.Fprintln(w, `</graphml>`)
}

// exportCypher writes Neo4j Cypher CREATE statements.
func exportCypher(w io.Writer, g *community.Graph) {
	fmt.Fprintln(w, "// Neo4j Cypher import — generated by gleann")
	fmt.Fprintln(w, "// Run: cat cypher.txt | cypher-shell -u neo4j -p password")
	fmt.Fprintln(w, "")

	for _, id := range g.NodeIDs() {
		n := g.GetNode(id)
		if n == nil {
			continue
		}
		label := "Symbol"
		if n.Kind == "file" {
			label = "CodeFile"
		}
		fmt.Fprintf(w, "MERGE (n:%s {fqn: %q}) SET n.name = %q, n.kind = %q, n.file = %q;\n",
			label, id, n.Name, n.Kind, n.File)
	}

	fmt.Fprintln(w, "")
	for _, e := range g.Edges() {
		fmt.Fprintf(w, "MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q}) MERGE (a)-[:CALLS]->(b);\n",
			e.From, e.To)
	}
}

// xmlEscape escapes special XML characters.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// cmdGraphWiki generates per-community wiki articles.
func cmdGraphWiki(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		os.Exit(1)
	}

	output := getFlag(args, "--output")
	if output == "" {
		output = "wiki"
	}

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("📚 Generating wiki articles...")
	result, err := community.FromKuzu(db, 5, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Re-load graph for wiki generation.
	g := community.NewGraph()
	loadGraphForViz(db, g)

	if err := os.MkdirAll(output, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating wiki dir: %v\n", err)
		os.Exit(1)
	}

	// Generate index.md
	indexFile, err := os.Create(filepath.Join(output, "index.md"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(indexFile, "# %s — Knowledge Wiki\n\n", indexName)
	fmt.Fprintf(indexFile, "Auto-generated graph wiki. %d communities, %d symbols, modularity=%.3f\n\n",
		len(result.Communities), result.NodeCount, result.Modularity)
	fmt.Fprintln(indexFile, "## Communities")
	fmt.Fprintln(indexFile)
	for _, c := range result.Communities {
		slug := strings.ReplaceAll(strings.ToLower(c.Label), " ", "-")
		fmt.Fprintf(indexFile, "- [%s](community-%s.md) — %d symbols, cohesion=%.3f\n",
			c.Label, slug, c.NodeCount, c.Cohesion)
	}
	if len(result.GodNodes) > 0 {
		fmt.Fprintln(indexFile)
		fmt.Fprintln(indexFile, "## God Nodes (Hub Symbols)")
		fmt.Fprintln(indexFile)
		for _, gn := range result.GodNodes {
			fmt.Fprintf(indexFile, "- **%s** [%s] — degree %d\n", gn.Name, gn.Kind, gn.TotalDeg)
		}
	}
	indexFile.Close()

	// Generate per-community articles
	for _, c := range result.Communities {
		slug := strings.ReplaceAll(strings.ToLower(c.Label), " ", "-")
		fname := filepath.Join(output, fmt.Sprintf("community-%s.md", slug))
		f, err := os.Create(fname)
		if err != nil {
			continue
		}
		fmt.Fprintf(f, "# Community: %s\n\n", c.Label)
		fmt.Fprintf(f, "**%d symbols** | Cohesion: %.3f\n\n", c.NodeCount, c.Cohesion)
		fmt.Fprintln(f, "## Symbols")
		fmt.Fprintln(f)
		fmt.Fprintln(f, "| Symbol | Kind |")
		fmt.Fprintln(f, "|--------|------|")
		for _, nid := range c.Nodes {
			n := g.GetNode(nid)
			kind := ""
			if n != nil {
				kind = n.Kind
			}
			fmt.Fprintf(f, "| `%s` | %s |\n", shortFQN(nid), kind)
		}

		// Cross-community edges
		var crossEdges []community.SurprisingEdge
		for _, se := range result.SurprisingEdges {
			if se.FromCommunity == c.ID || se.ToCommunity == c.ID {
				crossEdges = append(crossEdges, se)
			}
		}
		if len(crossEdges) > 0 {
			fmt.Fprintln(f)
			fmt.Fprintln(f, "## External Connections")
			fmt.Fprintln(f)
			for _, e := range crossEdges {
				fmt.Fprintf(f, "- `%s` ↔ `%s`\n", shortFQN(e.From), shortFQN(e.To))
			}
		}

		fmt.Fprintf(f, "\n---\n[← Back to Index](index.md)\n")
		f.Close()
	}

	fmt.Printf("✅ Wiki generated: %d articles in %s/\n", len(result.Communities)+1, output)
}

// cmdGraphHook manages git hooks for automatic graph rebuild on commit.
func cmdGraphHook(args []string, config gleann.Config) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: gleann graph hook <install|uninstall|status>")
		os.Exit(1)
	}

	// Find git root
	gitDir := ".git/hooks"
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "error: not in a git repository")
		os.Exit(1)
	}

	hookPath := filepath.Join(gitDir, "post-commit")

	switch args[0] {
	case "install":
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		hookScript := `#!/bin/sh
# gleann: auto-rebuild graph index after commit
# Installed by: gleann graph hook install
# Remove with:  gleann graph hook uninstall

GLEANN_BIN=$(command -v gleann || echo "")
if [ -z "$GLEANN_BIN" ]; then
    # Try common build locations
    for p in ./build/gleann-full ./build/gleann gleann; do
        if [ -x "$p" ]; then
            GLEANN_BIN="$p"
            break
        fi
    done
fi

if [ -z "$GLEANN_BIN" ]; then
    echo "gleann: binary not found, skipping graph rebuild"
    exit 0
fi

# Get changed files in last commit
CHANGED=$(git diff-tree --no-commit-id --name-only -r HEAD 2>/dev/null)
if [ -z "$CHANGED" ]; then
    exit 0
fi

# Check if any code files changed
CODE_CHANGED=$(echo "$CHANGED" | grep -E '\.(go|py|ts|js|tsx|jsx|rs|java|c|cpp|rb|cs|kt|scala|php|swift|lua|ex|exs|zig|ps1|jl|m|mm|vue|svelte)$' || true)
if [ -z "$CODE_CHANGED" ]; then
    exit 0
fi

echo "gleann: rebuilding graph index ($(echo "$CODE_CHANGED" | wc -l | tr -d ' ') code files changed)..."
# Note: user should set GLEANN_GRAPH_INDEX in their env or hook will be a no-op
INDEX="${GLEANN_GRAPH_INDEX:-}"
if [ -n "$INDEX" ]; then
    $GLEANN_BIN index build "$INDEX" --docs . --graph 2>/dev/null &
fi
`
		// Check if hook exists and is not ours
		if data, err := os.ReadFile(hookPath); err == nil {
			if !strings.Contains(string(data), "gleann:") {
				// Append to existing hook
				hookScript = string(data) + "\n" + hookScript
			}
		}

		if err := os.WriteFile(hookPath, []byte(hookScript), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error writing hook: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Git post-commit hook installed")
		fmt.Println("   Set GLEANN_GRAPH_INDEX=<name> to specify the index to rebuild")

	case "uninstall":
		data, err := os.ReadFile(hookPath)
		if err != nil {
			fmt.Println("No gleann hook found")
			return
		}
		content := string(data)
		if !strings.Contains(content, "gleann:") {
			fmt.Println("No gleann hook found in post-commit")
			return
		}
		// Remove our section
		if err := os.Remove(hookPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Git post-commit hook removed")

	case "status":
		if data, err := os.ReadFile(hookPath); err == nil && strings.Contains(string(data), "gleann:") {
			fmt.Println("✅ gleann post-commit hook is installed")
		} else {
			fmt.Println("❌ gleann post-commit hook is not installed")
		}

	default:
		fmt.Fprintln(os.Stderr, "Usage: gleann graph hook <install|uninstall|status>")
		os.Exit(1)
	}
}

// cmdGraphMap generates a compact repo map showing the most important symbols and files.
func cmdGraphMap(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		os.Exit(1)
	}

	topN := 20
	if n := getFlag(args, "--top"); n != "" {
		fmt.Sscanf(n, "%d", &topN)
	}

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	g := community.NewGraph()
	loadGraphForViz(db, g)

	nodes, edges := g.ExportForAnalysis()
	if len(nodes) == 0 {
		fmt.Println("⚠ No nodes in graph. Run: gleann index build <name> --docs <dir> --graph")
		return
	}

	ranks := community.PageRank(nodes, edges, 0.85, 30)
	top := community.TopRanked(ranks, topN)

	// Group by file.
	type fileGroup struct {
		file  string
		nodes []community.RankedNode
	}
	fileMap := make(map[string]*fileGroup)
	nodeFileMap := make(map[string]string)
	for _, n := range nodes {
		nodeFileMap[n.ID] = n.File
	}

	for _, rn := range top {
		f := nodeFileMap[rn.ID]
		if f == "" {
			f = rn.ID // file node
		}
		if fileMap[f] == nil {
			fileMap[f] = &fileGroup{file: f}
		}
		fileMap[f].nodes = append(fileMap[f].nodes, rn)
	}

	// Sort files by total importance.
	type fileSummary struct {
		file       string
		totalScore float64
		nodes      []community.RankedNode
	}
	var files []fileSummary
	for _, fg := range fileMap {
		total := 0.0
		for _, n := range fg.nodes {
			total += n.Score
		}
		files = append(files, fileSummary{file: fg.file, totalScore: total, nodes: fg.nodes})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].totalScore > files[j].totalScore
	})

	fmt.Printf("🗺️  Repo Map — %s (top %d symbols by PageRank)\n\n", indexName, topN)

	for _, fs := range files {
		fmt.Printf("📄 %s\n", fs.file)
		for _, n := range fs.nodes {
			kind := nodeFileMap[n.ID]
			if kind == n.ID {
				continue // skip file nodes in their own group
			}
			bar := renderBar(n.Score, ranks)
			fmt.Printf("   %s %-40s %s\n", bar, n.ID, formatScore(n.Score))
		}
	}

	fmt.Printf("\n📊 Total: %d nodes, %d edges\n", len(nodes), len(edges))
}

func renderBar(score float64, allRanks map[string]float64) string {
	maxRank := 0.0
	for _, r := range allRanks {
		if r > maxRank {
			maxRank = r
		}
	}
	if maxRank == 0 {
		return "▏"
	}
	norm := score / maxRank
	barLen := int(norm * 10)
	if barLen < 1 {
		barLen = 1
	}
	bars := "██████████"
	if barLen > 10 {
		barLen = 10
	}
	return bars[:barLen*3] // UTF-8 block char is 3 bytes
}

func formatScore(score float64) string {
	if score >= 0.01 {
		return fmt.Sprintf("%.4f", score)
	}
	return fmt.Sprintf("%.6f", score)
}

// cmdGraphRisk shows risk analysis for symbols in the graph.
func cmdGraphRisk(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		os.Exit(1)
	}

	topN := 20
	if n := getFlag(args, "--top"); n != "" {
		fmt.Sscanf(n, "%d", &topN)
	}

	byFile := false
	for _, a := range args {
		if a == "--by-file" {
			byFile = true
		}
	}

	dbPath := filepath.Join(config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	g := community.NewGraph()
	loadGraphForViz(db, g)

	nodes, edges := g.ExportForAnalysis()
	if len(nodes) == 0 {
		fmt.Println("⚠ No nodes in graph.")
		return
	}

	cfg := community.DefaultRiskConfig()
	allScores := community.ComputeRiskScores(nodes, edges, cfg)

	if byFile {
		fileSummary := community.FileRiskSummary(allScores)
		if topN > len(fileSummary) {
			topN = len(fileSummary)
		}
		fmt.Printf("🔥 File Risk Analysis — %s (top %d)\n\n", indexName, topN)
		fmt.Printf("%-50s %-10s %-8s %s\n", "FILE", "RISK", "SCORE", "TOP SYMBOL")
		fmt.Println(strings.Repeat("─", 90))
		for _, rs := range fileSummary[:topN] {
			icon := riskIcon(rs.RiskLevel)
			fmt.Printf("%-50s %s %-10s %.4f   %s\n", truncStr(rs.File, 48), icon, rs.RiskLevel, rs.Score, rs.Name)
		}
	} else {
		top := community.TopRisks(allScores, topN)
		fmt.Printf("🔥 Symbol Risk Analysis — %s (top %d)\n\n", indexName, topN)
		fmt.Printf("%-40s %-10s %-10s %-6s %-6s %-6s %s\n", "SYMBOL", "KIND", "RISK", "IN", "OUT", "BLAST", "SCORE")
		fmt.Println(strings.Repeat("─", 100))
		for _, rs := range top {
			icon := riskIcon(rs.RiskLevel)
			fmt.Printf("%-40s %-10s %s %-10s %-6d %-6d %-6d %.4f\n",
				truncStr(rs.ID, 38), rs.Kind, icon, rs.RiskLevel,
				rs.InDegree, rs.OutDegree, rs.BlastRadiusSize, rs.Score)
		}
	}
}

func riskIcon(level string) string {
	switch level {
	case "critical":
		return "🔴"
	case "high":
		return "🟠"
	case "medium":
		return "🟡"
	default:
		return "🟢"
	}
}

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
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

  gleann graph viz         --index <name> [--output <file.html>]
      Generate interactive HTML graph visualization (vis.js)

  gleann graph report      --index <name> [--output <file.md>]
      Generate Markdown graph report with communities, god nodes

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

Requires: gleann index build <name> --docs <dir> --graph

Examples:
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
