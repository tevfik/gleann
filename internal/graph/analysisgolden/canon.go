//go:build treesitter && !windows

package analysisgolden

// KNOWN NON-DETERMINISM of the Kùzu path (frozen here, not fixed)
//
//   - community.Graph.Edges() emits every undirected neighbour pair in
//     whichever direction the neighbour map yields first, so everything built on
//     ExportForAnalysis (PageRank in `graph map`, in/out degree, blast radius and
//     score in `graph risk`) and the edge direction and order of `graph export`
//     and `graph viz` change from run to run; community.TopRanked breaks ties by
//     map order.
//   - The Louvain pass iterates maps, so the number of communities, their
//     members, labels, cohesion, the modularity and the "surprising" cross-
//     community edges differ between runs (20 to 23 communities were seen on the
//     fixture).
//
// The goldens therefore keep only what is invariant across runs: node sets,
// node kinds and files, total degrees (in+out), god nodes (their in/out come
// from the directed edge loader and are stable), undirected edge pairs and the
// node and edge counts. A2 must reproduce exactly these; direction- and
// community-dependent numbers are not compared, and a deterministic
// implementation is allowed (and encouraged) to produce any stable value there.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/community"
)

// undirected renders an edge without its (run-dependent) direction.
func undirected(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + " -- " + b
}

// CanonResult reduces a community.Result to its invariant part: node and edge
// counts and the god nodes sorted by id.
func (e *Env) CanonResult(t testing.TB, resultJSON string) string {
	t.Helper()
	var r community.Result
	if err := json.Unmarshal([]byte(resultJSON), &r); err != nil {
		t.Fatalf("community result JSON: %v", err)
	}
	return e.Normalize(canonResult(&r))
}

func canonResult(r *community.Result) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "nodes=%d edges=%d god_nodes=%d\n", r.NodeCount, r.EdgeCount, len(r.GodNodes))
	gods := append([]community.GodNode{}, r.GodNodes...)
	sort.Slice(gods, func(i, j int) bool { return gods[i].ID < gods[j].ID })
	for _, g := range gods {
		fmt.Fprintf(&sb, "god %s kind=%s in=%d out=%d total=%d\n", g.ID, g.Kind, g.InDeg, g.OutDeg, g.TotalDeg)
	}
	return sb.String()
}

var graphMLEdgeRe = regexp.MustCompile(`(?s)\s*<edge id="e\d+" source="([^"]*)" target="([^"]*)">\s*<data key="d3">([^<]*)</data>\s*</edge>`)

// CanonGraphML replaces the edge elements (order and direction are map-defined)
// with sorted undirected pairs placed before the closing graph tag.
func CanonGraphML(text string) string {
	var pairs []string
	body := graphMLEdgeRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := graphMLEdgeRe.FindStringSubmatch(m)
		pairs = append(pairs, undirected(sub[1], sub[2])+" ["+sub[3]+"]")
		return ""
	})
	sort.Strings(pairs)
	var sb strings.Builder
	for _, p := range pairs {
		sb.WriteString("    <!-- edge " + p + " -->\n")
	}
	const closing = "  </graph>"
	if i := strings.Index(body, closing); i >= 0 {
		return body[:i] + sb.String() + body[i:]
	}
	return body + sb.String()
}

var cypherEdgeRe = regexp.MustCompile(`^MATCH \(a:Symbol \{fqn: ("(?:[^"\\]|\\.)*")\}\), \(b:Symbol \{fqn: ("(?:[^"\\]|\\.)*")\}\) MERGE \(a\)-\[:CALLS\]->\(b\);$`)

// CanonCypher sorts the edge statements and drops their direction.
func CanonCypher(text string) string {
	var head, edges []string
	for _, ln := range strings.Split(text, "\n") {
		if m := cypherEdgeRe.FindStringSubmatch(ln); m != nil {
			edges = append(edges, "CALLS "+undirected(m[1], m[2]))
			continue
		}
		head = append(head, ln)
	}
	sort.Strings(edges)
	// head ends with the blank line(s) after the node list; edges follow.
	return strings.TrimRight(strings.Join(head, "\n"), "\n") + "\n\n" + strings.Join(edges, "\n") + "\n"
}

// CanonViz reduces the data embedded in the visualisation page to its
// invariant part. nodes, edges, godNodes and physics are the raw JSON values
// of the page's variables.
func (e *Env) CanonViz(t testing.TB, nodes, edges, godNodes, physics string) string {
	t.Helper()
	var ns []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Size  int    `json:"size"`
		Shape string `json:"shape"`
		Color string `json:"color"`
	}
	if err := json.Unmarshal([]byte(nodes), &ns); err != nil {
		t.Fatalf("viz nodes: %v", err)
	}
	var es []struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(edges), &es); err != nil {
		t.Fatalf("viz edges: %v", err)
	}
	var gods []community.GodNode
	if err := json.Unmarshal([]byte(godNodes), &gods); err != nil {
		t.Fatalf("viz god nodes: %v", err)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "nodes=%d edges=%d god_nodes=%d physics=%s\n", len(ns), len(es), len(gods), physics)
	lines := make([]string, 0, len(ns))
	for _, n := range ns {
		lines = append(lines, fmt.Sprintf("node %s label=%s shape=%s size=%d color=%s", n.ID, n.Label, n.Shape, n.Size, n.Color))
	}
	sort.Strings(lines)
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
	pairs := make([]string, 0, len(es))
	for _, x := range es {
		pairs = append(pairs, "edge "+undirected(x.From, x.To))
	}
	sort.Strings(pairs)
	for _, p := range pairs {
		sb.WriteString(p + "\n")
	}
	sort.Slice(gods, func(i, j int) bool { return gods[i].ID < gods[j].ID })
	for _, g := range gods {
		fmt.Fprintf(&sb, "god %s kind=%s in=%d out=%d total=%d\n", g.ID, g.Kind, g.InDeg, g.OutDeg, g.TotalDeg)
	}
	return e.Normalize(sb.String())
}
