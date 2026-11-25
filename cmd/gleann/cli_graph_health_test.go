package main

// v1.22 T3/E5: the graph's health record (which extractor wrote it, how Go
// calls were resolved, when the rows were last written) must reach the user
// through `gleann graph stats`, `gleann index info` and `gleann doctor`, and
// the counts `graph explain`, `graph map` and `graph report` print must state
// what they count.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

// graphHealthSetMeta rewrites one graph metadata key of the fixture index, the
// way an older gleann would have left it.
func graphHealthSetMeta(t *testing.T, indexDir, key, value string) {
	t.Helper()
	gs, err := gleann.OpenGraphStore(indexDir, "fixture")
	if err != nil || gs == nil {
		t.Fatalf("OpenGraphStore(fixture): gs=%v err=%v", gs, err)
	}
	defer gs.Close()
	if err := gs.SetMeta(context.Background(), key, value); err != nil {
		t.Fatalf("SetMeta(%s): %v", key, err)
	}
}

func TestCLIGraphStatsPrintsHealthRecord(t *testing.T) {
	e := newCGraphEnv(t)

	r := e.graph("stats")
	wantExit(t, r, 0)
	mustContain(t, "graph stats health", r.Out,
		"Graph — fixture", "Symbols:", "Call edges:",
		"Graph health:", "Graph extractor:", "Resolver:", "Tree-sitter:", "Built at:", "Dirty files:")
	// The fixture has a go.mod, so its Go calls were resolved exactly, and the
	// graph was written by this binary: no rebuild hint.
	if !strings.Contains(r.Out, "Resolver:            exact") {
		t.Errorf("graph stats output, want resolver exact:\n%s", r.Out)
	}
	if strings.Contains(r.Out, "gleann graph build") {
		t.Errorf("a graph written by this binary must not be told to rebuild:\n%s", r.Out)
	}
}

func TestCLIGraphStatsHintsAtRebuildForOlderGraph(t *testing.T) {
	e := newCGraphEnv(t)
	graphHealthSetMeta(t, e.gdir, store.MetaGraphExtractorVersion, "0")

	r := e.graph("stats")
	wantExit(t, r, 0)
	mustContain(t, "graph stats stale graph", r.Out,
		"gleann graph build fixture",
	)
}

