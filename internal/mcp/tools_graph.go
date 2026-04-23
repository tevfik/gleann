//go:build treesitter

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/internal/graph/community"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
)

// ── Graph DB pool for community detection ────────────────────────────────

// graphPool caches open KuzuDB handles for community detection queries.
type graphPool struct {
	mu  sync.Mutex
	dbs map[string]*kgraph.DB
	dir string
}

func newGraphPool(indexDir string) *graphPool {
	return &graphPool{
		dbs: make(map[string]*kgraph.DB),
		dir: indexDir,
	}
}

func (p *graphPool) get(name string) (*kgraph.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if db, ok := p.dbs[name]; ok {
		return db, nil
	}
	dbPath := filepath.Join(p.dir, name+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		return nil, err
	}
	p.dbs[name] = db
	return db, nil
}

func (p *graphPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, db := range p.dbs {
		db.Close()
	}
	p.dbs = make(map[string]*kgraph.DB)
}

// initGraphPool initializes the graph pool for community detection tools.
// Called during NewServer if treesitter is available.
func (s *Server) initGraphPool() {
	s.gPool = newGraphPool(s.config.IndexDir)
}

// closeGraphPool cleans up the graph pool.
func (s *Server) closeGraphPool() {
	if s.gPool != nil {
		s.gPool.closeAll()
	}
}

// registerGraphTools registers community detection, risk analysis, and
// repo map tools. Only available with treesitter build tag.
func (s *Server) registerGraphTools() {
	s.mcpServer.AddTool(s.buildCommunitiesTool(), s.handleCommunities)
	s.mcpServer.AddTool(s.buildRiskAnalysisTool(), s.handleRiskAnalysis)
	s.mcpServer.AddTool(s.buildRepoMapTool(), s.handleRepoMap)
	s.mcpServer.AddTool(s.buildNavigateSymbolTool(), s.handleNavigateSymbol)
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

	db, err := s.gPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error opening graph %q: %v", indexName, err)), nil
	}

	result, err := community.FromKuzu(db, 5, 20)
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
		return mcpsdk.NewToolResultError("index is required"), nil
	}

	topN := 20
	if t, ok := args["top"].(float64); ok && t > 0 {
		topN = int(t)
	}

	byFile, _ := args["by_file"].(bool)

	db, err := s.gPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error opening graph %q: %v", indexName, err)), nil
	}

	// Build community graph and export for analysis.
	g := community.NewGraph()
	loadGraphFromKuzu(db, g)
	nodes, edges := g.ExportForAnalysis()

	if len(nodes) == 0 {
		return mcpsdk.NewToolResultText("No nodes in graph. Build index with --graph flag."), nil
	}

	cfg := community.DefaultRiskConfig()
	allScores := community.ComputeRiskScores(nodes, edges, cfg)

	var sb strings.Builder

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
			Required: []string{"index"},
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
		return mcpsdk.NewToolResultError("index is required"), nil
	}

	cfg := community.DefaultRepoMapConfig()
	if topK, ok := args["top_k"].(float64); ok && topK > 0 {
		cfg.TopK = int(topK)
	}
	if maxTokens, ok := args["max_tokens"].(float64); ok && maxTokens > 0 {
		cfg.MaxTokens = int(maxTokens)
	}

	db, err := s.gPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error opening graph %q: %v", indexName, err)), nil
	}

	g := community.NewGraph()
	loadGraphFromKuzu(db, g)
	nodes, edges := g.ExportForAnalysis()

	if len(nodes) == 0 {
		return mcpsdk.NewToolResultText("No nodes in graph. Build index with --graph flag."), nil
	}

	repoMap := community.GenerateRepoMap(nodes, edges, cfg)
	return mcpsdk.NewToolResultText(repoMap), nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

// loadGraphFromKuzu populates a community.Graph from KuzuDB.
func loadGraphFromKuzu(db *kgraph.DB, g *community.Graph) {
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

	db, err := s.gPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("Error opening graph %q: %v", indexName, err)), nil
	}

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
			sb.WriteString("  Callers:\n")
			limit := len(callers)
			if limit > 10 {
				limit = 10
			}
			for _, c := range callers[:limit] {
				sb.WriteString(fmt.Sprintf("    ← %s (%s)\n", c.FQN, c.Kind))
			}
			if len(callers) > 10 {
				sb.WriteString(fmt.Sprintf("    ... and %d more\n", len(callers)-10))
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
				sb.WriteString(fmt.Sprintf("    → %s (%s)\n", c.FQN, c.Kind))
			}
			if len(callees) > 10 {
				sb.WriteString(fmt.Sprintf("    ... and %d more\n", len(callees)-10))
			}
		} else {
			sb.WriteString("  Callees: none\n")
		}

		// For depth > 1, recurse one more level on callees.
		if depth > 1 && len(callees) > 0 {
			sb.WriteString("  2nd-level callees:\n")
			seen := make(map[string]bool)
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
							sb.WriteString(fmt.Sprintf("    %s → %s (%s)\n", c.FQN, sc.FQN, sc.Kind))
							count++
							if count >= 20 {
								break
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
