//go:build treesitter

package community

import (
	"fmt"
	"sort"
	"strings"
)

// RepoMapConfig controls compact repo map generation.
type RepoMapConfig struct {
	TopK          int     // Number of top symbols to include (default 30).
	DampingFactor float64 // PageRank damping (default 0.85).
	Iterations    int     // PageRank iterations (default 30).
	MaxTokens     int     // Approximate token budget for the map (default 2000).
}

// DefaultRepoMapConfig returns sensible defaults.
func DefaultRepoMapConfig() RepoMapConfig {
	return RepoMapConfig{
		TopK:          30,
		DampingFactor: 0.85,
		Iterations:    30,
		MaxTokens:     2000,
	}
}

// GenerateRepoMap creates a compact text representation of the codebase
// suitable for LLM context injection. It ranks symbols by PageRank importance
// and groups them by file, producing a tree-like summary.
func GenerateRepoMap(nodes []Node, edges []Edge, cfg RepoMapConfig) string {
	if len(nodes) == 0 {
		return ""
	}
	if cfg.TopK <= 0 {
		cfg.TopK = 30
	}

	ranks := PageRank(nodes, edges, cfg.DampingFactor, cfg.Iterations)
	top := TopRanked(ranks, cfg.TopK)

	// Build node lookup for file info.
	nodeMap := make(map[string]*Node, len(nodes))
	for i := range nodes {
		nodeMap[nodes[i].ID] = &nodes[i]
	}

	// Group by file.
	type entry struct {
		id    string
		kind  string
		score float64
	}
	fileGroups := make(map[string][]entry)
	for _, rn := range top {
		n := nodeMap[rn.ID]
		f := ""
		if n != nil {
			f = n.File
			if f == "" && n.Kind == "file" {
				continue // skip file nodes themselves
			}
		}
		if f == "" {
			f = "(unknown)"
		}
		kind := "symbol"
		if n != nil {
			kind = n.Kind
		}
		fileGroups[f] = append(fileGroups[f], entry{id: rn.ID, kind: kind, score: rn.Score})
	}

	// Sort files by total score.
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
		return files[i].total > files[j].total
	})

	// Build compact text representation.
	var sb strings.Builder
	sb.WriteString("## Repository Map (by importance)\n\n")

	tokenEstimate := 10 // header
	for _, fe := range files {
		line := fmt.Sprintf("### %s\n", fe.file)
		tokenEstimate += len(line) / 4
		if cfg.MaxTokens > 0 && tokenEstimate > cfg.MaxTokens {
			break
		}
		sb.WriteString(line)

		for _, e := range fe.entries {
			sym := fmt.Sprintf("- %s `%s`\n", e.kind, e.id)
			tokenEstimate += len(sym) / 4
			if cfg.MaxTokens > 0 && tokenEstimate > cfg.MaxTokens {
				break
			}
			sb.WriteString(sym)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// GenerateRepoMapFromGraph is a convenience that takes a Graph and returns a repo map string.
func GenerateRepoMapFromGraph(g *Graph, cfg RepoMapConfig) string {
	nodes, edges := g.ExportForAnalysis()
	return GenerateRepoMap(nodes, edges, cfg)
}
