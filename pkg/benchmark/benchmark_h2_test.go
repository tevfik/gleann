package benchmark

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func staticSearcher(paths []string, tokens int) SearcherFunc {
	return func(context.Context, string, int) ([]string, int, error) { return paths, tokens, nil }
}

func TestRunRejectsEmptyInputs(t *testing.T) {
	if _, err := NewRunner(nil).Run(context.Background(), 5); err == nil {
		t.Error("no tasks must be an error")
	}
	r := NewRunner([]Task{{ID: "t"}})
	if _, err := r.Run(context.Background(), 5); err == nil {
		t.Error("no strategies must be an error")
	}
}

func TestRunPropagatesSearcherError(t *testing.T) {
	r := NewRunner([]Task{{ID: "t1", Query: "q"}})
	r.RegisterStrategy("broken", func(context.Context, string, int) ([]string, int, error) {
		return nil, 0, errors.New("index missing")
	})
	_, err := r.Run(context.Background(), 5)
	if err == nil || !strings.Contains(err.Error(), "strategy broken failed on task t1: index missing") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunWritesPerTaskLinesAndUsesFirstBaselineWithoutBM25(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pertask.jsonl")
	t.Setenv("GLEANN_BENCH_PERTASK", out)

	tasks := []Task{
		{ID: "t1", Query: "q1", GoldFiles: []string{"a.go"}},
		{ID: "t2", Query: "q2", GoldFiles: []string{"b.go"}},
	}
	r := NewRunner(tasks)
	r.RegisterStrategy("Only", staticSearcher([]string{"a.go", "b.go"}, 100))
	sums, err := r.Run(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	s := sums["Only"]
	// The single strategy is its own baseline: no savings, gain 1.
	if s.TokenSavings != 0 || s.RecallGainX != 1 {
		t.Fatalf("self-baseline: %+v", s)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 2 || lines[0]["task"] != "t1" || lines[1]["task"] != "t2" || lines[0]["strategy"] != "Only" {
		t.Fatalf("per-task lines = %v", lines)
	}
	if lines[0]["r1"] != 1.0 || lines[1]["mrr"] != 0.5 {
		t.Fatalf("metrics in per-task lines = %v", lines)
	}
}

func TestEvaluateTaskWithoutGoldFiles(t *testing.T) {
	res := evaluateTask(Task{ID: "x"}, []string{"a.go", "", "a.go"}, 7, 0)
	if res.Recall5 != 0 || res.MRR != 0 || res.Coverage != 0 || res.Tokens != 7 {
		t.Fatalf("no gold files: %+v", res)
	}
}

func TestRenderMarkdownReportExtraStrategiesAndFindings(t *testing.T) {
	sums := map[string]SummaryResult{
		"BM25":                {Strategy: "BM25", AvgRecall5: 20, AvgRecall10: 90},
		"Vector (DiskANN+PQ)": {Strategy: "Vector (DiskANN+PQ)", AvgRecall5: 50, AvgRecall10: 60, TokenSavings: 30},
		"Custom":              {Strategy: "Custom", AvgRecall5: 10, TokenSavings: -5},
	}
	md := RenderMarkdownReport(sums, 3)
	for _, want := range []string{
		"| **Custom** |",
		"+5.0%",
		"**-30.0%**",
		"- Best Recall@5: Vector (DiskANN+PQ) (50.0%).",
		// The report names the winner of every metric, also when it is the
		// keyword baseline.
		"- Best Recall@10: BM25 (90.0%).",
		"- 3 tasks: one task weighs 33.3 points",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Higher Recall") || strings.Contains(md, "GraphRAG") {
		t.Errorf("report must not claim a gain or mention a strategy that did not run:\n%s", md)
	}
}

// TestRenderMarkdownReportGraphAddsNothing: identical GraphRAG and Hybrid
// numbers are reported as such, and ties name every strategy.
func TestRenderMarkdownReportGraphAddsNothing(t *testing.T) {
	same := SummaryResult{AvgRecall1: 55, AvgRecall5: 80, AvgRecall10: 80, AvgMRR: 0.652}
	hy, gr := same, same
	hy.Strategy, gr.Strategy = "Hybrid", "GraphRAG"
	md := RenderMarkdownReport(map[string]SummaryResult{
		"BM25": {Strategy: "BM25", AvgRecall5: 70, AvgRecall10: 95, AvgMRR: 0.615}, "Hybrid": hy, "GraphRAG": gr,
	}, 20)
	for _, want := range []string{
		"- Best Recall@5: GraphRAG, Hybrid (80.0%).",
		"- Best Recall@10: BM25 (95.0%).",
		"- Best MRR: GraphRAG, Hybrid (0.652).",
		"GraphRAG and Hybrid have the same recall and MRR",
		"- 20 tasks: one task weighs 5.0 points",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
}

// TestRenderMarkdownReportOrdersHybridVariants: the default row comes right
// after the vector backends, GraphRAG follows, and the other hybrid rows are
// ordered by their weight, not by the byte order of "α".
func TestRenderMarkdownReportOrdersHybridVariants(t *testing.T) {
	names := []string{
		"Ripgrep", "Hybrid (α=0.5)", "GraphRAG", "Hybrid (α=0.3)",
		"Hybrid (default, α=0.7)", "Vector (HNSW)", "BM25", "Hybrid (1 chunk/file)",
	}
	sums := make(map[string]SummaryResult, len(names))
	for _, n := range names {
		sums[n] = SummaryResult{Strategy: n, AvgRecall5: 50, AvgRecall10: 60, AvgMRR: 0.5}
	}
	md := RenderMarkdownReport(sums, 10)
	want := []string{
		"| **BM25** |",
		"| **Vector (HNSW)** |",
		"| **Hybrid (default, α=0.7)** |",
		"| **GraphRAG** |",
		"| **Hybrid (1 chunk/file)** |",
		"| **Hybrid (α=0.3)** |",
		"| **Hybrid (α=0.5)** |",
		"| **Ripgrep** |",
	}
	prev := -1
	for _, row := range want {
		i := strings.Index(md, row)
		if i < 0 {
			t.Fatalf("report lacks row %q:\n%s", row, md)
		}
		if i < prev {
			t.Errorf("row %q is out of the expected order:\n%s", row, md)
		}
		prev = i
	}
}

// TestRenderMarkdownReportGraphComparesSameWeight: SearchGraphRAG seeds with
// α=0.5, so its "no change" note names the α=0.5 row, not the 0.7 default.
func TestRenderMarkdownReportGraphComparesSameWeight(t *testing.T) {
	seed := SummaryResult{AvgRecall1: 55, AvgRecall5: 80, AvgRecall10: 80, AvgMRR: 0.652}
	gr, h05 := seed, seed
	gr.Strategy, h05.Strategy = "GraphRAG", "Hybrid (α=0.5)"
	h07 := SummaryResult{Strategy: "Hybrid (default, α=0.7)", AvgRecall1: 50, AvgRecall5: 70, AvgRecall10: 75, AvgMRR: 0.60}
	md := RenderMarkdownReport(map[string]SummaryResult{
		"Hybrid (α=0.5)": h05, "Hybrid (default, α=0.7)": h07, "GraphRAG": gr,
	}, 20)
	if !strings.Contains(md, "GraphRAG and Hybrid (α=0.5) have the same recall and MRR") {
		t.Errorf("the equality note must name the row with GraphRAG's seed weight:\n%s", md)
	}
	if strings.Contains(md, "GraphRAG and Hybrid (default") {
		t.Errorf("the equality note must not compare GraphRAG with the 0.7 default:\n%s", md)
	}
}

func TestRenderJSONRejectsNaN(t *testing.T) {
	if _, err := RenderJSON(map[string]SummaryResult{"x": {AvgMRR: math.NaN()}}); err == nil {
		t.Fatal("NaN metrics cannot be encoded as JSON")
	}
}

func TestAgentEvalWriteReport(t *testing.T) {
	sum, err := RunAgentEvaluation(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "reports", "nested")

	jsonPath := filepath.Join(dir, "agent.json")
	if err := sum.WriteReport(jsonPath, true); err != nil {
		t.Fatal(err)
	}
	var back AgentEvalSummary
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &back); err != nil || back.TotalTasks != sum.TotalTasks || len(back.Tasks) != len(sum.Tasks) {
		t.Fatalf("JSON report round trip: %v %+v", err, back)
	}

	mdPath := filepath.Join(dir, "agent.md")
	if err := sum.WriteReport(mdPath, false); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(md) != sum.FormatMarkdown() {
		t.Fatal("markdown report differs from FormatMarkdown")
	}

	// The parent directory cannot be created below a regular file.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sum.WriteReport(filepath.Join(file, "sub", "r.md"), false); err == nil {
		t.Fatal("WriteReport below a regular file must fail")
	}
}

func TestGenerateGoTasksSkipsAndCaps(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("p/p.go", `package p

// Alpha computes the first value used by the scheduler loop.
func Alpha() {}

// Beta computes the second value used by the scheduler loop.
func Beta() {}

// Gamma computes the third value used by the scheduler loop.
type Gamma struct{}

// helper computes something private that is never a task.
func helper() {}

func Undocumented() {}
`)
	write("p/broken.go", "package p\nfunc (") // unparsable: skipped
	write("vendor/v/v.go", "package v\n// Vendored does a thing that should never show up here.\nfunc Vendored() {}\n")
	write("testdata/t.go", "package t\n// Fixture does a thing that should never show up here.\nfunc Fixture() {}\n")

	tasks, err := GenerateGoTasks(root, GenOptions{Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("Count must cap the tasks: got %d", len(tasks))
	}
	all, err := GenerateGoTasks(root, GenOptions{Kind: "symbol"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tk := range all {
		names = append(names, tk.Description)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"Alpha", "Beta", "Gamma", "Undocumented"} {
		if !strings.Contains(got, want) {
			t.Errorf("symbol tasks lack %s: %s", want, got)
		}
	}
	for _, unwanted := range []string{"helper", "Vendored", "Fixture"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("symbol tasks include %s: %s", unwanted, got)
		}
	}
	// In doc mode an undocumented declaration has no query and is dropped.
	docs, err := GenerateGoTasks(root, GenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range docs {
		if tk.Description == "Undocumented" {
			t.Error("an undocumented declaration produced a doc task")
		}
	}
}

func TestGenerateGoTasksMissingRoot(t *testing.T) {
	if _, err := GenerateGoTasks(filepath.Join(t.TempDir(), "missing"), GenOptions{}); err == nil {
		t.Fatal("a missing root must be an error")
	}
}

// TestRunnerIgnoresTheTasksFile: a result that is the tasks file itself takes
// no rank. With the file in the index it matched every query verbatim and the
// gold file fell from rank 1 to rank 2 (Recall@1 0 instead of 1).
func TestRunnerIgnoresTheTasksFile(t *testing.T) {
	tasks := []Task{{ID: "t1", Query: "how are scores fused", GoldFiles: []string{"pkg/search/fuse.go"}}}
	search := func(context.Context, string, int) ([]string, int, error) {
		return []string{"/src/repo/bench/tasks/repo.json", "/src/repo/pkg/search/fuse.go"}, 10, nil
	}

	plain := NewRunner(tasks)
	plain.RegisterStrategy("BM25", search)
	got, err := plain.Run(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if got["BM25"].AvgRecall1 != 0 {
		t.Fatalf("without the filter Recall@1 = %v, want 0 (the tasks file is rank 1)", got["BM25"].AvgRecall1)
	}

	r := NewRunner(tasks)
	r.RegisterStrategy("BM25", search)
	r.IgnorePaths(TaskFileMatcher("bench/tasks/repo.json"))
	got, err = r.Run(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if got["BM25"].AvgRecall1 != 100 || r.Ignored() != 1 {
		t.Fatalf("Recall@1 = %v, ignored = %d; want 100 and 1", got["BM25"].AvgRecall1, r.Ignored())
	}
}

func TestTaskFileMatcher(t *testing.T) {
	m := TaskFileMatcher("./bench/tasks/gleann.json")
	for p, want := range map[string]bool{
		"/home/u/repo/bench/tasks/gleann.json": true,
		"bench/tasks/gleann.json":              true,
		"tasks/gleann.json":                    true,
		"/home/u/repo/bench/tasks/other.json":  false,
		"/home/u/repo/xtasks/gleann.json":      false,
		"pkg/gleann.json":                      false,
	} {
		if got := m(p); got != want {
			t.Errorf("match(%q) = %v, want %v", p, got, want)
		}
	}
}
