package main

import (
	"encoding/json"
	"os"
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
		"BM25", "Hybrid", "Vector (DiskANN+PQ)", "Hybrid (1 chunk/file)", "GraphRAG", "Report saved to "+report)
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
	// working directory; an empty query finds nothing.
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("the quicksilverword is here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nothing"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _, err := runRipgrepSearch("find quicksilverword", 5)
	if err != nil || len(files) != 1 || files[0] != "a.txt" {
		t.Errorf("runRipgrepSearch = %v, %v; want [a.txt]", files, err)
	}
	if files, _, _ := runRipgrepSearch("   ", 5); files != nil {
		t.Errorf("empty query = %v", files)
	}
	if files, _, _ := runRipgrepSearch(`"()"`, 5); files != nil {
		t.Errorf("punctuation-only query = %v", files)
	}
}
