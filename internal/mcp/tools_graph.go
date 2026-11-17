package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── Graph pool for the analysis tools ────────────────────────────────────

// analysisGraph is the code graph of one index as the analysis tools
// (communities, risk, repo map, navigate) need it: the format-2 SQLite graph,
// in every build.
type analysisGraph interface {
	// CallGraph is the CALLS-only view (every symbol, internal CALLS edges).
	CallGraph() (*community.Graph, error)
	// FullGraph adds the file nodes, their DECLARES links and IMPLEMENTS edges.
	FullGraph() (*community.Graph, error)
	Communities(godNodeThreshold, maxSurprising int) (*community.Result, error)
	SymbolCount() (int, error)
	// InDegreeTop lists the most-called symbols (internal CALLS edges), most
	// calls first, ties by FQN, without the given symbol kinds.
	InDegreeTop(n int, excludeKinds ...string) ([]graphstore.InDegree, error)
	// Declarations lists up to limit declared symbols, ordered by FQN,
	// without the given kinds.
	Declarations(limit int, excludeKinds ...string) ([]graphstore.Symbol, error)
	// DB is the GraphDB view (callers, callees, symbol search).
	DB() gleann.GraphDB
	Close()
}

// storeGraph is the format-2 analysisGraph.
type storeGraph struct{ g *gleann.GraphStore }

func (s storeGraph) CallGraph() (*community.Graph, error) {
	return community.LoadCallGraphFromStore(s.g.SQLite)
}
func (s storeGraph) FullGraph() (*community.Graph, error) {
	return community.LoadGraphFromStore(s.g.SQLite)
}
func (s storeGraph) Communities(god, maxSurprising int) (*community.Result, error) {
	return community.FromStore(s.g.SQLite, god, maxSurprising)
}
func (s storeGraph) SymbolCount() (int, error) {
	st, err := s.g.Stats()
	return st.Symbols, err
}
func (s storeGraph) InDegreeTop(n int, excludeKinds ...string) ([]graphstore.InDegree, error) {
	return s.g.InDegreeTopExcluding(n, excludeKinds...)
}
func (s storeGraph) Declarations(limit int, excludeKinds ...string) ([]graphstore.Symbol, error) {
	all, err := s.g.AllSymbols()
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, k := range excludeKinds {
		skip[k] = true
	}
	var out []graphstore.Symbol
	for _, sym := range all {
		if sym.File == "" || skip[sym.Kind] {
			continue
		}
		out = append(out, sym)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
func (s storeGraph) DB() gleann.GraphDB { return s.g.GraphDB() }
func (s storeGraph) Close()             { s.g.Close() }

// graphPool caches one open code graph per index so repeated tool calls do not
// reopen it; evictGraph / closeGraphPool release the handles (index remove or
// migrate must be able to delete the files on Windows).
type graphPool struct {
	mu  sync.Mutex
	dbs map[string]analysisGraph
	dir string
}

func newGraphPool(indexDir string) *graphPool {
	return &graphPool{
		dbs: make(map[string]analysisGraph),
		dir: indexDir,
	}
}

// get returns the open graph of an index, opening it on first use. An index
// without graph data is an error and is not cached, so a graph built later is
// picked up by the next call.
func (p *graphPool) get(name string) (analysisGraph, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if db, ok := p.dbs[name]; ok {
		return db, nil
	}
	if err := gleann.ValidateName(name); err != nil {
		return nil, err
	}
	g, err := gleann.OpenGraphStore(p.dir, name)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, gleann.NoCodeGraphError(p.dir, name)
	}
	db := storeGraph{g}
	p.dbs[name] = db
	return db, nil
}

func (p *graphPool) close(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if db, ok := p.dbs[name]; ok {
		db.Close()
		delete(p.dbs, name)
	}
}

func (p *graphPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, db := range p.dbs {
		db.Close()
	}
	p.dbs = make(map[string]analysisGraph)
}

// initGraphPool initializes the graph pool for the analysis tools.
func (s *Server) initGraphPool() {
	s.gPool = newGraphPool(s.config.IndexDir)
}

// closeGraphPool cleans up the graph pool.
func (s *Server) closeGraphPool() {
	if s.gPool != nil {
		s.gPool.closeAll()
	}
}

// evictGraph closes and evicts the graph handle for a specific index.
func (s *Server) evictGraph(name string) {
	if s.gPool != nil {
		s.gPool.close(name)
	}
}

// registerGraphTools registers community detection, risk analysis, repo map
// and symbol navigation. They run on the SQLite code graph of the index
// database in every build.
func (s *Server) registerGraphTools() {
	s.addTool(s.buildCommunitiesTool(), s.handleCommunities)
	s.addTool(s.buildRiskAnalysisTool(), s.handleRiskAnalysis)
	s.addTool(s.buildRepoMapTool(), s.handleRepoMap)
	s.addTool(s.buildNavigateSymbolTool(), s.handleNavigateSymbol)
}

// ── Communities Tool ─────────────────────────────────────────────────────

func (s *Server) buildCommunitiesTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "gleann_communities",
		Description: "Run Louvain community detection on the code graph. Returns communities, god nodes (high-degree hubs), surprising cross-community edges, and modularity score.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to analyze",
				},
			},
			Required: []string{"index"},
		},
	}
}

