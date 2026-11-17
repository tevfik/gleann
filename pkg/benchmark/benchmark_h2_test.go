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

func TestRenderMarkdownReportExtraStrategiesAndVectorTakeaway(t *testing.T) {
	sums := map[string]SummaryResult{
		"BM25":                {Strategy: "BM25", AvgRecall5: 20},
		"Vector (DiskANN+PQ)": {Strategy: "Vector (DiskANN+PQ)", AvgRecall5: 50, TokenSavings: 30},
		"Custom":              {Strategy: "Custom", AvgRecall5: 10, TokenSavings: -5},
	}
	md := RenderMarkdownReport(sums, 3)
	for _, want := range []string{
		"| **Custom** |",
		"-5.0%",
		"**2.5x Higher Recall**: DiskANN+PQ achieved 50.0%",
		"**-30.0%**",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "GraphRAG achieved") {
		t.Error("no GraphRAG takeaway without GraphRAG results")
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
