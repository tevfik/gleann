package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/benchmark"
)

// CLI tests of `gleann benchmark` / `gleann bench` against an index built
// through the fake Ollama.

// cWriteTasks writes a ContextBench task file and returns its path.
func cWriteTasks(t *testing.T, path string, tasks []benchmark.Task) string {
	t.Helper()
	data, err := json.Marshal(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var cBenchTasks = []benchmark.Task{
	{ID: "t1", Query: "ValidateToken bearer token", GoldFiles: []string{"auth/token.go"}},
	{ID: "t2", Query: "token bucket rate limiter bursts", GoldFiles: []string{"README.md"}},
}

func TestCLICBenchUsageAndArgumentErrors(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("bench")
	wantExit(t, r, 1)
	mustContain(t, "bench without args", r.Out, "gleann benchmark / gleann bench", "--gen-tasks")

	// --docs alone (no index) is the token-reduction mode and needs --index.
	r = e.run("benchmark", "--docs", e.Docs)
	wantExit(t, r, 1)
	mustContain(t, "bench --docs without --index", r.Out, "Usage:")

	r = e.run("bench", "--suite", "contextbench")
	wantExit(t, r, 1)
	mustContain(t, "suite without index", r.Err, "--index <name> is required")

	r = e.run("bench", "--index", "missing", "--tasks", "x.json")
	wantExit(t, r, 1)
	mustContain(t, "unknown index", r.Err, `error loading index "missing"`)

	r = e.run("bench", "--bogus")
	if r.Code == 0 {
		t.Errorf("bench --bogus must fail: %q", r.combined())
	}
}

func TestBenchmarkUsageTextHonesty(t *testing.T) {
	out := captureHelp(t, printBenchmarkUsage)
	if strings.Contains(out, "docs/evaluation.md") {
		t.Errorf("usage text must not suggest overwriting docs/evaluation.md")
	}
	if strings.Contains(out, "auto-generates if omitted") {
		t.Errorf("usage text must not claim --tasks auto-generates when omitted")
	}
	if idxAgent := strings.Index(out, "Agent-Level"); idxAgent != -1 {
		idxEmpirical := strings.Index(out, "SWE-Bench / ContextBench")
		if idxEmpirical != -1 && idxAgent < idxEmpirical {
			t.Errorf("simulated agent suite must be moved to the end of the usage text, not listed before empirical evaluation")
		}
	}
	if !strings.Contains(out, "default: bench/tasks/<index>.json") {
		t.Errorf("usage text should state default task file path")
	}
	if !strings.Contains(out, "fallback: bench/tasks/gleann.json") {
		t.Errorf("usage text should state fallback task file path (bench/tasks/gleann.json)")
	}
}

func TestCLIBenchFallbackToDefaultTasksNotice(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("other", "--graph")

	// Create bench/tasks/gleann.json in test workspace
	tasksPath := filepath.Join(e.Home, "bench", "tasks", "gleann.json")
	cWriteTasks(t, tasksPath, cBenchTasks)

	// Change cwd to e.Home so bench/tasks/gleann.json is found
	t.Chdir(e.Home)

	r := e.run("bench", "--index", "other", "--top-k", "3")
	wantExit(t, r, 0)
	mustContain(t, "fallback notice on stderr", r.Err, "note:", "falling back to default tasks", filepath.Join("bench", "tasks", "gleann.json"))
}

func TestCLICBenchContextSuite(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--graph")

	// Task file errors.
	r := e.run("bench", "--index", "demo")
	wantExit(t, r, 1)
	mustContain(t, "no tasks file", r.Err, "--tasks <file.json> is required")

	r = e.run("bench", "--index", "demo", "--tasks", filepath.Join(e.Home, "nope.json"))
	wantExit(t, r, 1)
	mustContain(t, "unreadable tasks", r.Err, "error reading tasks file")

	bad := filepath.Join(e.Home, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("bench", "--index", "demo", "--tasks", bad)
	wantExit(t, r, 1)
	mustContain(t, "bad tasks json", r.Err, "error parsing tasks json")

	empty := cWriteTasks(t, filepath.Join(e.Home, "empty.json"), nil)
	r = e.run("bench", "--index", "demo", "--tasks", empty)
	wantExit(t, r, 1)
	mustContain(t, "empty tasks", r.Err, "no benchmark tasks found")

	// Markdown report, printed and saved; the graph adds the GraphRAG strategy.
	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)
	report := filepath.Join(e.Home, "report.md")
	r = e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "3", "--output", report)
	wantExit(t, r, 0)
	mustContain(t, "bench report", r.Out, `retrieval suite on index "demo" (2 tasks)`,
		"BM25", "Hybrid (default, α=0.7)", "Hybrid (α=0.5)", "Vector (HNSW)",
		"Hybrid (1 chunk/file)", "GraphRAG", "Report saved to "+report)
	if b, err := os.ReadFile(report); err != nil || !strings.Contains(string(b), "Hybrid") {
		t.Errorf("report file: %v %.200s", err, b)
	}

	// JSON output, with the rerank and HyDE strategies switched on by env.
	t.Setenv("GLEANN_BENCH_RERANK", "1")
	t.Setenv("GLEANN_BENCH_RERANK_URL", e.Ollama.URL)
	t.Setenv("GLEANN_BENCH_RERANK_POOL", "5")
	t.Setenv("GLEANN_BENCH_RERANK_CHARS", "100")
	t.Setenv("GLEANN_BENCH_EXPAND_MODEL", "llama3.2")
	jsonOut := filepath.Join(e.Home, "report.json")
	r = e.run("bench", "--index", "demo", "--tasks", tasks, "--json", "--output", jsonOut)
	wantExit(t, r, 0)
	var summaries map[string]benchmark.SummaryResult // keyed by strategy
	if err := json.Unmarshal([]byte(r.Out), &summaries); err != nil {
		t.Fatalf("bench --json is not a strategy map: %v\n%s", err, r.Out)
	}
	var names []string
	for name := range summaries {
		names = append(names, name)
	}
	all := strings.Join(names, "|")
	mustContain(t, "bench --json strategies", all, "Ripgrep", "Rerank pool=5 chars=100", "+skip-ident",
		"Hybrid + HyDE(llama3.2)", "Hybrid + HyDE + rerank pool=5 +skip-ident")
	if e.Ollama.count("/v1/rerank") == 0 || e.Ollama.count("/api/chat") == 0 {
		t.Errorf("rerank (%d) and HyDE (%d) strategies did not call the servers",
			e.Ollama.count("/v1/rerank"), e.Ollama.count("/api/chat"))
	}
	if b, err := os.ReadFile(jsonOut); err != nil || !json.Valid(b) {
		t.Errorf("--json --output file: %v", err)
	}
}

// TestCLICBenchHybridDefaultStrategyName: the benchmark row that measures what
// `gleann search` does must carry the configured default weight (0.7 unless
// --hybrid changed it), not a fixed α=0.5. The named variants stay available
// and no weight is measured twice.
func TestCLICBenchHybridDefaultStrategyName(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)

	names := func(extra ...string) map[string]bool {
		t.Helper()
		args := append([]string{"bench", "--index", "demo", "--tasks", tasks, "--top-k", "3", "--json"}, extra...)
		r := e.run(args...)
		wantExit(t, r, 0)
		var summaries map[string]benchmark.SummaryResult
		if err := json.Unmarshal([]byte(r.Out), &summaries); err != nil {
			t.Fatalf("bench --json %v: %v\n%s", extra, err, r.Out)
		}
		out := make(map[string]bool, len(summaries))
		for name := range summaries {
			out[name] = true
		}
		return out
	}

	got := names()
	for _, want := range []string{"Hybrid (default, α=0.7)", "Hybrid (α=0.5)", "Hybrid (α=0.3)"} {
		if !got[want] {
			t.Errorf("strategies lack %q: %v", want, got)
		}
	}
	if got["Hybrid"] {
		t.Errorf("a row named only \"Hybrid\" is ambiguous: it hid whether it measured the 0.7 default or α=0.5")
	}

	// A non-default configured weight renames the default row.
	got = names("--hybrid", "0.6")
	if !got["Hybrid (default, α=0.6)"] {
		t.Errorf("--hybrid 0.6 must name the default row α=0.6: %v", got)
	}
	if got["Hybrid (default, α=0.7)"] {
		t.Errorf("--hybrid 0.6 must not keep the 0.7 default row: %v", got)
	}

	// A configured default equal to a named variant must not appear twice.
	for _, alpha := range []string{"0.5", "0.3"} {
		got = names("--hybrid", alpha)
		if !got["Hybrid (default, α="+alpha+")"] {
			t.Errorf("--hybrid %s must name the default row: %v", alpha, got)
		}
		if got["Hybrid (α="+alpha+")"] {
			t.Errorf("--hybrid %s must not register the same weight as a second row: %v", alpha, got)
		}
	}
}

