//go:build treesitter && !windows

package mcp

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
)

// RFC 004 A0: the MCP graph tools on the Kùzu (format-1) path, frozen as
// goldens under internal/graph/testdata/analysis_golden/mcp_*. Regenerate with
//
//	go test -tags treesitter -run TestGraphToolsGolden ./internal/mcp -update
//
// The direction- and community-dependent numbers are not deterministic on the
// Kùzu path; see internal/graph/analysisgolden/canon.go for what is kept.

func callTool(t *testing.T, h func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error), args map[string]interface{}) string {
	t.Helper()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	return res.Content[0].(mcpsdk.TextContent).Text
}

// canonicalRepoMap sorts the "### file" groups and the "- kind `name`" lines
// under each (their order follows run-dependent PageRank scores).
func canonicalRepoMap(text string) string {
	groups := map[string][]string{}
	var files []string
	var head []string
	cur := ""
	for _, ln := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(ln, "### "):
			cur = strings.TrimPrefix(ln, "### ")
			files = append(files, cur)
		case cur != "" && strings.HasPrefix(ln, "- "):
			groups[cur] = append(groups[cur], ln)
		case cur == "":
			head = append(head, ln)
		}
	}
	sort.Strings(files)
	out := head
	for _, f := range files {
		out = append(out, "### "+f)
		l := append([]string{}, groups[f]...)
		sort.Strings(l)
		out = append(out, l...)
	}
	return strings.Join(out, "\n") + "\n"
}

// canonicalRisk keeps, per symbol, the kind and total degree (IN+OUT), sorted;
// with byFile only the file names.
func canonicalRisk(t *testing.T, text string, byFile bool) string {
	t.Helper()
	var head, rows []string
	for _, ln := range strings.Split(text, "\n") {
		f := strings.Fields(ln)
		switch {
		case len(f) == 0 || strings.HasPrefix(ln, "─") || strings.Contains(ln, "Risk Analysis") || f[0] == "SYMBOL" || f[0] == "FILE":
			head = append(head, ln)
		case byFile:
			rows = append(rows, f[0])
		default:
			if len(f) != 7 {
				t.Fatalf("risk row %q: want 7 fields", ln)
			}
			in, err1 := strconv.Atoi(f[3])
			out, err2 := strconv.Atoi(f[4])
			if err1 != nil || err2 != nil {
				t.Fatalf("risk row %q: bad degree", ln)
			}
			rows = append(rows, fmt.Sprintf("%-40s %-10s total_degree=%d", f[0], f[1], in+out))
		}
	}
	sort.Strings(rows)
	return strings.Join(append(append([]string{}, head...), rows...), "\n") + "\n"
}

// canonicalNavigate sorts the caller/callee lines of every match and the
// matches themselves (Kùzu returns both in engine order), renumbering them.
func canonicalNavigate(text string) string {
	blocks := strings.Split(strings.TrimRight(text, "\n"), "\n\n")
	if len(blocks) == 0 {
		return text
	}
	title := blocks[0]
	var matches []string
	for _, b := range blocks[1:] {
		lines := strings.Split(b, "\n")
		var out []string
		var run []string
		flush := func() {
			sort.Strings(run)
			out = append(out, run...)
			run = nil
		}
		for _, ln := range lines {
			if strings.HasPrefix(ln, "    ") {
				run = append(run, ln)
				continue
			}
			flush()
			out = append(out, ln)
		}
		flush()
		matches = append(matches, strings.Join(out, "\n"))
	}
	strip := func(b string) string {
		_, rest, _ := strings.Cut(b, ". ")
		return rest
	}
	sort.Slice(matches, func(i, j int) bool { return strip(matches[i]) < strip(matches[j]) })
	for i, m := range matches {
		matches[i] = fmt.Sprintf("── %d. %s", i+1, strip(m))
	}
	return strings.Join(append([]string{title}, matches...), "\n\n") + "\n"
}

func TestGraphToolsGolden(t *testing.T) {
	env := analysisgolden.BuildIndex(t)
	s := &Server{gPool: newGraphPool(env.IndexDir)}
	t.Cleanup(s.closeGraphPool)
	idx := analysisgolden.IndexName
	norm := env.Normalize

	// gleann_communities: counts and god nodes of the raw JSON.
	text := callTool(t, s.handleCommunities, map[string]interface{}{"index": idx})
	_, raw, ok := strings.Cut(text, "--- Raw JSON ---\n")
	if !ok {
		t.Fatalf("communities: no raw JSON in %q", text)
	}
	analysisgolden.Check(t, "mcp_communities.txt", env.CanonResult(t, raw))

	// gleann_risk_analysis (all rows, then by file).
	analysisgolden.Check(t, "mcp_risk_analysis.txt",
		canonicalRisk(t, norm(callTool(t, s.handleRiskAnalysis, map[string]interface{}{"index": idx, "top": 1000.0})), false))
	analysisgolden.Check(t, "mcp_risk_analysis_by_file.txt",
		canonicalRisk(t, norm(callTool(t, s.handleRiskAnalysis, map[string]interface{}{"index": idx, "top": 1000.0, "by_file": true})), true))

	// gleann_repo_map with every symbol and no token cut-off.
	analysisgolden.Check(t, "mcp_repo_map.txt",
		canonicalRepoMap(norm(callTool(t, s.handleRepoMap, map[string]interface{}{"index": idx, "top_k": 1000.0, "max_tokens": 1000000.0}))))

	// gleann_navigate_symbol.
	for _, q := range []struct {
		golden, symbol string
		depth          float64
	}{
		{"mcp_navigate_validate.txt", "validate", 1},            // Go, one caller
		{"mcp_navigate_clean.txt", "clean", 2},                  // Python, two callers, 2nd-level callees
		{"mcp_navigate_slug.txt", "slug", 3},                    // TypeScript, 3 levels
		{"mcp_navigate_clamp.txt", "clamp", 1},                  // Rust
		{"mcp_navigate_main.txt", "main", 1},                    // four matches in four languages
		{"mcp_navigate_nomatch.txt", "NoSuchSymbolAnywhere", 1}, // no match text
	} {
		out := callTool(t, s.handleNavigateSymbol, map[string]interface{}{"index": idx, "symbol": q.symbol, "depth": q.depth})
		analysisgolden.Check(t, q.golden, canonicalNavigate(norm(out)))
	}
}
