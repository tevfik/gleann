package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/internal/graph/viz"
	"github.com/tevfik/gleann/pkg/gleann"
)

// The analysis subcommands of `gleann graph` (viz, report, communities,
// export, wiki, hook, map, risk) run on the code graph of a format-2 index,
// the SQLite tables of <name>.db, in every build including CGO_ENABLED=0
// (RFC 004 A2).

// analysisSource loads the graphs the analysis commands work on.
type analysisSource interface {
	// CallGraph is the CALLS-only view: every symbol, internal CALLS edges.
	CallGraph() (*community.Graph, error)
	// Communities runs community detection on the full graph (symbols, files
	// and their DECLARES links, IMPLEMENTS edges).
	Communities(godNodeThreshold, maxSurprising int) (*community.Result, error)
	Close()
}

// storeSource is the format-2 analysisSource.
type storeSource struct{ g *gleann.GraphStore }

func (s storeSource) CallGraph() (*community.Graph, error) {
	return community.LoadCallGraphFromStore(s.g.SQLite)
}

func (s storeSource) Communities(god, maxSurprising int) (*community.Result, error) {
	return community.FromStore(s.g.SQLite, god, maxSurprising)
}

func (s storeSource) Close() { s.g.Close() }

// openAnalysis opens the SQLite code graph of an index for the analysis
// commands.
func openAnalysis(config gleann.Config, indexName string) (analysisSource, error) {
	g, err := gleann.OpenGraphStore(config.IndexDir, indexName)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, gleann.NoCodeGraphError(config.IndexDir, indexName)
	}
	return storeSource{g}, nil
}

func openAnalysisOrExit(config gleann.Config, indexName string) analysisSource {
	src, err := openAnalysis(config, indexName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening graph db: %v\n", err)
		exitFunc(1)
	}
	return src
}

func callGraphOrExit(src analysisSource) *community.Graph {
	g, err := src.CallGraph()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading graph: %v\n", err)
		exitFunc(1)
	}
	return g
}

// cmdGraphViz generates an interactive HTML visualization.
func cmdGraphViz(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		exitFunc(1)
	}

	output := getFlag(args, "--output")
	if output == "" {
		output = indexName + "_graph.html"
	}

	src := openAnalysisOrExit(config, indexName)
	defer src.Close()

	fmt.Println("🕸️  Running community detection...")
	start := time.Now()
	result, err := src.Communities(5, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
	fmt.Printf("   %d communities, %d god nodes, modularity=%.3f (%s)\n",
		len(result.Communities), len(result.GodNodes), result.Modularity,
		time.Since(start).Round(time.Millisecond))

	// The CALLS-only graph viz draws (Communities does not expose its graph).
	g := callGraphOrExit(src)

	f, err := os.Create(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating file: %v\n", err)
		exitFunc(1)
	}
	defer f.Close()

	opts := viz.DefaultOptions()
	opts.Title = fmt.Sprintf("gleann — %s", indexName)
	if err := viz.RenderHTML(f, g, result, opts); err != nil {
		fmt.Fprintf(os.Stderr, "error rendering HTML: %v\n", err)
		exitFunc(1)
	}

	fmt.Printf("✅ Graph visualization saved to %s\n", output)
}

// cmdGraphReport generates a Markdown graph report.
func cmdGraphReport(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		exitFunc(1)
	}

	output := getFlag(args, "--output")
	if output == "" {
		output = "GRAPH_REPORT.md"
	}

	// The default report lives in the working directory, where agent hooks look
	// for it. Do not silently replace the report of another index.
	if other := existingReportIndex(output); other != "" && other != indexName && !hasFlag(args, "--force") {
		fmt.Fprintf(os.Stderr, "error: %s already holds the graph report of index %q; refusing to overwrite it with index %q.\n"+
			"       Use --output <file.md> to write elsewhere or --force to overwrite.\n", absPath(output), other, indexName)
		exitFunc(1)
	}

	fmt.Println("📊 Running community detection for report...")
	if err := generateGraphReportFile(indexName, config.IndexDir, getFlag(args, "--docs"), output); err != nil {
		fmt.Fprintf(os.Stderr, "error generating graph report: %v\n", err)
		exitFunc(1)
	}

	fmt.Printf("✅ Graph report saved to %s\n", absPath(output))
}

// absPath returns the absolute form of p, or p itself if that fails.
func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// existingReportIndex returns the index name recorded in the heading
// ("# Graph Report: <index>") of the report at path, or "" when there is no
// such report.
func existingReportIndex(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	first, _, _ := strings.Cut(string(buf[:n]), "\n")
	const prefix = "# Graph Report: "
	if !strings.HasPrefix(first, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(first, prefix))
}