func (s *Server) handleCommunities(ctx context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	if indexName == "" {
		return mcpsdk.NewToolResultError("index is required"), nil
	}

	ag, err := s.gPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error opening graph %q: %v", indexName, err)), nil
	}

	result, err := ag.Communities(5, 20)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Community detection failed: %v", err)), nil
	}

	data, err := json.Marshal(result)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("JSON encoding failed: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Community Detection — %s\n", indexName))
	sb.WriteString(fmt.Sprintf("Nodes: %d | Edges: %d | Communities: %d | Modularity: %.4f\n\n",
		result.NodeCount, result.EdgeCount, len(result.Communities), result.Modularity))

	for _, c := range result.Communities {
		sb.WriteString(fmt.Sprintf("Community %d: %s (%d nodes, cohesion=%.3f)\n",
			c.ID, c.Label, c.NodeCount, c.Cohesion))
	}

	if len(result.GodNodes) > 0 {
		sb.WriteString("\nGod Nodes (high-degree hubs):\n")
		for _, g := range result.GodNodes {
			sb.WriteString(fmt.Sprintf("  [%s] %s (in=%d, out=%d)\n", g.Kind, g.Name, g.InDeg, g.OutDeg))
		}
	}

	if len(result.SurprisingEdges) > 0 {
		sb.WriteString("\nSurprising edges:\n")
		for _, e := range result.SurprisingEdges {
			sb.WriteString(fmt.Sprintf("  %s → %s (community %d → %d)\n",
				e.From, e.To, e.FromCommunity, e.ToCommunity))
		}
	}

	sb.WriteString("\n--- Raw JSON ---\n")
	sb.Write(data)

	return mcpsdk.NewToolResultText(sb.String()), nil
}

// ── Risk Analysis Tool ───────────────────────────────────────────────────

func (s *Server) buildRiskAnalysisTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "gleann_risk_analysis",
		Description: "Compute change risk scores for symbols in the code graph using PageRank centrality, coupling, and blast radius metrics. Returns ranked symbols with risk levels (critical/high/medium/low).",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to analyze",
				},
				"top": map[string]interface{}{
					"type":        "integer",
					"description": "Number of top risk symbols to return (default 20)",
				},
				"by_file": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, aggregate risk scores by file instead of individual symbols",
				},
			},
			Required: []string{"index"},
		},
	}
}

