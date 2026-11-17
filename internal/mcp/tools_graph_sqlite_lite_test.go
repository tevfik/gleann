//go:build !treesitter

package mcp

import (
	"sort"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
)

// TestMCPGraphToolsSQLiteGolden freezes the full, deterministic text of the
// analysis tools on the format-2 graph of the fixture in a build without
// tree-sitter (Go symbols with CALLS edges; the other languages' symbols
// without edges). The files live in analysis_golden/sqlite_lite/.
func TestMCPGraphToolsSQLiteGolden(t *testing.T) {
	got := newSQLiteMCP(t).all()
	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		analysisgolden.Check(t, "sqlite_lite/"+n, got[n])
	}
}
