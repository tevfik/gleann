//go:build treesitter

// Package report generates Markdown reports from community detection results.
package report

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/graph/community"
)

// Options configures report generation.
type Options struct {
	IndexName string
	DocsDir   string
	MaxNodes  int // max nodes to list per community (default 20)
}

// WriteMarkdown writes a GRAPH_REPORT.md style report to w.
func WriteMarkdown(w io.Writer, result *community.Result, opts Options) error {
	maxNodes := opts.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 20
	}

	var buf bytes.Buffer
	maxBytes := 50 * 1024
	truncated := false

	p := func(format string, args ...any) {
		if truncated {
			return
		}
		line := fmt.Sprintf(format+"\n", args...)
		if buf.Len()+len(line) > maxBytes-64 {
			buf.WriteString("\n*(Report truncated at 50 KB max size limit)*\n")
			truncated = true
			return
		}
		buf.WriteString(line)
	}

	p("# Graph Report: %s", opts.IndexName)
	p("")
	p("Generated: %s", time.Now().Format("2006-01-02 15:04:05"))
	if opts.DocsDir != "" {
		p("Source: `%s`", opts.DocsDir)
	}
	p("")

	// Filter god nodes
	var filteredGodNodes []community.GodNode
	for _, g := range result.GodNodes {
		if !isExcludedReportSymbol(g.ID) {
			filteredGodNodes = append(filteredGodNodes, g)
		}
	}

	// Summary
	p("## Summary")
	p("")
	p("| Metric | Value |")
	p("|--------|-------|")
	p("| Nodes | %d |", result.NodeCount)
	p("| Edges | %d |", result.EdgeCount)
	p("| Communities | %d |", len(result.Communities))
	p("| Modularity (Q) | %.4f |", result.Modularity)
	p("| God Nodes | %d |", len(filteredGodNodes))
	p("| Cross-Community Edges | %d |", len(result.SurprisingEdges))
	p("")

	// Modularity interpretation
	if result.Modularity > 0.4 {
		p("**Modularity interpretation:** Strong community structure (Q > 0.4). Code is well-modularized.")
	} else if result.Modularity > 0.2 {
		p("**Modularity interpretation:** Moderate community structure (0.2 < Q < 0.4). Some coupling between modules.")
	} else {
		p("**Modularity interpretation:** Weak community structure (Q < 0.2). Code may benefit from refactoring into clearer modules.")
	}
	p("")

	// God Nodes
	if len(filteredGodNodes) > 0 {
		p("## God Nodes (High-Degree Hubs)")
		p("")
		p("These symbols have an unusually high number of connections, making them central to the codebase.")
		p("")
		p("| Rank | Symbol | Kind | In° | Out° | Total° |")
		p("|------|--------|------|-----|------|--------|")
		for i, g := range filteredGodNodes {
			name := shortName(g.ID)
			p("| %d | `%s` | %s | %d | %d | %d |", i+1, name, g.Kind, g.InDeg, g.OutDeg, g.TotalDeg)
		}
		p("")
		p("> **Tip:** God nodes are potential coupling hotspots. If a god node changes, many dependents may be affected.")
		p("")
	}

	// Communities (limit to top 15 communities)
	if len(result.Communities) > 0 {
		p("## Communities")
		p("")
		p("Detected via the Louvain algorithm. Each community represents a group of tightly-connected symbols.")
		p("")

		maxCommunities := 15
		displayedCommunities := result.Communities
		if len(displayedCommunities) > maxCommunities {
			displayedCommunities = displayedCommunities[:maxCommunities]
		}

		for _, c := range displayedCommunities {
			p("### Community %d: %s (%d nodes, cohesion=%.3f)", c.ID, c.Label, c.NodeCount, c.Cohesion)
			p("")

			var validNodes []string
			for _, nid := range c.Nodes {
				if !isExcludedReportSymbol(nid) {
					validNodes = append(validNodes, nid)
				}
			}

			displayed := validNodes
			if len(displayed) > maxNodes {
				displayed = displayed[:maxNodes]
			}
			for _, nid := range displayed {
				p("- `%s`", shortName(nid))
			}
			if len(validNodes) > maxNodes {
				p("- ... and %d more", len(validNodes)-maxNodes)
			}
			p("")
		}
		if len(result.Communities) > maxCommunities {
			p("*(%d additional communities omitted)*\n", len(result.Communities)-maxCommunities)
		}
	}

	// Surprising Edges (filter excluded symbols)
	var validEdges []community.SurprisingEdge
	for _, e := range result.SurprisingEdges {
		if !isExcludedReportSymbol(e.From) && !isExcludedReportSymbol(e.To) {
			validEdges = append(validEdges, e)
		}
	}

	if len(validEdges) > 0 {
		p("## Cross-Community Edges (Surprising Connections)")
		p("")
		p("These edges connect symbols in different communities, indicating inter-module coupling.")
		p("Ranked by composite score: cross-community edges involving different packages score higher.")
		p("")
		p("| From | To | Communities | Score |")
		p("|------|----|------------|-------|")
		for _, e := range validEdges {
			score := surprisingScore(e)
			p("| `%s` | `%s` | %d → %d | %.2f |", shortName(e.From), shortName(e.To), e.FromCommunity, e.ToCommunity, score)
		}
		p("")
		p("> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.")
		p("")
	}

	// Suggested Questions
	questions := suggestQuestions(result)
	if len(questions) > 0 {
		p("## Suggested Questions")
		p("")
		p("Based on graph structure, these questions may reveal useful insights:")
		p("")
		for i, q := range questions {
			p("%d. %s", i+1, q)
		}
		p("")
	}

	_, err := w.Write(buf.Bytes())
	return err
}