func (s *Server) handleRiskAnalysis(ctx context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	if indexName == "" {
		indexName = s.resolveIndexName()
		if indexName == "" {
			return mcpsdk.NewToolResultError("index is required"), nil
		}
	}

	topN := 20
	if t, ok := args["top"].(float64); ok && t > 0 {
		topN = int(t)
	}

	byFile, _ := args["by_file"].(bool)

	ag, err := s.gPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error opening graph %q: %v", indexName, err)), nil
	}

	// Check graph size to choose between in-memory PageRank and the fast
	// in-degree aggregation.
	totalSymbols, err := ag.SymbolCount()
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Query error: %v", err)), nil
	}

	if totalSymbols == 0 {
		return mcpsdk.NewToolResultText("No nodes in graph. Build index with --graph flag."), nil
	}

	var sb strings.Builder

	if totalSymbols > 2000 {
		// Fast path for large graphs: aggregate hub symbols straight from the
		// edge table (a GROUP BY, milliseconds) instead of running PageRank.
		riskLevel := func(callers int) string {
			switch {
			case callers >= 50:
				return "CRITICAL"
			case callers >= 20:
				return "HIGH"
			case callers >= 5:
				return "MEDIUM"
			}
			return "LOW"
		}
		if byFile {
			top, err := ag.InDegreeTop(topN)
			if err != nil {
				return mcpsdk.NewToolResultError(fmt.Sprintf("Query error: %v", err)), nil
			}
			sb.WriteString(fmt.Sprintf("File Risk Analysis — %s (top %d)\n\n", indexName, topN))
			sb.WriteString(fmt.Sprintf("%-50s %-10s %-8s %s\n", "FILE", "RISK", "SCORE", "TOP SYMBOL"))
			sb.WriteString(strings.Repeat("─", 90) + "\n")
			for _, d := range top {
				sb.WriteString(fmt.Sprintf("%-50s %-10s %.4f   %s\n",
					truncPath(d.File, 48), riskLevel(d.InDegree), float64(d.InDegree)/100.0, d.Name))
			}
		} else {
			top, err := ag.InDegreeTop(topN, "macro")
			if err != nil {
				return mcpsdk.NewToolResultError(fmt.Sprintf("Query error: %v", err)), nil
			}
			sb.WriteString(fmt.Sprintf("Symbol Risk Analysis — %s (top %d)\n\n", indexName, topN))
			sb.WriteString(fmt.Sprintf("%-40s %-10s %-10s %-6s %-6s %-6s %s\n",
				"SYMBOL", "KIND", "RISK", "IN", "OUT", "BLAST", "SCORE"))
			sb.WriteString(strings.Repeat("─", 100) + "\n")
			for _, d := range top {
				sb.WriteString(fmt.Sprintf("%-40s %-10s %-10s %-6d %-6d %-6d %.4f\n",
					truncPath(d.Name, 38), d.Kind, riskLevel(d.InDegree),
					d.InDegree, 0, d.InDegree, float64(d.InDegree)/100.0))
			}
		}
		return mcpsdk.NewToolResultText(sb.String()), nil
	}

	// Small graph: full in-memory community graph analysis
	g, err := ag.CallGraph()
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error loading graph %q: %v", indexName, err)), nil
	}
	nodes, edges := g.ExportForAnalysis()

	if len(nodes) == 0 {
		return mcpsdk.NewToolResultText("No nodes in graph. Build index with --graph flag."), nil
	}

	cfg := community.DefaultRiskConfig()
	allScores := community.ComputeRiskScores(nodes, edges, cfg)

	if byFile {
		fileSummary := community.FileRiskSummary(allScores)
		if topN > len(fileSummary) {
			topN = len(fileSummary)
		}
		sb.WriteString(fmt.Sprintf("File Risk Analysis — %s (top %d)\n\n", indexName, topN))
		sb.WriteString(fmt.Sprintf("%-50s %-10s %-8s %s\n", "FILE", "RISK", "SCORE", "TOP SYMBOL"))
		sb.WriteString(strings.Repeat("─", 90) + "\n")
		for _, rs := range fileSummary[:topN] {
			sb.WriteString(fmt.Sprintf("%-50s %-10s %.4f   %s\n",
				truncPath(rs.File, 48), rs.RiskLevel, rs.Score, rs.Name))
		}
	} else {
		top := community.TopRisks(allScores, topN)
		sb.WriteString(fmt.Sprintf("Symbol Risk Analysis — %s (top %d)\n\n", indexName, topN))
		sb.WriteString(fmt.Sprintf("%-40s %-10s %-10s %-6s %-6s %-6s %s\n",
			"SYMBOL", "KIND", "RISK", "IN", "OUT", "BLAST", "SCORE"))
		sb.WriteString(strings.Repeat("─", 100) + "\n")
		for _, rs := range top {
			sb.WriteString(fmt.Sprintf("%-40s %-10s %-10s %-6d %-6d %-6d %.4f\n",
				truncPath(rs.Name, 38), rs.Kind, rs.RiskLevel,
				rs.InDegree, rs.OutDegree, rs.BlastRadiusSize, rs.Score))
		}
	}

	return mcpsdk.NewToolResultText(sb.String()), nil
}