func TestCLICBenchDefaultTasksFile(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	// bench/tasks/<index>.json in the working directory is picked up.
	cWriteTasks(t, filepath.Join("bench", "tasks", "demo.json"), cBenchTasks[:1])
	r := e.run("bench", "--index", "demo", "--json")
	wantExit(t, r, 0)
	if !json.Valid([]byte(r.Out)) {
		t.Fatalf("bench --json: %s", r.Out)
	}
	mustNotContain(t, "bench without graph", r.Out, "GraphRAG")

	// ... and else bench/tasks/gleann.json.
	if err := os.Remove(filepath.Join("bench", "tasks", "demo.json")); err != nil {
		t.Fatal(err)
	}
	cWriteTasks(t, filepath.Join("bench", "tasks", "gleann.json"), cBenchTasks)
	r = e.run("bench", "--index", "demo")
	wantExit(t, r, 0)
	mustContain(t, "bench with gleann.json", r.Out, "(2 tasks)")
}

func TestCLICBenchTokenReduction(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	// Binary and non-UTF-8 files are not counted.
	e.write("logo.png", "\x89PNG")
	e.write("blob.txt", "\xff\xfe\xfd")
	e.build("demo", "--graph")

	r := e.run("benchmark", "--index", "demo", "--docs", e.Docs, "--top-k", "2")
	wantExit(t, r, 0)
	mustContain(t, "token reduction", r.Out, "Token Reduction Analysis", "Index: demo",
		"Files: 4", "Token Reduction:", "top-2 passages", "Phase 4: AST Graph Stats", "Graph storage: demo.db (")

	// An empty corpus cannot be compared.
	emptyDocs := t.TempDir()
	r = e.run("benchmark", "--index", "nosuch", "--docs", emptyDocs)
	wantExit(t, r, 0)
	mustContain(t, "empty corpus", r.Out, "Files: 0", "Could not calculate reduction")
}

