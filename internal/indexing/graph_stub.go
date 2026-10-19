//go:build !treesitter

package indexing

import "fmt"

// GraphSupported reports whether this binary was built with graph
// indexing support (-tags treesitter).
const GraphSupported = false

func buildGraphIndex(_ GraphOptions, p *printer) error {
	p.Warnf("⚠️  Graph indexing requires CGo (build with -tags treesitter).\n")
	return nil
}

// GenerateGraphReport writes GRAPH_REPORT.md-style Markdown for an index.
// It requires -tags treesitter.
func GenerateGraphReport(indexName, indexDir, docsDir, outputPath string) error {
	return fmt.Errorf("graph report requires CGo (build with -tags treesitter)")
}
