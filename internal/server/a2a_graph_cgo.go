//go:build treesitter

package server

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/internal/graph/community"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// a2aCommunitiesHandler runs Louvain community detection on the code graph.
func (s *Server) a2aCommunitiesHandler(ctx a2a.SkillContext) (string, error) {
	indexName := resolveIndex(ctx.Metadata, s.config.IndexDir)
	if indexName == "" {
		return "", fmt.Errorf("no indexes available")
	}

	dbPath := filepath.Join(s.config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("graph index %q not found: %v", indexName, err)
	}
	defer db.Close()

	result, err := community.FromKuzu(db, 5, 20)
	if err != nil {
		return "", fmt.Errorf("community detection failed: %v", err)
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
		sb.WriteString("\nGod Nodes:\n")
		for _, g := range result.GodNodes {
			sb.WriteString(fmt.Sprintf("  [%s] %s (in=%d, out=%d)\n", g.Kind, g.Name, g.InDeg, g.OutDeg))
		}
	}

	return sb.String(), nil
}

// a2aRepoMapHandler generates a compact repository map.
func (s *Server) a2aRepoMapHandler(ctx a2a.SkillContext) (string, error) {
	indexName := resolveIndex(ctx.Metadata, s.config.IndexDir)
	if indexName == "" {
		return "", fmt.Errorf("no indexes available")
	}

	dbPath := filepath.Join(s.config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("graph index %q not found: %v", indexName, err)
	}
	defer db.Close()

	g := community.NewGraph()
	loadGraphForA2A(db, g)
	nodes, edges := g.ExportForAnalysis()

	if len(nodes) == 0 {
		return "No nodes in graph. Build index with --graph flag.", nil
	}

	cfg := community.DefaultRepoMapConfig()
	return community.GenerateRepoMap(nodes, edges, cfg), nil
}

// a2aRiskHandler computes change risk scores.
func (s *Server) a2aRiskHandler(ctx a2a.SkillContext) (string, error) {
	indexName := resolveIndex(ctx.Metadata, s.config.IndexDir)
	if indexName == "" {
		return "", fmt.Errorf("no indexes available")
	}

	dbPath := filepath.Join(s.config.IndexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("graph index %q not found: %v", indexName, err)
	}
	defer db.Close()

	g := community.NewGraph()
	loadGraphForA2A(db, g)
	nodes, edges := g.ExportForAnalysis()

	if len(nodes) == 0 {
		return "No nodes in graph. Build index with --graph flag.", nil
	}

	cfg := community.DefaultRiskConfig()
	allScores := community.ComputeRiskScores(nodes, edges, cfg)
	top := community.TopRisks(allScores, 20)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Risk Analysis — %s (top %d)\n\n", indexName, len(top)))
	for _, rs := range top {
		sb.WriteString(fmt.Sprintf("  [%s] %s — %s (score=%.4f, in=%d, out=%d, blast=%d)\n",
			rs.Kind, rs.Name, rs.RiskLevel, rs.Score, rs.InDegree, rs.OutDegree, rs.BlastRadiusSize))
	}

	return sb.String(), nil
}

// loadGraphForA2A populates a community.Graph from KuzuDB for A2A handlers.
func loadGraphForA2A(db *kgraph.DB, g *community.Graph) {
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

// resolveIndex picks the index name from metadata or falls back to first available.
func resolveIndex(metadata map[string]interface{}, indexDir string) string {
	if idx, ok := metadata["index"].(string); ok && idx != "" {
		return idx
	}
	indexes, err := gleann.ListIndexes(indexDir)
	if err != nil || len(indexes) == 0 {
		return ""
	}
	return indexes[0].Name
}
