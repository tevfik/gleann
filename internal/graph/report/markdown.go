//go:build treesitter

// Package report generates Markdown reports from community detection results.
package report

import (
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

	p := func(format string, args ...any) {
		fmt.Fprintf(w, format+"\n", args...)
	}

	p("# Graph Report: %s", opts.IndexName)
	p("")
	p("Generated: %s", time.Now().Format("2006-01-02 15:04:05"))
	if opts.DocsDir != "" {
		p("Source: `%s`", opts.DocsDir)
	}
	p("")

	// Summary
	p("## Summary")
	p("")
	p("| Metric | Value |")
	p("|--------|-------|")
	p("| Nodes | %d |", result.NodeCount)
	p("| Edges | %d |", result.EdgeCount)
	p("| Communities | %d |", len(result.Communities))
	p("| Modularity (Q) | %.4f |", result.Modularity)
	p("| God Nodes | %d |", len(result.GodNodes))
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
	if len(result.GodNodes) > 0 {
		p("## God Nodes (High-Degree Hubs)")
		p("")
		p("These symbols have an unusually high number of connections, making them central to the codebase.")
		p("")
		p("| Rank | Symbol | Kind | In° | Out° | Total° |")
		p("|------|--------|------|-----|------|--------|")
		for i, g := range result.GodNodes {
			name := shortName(g.ID)
			p("| %d | `%s` | %s | %d | %d | %d |", i+1, name, g.Kind, g.InDeg, g.OutDeg, g.TotalDeg)
		}
		p("")
		p("> **Tip:** God nodes are potential coupling hotspots. If a god node changes, many dependents may be affected.")
		p("")
	}

	// Communities
	if len(result.Communities) > 0 {
		p("## Communities")
		p("")
		p("Detected via the Louvain algorithm. Each community represents a group of tightly-connected symbols.")
		p("")

		for _, c := range result.Communities {
			p("### Community %d: %s (%d nodes, cohesion=%.3f)", c.ID, c.Label, c.NodeCount, c.Cohesion)
			p("")

			displayed := c.Nodes
			if len(displayed) > maxNodes {
				displayed = displayed[:maxNodes]
			}
			for _, nid := range displayed {
				p("- `%s`", shortName(nid))
			}
			if len(c.Nodes) > maxNodes {
				p("- ... and %d more", len(c.Nodes)-maxNodes)
			}
			p("")
		}
	}

	// Surprising Edges
	if len(result.SurprisingEdges) > 0 {
		p("## Cross-Community Edges (Surprising Connections)")
		p("")
		p("These edges connect symbols in different communities, indicating inter-module coupling.")
		p("")
		p("| From | To | Communities |")
		p("|------|----|------------|")
		for _, e := range result.SurprisingEdges {
			p("| `%s` | `%s` | %d → %d |", shortName(e.From), shortName(e.To), e.FromCommunity, e.ToCommunity)
		}
		p("")
		p("> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.")
		p("")
	}

	return nil
}

// shortName extracts the short symbol name from an FQN.
func shortName(fqn string) string {
	if i := strings.LastIndex(fqn, "/"); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}
