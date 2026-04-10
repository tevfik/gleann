//go:build treesitter

package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/community"
)

func TestWriteMarkdown(t *testing.T) {
	result := &community.Result{
		Communities: []community.Community{
			{ID: 0, Label: "server", NodeCount: 10, Nodes: []string{"a", "b", "c"}, Cohesion: 0.5},
			{ID: 1, Label: "client", NodeCount: 5, Nodes: []string{"d", "e"}, Cohesion: 0.8},
		},
		GodNodes: []community.GodNode{
			{ID: "hub.Func", Name: "Func", Kind: "function", InDeg: 20, OutDeg: 5, TotalDeg: 25},
		},
		SurprisingEdges: []community.SurprisingEdge{
			{From: "a", To: "d", FromCommunity: 0, ToCommunity: 1, Weight: 1.0},
		},
		Modularity: 0.45,
		NodeCount:  15,
		EdgeCount:  20,
	}

	var buf bytes.Buffer
	err := WriteMarkdown(&buf, result, Options{IndexName: "test-idx"})
	if err != nil {
		t.Fatal(err)
	}

	md := buf.String()

	if !strings.Contains(md, "# Graph Report: test-idx") {
		t.Error("missing title")
	}
	if !strings.Contains(md, "| Nodes | 15 |") {
		t.Error("missing node count")
	}
	if !strings.Contains(md, "God Nodes") {
		t.Error("missing god nodes section")
	}
	if !strings.Contains(md, "`hub.Func`") {
		t.Error("missing god node name")
	}
	if !strings.Contains(md, "Community 0: server") {
		t.Error("missing community")
	}
	if !strings.Contains(md, "Cross-Community Edges") {
		t.Error("missing surprising edges section")
	}
	if !strings.Contains(md, "well-modularized") {
		t.Error("missing modularity interpretation")
	}
}
