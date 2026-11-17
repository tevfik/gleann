package report

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/community"
)

func render(t *testing.T, result *community.Result, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, result, opts); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestWriteMarkdownModularityBands(t *testing.T) {
	tests := []struct {
		q    float64
		want string
	}{
		{0.5, "Strong community structure"},
		{0.3, "Moderate community structure"},
		{0.1, "Weak community structure"},
	}
	for _, tc := range tests {
		md := render(t, &community.Result{Modularity: tc.q}, Options{IndexName: "x"})
		if !strings.Contains(md, tc.want) {
			t.Errorf("Q=%.1f: report lacks %q", tc.q, tc.want)
		}
	}
}

func TestWriteMarkdownSourceAndLimits(t *testing.T) {
	var comms []community.Community
	for i := 0; i < 17; i++ {
		comms = append(comms, community.Community{ID: i, Label: fmt.Sprintf("c%d", i)})
	}
	nodes := []string{"pkg/a.F1", "pkg/a.F2", "pkg/a.F3", "pkg/a_test.TestX", "builtin.len"}
	comms[0].Nodes = nodes
	comms[0].NodeCount = len(nodes)

	md := render(t, &community.Result{Communities: comms, NodeCount: 100, Modularity: 0.1}, Options{
		IndexName: "idx", DocsDir: "/src/proj", MaxNodes: 2,
	})
	for _, want := range []string{
		"Source: `/src/proj`",
		"- `a.F1`", "- `a.F2`",
		"- ... and 1 more", // 3 valid nodes, 2 listed; test and builtin symbols dropped
		"### Community 14: c14",
		"*(2 additional communities omitted)*",
		"modularity low", // Q < 0.3 with > 50 nodes
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	for _, unwanted := range []string{"a.F3", "TestX", "builtin.len", "### Community 15:"} {
		if strings.Contains(md, unwanted) {
			t.Errorf("report contains %q", unwanted)
		}
	}
}

func TestWriteMarkdownExcludesTestAndVendorSymbols(t *testing.T) {
	result := &community.Result{
		GodNodes: []community.GodNode{
			{ID: "vendor/x.Y", TotalDeg: 9},
			{ID: "third_party/z.W", TotalDeg: 8},
			{ID: "pkg.helper_test", TotalDeg: 7},
		},
		SurprisingEdges: []community.SurprisingEdge{
			{From: "pkg/test_util.F", To: "pkg.G", FromCommunity: 0, ToCommunity: 1},
			{From: "pkg.G", To: "pkg.H.test", FromCommunity: 1, ToCommunity: 2},
		},
	}
	md := render(t, result, Options{IndexName: "x"})
	if strings.Contains(md, "## God Nodes") {
		t.Error("only excluded god nodes: the section must be omitted")
	}
	if strings.Contains(md, "Surprising Connections") {
		t.Error("only excluded edges: the section must be omitted")
	}
	if !strings.Contains(md, "| God Nodes | 0 |") {
		t.Error("summary must count only the kept god nodes")
	}
}

func TestWriteMarkdownTruncatesAt50KB(t *testing.T) {
	var nodes []string
	for i := 0; i < 5000; i++ {
		nodes = append(nodes, fmt.Sprintf("pkg/long/path/name.Symbol%05d", i))
	}
	result := &community.Result{
		Communities: []community.Community{{ID: 0, Label: "big", NodeCount: len(nodes), Nodes: nodes}},
		GodNodes:    []community.GodNode{{ID: "a.B", Name: "B"}, {ID: "c.D", Name: "D"}},
	}
	md := render(t, result, Options{IndexName: "x", MaxNodes: len(nodes)})
	if len(md) > 50*1024 {
		t.Fatalf("report is %d bytes, over the 50 KB cap", len(md))
	}
	if !strings.HasSuffix(md, "*(Report truncated at 50 KB max size limit)*\n") {
		t.Fatalf("report does not end with the truncation note: %q", md[len(md)-80:])
	}
	if strings.Count(md, "truncated at 50 KB") != 1 {
		t.Error("the truncation note must appear once")
	}
	if strings.Contains(md, "Suggested Questions") {
		t.Error("nothing may be written after truncation")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteMarkdownWriteError(t *testing.T) {
	if err := WriteMarkdown(failWriter{}, &community.Result{}, Options{}); err == nil {
		t.Fatal("a write error must be returned")
	}
}

func TestShortNameWithoutSlash(t *testing.T) {
	if got := shortName("main.run"); got != "main.run" {
		t.Fatalf("shortName = %q", got)
	}
	if got := shortName("a/b/c.D"); got != "c.D" {
		t.Fatalf("shortName = %q", got)
	}
}