// ── Repo Map Tool ────────────────────────────────────────────────────────

func (s *Server) buildRepoMapTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "gleann_repo_map",
		Description: "Generate a compact text map of the repository ranked by symbol importance (PageRank). Ideal for injecting into LLM context to provide codebase awareness.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to generate map for",
				},
				"top_k": map[string]interface{}{
					"type":        "integer",
					"description": "Number of top symbols to include (default 30)",
				},
				"max_tokens": map[string]interface{}{
					"type":        "integer",
					"description": "Approximate token budget for the map (default 2000)",
				},
			},
			Required: []string{},
		},
	}
}

func (s *Server) handleRepoMap(ctx context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	if indexName == "" {
		indexName = s.resolveIndexName()
		if indexName == "" {
			return mcpsdk.NewToolResultError("index is required"), nil
		}
	}

	topK := 30
	if t, ok := args["top_k"].(float64); ok && t > 0 {
		topK = int(t)
	}
	maxTokens := 2000
	if mt, ok := args["max_tokens"].(float64); ok && mt > 0 {
		maxTokens = int(mt)
	}

	ag, err := s.gPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error opening graph %q: %v", indexName, err)), nil
	}

	candidateLimit := topK * 10
	if candidateLimit < 100 {
		candidateLimit = 100
	}

	type repoMapItem struct {
		FQN   string
		Name  string
		Kind  string
		File  string
		Score float64
	}
	var cleanItems []repoMapItem
	var fallbackItems []repoMapItem

	// Primary path: compute true PageRank centrality across the entire AST code graph
	if g, err := ag.FullGraph(); err == nil && g.NodeCount() > 0 {
		nodes, edges := g.ExportForAnalysis()
		ranks := community.PageRank(nodes, edges, 0.85, 30)

		for _, n := range nodes {
			if n.Kind == "file" || n.File == "" || n.Kind == "macro" {
				continue
			}
			score := ranks[n.ID]
			item := repoMapItem{
				FQN:   n.ID,
				Name:  n.Name,
				Kind:  n.Kind,
				File:  n.File,
				Score: score,
			}
			if isNoisePath(item.File) || isNoiseSymbol(item.Name, item.Kind) {
				fallbackItems = append(fallbackItems, item)
			} else {
				cleanItems = append(cleanItems, item)
			}
		}

		byScore := func(items []repoMapItem) func(i, j int) bool {
			return func(i, j int) bool {
				if items[i].Score != items[j].Score {
					return items[i].Score > items[j].Score
				}
				return items[i].FQN < items[j].FQN
			}
		}
		sort.Slice(cleanItems, byScore(cleanItems))
		sort.Slice(fallbackItems, byScore(fallbackItems))
	}

	// Secondary path: if no graph/edges loaded, fall back to the caller counts.
	if len(cleanItems) == 0 && len(fallbackItems) == 0 {
		top, err := ag.InDegreeTop(candidateLimit, "macro")
		if err == nil {
			for _, d := range top {
				item := repoMapItem{FQN: d.FQN, Name: d.Name, Kind: d.Kind, File: d.File, Score: float64(d.InDegree)}
				if isNoisePath(item.File) || isNoiseSymbol(item.Name, item.Kind) {
					fallbackItems = append(fallbackItems, item)
				} else {
					cleanItems = append(cleanItems, item)
				}
			}
		}
	}

	var items []repoMapItem
	if len(cleanItems) >= topK {
		items = cleanItems[:topK]
	} else {
		items = append(items, cleanItems...)
		needed := topK - len(items)
		if needed > len(fallbackItems) {
			needed = len(fallbackItems)
		}
		items = append(items, fallbackItems[:needed]...)
	}

	// Fallback if no CALLS edges exist in the graph (e.g. declarations only)
	if len(items) == 0 {
		if decls, err := ag.Declarations(candidateLimit, "macro"); err == nil {
			for _, d := range decls {
				item := repoMapItem{FQN: d.FQN, Name: d.Name, Kind: d.Kind, File: d.File, Score: 1.0}
				if isNoisePath(item.File) || isNoiseSymbol(item.Name, item.Kind) {
					fallbackItems = append(fallbackItems, item)
				} else {
					cleanItems = append(cleanItems, item)
				}
			}
			if len(cleanItems) >= topK {
				items = cleanItems[:topK]
			} else {
				items = append(items, cleanItems...)
				needed := topK - len(items)
				if needed > len(fallbackItems) {
					needed = len(fallbackItems)
				}
				items = append(items, fallbackItems[:needed]...)
			}
		}
	}

	if len(items) == 0 {
		return mcpsdk.NewToolResultText("No nodes in graph. Build index with --graph flag."), nil
	}

	// Group by file
	type entry struct {
		id    string
		kind  string
		score float64
	}
	fileGroups := make(map[string][]entry)
	for _, it := range items {
		f := it.File
		if f == "" {
			f = "(unknown)"
		}
		name := it.Name
		if name == "" {
			name = it.FQN
		}
		fileGroups[f] = append(fileGroups[f], entry{id: name, kind: it.Kind, score: it.Score})
	}

	// Sort files by total score
	type fileEntry struct {
		file    string
		total   float64
		entries []entry
	}
	var files []fileEntry
	for f, entries := range fileGroups {
		total := 0.0
		for _, e := range entries {
			total += e.score
		}
		files = append(files, fileEntry{file: f, total: total, entries: entries})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].total != files[j].total {
			return files[i].total > files[j].total
		}
		return files[i].file < files[j].file
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Repository Map — %s (by importance)\n\n", indexName))
	tokenEstimate := 10
	for _, fe := range files {
		line := fmt.Sprintf("### %s\n", fe.file)
		tokenEstimate += len(line) / 4
		if maxTokens > 0 && tokenEstimate > maxTokens {
			break
		}
		sb.WriteString(line)

		for _, e := range fe.entries {
			sym := fmt.Sprintf("- %s `%s`\n", e.kind, e.id)
			tokenEstimate += len(sym) / 4
			if maxTokens > 0 && tokenEstimate > maxTokens {
				break
			}
			sb.WriteString(sym)
		}
		sb.WriteString("\n")
	}

	return mcpsdk.NewToolResultText(sb.String()), nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