func TestCLICBenchGenTasks(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	r := e.run("bench", "--gen-tasks", e.Docs, "--kind", "symbol", "--count", "5")
	wantExit(t, r, 0)
	var tasks []benchmark.Task
	if err := json.Unmarshal([]byte(r.Out), &tasks); err != nil || len(tasks) == 0 {
		t.Fatalf("gen-tasks: %v %s", err, r.Out)
	}
	found := false
	for _, tk := range tasks {
		if tk.Query == "ValidateToken" && reflect.DeepEqual(tk.GoldFiles, []string{"auth/token.go"}) {
			found = true
		}
	}
	if !found {
		t.Errorf("gen-tasks has no ValidateToken task: %+v", tasks)
	}

	out := filepath.Join(e.Home, "tasks.json")
	r = e.run("bench", "--gen-tasks", e.Docs, "--kind", "symbol", "--output", out)
	wantExit(t, r, 0)
	mustContain(t, "gen-tasks --output", r.Out, "wrote ", "tasks to "+out)
	if b, err := os.ReadFile(out); err != nil || !json.Valid(b) {
		t.Errorf("gen-tasks output file: %v", err)
	}

	r = e.run("bench", "--gen-tasks", e.Docs, "--kind", "symbol", "--output", filepath.Join(e.Home, "no", "dir", "t.json"))
	wantExit(t, r, 1)
	mustContain(t, "gen-tasks unwritable", r.Err, "error:")

	r = e.run("bench", "--gen-tasks", e.Docs, "--kind", "weird")
	wantExit(t, r, 1)
	mustContain(t, "gen-tasks bad kind", r.Err, `unknown task kind "weird"`)

	r = e.run("bench", "--gen-tasks", t.TempDir())
	wantExit(t, r, 1)
	mustContain(t, "gen-tasks empty tree", r.Err, "no tasks could be generated")
}