// cmdGraphCommunities prints community detection results to stdout.
func cmdGraphCommunities(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		exitFunc(1)
	}

	src := openAnalysisOrExit(config, indexName)
	defer src.Close()

	result, err := src.Communities(5, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
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

// cmdGraphExport exports the graph to GraphML or Neo4j Cypher format.
func cmdGraphExport(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		exitFunc(1)
	}
	format := getFlag(args, "--format")
	if format == "" {
		fmt.Fprintln(os.Stderr, "error: --format flag required (graphml or cypher)")
		exitFunc(1)
	}

	output := getFlag(args, "--output")

	src := openAnalysisOrExit(config, indexName)
	defer src.Close()

	g := callGraphOrExit(src)

	switch format {
	case "graphml":
		if output == "" {
			output = indexName + "_graph.graphml"
		}
		f, err := os.Create(output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
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
			exitFunc(1)
		}
		defer f.Close()
		exportCypher(f, g)
		fmt.Printf("✅ Neo4j Cypher exported to %s\n", output)

	default:
		fmt.Fprintf(os.Stderr, "error: unsupported format %q (use graphml or cypher)\n", format)
		exitFunc(1)
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
		exitFunc(1)
	}

	output := getFlag(args, "--output")
	if output == "" {
		output = "wiki"
	}

	src := openAnalysisOrExit(config, indexName)
	defer src.Close()

	fmt.Println("📚 Generating wiki articles...")
	result, err := src.Communities(5, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}

	// The CALLS-only graph the articles list symbols from.
	g := callGraphOrExit(src)

	if err := os.MkdirAll(output, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating wiki dir: %v\n", err)
		exitFunc(1)
	}

	// Generate index.md
	indexFile, err := os.Create(filepath.Join(output, "index.md"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
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

const (
	hookMarkerStart = "# BEGIN GLEANN HOOK"
	hookMarkerEnd   = "# END GLEANN HOOK"
)

// gitHooksDir locates the git hooks directory using git rev-parse --git-path hooks,
// which correctly resolves worktrees, repository subdirectories, and core.hooksPath.
// If the git command is unavailable or fails, it falls back to .git/hooks if .git is a directory.
func gitHooksDir() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-path", "hooks")
	out, err := cmd.Output()
	if err == nil {
		p := filepath.Clean(strings.TrimSpace(string(out)))
		if p != "" {
			return p, nil
		}
	}
	// Fallback when git CLI is unavailable or in mock test environments
	if fi, err := os.Stat(".git"); err == nil && fi.IsDir() {
		return filepath.Join(".git", "hooks"), nil
	}
	return "", fmt.Errorf("not in a git repository")
}

// hasGleannHook reports whether a post-commit hook holds gleann's section:
// the marked one, or the unmarked one older versions wrote. A hook of the
// user's that merely mentions gleann is not ours.
func hasGleannHook(content string) bool {
	return strings.Contains(content, hookMarkerStart) ||
		strings.Contains(content, "gleann: auto-rebuild graph index")
}

func removeGleannHook(content string) (string, bool) {
	if start := strings.Index(content, hookMarkerStart); start != -1 {
		end := strings.Index(content, hookMarkerEnd)
		if end != -1 && end >= start {
			end += len(hookMarkerEnd)
			before := strings.TrimRight(content[:start], "\r\n")
			after := strings.TrimLeft(content[end:], "\r\n")
			if before != "" && after != "" {
				return before + "\n" + after, true
			}
			if before != "" {
				return before, true
			}
			return after, true
		}
	}

	// Legacy hook without markers
	lines := strings.Split(content, "\n")
	startLine := -1
	endLine := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if startLine == -1 {
			if strings.Contains(trimmed, "gleann: auto-rebuild graph index") {
				startLine = i
				if i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "#!") {
					startLine = i - 1
				}
			}
		} else {
			if strings.Contains(trimmed, "index build") || strings.Contains(trimmed, "index sync") || strings.Contains(trimmed, "GLEANN_GRAPH_INDEX") {
				for j := i; j < len(lines); j++ {
					if strings.TrimSpace(lines[j]) == "fi" {
						endLine = j
						break
					}
				}
			}
		}
	}
	if startLine != -1 {
		if endLine == -1 {
			endLine = len(lines) - 1
		}
		var kept []string
		kept = append(kept, lines[:startLine]...)
		if endLine+1 < len(lines) {
			kept = append(kept, lines[endLine+1:]...)
		}
		return strings.Join(kept, "\n"), true
	}

	return content, false
}

