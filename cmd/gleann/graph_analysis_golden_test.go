//go:build treesitter && !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/pkg/gleann"
)

// RFC 004 A0: the CLI analysis commands on the Kùzu (format-1) path, frozen as
// goldens under internal/graph/testdata/analysis_golden/cli_*. Regenerate with
//
//	go test -tags treesitter -run TestGraphAnalysisGolden ./cmd/gleann -update
//
// RFC 004 A2 compares the SQLite path's output with these files.

var vizVarRe = regexp.MustCompile(`(?m)^var (nodesData|edgesData|communities|godNodes|physicsEnabled) = (.*);$`)

// vizGraphJSON extracts the graph data embedded in the HTML visualisation and
// reduces it to its invariant part.
func vizGraphJSON(t *testing.T, env *analysisgolden.Env, htmlPath string) string {
	t.Helper()
	b, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	for _, m := range vizVarRe.FindAllStringSubmatch(string(b), -1) {
		vars[m[1]] = m[2]
	}
	if len(vars) != 5 {
		t.Fatalf("viz HTML: found %d of 5 embedded variables", len(vars))
	}
	return env.CanonViz(t, vars["nodesData"], vars["edgesData"], vars["godNodes"], vars["physicsEnabled"])
}

// The Kùzu path is not deterministic for direction- and community-dependent
// numbers; see the comment in internal/graph/analysisgolden/canon.go. The
// canonical* helpers below keep only the invariant part of each output.

// canonicalMap reduces the `graph map` listing (run with --top above the node
// count) to its invariant part: the files and the symbols listed under each,
// sorted, without scores or bars.
func canonicalMap(t *testing.T, text string) string {
	t.Helper()
	groups := map[string][]string{}
	var files []string
	var head, tail []string
	cur := ""
	for _, ln := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(ln, "📄 "):
			cur = strings.TrimPrefix(ln, "📄 ")
			files = append(files, cur)
		case cur != "" && strings.HasPrefix(ln, "   "):
			f := strings.Fields(ln)
			if len(f) != 3 {
				t.Fatalf("map row %q: want 3 fields", ln)
			}
			groups[cur] = append(groups[cur], f[1])
		case cur == "":
			head = append(head, ln)
		default:
			tail = append(tail, ln)
		}
	}
	sort.Strings(files)
	out := append([]string{}, head...)
	for _, f := range files {
		out = append(out, "📄 "+f)
		ids := append([]string{}, groups[f]...)
		sort.Strings(ids)
		for _, id := range ids {
			out = append(out, "   "+id)
		}
	}
	return strings.Join(append(out, tail...), "\n")
}

// canonicalRisk reduces the `graph risk` table to its invariant part: per
// symbol the kind and total degree (IN+OUT); per file (--by-file) only the
// file. Rows are sorted by name.
func canonicalRisk(t *testing.T, text string, byFile bool) string {
	t.Helper()
	var head, rows []string
	for _, ln := range strings.Split(text, "\n") {
		f := strings.Fields(ln)
		switch {
		case len(f) == 0 || strings.HasPrefix(ln, "─") || strings.HasPrefix(ln, "🔥") || f[0] == "SYMBOL" || f[0] == "FILE":
			head = append(head, ln)
		case byFile:
			rows = append(rows, f[0])
		default:
			if len(f) != 8 {
				t.Fatalf("risk row %q: want 8 fields", ln)
			}
			in, err1 := strconv.Atoi(f[4])
			out, err2 := strconv.Atoi(f[5])
			if err1 != nil || err2 != nil {
				t.Fatalf("risk row %q: bad degree", ln)
			}
			rows = append(rows, fmt.Sprintf("%-40s %-10s total_degree=%d", f[0], f[1], in+out))
		}
	}
	sort.Strings(rows)
	return strings.Join(append(append([]string{}, head...), rows...), "\n") + "\n"
}

