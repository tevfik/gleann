package indexing

import (
	"fmt"
	"os"

	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/internal/graph/report"
	"github.com/tevfik/gleann/pkg/gleann"
)

// GenerateGraphReport writes a Markdown graph report (GRAPH_REPORT.md
// format) for an index to outputPath. A format-2 index is analysed from its
// SQLite code graph in every build.
func GenerateGraphReport(indexName, indexDir, docsDir, outputPath string) error {
	g, err := gleann.OpenGraphStore(indexDir, indexName)
	if err != nil {
		return err
	}
	if g == nil {
		return gleann.NoCodeGraphError(indexDir, indexName)
	}
	result, err := community.FromStore(g.SQLite, 5, 20)
	g.Close()
	if err != nil {
		return fmt.Errorf("running community detection: %w", err)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer f.Close()

	return report.WriteMarkdown(f, result, report.Options{IndexName: indexName, DocsDir: docsDir})
}