func TestCLIGraphStatsJSONCarriesHealth(t *testing.T) {
	e := newCGraphEnv(t)

	r := e.graph("stats", "--json")
	wantExit(t, r, 0)
	var got struct {
		Symbols int `json:"symbols"`
		Health  *struct {
			ExtractorVersion string `json:"extractor_version"`
			Resolver         string `json:"resolver"`
			BuiltAt          string `json:"built_at"`
		} `json:"health"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Out)), &got); err != nil {
		t.Fatalf("graph stats --json is not JSON (%v):\n%s", err, r.Out)
	}
	if got.Symbols == 0 {
		t.Errorf("graph stats --json symbols = 0:\n%s", r.Out)
	}
	if got.Health == nil || got.Health.ExtractorVersion == "" || got.Health.Resolver != "exact" || got.Health.BuiltAt == "" {
		t.Errorf("graph stats --json health = %+v, want this binary's record", got.Health)
	}
}

func TestCLIIndexInfoShowsGraphHealth(t *testing.T) {
	e := newCGraphEnv(t)

	r := e.run("index", "info", "fixture", "--index-dir", e.gdir)
	wantExit(t, r, 0)
	mustContain(t, "index info graph line", r.Out, "Graph:", "extractor v", "resolver exact")

	// An index whose graph predates this binary also gets the hint here.
	graphHealthSetMeta(t, e.gdir, store.MetaGraphExtractorVersion, "0")
	r = e.run("index", "info", "fixture", "--index-dir", e.gdir)
	wantExit(t, r, 0)
	mustContain(t, "index info stale graph", r.Out, "gleann graph build fixture")
}

// graphHealthCompleteMeta fills in the meta.json fields a real index records but
// the analysisgolden fixture omits, so `gleann doctor` can list the index by
// name and sees that it has a code graph.
func graphHealthCompleteMeta(t *testing.T, indexDir, name string) {
	t.Helper()
	path := filepath.Join(indexDir, name, name+".meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	raw["name"] = name
	raw["has_graph"] = true
	out, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDoctorWarnsAboutStaleGraph(t *testing.T) {
	e := newCGraphEnv(t)
	graphHealthCompleteMeta(t, e.gdir, "fixture")
	// Fresh graph (written by this binary): doctor says nothing about it.
	var warns []string
	warnStaleGraphs(e.gdir, []gleann.IndexMeta{{Name: "fixture", HasGraph: true}}, func(s string) { warns = append(warns, s) })
	if len(warns) != 0 {
		t.Errorf("doctor warnings for a current graph = %v, want none", warns)
	}

	graphHealthSetMeta(t, e.gdir, store.MetaGraphExtractorVersion, "0")
	warns = nil
	warnStaleGraphs(e.gdir, []gleann.IndexMeta{{Name: "fixture", HasGraph: true}}, func(s string) { warns = append(warns, s) })
	if len(warns) != 1 || !strings.Contains(warns[0], "gleann graph build fixture") {
		t.Errorf("doctor warnings for a stale graph = %v, want one naming 'gleann graph build fixture'", warns)
	}

	// End to end, through `gleann doctor` itself: the config points doctor at
	// the fixture's index directory, where the graph is now stale.
	e.Ollama.d1SetModels("bge-m3:latest", "qwen3:latest")
	d1WriteConfig(t, e.cliEnv, map[string]any{
		"ollama_host": e.Ollama.URL, "embedding_provider": "ollama", "embedding_model": "bge-m3",
		"llm_provider": "ollama", "llm_model": "qwen3", "index_dir": e.gdir,
	})
	r := e.run("doctor")
	wantExit(t, r, 0)
	mustContain(t, "gleann doctor stale graph", r.Out, "gleann graph build fixture")
}

// TestCLICGraphExplainCountLabelsAreConsistent is the E5 red test: `graph
// explain` used to print "Called by (3)" and "Direct callers: 2" for the same
// symbol, because one count included the test callers and the other did not.
// Each number now says what it counts, and the two add up.
func TestCLICGraphExplainCountLabelsAreConsistent(t *testing.T) {
	e := newCGraphEnv(t)

	r := e.graph("explain", cSvcLoad)
	wantExit(t, r, 0)
	out := r.Out

	calledBy := regexp.MustCompile(`Called by \((\d+),\s*(\d+) production \+ (\d+) test\):`).FindStringSubmatch(out)
	if calledBy == nil {
		t.Fatalf("explain does not label the Called-by count with what it counts:\n%s", out)
	}
	total, err := strconv.Atoi(calledBy[1])
	if err != nil {
		t.Fatalf("Called by count: %v", err)
	}
	production, err := strconv.Atoi(calledBy[2])
	if err != nil {
		t.Fatalf("Called by production count: %v", err)
	}
	testCallers, err := strconv.Atoi(calledBy[3])
	if err != nil {
		t.Fatalf("Called by test-caller count: %v", err)
	}
	if production+testCallers != total {
		t.Errorf("the Called-by split does not add up: %d = %d + %d\n%s", total, production, testCallers, out)
	}
	direct := regexp.MustCompile(`Direct callers \(([^)]*)\):\s+(\d+)`).FindStringSubmatch(out)
	if direct == nil {
		t.Fatalf("explain prints an unqualified Direct callers count:\n%s", out)
	}
	if !strings.Contains(direct[1], "production") {
		t.Errorf("Direct callers label %q must say it counts production callers:\n%s", direct[0], out)
	}
	directN, _ := strconv.Atoi(direct[2])
	if directN != production {
		t.Errorf("the two production-caller counts disagree: Called by %d, Direct callers %d\n%s",
			production, directN, out)
	}
	// The test callers are marked in the list itself, not only in the totals.
	if marked := strings.Count(out, "\n    🧪 ["); marked != testCallers {
		t.Errorf("Called by says %d test callers but the list marks %d:\n%s", testCallers, marked, out)
	}
	// Impact's blast radius is a different question: it walks production
	// callers transitively and collects test callers at any depth, so its
	// "Test callers" count is not the direct one and must not be added to it.
	testLine := regexp.MustCompile(`Test callers:\s+(\d+)`).FindStringSubmatch(out)
	if testLine == nil {
		t.Fatalf("explain prints no Test callers count:\n%s", out)
	}
	if n, _ := strconv.Atoi(testLine[1]); n < testCallers {
		t.Errorf("blast radius test callers %d < direct test callers %d\n%s", n, testCallers, out)
	}
}

func TestCLIGraphMapAndReportStateWhatTheyCount(t *testing.T) {
	e := newCGraphEnv(t)

	// graph map counts the call graph: code symbols and CALLS edges only.
	r := e.graph("map")
	wantExit(t, r, 0)
	mustContain(t, "graph map count labels", r.Out, "code symbols", "call edges")
	if regexp.MustCompile(`Total: \d+ nodes, \d+ edges`).MatchString(r.Out) {
		t.Errorf("graph map still prints bare node/edge counts:\n%s", r.Out)
	}

	// graph report counts the whole graph: symbol nodes plus file nodes, and
	// DECLARES + IMPLEMENTS + CALLS edges.
	report := filepath.Join(t.TempDir(), "report.md")
	r = e.graph("report", "--output", report)
	wantExit(t, r, 0)
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(b), "Nodes (") || !strings.Contains(string(b), "Edges (") {
		t.Errorf("graph report does not say what its Nodes/Edges count:\n%s", b)
	}
}
