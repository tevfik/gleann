//go:build treesitter

package viz

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/community"
)

func TestRenderHTML(t *testing.T) {
	g := community.NewGraph()
	g.AddNode(community.Node{ID: "a.Foo", Name: "Foo", Kind: "function", File: "a.go"})
	g.AddNode(community.Node{ID: "b.Bar", Name: "Bar", Kind: "method", File: "b.go"})
	g.AddEdge("a.Foo", "b.Bar", 1.0)

	result := &community.Result{
		Communities: []community.Community{
			{ID: 0, Label: "test", NodeCount: 2, Nodes: []string{"a.Foo", "b.Bar"}},
		},
		GodNodes:   []community.GodNode{},
		Modularity: 0.5,
		NodeCount:  2,
		EdgeCount:  1,
	}

	var buf bytes.Buffer
	opts := DefaultOptions()
	err := RenderHTML(&buf, g, result, opts)
	if err != nil {
		t.Fatal(err)
	}

	html := buf.String()
	if !strings.Contains(html, "vis-network") {
		t.Error("expected vis-network script reference")
	}
	if !strings.Contains(html, "a.Foo") {
		t.Error("expected node 'a.Foo' in output")
	}
	if !strings.Contains(html, "gleann Code Graph") {
		t.Error("expected default title")
	}
	if len(html) < 1000 {
		t.Errorf("HTML output suspiciously small: %d bytes", len(html))
	}
}

func TestRenderHTMLEmptyGraph(t *testing.T) {
	g := community.NewGraph()
	result := &community.Result{}
	var buf bytes.Buffer
	err := RenderHTML(&buf, g, result, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<!DOCTYPE html>") {
		t.Error("expected valid HTML")
	}
}
