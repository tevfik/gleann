package viz

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/community"
)

func render(t *testing.T, g *community.Graph, res *community.Result, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderHTML(&buf, g, res, opts); err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	return buf.String()
}

func TestShortID(t *testing.T) {
	cases := map[string]string{
		"pkg/sub.Func":   "Func",
		"pkg/sub.T.Meth": "Meth",
		"plain/path":     "path",
		"noseparators":   "noseparators",
		"trailing.":      "",
		"github.com/x/y": "com/x/y", // the dot wins over the slash
		"dir.name/inner": "name/inner",
		"":               "",
		"a.b/c.d":        "d",
	}
	for in, want := range cases {
		if got := shortID(in); got != want {
			t.Errorf("shortID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderHTMLNodeAndEdgeStyling(t *testing.T) {
	g := community.NewGraph()
	g.AddNode(community.Node{ID: "p.Hub", Name: "Hub", Kind: "function", File: "p.go"})
	g.AddNode(community.Node{ID: "p.Leaf", Name: "Leaf", Kind: "interface", File: "p.go"})
	g.AddNode(community.Node{ID: "q/mod.NoName", Kind: "mystery"}) // label falls back to shortID
	g.AddEdge("p.Hub", "p.Leaf", 1)
	g.AddEdge("p.Hub", "q/mod.NoName", 1)

	res := &community.Result{
		Communities: []community.Community{
			{ID: 0, Nodes: []string{"p.Hub", "p.Leaf"}},
			{ID: 1, Nodes: []string{"q/mod.NoName"}},
		},
		GodNodes: []community.GodNode{{ID: "p.Hub", Name: "Hub", Kind: "function", TotalDeg: 2}},
	}
	out := render(t, g, res, DefaultOptions())

	// God node is painted red and enlarged: 8 + 2*2 degree + 5 god bonus.
	if !strings.Contains(out, `"id":"p.Hub","label":"Hub"`) || !strings.Contains(out, `"size":17,"shape":"dot","color":"#F44336"`) {
		t.Errorf("god node styling missing:\n%s", nodesLine(out))
	}
	// Interface kind: its own shape and colour.
	if !strings.Contains(out, `"shape":"triangle","color":"#9C27B0"`) {
		t.Error("interface node should be a purple triangle")
	}
	// Unknown kind: default dot / grey, label from shortID of the id.
	if !strings.Contains(out, `"label":"NoName"`) || !strings.Contains(out, `"shape":"dot","color":"#9E9E9E"`) {
		t.Error("unknown kind or missing name should fall back to defaults")
	}
	// Intra-community edge is thin grey, cross-community edge thick red.
	if !strings.Contains(out, `"to":"p.Leaf","color":"#cccccc"`) {
		t.Error("intra-community edge should be grey")
	}
	if !strings.Contains(out, `"to":"q/mod.NoName","color":"#ff5252","width":2`) {
		t.Error("cross-community edge should be red and wide")
	}
}

func nodesLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, `"id":"p.Hub"`) {
			return l
		}
	}
	return s
}

func TestRenderHTMLNodeSizeIsCapped(t *testing.T) {
	g := community.NewGraph()
	g.AddNode(community.Node{ID: "hub", Name: "hub", Kind: "function"})
	for i := 0; i < 30; i++ {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		g.AddNode(community.Node{ID: id, Name: id, Kind: "function"})
		g.AddEdge("hub", id, 1)
	}
	out := render(t, g, &community.Result{}, DefaultOptions())
	if !strings.Contains(out, `"id":"hub","label":"hub"`) || !strings.Contains(out, `"size":40,`) {
		t.Error("degree-30 node should be capped at size 40")
	}
	if strings.Contains(out, `"size":41`) || strings.Contains(out, `"size":68`) {
		t.Error("size exceeded the cap")
	}
}

func TestRenderHTMLFileNodesAndMaxNodes(t *testing.T) {
	g := community.NewGraph()
	g.AddNode(community.Node{ID: "a.go", Name: "a.go", Kind: "file"})
	g.AddNode(community.Node{ID: "b.F", Name: "F", Kind: "function"})
	g.AddNode(community.Node{ID: "c.G", Name: "G", Kind: "function"})
	g.AddEdge("a.go", "b.F", 1)
	g.AddEdge("b.F", "c.G", 1)

	// Default: files hidden, and an edge touching a hidden node still appears
	// only if both endpoints were within MaxNodes (a.go is, so its edge stays).
	out := render(t, g, &community.Result{}, DefaultOptions())
	if strings.Contains(out, `"id":"a.go"`) {
		t.Error("file node rendered although ShowFiles is false")
	}

	opts := DefaultOptions()
	opts.ShowFiles = true
	out = render(t, g, &community.Result{}, opts)
	if !strings.Contains(out, `"id":"a.go"`) || !strings.Contains(out, `"shape":"square","color":"#607D8B"`) {
		t.Error("file node missing with ShowFiles")
	}

	// MaxNodes=1 keeps only the first (sorted) node and drops every edge.
	opts = Options{MaxNodes: 1, ShowFiles: true}
	out = render(t, g, &community.Result{}, opts)
	if !strings.Contains(out, `"id":"a.go"`) || strings.Contains(out, `"id":"b.F"`) || strings.Contains(out, `"id":"c.G"`) {
		t.Error("MaxNodes=1 should keep only a.go")
	}
	if strings.Contains(out, `"from":"a.go"`) {
		t.Error("edges to truncated nodes must be dropped")
	}
}

func TestRenderHTMLEscapesTitleAndTooltip(t *testing.T) {
	g := community.NewGraph()
	g.AddNode(community.Node{ID: "x.<b>", Name: "n", Kind: "function", File: "<script>alert(1)</script>.go"})
	out := render(t, g, &community.Result{}, Options{Title: `<img src=x onerror=alert(1)>`})
	if strings.Contains(out, "<img src=x") {
		t.Error("title must be HTML-escaped")
	}
	if !strings.Contains(out, "&lt;img src=x") {
		t.Error("escaped title missing")
	}
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("tooltip file name must be escaped")
	}
}

func TestRenderHTMLDefaultsApplyForZeroOptions(t *testing.T) {
	out := render(t, community.NewGraph(), &community.Result{}, Options{})
	if !strings.Contains(out, "gleann Code Graph") {
		t.Error("empty title should default")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errWrite }

var errWrite = &writeErr{}

type writeErr struct{}

func (*writeErr) Error() string { return "disk full" }

func TestRenderHTMLPropagatesWriteError(t *testing.T) {
	err := RenderHTML(failWriter{}, community.NewGraph(), &community.Result{}, DefaultOptions())
	if err != errWrite {
		t.Fatalf("err = %v, want the writer's error", err)
	}
}
