package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/pkg/gleann"
)

// RFC 004 A2: the CLI analysis commands on the format-2 SQLite graph, in every
// build. The fixture graph of a build without tree-sitter holds the Go files
// only; a -tags treesitter build adds Python, TypeScript and Rust symbols.

// sqliteRun runs the analysis commands on a format-2 index of the fixture and
// returns every output keyed by golden file name (full output, normalised).
type sqliteCLI struct {
	t   *testing.T
	env *analysisgolden.Env
	cfg gleann.Config
	out string
}

func newSQLiteCLI(t *testing.T) *sqliteCLI {
	env := analysisgolden.BuildIndexFormat2(t)
	return &sqliteCLI{t: t, env: env, cfg: gleann.Config{IndexDir: env.IndexDir}, out: t.TempDir()}
}

func (c *sqliteCLI) norm(s string) string {
	return c.env.Normalize(strings.ReplaceAll(s, c.out, "<OUT>"))
}

func (c *sqliteCLI) run(fn func([]string, gleann.Config), args ...string) string {
	idx := []string{"--index", analysisgolden.IndexName}
	return c.norm(analysisgolden.Capture(c.t, func() { fn(append(idx, args...), c.cfg) }))
}

func (c *sqliteCLI) file(name string) string {
	b, err := os.ReadFile(filepath.Join(c.out, name))
	if err != nil {
		c.t.Fatal(err)
	}
	return c.norm(string(b))
}

// all returns every analysis output, full and unreduced.
func (c *sqliteCLI) all() map[string]string {
	t := c.t
	res := map[string]string{
		"cli_map.txt":          c.run(cmdGraphMap, "--top", "1000"),
		"cli_map_top5.txt":     c.run(cmdGraphMap, "--top", "5"),
		"cli_risk.txt":         c.run(cmdGraphRisk, "--top", "1000"),
		"cli_risk_by_file.txt": c.run(cmdGraphRisk, "--by-file", "--top", "1000"),
		"cli_communities.txt":  c.run(cmdGraphCommunities),
	}
	for _, f := range []struct{ format, golden string }{
		{"graphml", "cli_export.graphml"},
		{"cypher", "cli_export_cypher.txt"},
	} {
		path := filepath.Join(c.out, "export."+f.format)
		c.run(cmdGraphExport, "--format", f.format, "--output", path)
		res[f.golden] = c.file("export." + f.format)
	}
	report := filepath.Join(c.out, "GRAPH_REPORT.md")
	c.run(cmdGraphReport, "--docs", c.env.Fixture, "--output", report, "--force")
	res["cli_report.md"] = c.file("GRAPH_REPORT.md")

	htmlPath := filepath.Join(c.out, "viz.html")
	c.run(cmdGraphViz, "--output", htmlPath)
	b, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	res["cli_viz.html"] = c.norm(string(b))

	wiki := filepath.Join(c.out, "wiki")
	c.run(cmdGraphWiki, "--output", wiki)
	entries, err := os.ReadDir(wiki)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(wiki, e.Name()))
		sb.WriteString("=== " + e.Name() + "\n" + string(b))
	}
	res["cli_wiki.txt"] = c.norm(sb.String())
	return res
}

// TestGraphAnalysisSQLiteDeterministic runs every analysis command repeatedly
// on the same index: the Kùzu-era path gave different PageRank, risk, export
// order and community counts from run to run (A0 finding); the SQLite path
// must not.
func TestGraphAnalysisSQLiteDeterministic(t *testing.T) {
	c := newSQLiteCLI(t)
	first := c.all()
	for name, v := range first {
		if strings.TrimSpace(v) == "" {
			t.Errorf("%s: empty output", name)
		}
	}
	for i := 0; i < 4; i++ {
		again := c.all()
		for name, want := range first {
			if again[name] != want {
				t.Fatalf("run %d: %s differs from the first run", i+2, name)
			}
		}
	}
}
