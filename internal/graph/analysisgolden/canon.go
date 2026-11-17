package analysisgolden

// KNOWN NON-DETERMINISM of the former Kùzu path (frozen in the A0 goldens)
//
//   - community.Graph.Edges() emitted every undirected neighbour pair in
//     whichever direction the neighbour map yielded first, so everything built on
//     ExportForAnalysis (PageRank in `graph map`, in/out degree, blast radius and
//     score in `graph risk`) and the edge direction and order of `graph export`
//     and `graph viz` changed from run to run; community.TopRanked broke ties by
//     map order.
//   - The Louvain pass iterated maps, so the number of communities, their
//     members, labels, cohesion, the modularity and the "surprising" cross-
//     community edges differed between runs (20 to 23 communities were seen on
//     the fixture).
//
// The A0 goldens therefore keep only what is invariant across runs: node sets,
// node kinds and files, total degrees (in+out), god nodes, undirected edge
// pairs and the node and edge counts. The SQLite path (RFC 004 A2) is compared
// against exactly these; its own, deterministic outputs are frozen in full
// under sqlite_lite/.

import (
	"regexp"
	"sort"
	"strings"
)

// undirected renders an edge without its (run-dependent) direction.
func undirected(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + " -- " + b
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