// truncPath truncates a string for display in tabular output.
func truncPath(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ── Navigate Symbol Tool ─────────────────────────────────────────────────

func (s *Server) buildNavigateSymbolTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "gleann_navigate_symbol",
		Description: "Navigate to a symbol in the code graph. Returns the symbol's definition context, callers, and callees without loading full files. Ideal for pointer-based code exploration that saves context window budget.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the index to search",
				},
				"symbol": map[string]interface{}{
					"type":        "string",
					"description": "Symbol name or pattern to navigate to (e.g. 'handleRequest', 'Server.Start')",
				},
				"depth": map[string]interface{}{
					"type":        "integer",
					"description": "How many levels of callers/callees to return (default 1, max 3)",
				},
			},
			Required: []string{"index", "symbol"},
		},
	}
}

func (s *Server) handleNavigateSymbol(ctx context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments format"), nil
	}

	indexName, _ := args["index"].(string)
	if indexName == "" {
		return mcpsdk.NewToolResultError("index is required"), nil
	}
	symbol, _ := args["symbol"].(string)
	if symbol == "" {
		return mcpsdk.NewToolResultError("symbol is required"), nil
	}

	depth := 1
	if d, ok := args["depth"].(float64); ok && d >= 1 && d <= 3 {
		depth = int(d)
	}

	ag, err := s.gPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error opening graph %q: %v", indexName, err)), nil
	}
	db := ag.DB()

	// Find matching symbols.
	matches, err := db.SymbolSearch(symbol)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Symbol search failed: %v", err)), nil
	}
	if len(matches) == 0 {
		return mcpsdk.NewToolResultText(fmt.Sprintf("No symbols matching %q found in graph.", symbol)), nil
	}

	// Limit to top 5 matches.
	if len(matches) > 5 {
		matches = matches[:5]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Symbol Navigation — %q in %s\n\n", symbol, indexName))

	for i, m := range matches {
		sb.WriteString(fmt.Sprintf("── %d. %s (%s)\n", i+1, m.FQN, m.Kind))

		// Get callers.
		callers, err := db.Callers(m.FQN)
		if err == nil && len(callers) > 0 {
			var prodCallers, testCallers []gleann.Callee
			for _, c := range callers {
				if c.IsTest {
					testCallers = append(testCallers, c)
				} else {
					prodCallers = append(prodCallers, c)
				}
			}

			sb.WriteString("  Callers:\n")
			limit := len(prodCallers)
			if limit > 10 {
				limit = 10
			}
			for _, c := range prodCallers[:limit] {
				loc := ""
				if c.File != "" {
					if c.Line > 0 {
						loc = fmt.Sprintf(" [%s:%d]", c.File, c.Line)
					} else {
						loc = fmt.Sprintf(" [%s]", c.File)
					}
				}
				sb.WriteString(fmt.Sprintf("    ← %s (%s)%s\n", c.FQN, c.Kind, loc))
			}
			if len(prodCallers) > 10 {
				sb.WriteString(fmt.Sprintf("    ... and %d more production callers\n", len(prodCallers)-10))
			}
			if len(testCallers) > 0 {
				sb.WriteString(fmt.Sprintf("    [+ %d test caller(s)]\n", len(testCallers)))
			}
			if len(prodCallers) == 0 && len(testCallers) == 0 {
				sb.WriteString("    none\n")
			}
		} else {
			sb.WriteString("  Callers: none\n")
		}

		// Get callees.
		callees, err := db.Callees(m.FQN)
		if err == nil && len(callees) > 0 {
			sb.WriteString("  Callees:\n")
			limit := len(callees)
			if limit > 10 {
				limit = 10
			}
			for _, c := range callees[:limit] {
				loc := ""
				if c.File != "" {
					if c.Line > 0 {
						loc = fmt.Sprintf(" [%s:%d]", c.File, c.Line)
					} else {
						loc = fmt.Sprintf(" [%s]", c.File)
					}
				}
				sb.WriteString(fmt.Sprintf("    → %s (%s)%s\n", c.FQN, c.Kind, loc))
			}
			if len(callees) > 10 {
				sb.WriteString(fmt.Sprintf("    ... and %d more\n", len(callees)-10))
			}
		} else {
			sb.WriteString("  Callees: none\n")
		}

		// For depth > 1, recurse on callees.
		if depth > 1 && len(callees) > 0 {
			sb.WriteString("  2nd-level callees:\n")
			seen := make(map[string]bool)
			var secondLevel []gleann.Callee
			count := 0
			for _, c := range callees {
				if count >= 20 {
					break
				}
				sub, err := db.Callees(c.FQN)
				if err == nil {
					for _, sc := range sub {
						if !seen[sc.FQN] {
							seen[sc.FQN] = true
							secondLevel = append(secondLevel, sc)
							sb.WriteString(fmt.Sprintf("    %s → %s (%s)\n", c.FQN, sc.FQN, sc.Kind))
							count++
							if count >= 20 {
								break
							}
						}
					}
				}
			}

			// If depth >= 3, recurse to 3rd-level callees
			if depth >= 3 && len(secondLevel) > 0 {
				sb.WriteString("  3rd-level callees:\n")
				seen3 := make(map[string]bool)
				count3 := 0
				for _, sc := range secondLevel {
					if count3 >= 20 {
						break
					}
					sub3, err := db.Callees(sc.FQN)
					if err == nil {
						for _, ssc := range sub3 {
							if !seen[ssc.FQN] && !seen3[ssc.FQN] {
								seen3[ssc.FQN] = true
								sb.WriteString(fmt.Sprintf("    %s → %s (%s)\n", sc.FQN, ssc.FQN, ssc.Kind))
								count3++
								if count3 >= 20 {
									break
								}
							}
						}
					}
				}
			}
		}

		sb.WriteString("\n")
	}

	return mcpsdk.NewToolResultText(sb.String()), nil
}