func isHookEmpty(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#!") {
			continue
		}
		return false
	}
	return true
}

// cmdGraphHook manages git hooks for automatic graph rebuild on commit.
func cmdGraphHook(args []string, config gleann.Config) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: gleann graph hook <install|uninstall|status>")
		exitFunc(1)
		return
	}

	switch args[0] {
	case "install", "uninstall", "status":
	default:
		fmt.Fprintln(os.Stderr, "Usage: gleann graph hook <install|uninstall|status>")
		exitFunc(1)
		return
	}

	gitDir, err := gitHooksDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: not in a git repository")
		exitFunc(1)
		return
	}

	hookPath := filepath.Join(gitDir, "post-commit")

	switch args[0] {
	case "install":
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
			return
		}

		hookScript := hookMarkerStart + `
# gleann: auto-rebuild graph index after commit
# Installed by: gleann graph hook install
# Remove with:  gleann graph hook uninstall

# Nothing to sync without an index name: stay silent. The block never
# calls exit, so hook lines after it still run.
INDEX="${GLEANN_GRAPH_INDEX:-}"
if [ -n "$INDEX" ]; then
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
    else
        # Get changed files in last commit
        CHANGED=$(git diff-tree --no-commit-id --name-only -r HEAD 2>/dev/null)
        # Check if any code files changed
        CODE_CHANGED=$(echo "$CHANGED" | grep -E '\.(go|py|ts|js|tsx|jsx|rs|java|c|cpp|rb|cs|kt|scala|php|swift|lua|ex|exs|zig|ps1|jl|m|mm|vue|svelte)$' || true)
        if [ -n "$CODE_CHANGED" ]; then
            echo "gleann: syncing index $INDEX ($(echo "$CODE_CHANGED" | wc -l | tr -d ' ') code files changed)..."
            $GLEANN_BIN index sync "$INDEX" 2>/dev/null &
        fi
    fi
fi
` + hookMarkerEnd

		var fullScript string
		if data, err := os.ReadFile(hookPath); err == nil {
			content := string(data)
			if hasGleannHook(content) {
				cleaned, _ := removeGleannHook(content)
				cleaned = strings.TrimSpace(cleaned)
				if cleaned == "" || isHookEmpty(cleaned) {
					fullScript = "#!/bin/sh\n" + hookScript + "\n"
				} else {
					fullScript = cleaned + "\n\n" + hookScript + "\n"
				}
			} else {
				existing := strings.TrimSpace(content)
				if existing == "" {
					fullScript = "#!/bin/sh\n" + hookScript + "\n"
				} else {
					fullScript = existing + "\n\n" + hookScript + "\n"
				}
			}
		} else {
			fullScript = "#!/bin/sh\n" + hookScript + "\n"
		}

		if err := os.WriteFile(hookPath, []byte(fullScript), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error writing hook: %v\n", err)
			exitFunc(1)
			return
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
		if !hasGleannHook(content) {
			fmt.Println("No gleann hook found in post-commit")
			return
		}
		cleaned, found := removeGleannHook(content)
		if !found {
			fmt.Println("No gleann hook found in post-commit")
			return
		}

		if isHookEmpty(cleaned) {
			// Remove our section
			if err := os.Remove(hookPath); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				exitFunc(1)
				return
			}
		} else {
			cleaned = strings.TrimRight(cleaned, "\n") + "\n"
			if err := os.WriteFile(hookPath, []byte(cleaned), 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				exitFunc(1)
				return
			}
		}
		fmt.Println("✅ Git post-commit hook removed")

	case "status":
		if data, err := os.ReadFile(hookPath); err == nil && hasGleannHook(string(data)) {
			fmt.Println("✅ gleann post-commit hook is installed")
		} else {
			fmt.Println("❌ gleann post-commit hook is not installed")
		}
	}
}

// cmdGraphMap generates a compact repo map showing the most important symbols and files.
func cmdGraphMap(args []string, config gleann.Config) {
	indexName := getFlag(args, "--index")
	if indexName == "" {
		fmt.Fprintln(os.Stderr, "error: --index flag required")
		exitFunc(1)
	}

	topN := 20
	if n := getFlag(args, "--top"); n != "" {
		fmt.Sscanf(n, "%d", &topN)
	}

	src := openAnalysisOrExit(config, indexName)
	defer src.Close()

	g := callGraphOrExit(src)

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
		if files[i].totalScore != files[j].totalScore {
			return files[i].totalScore > files[j].totalScore
		}
		return files[i].file < files[j].file
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
		exitFunc(1)
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

	src := openAnalysisOrExit(config, indexName)
	defer src.Close()

	g := callGraphOrExit(src)

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