func isExcludedReportSymbol(fqn string) bool {
	lower := strings.ToLower(fqn)
	if strings.Contains(lower, "_test.") || strings.HasSuffix(lower, "_test") ||
		strings.Contains(lower, "/test_") || strings.Contains(lower, ".test") {
		return true
	}
	if strings.HasPrefix(lower, "builtin.") || strings.HasPrefix(lower, "vendor/") ||
		strings.HasPrefix(lower, "third_party/") {
		return true
	}
	return false
}

// surprisingScore computes a composite score for a surprising edge.
// Higher score = more architecturally significant.
func surprisingScore(e community.SurprisingEdge) float64 {
	score := e.Weight

	// Cross-package edges are more surprising than intra-package
	fromPkg := extractPkg(e.From)
	toPkg := extractPkg(e.To)
	if fromPkg != toPkg && fromPkg != "" && toPkg != "" {
		score *= 1.5
	}

	// Greater community distance = more surprising
	commDist := e.FromCommunity - e.ToCommunity
	if commDist < 0 {
		commDist = -commDist
	}
	if commDist > 3 {
		score *= 1.2
	}

	return score
}

// extractPkg extracts the package portion from an FQN.
func extractPkg(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i > 0 {
		return fqn[:i]
	}
	return ""
}

// suggestQuestions generates questions the graph is uniquely positioned to answer.
func suggestQuestions(result *community.Result) []string {
	var qs []string

	if len(result.GodNodes) >= 2 {
		qs = append(qs, fmt.Sprintf("What would break if `%s` (degree %d) were refactored?",
			result.GodNodes[0].Name, result.GodNodes[0].TotalDeg))
		qs = append(qs, fmt.Sprintf("Is `%s` a genuine hub or should it be split into smaller interfaces?",
			result.GodNodes[1].Name))
	}

	if len(result.Communities) >= 2 {
		qs = append(qs, fmt.Sprintf("Why do communities '%s' and '%s' share cross-module edges?",
			result.Communities[0].Label, result.Communities[1].Label))
	}

	if len(result.SurprisingEdges) > 0 {
		e := result.SurprisingEdges[0]
		qs = append(qs, fmt.Sprintf("What is the relationship between `%s` and `%s` (surprising cross-community edge)?",
			shortName(e.From), shortName(e.To)))
	}

	if result.Modularity < 0.3 && result.NodeCount > 50 {
		qs = append(qs, "Why is the modularity low — is there a central package coupling everything together?")
	}

	return qs
}

// shortName extracts the short symbol name from an FQN.
func shortName(fqn string) string {
	if i := strings.LastIndex(fqn, "/"); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}
