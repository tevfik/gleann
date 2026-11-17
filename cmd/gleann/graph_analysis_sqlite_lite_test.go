//go:build !treesitter

package main

import (
	"sort"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
)

// TestGraphAnalysisSQLiteGolden freezes the full, now deterministic outputs
// (scores, ranking, risk numbers, edge direction and order, communities,
// report, viz page, wiki) of the analysis commands on the format-2 graph of
// the fixture in a build without tree-sitter, where the graph holds the Go
// symbols only. They live in analysis_golden/sqlite_lite/. A treesitter build
// adds the non-Go symbols (and, after RFC 004 A1, their CALLS edges), so its
// numbers are covered by the determinism and invariant tests instead.
func TestGraphAnalysisSQLiteGolden(t *testing.T) {
	c := newSQLiteCLI(t)
	got := c.all()
	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		analysisgolden.Check(t, "sqlite_lite/"+n, got[n])
	}
}
