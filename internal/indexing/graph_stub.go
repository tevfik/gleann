//go:build !treesitter

package indexing

import "fmt"

// GraphSupported reports whether this binary supports the Kùzu graph path
// for format-1 indexes (-tags treesitter). Format-2 indexes build and serve
// their graph from SQLite in every build, regardless of this constant.
const GraphSupported = false

func buildGraphIndexKuzu(_ GraphOptions, p *printer) error {
	p.Warnf("⚠️  Graph indexing requires CGo (build with -tags treesitter).\n")
	return nil
}

// GenerateGraphReport writes GRAPH_REPORT.md-style Markdown for an index.
// It requires -tags treesitter.
func GenerateGraphReport(indexName, indexDir, docsDir, outputPath string) error {
	return fmt.Errorf("graph report requires CGo (build with -tags treesitter)")
}