// canonicalCommunities keeps the node/edge counts and the sorted god nodes of
// the `graph communities` output.
func canonicalCommunities(text string) string {
	var out, gods []string
	inGods := false
	for _, ln := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(ln, "   Nodes:"):
			parts := strings.Split(strings.TrimSpace(ln), " | ")
			out = append(out, strings.Join(parts[:2], " | "))
		case strings.HasPrefix(ln, "🔴"):
			inGods = true
		case strings.HasPrefix(ln, "⚡"):
			inGods = false
		case inGods && strings.HasPrefix(ln, "  ["):
			gods = append(gods, ln)
		}
	}
	sort.Strings(gods)
	return strings.Join(append(append(out, "god nodes:"), gods...), "\n") + "\n"
}

// canonicalReport keeps the headings, the node/edge/god-node counts of the
// summary and the god-node table (sorted, without its rank column) of
// GRAPH_REPORT.md.
func canonicalReport(text string) string {
	var out, gods []string
	section := ""
	for _, ln := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(ln, "## "):
			section = ln
			out = append(out, ln)
		case strings.HasPrefix(ln, "# "), strings.HasPrefix(ln, "Source:"):
			out = append(out, ln)
		case strings.HasPrefix(section, "## Summary") &&
			(strings.HasPrefix(ln, "| Nodes ") || strings.HasPrefix(ln, "| Edges ") || strings.HasPrefix(ln, "| God Nodes ")):
			out = append(out, ln)
		case strings.HasPrefix(section, "## God Nodes") && strings.HasPrefix(ln, "| ") &&
			!strings.HasPrefix(ln, "| Rank"):
			cells := strings.SplitN(ln, "|", 3)
			if len(cells) == 3 {
				gods = append(gods, "|"+cells[2])
			}
		}
	}
	sort.Strings(gods)
	return strings.Join(append(append(out, "god node rows (rank dropped):"), gods...), "\n") + "\n"
}

func TestGraphAnalysisGolden(t *testing.T) {
	env := analysisgolden.BuildIndex(t)
	cfg := gleann.Config{IndexDir: env.IndexDir}
	idx := []string{"--index", analysisgolden.IndexName}
	out := t.TempDir()

	norm := func(s string) string {
		s = strings.ReplaceAll(s, out, "<OUT>")
		return env.Normalize(s)
	}
	run := func(fn func([]string, gleann.Config), args ...string) string {
		return norm(analysisgolden.Capture(t, func() { fn(append(append([]string{}, idx...), args...), cfg) }))
	}

	// graph map / graph risk / graph communities (stdout).
	analysisgolden.Check(t, "cli_map.txt", canonicalMap(t, run(cmdGraphMap, "--top", "1000")))
	analysisgolden.Check(t, "cli_risk.txt", canonicalRisk(t, run(cmdGraphRisk, "--top", "1000"), false))
	analysisgolden.Check(t, "cli_risk_by_file.txt", canonicalRisk(t, run(cmdGraphRisk, "--by-file", "--top", "1000"), true))
	analysisgolden.Check(t, "cli_communities.txt", canonicalCommunities(run(cmdGraphCommunities)))

	// graph export --format graphml | cypher.
	for _, f := range []struct{ format, golden string }{
		{"graphml", "cli_export.graphml"},
		{"cypher", "cli_export_cypher.txt"},
	} {
		path := filepath.Join(out, "export."+f.format)
		stdout := run(cmdGraphExport, "--format", f.format, "--output", path)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("export %s: %v (stdout %q)", f.format, err, stdout)
		}
		text := norm(string(b))
		switch f.format {
		case "graphml":
			text = analysisgolden.CanonGraphML(text)
		case "cypher":
			text = analysisgolden.CanonCypher(text)
		}
		analysisgolden.Check(t, f.golden, text)
	}

	// graph report -> GRAPH_REPORT.md (--docs makes the Source line testable).
	report := filepath.Join(out, "GRAPH_REPORT.md")
	run(cmdGraphReport, "--docs", env.Fixture, "--output", report)
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	analysisgolden.Check(t, "cli_report.md", canonicalReport(norm(string(b))))

	// graph viz -> HTML; only the embedded graph JSON is compared.
	htmlPath := filepath.Join(out, "viz.html")
	run(cmdGraphViz, "--output", htmlPath)
	analysisgolden.Check(t, "cli_viz_graph.txt", vizGraphJSON(t, env, htmlPath))
}
