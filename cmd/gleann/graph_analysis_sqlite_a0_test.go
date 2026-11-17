package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
)

var gmlNodeRe = regexp.MustCompile(`(?s)<node id="([^"]*)">\s*<data key="d0">([^<]*)</data>\s*<data key="d1">([^<]*)</data>\s*<data key="d2">([^<]*)</data>`)
var gmlEdgeRe = regexp.MustCompile(`<!-- edge (.*) -- (.*) \[CALLS\] -->`)

// goGraph reduces a GraphML export (canonicalised: edges as sorted undirected
// pairs) to its Go part: the anchored Go symbols (id -> label|kind|file) and
// the edges between them. Symbols without a source anchor (the Kùzu path's
// phantom nodes, file "<nil>") are dropped.
func goGraph(text string) (nodes map[string]string, edges map[string]bool) {
	text = analysisgolden.CanonGraphML(text)
	nodes, edges = map[string]string{}, map[string]bool{}
	for _, m := range gmlNodeRe.FindAllStringSubmatch(text, -1) {
		if strings.HasSuffix(m[4], ".go") {
			nodes[m[1]] = m[2] + "|" + m[3] + "|" + m[4]
		}
	}
	for _, m := range gmlEdgeRe.FindAllStringSubmatch(text, -1) {
		if _, ok := nodes[m[1]]; !ok {
			continue
		}
		if _, ok := nodes[m[2]]; !ok {
			continue
		}
		a, b := m[1], m[2]
		if a > b {
			a, b = b, a
		}
		edges[a+" -- "+b] = true
	}
	return nodes, edges
}

// TestGraphAnalysisSQLiteMatchesKuzuGolden compares the SQLite path's export
// of the fixture with the A0 golden of the Kùzu path, for the Go part (the
// non-Go extractors differ: see the README of analysis_golden). The Go
// symbols and their kinds and files are identical; every Kùzu edge between
// two Go symbols is present; the SQLite graph has the four method-call edges
// the Kùzu extractor missed (calls through a receiver or field), and has no
// phantom symbol for built-ins and library calls (AGENTS.md section 2).
func TestGraphAnalysisSQLiteMatchesKuzuGolden(t *testing.T) {
	c := newSQLiteCLI(t)
	path := filepath.Join(c.out, "e.graphml")
	c.run(cmdGraphExport, "--format", "graphml", "--output", path)
	gotNodes, gotEdges := goGraph(c.file("e.graphml"))

	golden, err := os.ReadFile(filepath.Join(analysisgolden.GoldenDir(), "cli_export.graphml"))
	if err != nil {
		t.Fatal(err)
	}
	wantNodes, wantEdges := goGraph(string(golden))

	for id, v := range wantNodes {
		if gotNodes[id] != v {
			t.Errorf("Go symbol %s: golden %q, SQLite %q", id, v, gotNodes[id])
		}
	}
	for id, v := range gotNodes {
		if _, ok := wantNodes[id]; !ok {
			t.Errorf("Go symbol %s (%s) is not in the Kùzu golden", id, v)
		}
	}
	for e := range wantEdges {
		if !gotEdges[e] {
			t.Errorf("Kùzu edge missing from the SQLite graph: %s", e)
		}
	}
	var extra []string
	for e := range gotEdges {
		if !wantEdges[e] {
			extra = append(extra, e)
		}
	}
	sort.Strings(extra)
	wantExtra := []string{
		"example.com/fixture.main -- example.com/fixture/service.Service.Report",
		"example.com/fixture.main -- example.com/fixture/service.Service.Save",
		"example.com/fixture.run -- example.com/fixture/service.Service.Load",
		"example.com/fixture/service.Service.Load -- example.com/fixture/service.Service.Report",
	}
	if strings.Join(extra, "\n") != strings.Join(wantExtra, "\n") {
		t.Errorf("SQLite-only Go edges:\n%s\nwant:\n%s", strings.Join(extra, "\n"), strings.Join(wantExtra, "\n"))
	}

	// Communities: the god nodes made of Go files hold the same degrees.
	comm := c.run(cmdGraphCommunities)
	for _, want := range []string{
		"[file] service/service.go (in=0, out=9, total=9)",
		"[file] store/store.go (in=0, out=7, total=7)",
	} {
		if !strings.Contains(comm, want) {
			t.Errorf("communities output lacks god node %q:\n%s", want, comm)
		}
	}
}