func TestCLICBenchAgentSuite(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("bench", "--agent")
	wantExit(t, r, 0)
	mustContain(t, "bench --agent", r.Err, benchmark.SimulationNotice)
	if strings.TrimSpace(r.Out) == "" {
		t.Error("bench --agent printed no report")
	}

	out := filepath.Join(e.Home, "agent.json")
	r = e.run("bench", "--suite", "agent", "--json", "--output", out)
	wantExit(t, r, 0)
	mustContain(t, "bench agent --output", r.Out, "Report saved to "+out)
	js := r.Out[strings.Index(r.Out, "{"):]
	var summary map[string]any
	if err := json.Unmarshal([]byte(js), &summary); err != nil {
		t.Fatalf("bench --agent --json: %v\n%s", err, r.Out)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("agent report not written: %v", err)
	}

	r = e.run("bench", "--agent", "--output", e.Home) // a directory
	wantExit(t, r, 1)
	mustContain(t, "agent report unwritable", r.Err, "error writing report")
}

// requireRipgrep skips the calling test when ripgrep is not on PATH. The
// lexical baseline shells out to rg, so tests that exercise it for real
// (rather than its "unavailable" reporting) can only run where rg exists.
func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not installed on PATH")
	}
}

func TestCLICBenchHelpers(t *testing.T) {
	for _, c := range []struct {
		in   int64
		want string
	}{{0, "0 B"}, {1023, "1023 B"}, {1024, "1.0 KB"}, {1536, "1.5 KB"}, {5 << 20, "5.0 MB"}, {3 << 30, "3.0 GB"}} {
		if got := benchFormatBytes(c.in); got != c.want {
			t.Errorf("benchFormatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
	for ext, want := range map[string]bool{".png": true, ".db": true, ".pdf": true, ".go": false, ".md": false, "": false} {
		if isBinaryExt(ext) != want {
			t.Errorf("isBinaryExt(%q) = %v", ext, !want)
		}
	}
	if getMetaStr(nil, "x") != "" || getMetaStr(map[string]any{"x": 3}, "x") != "" || getMetaStr(map[string]any{"x": "v"}, "x") != "v" {
		t.Error("getMetaStr")
	}

	// The lexical baseline picks the longest query word and greps the
	// working directory; an empty query finds nothing. Needs rg.
	requireRipgrep(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("the quicksilverword is here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nothing"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _, err := runRipgrepSearch(dir, "find quicksilverword", 5)
	if err != nil || len(files) != 1 || files[0] != "a.txt" {
		t.Errorf("runRipgrepSearch = %v, %v; want [a.txt]", files, err)
	}
	if files, _, _ := runRipgrepSearch(dir, "   ", 5); files != nil {
		t.Errorf("empty query = %v", files)
	}
	if files, _, _ := runRipgrepSearch(dir, `"()"`, 5); files != nil {
		t.Errorf("punctuation-only query = %v", files)
	}
}

func TestCLIRipgrepBaselineFixtureRankingAndTokens(t *testing.T) {
	requireRipgrep(t)
	fixtureDir := t.TempDir()
	// zzz.txt sorts alphabetically AFTER aaa.txt, but has 3 matching lines
	zzzContent := "resolver line 1\nendpoint line 2\nresolver line 3\nother line 4\n"
	// aaa.txt sorts alphabetically BEFORE zzz.txt, but has only 1 matching line
	aaaContent := "endpoint only once in this file\nother line 2\nother line 3\n"
	// stop.txt has only stopwords
	stopContent := "the is at on for with which and"

	if err := os.WriteFile(filepath.Join(fixtureDir, "zzz.txt"), []byte(zzzContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "aaa.txt"), []byte(aaaContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "stop.txt"), []byte(stopContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Change cwd to another isolated directory to prove independence from cwd.
	otherDir := t.TempDir()
	t.Chdir(otherDir)

	files, tokens, err := runRipgrepSearch(fixtureDir, "the endpoint and resolver in query", 2)
	if err != nil {
		t.Fatalf("runRipgrepSearch failed: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("expected at least 2 files, got %v", files)
	}
	if files[0] != "zzz.txt" || files[1] != "aaa.txt" {
		t.Errorf("expected ranking [zzz.txt, aaa.txt], got %v", files)
	}
	if tokens <= 0 {
		t.Errorf("expected positive token count for retrieved files, got %d", tokens)
	}
}

func TestCLIRipgrepGitignoreInNonGitDir(t *testing.T) {
	requireRipgrep(t)
	fixtureDir := t.TempDir()
	// Ensure no .git exists in fixtureDir
	if err := os.WriteFile(filepath.Join(fixtureDir, ".gitignore"), []byte("secret.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secretContent := "secretword line 1\nsecretword line 2\nsecretword line 3\n"
	visibleContent := "secretword line 1\n"
	if err := os.WriteFile(filepath.Join(fixtureDir, "secret.md"), []byte(secretContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "visible.md"), []byte(visibleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	files, _, err := runRipgrepSearch(fixtureDir, "secretword", 5)
	if err != nil {
		t.Fatalf("runRipgrepSearch failed: %v", err)
	}
	for _, f := range files {
		if f == "secret.md" {
			t.Errorf("expected secret.md to be ignored via .gitignore in non-git directory, but ripgrep returned it: %v", files)
		}
	}
	if len(files) != 1 || files[0] != "visible.md" {
		t.Errorf("expected [visible.md], got %v", files)
	}
}

func TestCLICBenchCompareStandalone(t *testing.T) {
	e := newCLIEnv(t)
	dir := e.Home
	fileA := filepath.Join(dir, "runA.jsonl")
	fileB := filepath.Join(dir, "runB.jsonl")

	contentA := `{"task":"t1","strategy":"BM25","r1":0.0,"r5":0.5,"r10":1.0,"mrr":0.3,"tokens":1500}
{"task":"t2","strategy":"BM25","r1":0.0,"r5":0.0,"r10":0.5,"mrr":0.2,"tokens":1600}
`
	contentB := `{"task":"t1","strategy":"Hybrid","r1":1.0,"r5":1.0,"r10":1.0,"mrr":1.0,"tokens":1200}
{"task":"t2","strategy":"Hybrid","r1":0.0,"r5":1.0,"r10":1.0,"mrr":0.5,"tokens":1300}
`
	if err := os.WriteFile(fileA, []byte(contentA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte(contentB), 0o644); err != nil {
		t.Fatal(err)
	}

	r := e.run("bench", "--compare", fileA+","+fileB)
	wantExit(t, r, 0)
	mustContain(t, "compare markdown", r.Out, "Paired comparison: Hybrid vs BM25 (2 tasks)",
		"MRR", "Recall@1", "Recall@5", "Recall@10", "Sign Test", "95% Bootstrap CI")

	r = e.run("bench", "--compare", fileA+","+fileB, "--json")
	wantExit(t, r, 0)
	var comp benchmark.ComparisonResult
	if err := json.Unmarshal([]byte(r.Out), &comp); err != nil {
		t.Fatalf("unmarshal compare json: %v\n%s", err, r.Out)
	}
	if comp.TaskCount != 2 || comp.BaselineName != "BM25" || comp.CandidateName != "Hybrid" {
		t.Errorf("unexpected comparison json: %+v", comp)
	}
}

func TestCLICBenchCompareInRun(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--graph")

	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)
	perTaskOut := filepath.Join(e.Home, "pertask.jsonl")

	r := e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "3",
		"--per-task", perTaskOut, "--compare", "BM25,Hybrid (default, α=0.7)")
	wantExit(t, r, 0)
	mustContain(t, "in-run compare", r.Out, "Paired comparison: Hybrid (default, α=0.7) vs BM25", "Sign Test", "95% Bootstrap CI")

	// Verify per-task file was written
	if b, err := os.ReadFile(perTaskOut); err != nil || len(b) == 0 {
		t.Fatalf("per-task file not written: %v", err)
	}
}

func TestCLICBenchInRunCompareMalformedSpec(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--graph")

	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)

	// In-run compare with single strategy spec
	r := e.run("bench", "--index", "demo", "--tasks", tasks, "--compare", "Hybrid")
	wantExit(t, r, 1)
	mustContain(t, "single strategy compare error", r.Err, "error:", "--compare requires two strategies separated by comma")

	// In-run compare with trailing comma / empty strategy
	r = e.run("bench", "--index", "demo", "--tasks", tasks, "--compare", "Hybrid,")
	wantExit(t, r, 1)
	mustContain(t, "trailing comma compare error", r.Err, "error:", "--compare requires two strategies separated by comma")
}

func TestCLICBenchPerTaskNoEnvLeak(t *testing.T) {
	// Ensure clean env before test
	_ = os.Unsetenv("GLEANN_BENCH_PERTASK")
	defer os.Unsetenv("GLEANN_BENCH_PERTASK")

	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--graph")

	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)
	perTaskFile := filepath.Join(e.Home, "run1.jsonl")

	// Run 1: with --per-task
	r1 := e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "3", "--per-task", perTaskFile)
	wantExit(t, r1, 0)

	// Check env was NOT mutated
	if leaked := os.Getenv("GLEANN_BENCH_PERTASK"); leaked != "" {
		t.Errorf("GLEANN_BENCH_PERTASK leaked into process env: %q", leaked)
	}

	info1, err := os.Stat(perTaskFile)
	if err != nil || info1.Size() == 0 {
		t.Fatalf("expected per-task file to be created: %v", err)
	}
	content1, _ := os.ReadFile(perTaskFile)

	// Run 2: without --per-task
	r2 := e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "2")
	wantExit(t, r2, 0)

	// Check env is still not set
	if leaked := os.Getenv("GLEANN_BENCH_PERTASK"); leaked != "" {
		t.Errorf("GLEANN_BENCH_PERTASK leaked after second run: %q", leaked)
	}

	// Verify run1.jsonl was NOT overwritten or truncated
	content2, err := os.ReadFile(perTaskFile)
	if err != nil {
		t.Fatalf("failed reading perTaskFile: %v", err)
	}
	if string(content1) != string(content2) {
		t.Errorf("per-task file was overwritten by subsequent run without --per-task flag: size before=%d after=%d", len(content1), len(content2))
	}
}

func TestCLICBenchCompareStandaloneMultiStrategy(t *testing.T) {
	e := newCLIEnv(t)
	dir := e.Home
	fileA := filepath.Join(dir, "multiA.jsonl")
	fileB := filepath.Join(dir, "multiB.jsonl")

	contentA := `{"task":"t1","strategy":"BM25","r1":0.0,"r5":0.5,"r10":1.0,"mrr":0.1,"tokens":1500}
{"task":"t1","strategy":"Hybrid","r1":1.0,"r5":1.0,"r10":1.0,"mrr":0.9,"tokens":1200}
{"task":"t2","strategy":"BM25","r1":0.0,"r5":0.0,"r10":0.5,"mrr":0.1,"tokens":1600}
{"task":"t2","strategy":"Hybrid","r1":0.0,"r5":1.0,"r10":1.0,"mrr":0.9,"tokens":1300}
`
	contentB := `{"task":"t1","strategy":"Hybrid","r1":1.0,"r5":1.0,"r10":1.0,"mrr":0.9,"tokens":1200}
{"task":"t1","strategy":"BM25","r1":0.0,"r5":0.5,"r10":1.0,"mrr":0.1,"tokens":1500}
{"task":"t2","strategy":"Hybrid","r1":0.0,"r5":1.0,"r10":1.0,"mrr":0.9,"tokens":1300}
{"task":"t2","strategy":"BM25","r1":0.0,"r5":0.0,"r10":0.5,"mrr":0.1,"tokens":1600}
`
	if err := os.WriteFile(fileA, []byte(contentA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte(contentB), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without strategy suffix on multi-strategy files, must fail with clear error
	r := e.run("bench", "--compare", fileA+","+fileB)
	wantExit(t, r, 1)
	mustContain(t, "multi-strategy error", r.Err, "contains multiple strategies", "BM25", "Hybrid")

	// With explicit suffix, comparison succeeds and computes diff correctly (+0.8, not -0.8)
	r = e.run("bench", "--compare", fileA+":BM25,"+fileB+":Hybrid", "--json")
	wantExit(t, r, 0)
	var comp benchmark.ComparisonResult
	if err := json.Unmarshal([]byte(r.Out), &comp); err != nil {
		t.Fatalf("unmarshal compare json: %v\n%s", err, r.Out)
	}
	if comp.BaselineName != "BM25" || comp.CandidateName != "Hybrid" {
		t.Fatalf("unexpected strategies: baseline=%s candidate=%s", comp.BaselineName, comp.CandidateName)
	}
	for _, m := range comp.Metrics {
		if m.Metric == "MRR" {
			if math.Abs(m.Diff-0.8) > 1e-4 {
				t.Errorf("expected MRR diff +0.8, got %f", m.Diff)
			}
		}
	}
}

func TestCLIRipgrepMissingSourceDirAndExit2(t *testing.T) {
	requireRipgrep(t)
	// 1. Missing source directory must return ErrStrategyUnavailable
	missingDir := filepath.Join(t.TempDir(), "moved-away")
	files, tokens, err := runRipgrepSearch(missingDir, "resolver endpoint", 5)
	if err == nil {
		t.Fatalf("expected error for missing sourceDir, got files=%v tokens=%d", files, tokens)
	}
	if !errors.Is(err, benchmark.ErrStrategyUnavailable) {
		t.Errorf("expected error wrapping ErrStrategyUnavailable, got %v", err)
	}

	// 2. rg exit code 2 (e.g. unreadable subdirectory) must return wrapped error, not silent (nil, 0, nil)
	fixtureDir := t.TempDir()
	unreadableSub := filepath.Join(fixtureDir, "unreadable")
	if err := os.Mkdir(unreadableSub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unreadableSub, "match.txt"), []byte("resolver endpoint match"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadableSub, 0o000); err != nil {
		t.Skip("cannot chmod 000 in this environment")
	}
	t.Cleanup(func() {
		_ = os.Chmod(unreadableSub, 0o755)
	})

	files, tokens, err = runRipgrepSearch(fixtureDir, "resolver endpoint", 5)
	if err == nil {
		t.Fatalf("expected error on rg exit 2 (unreadable subdir), got files=%v tokens=%d", files, tokens)
	}
	if !strings.Contains(err.Error(), "running ripgrep") {
		t.Errorf("expected error message to contain 'running ripgrep', got %v", err)
	}
}

func TestCLIBenchMovedSourceDirReportsUnavailable(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--graph")

	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)

	// Move source directory away
	movedDir := e.Docs + "-moved"
	if err := os.Rename(e.Docs, movedDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Rename(movedDir, e.Docs)
	}()

	r := e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "3")
	wantExit(t, r, 0)
	// Must indicate Ripgrep is unavailable rather than reporting measured zeros
	// Without rg on PATH the baseline is unavailable for a different (earlier)
	// reason; either way it must be reported as unavailable, not as zeros.
	mustContain(t, "ripgrep unavailable", r.Out, "Ripgrep")
	if _, err := exec.LookPath("rg"); err != nil {
		mustContain(t, "ripgrep unavailable", r.Out, "ripgrep not installed")
	} else {
		mustContain(t, "ripgrep unavailable", r.Out, "Unavailable")
	}
	if strings.Contains(r.Out, "-100.0%") {
		t.Errorf("benchmark output should not report fake -100.0%% tokens when source dir is moved: %s", r.Out)
	}
}

func TestCLIRipgrepOlderVersionUnsupportedFlagRetryAndRealExit2(t *testing.T) {
	requireRipgrep(t)
	realRg, err := exec.LookPath("rg")
	if err != nil {
		t.Fatal(err)
	}

	shimDir := t.TempDir()
	shimPath := filepath.Join(shimDir, "rg")
	// The shim script rejects --no-require-git or --no-ignore-parent with exit 2 and clap stderr,
	// exactly reproducing older ripgrep behavior.
	// If neither is passed, it delegates to real rg.
	// If a trigger for real exit 2 is passed, it exits 2 with real error text.
	shimScript := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
    if [ "$arg" = "--no-require-git" ] || [ "$arg" = "--no-ignore-parent" ]; then
        echo "error: Found argument '$arg' which wasn't expected, or isn't valid in this context" >&2
        exit 2
    fi
    if [ "$arg" = "--real-exit-2-error" ]; then
        echo "rg: permission denied reading directory" >&2
        exit 2
    fi
done
exec %s "$@"
`, realRg)
	if err := os.WriteFile(shimPath, []byte(shimScript), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", shimDir+":"+os.Getenv("PATH"))

	fixtureDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixtureDir, "file.txt"), []byte("searchterm line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, _, err := runRipgrepSearch(fixtureDir, "searchterm", 5)
	if err != nil {
		t.Fatalf("runRipgrepSearch failed with old-rg shim: %v", err)
	}
	if len(files) != 1 || files[0] != "file.txt" {
		t.Errorf("expected [file.txt], got %v", files)
	}

	// Now verify that real exit 2 error (without unknown-flag stderr) still errors!
	unreadableSub := filepath.Join(fixtureDir, "unreadable")
	if err := os.Mkdir(unreadableSub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(unreadableSub, 0o755)
	})
	_, _, err = runRipgrepSearch(fixtureDir, "searchterm", 5)
	if err == nil {
		t.Fatalf("expected real exit 2 to return error, got nil")
	}
	if !strings.Contains(err.Error(), "running ripgrep") {
		t.Errorf("expected wrapped 'running ripgrep' error, got %v", err)
	}
}

func TestCLIRipgrepIgnoresAncestorGitignoreWithNoIgnoreParent(t *testing.T) {
	requireRipgrep(t)

	// Create parent repo root with .git and .gitignore ignoring *.go
	parentDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(parentDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, ".gitignore"), []byte("*.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Subdirectory sourceDir
	sourceDir := filepath.Join(parentDir, "src")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goFixture := "package src\n// rareterm line 1\n// rareterm line 2\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "target.go"), []byte(goFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	files, _, err := runRipgrepSearch(sourceDir, "rareterm", 5)
	if err != nil {
		t.Fatalf("runRipgrepSearch failed: %v", err)
	}
	if len(files) != 1 || files[0] != "target.go" {
		t.Errorf("expected target.go to be found ignoring ancestor .gitignore, got: %v", files)
	}
}

func TestCLICBenchUnwritablePerTaskFileErrors(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--graph")

	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)
	unwritablePerTask := filepath.Join(e.Home, "no-such-dir", "sub", "pertask.jsonl")

	r := e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "2", "--per-task", unwritablePerTask)
	wantExit(t, r, 1)
	mustContain(t, "unwritable per-task error", r.Err, "creating per-task file")
}

func TestCLIBenchSourceDirIsFileReportsCleanReason(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--graph")

	tasks := cWriteTasks(t, filepath.Join(e.Home, "tasks.json"), cBenchTasks)

	// Replace docs directory with a regular file
	if err := os.RemoveAll(e.Docs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Docs, []byte("regular file not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := e.run("bench", "--index", "demo", "--tasks", tasks, "--top-k", "2")
	wantExit(t, r, 0)
	if strings.Contains(r.Out, "<nil>") || strings.Contains(r.Err, "<nil>") {
		t.Errorf("output or stderr contained <nil> for non-directory sourceDir:\nout:\n%s\nerr:\n%s", r.Out, r.Err)
	}
	if _, err := exec.LookPath("rg"); err != nil {
		// The rg lookup precedes the source-directory check.
		mustContain(t, "rg missing reason", r.Out, "ripgrep not installed")
	} else {
		mustContain(t, "not a directory reason", r.Out, "not a directory")
	}
}
