package main

import (
	"encoding/json"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// This file is the in-process harness the CLI tests share: a throwaway home
// and index directory, a loopback fake of the Ollama API (embeddings, chat,
// generate, tags) and a runner that executes `gleann <args...>` through
// main() with the exit status, standard output and standard error captured.

// cliResult is what one in-process `gleann` invocation produced.
type cliResult struct {
	Out, Err string
	Code     int // status passed to exitFunc; 0 when the command returned
}

// combined returns stdout followed by stderr.
func (r cliResult) combined() string { return r.Out + r.Err }

// fakeOllama is an httptest server that speaks the subset of the Ollama API
// gleann uses. Embeddings are a deterministic bag of hashed words, so texts
// that share words are close; chat and generate answer with a fixed reply.
type fakeOllama struct {
	*httptest.Server
	dim int

	mu       sync.Mutex
	reply    string            // /api/chat and /api/generate answer
	models   []string          // /api/tags
	requests map[string]int    // path -> count
	bodies   map[string]string // path -> last request body
	failChat bool              // /api/chat returns 500
	failEmb  bool              // /api/embed returns 500
}

func newFakeOllama(t *testing.T) *fakeOllama {
	t.Helper()
	f := &fakeOllama{
		dim:      32,
		reply:    "The answer is forty-two.",
		models:   []string{"bge-m3:latest", "nomic-embed-text:latest", "llama3.2:latest", "gemma3:4b"},
		requests: map[string]int{},
		bodies:   map[string]string{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// fakeEmbedding hashes the words of text into a unit vector of size dim.
func fakeEmbedding(text string, dim int) []float32 {
	v := make([]float32, dim)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(strings.Trim(w, ".,;:()\"'`{}[]")))
		v[h.Sum32()%uint32(dim)] += 1
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	if n == 0 {
		v[0] = 1
		return v
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / math.Sqrt(n))
	}
	return v
}

func (f *fakeOllama) setReply(s string) {
	f.mu.Lock()
	f.reply = s
	f.mu.Unlock()
}

// setDim changes the embedding size for later requests.
func (f *fakeOllama) setDim(n int) {
	f.mu.Lock()
	f.dim = n
	f.mu.Unlock()
}

func (f *fakeOllama) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

func (f *fakeOllama) lastBody(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[path]
}

func (f *fakeOllama) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests[r.URL.Path]++
	f.bodies[r.URL.Path] = string(body)
	reply, models, failChat, failEmb, dim := f.reply, f.models, f.failChat, f.failEmb, f.dim
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/embed":
		if failEmb {
			http.Error(w, "embedding backend down", http.StatusInternalServerError)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.Unmarshal(body, &req)
		out := make([][]float32, len(req.Input))
		for i, s := range req.Input {
			out[i] = fakeEmbedding(s, dim)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	case "/api/embeddings":
		var req struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(body, &req)
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": fakeEmbedding(req.Prompt, dim)})
	case "/api/chat":
		if failChat {
			http.Error(w, "llm backend down", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": reply},
			"done":    true,
		})
	case "/api/generate":
		_ = json.NewEncoder(w).Encode(map[string]any{"response": reply, "done": true})
	case "/api/tags":
		var ms []map[string]any
		for _, m := range models {
			ms = append(ms, map[string]any{"name": m, "model": m, "size": 1 << 20})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": ms})
	case "/api/show":
		_ = json.NewEncoder(w).Encode(map[string]any{"model_info": map[string]any{"general.context_length": 8192}})
	case "/api/version":
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "0.9.0"})
	case "/v1/rerank":
		// Scores documents by how many query words they contain.
		var req struct {
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
		}
		_ = json.Unmarshal(body, &req)
		type item struct {
			Index int     `json:"index"`
			Score float64 `json:"relevance_score"`
		}
		var res []item
		for i, d := range req.Documents {
			s := 0.0
			for _, w := range strings.Fields(strings.ToLower(req.Query)) {
				if strings.Contains(strings.ToLower(d), w) {
					s++
				}
			}
			res = append(res, item{i, s})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": res})
	case "/api/pull":
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	default:
		http.NotFound(w, r)
	}
}

// cliEnv is an isolated environment for running the CLI: its own home (so
// ~/.gleann is a temp dir), index directory, docs directory and fake Ollama.
type cliEnv struct {
	t        *testing.T
	Home     string
	IndexDir string
	Docs     string
	Ollama   *fakeOllama
}

func newCLIEnv(t *testing.T) *cliEnv {
	t.Helper()
	root := t.TempDir()
	e := &cliEnv{
		t:        t,
		Home:     filepath.Join(root, "home"),
		IndexDir: filepath.Join(root, "indexes"),
		Docs:     filepath.Join(root, "docs"),
		Ollama:   newFakeOllama(t),
	}
	for _, d := range []string{e.Home, e.IndexDir, e.Docs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", e.Home)
	t.Setenv("USERPROFILE", e.Home)
	t.Setenv("GLEANN_INDEX_DIR", e.IndexDir)
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(e.Home, ".gleann"))
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(e.Home, ".gleann", "memory"))
	t.Setenv("OLLAMA_HOST", e.Ollama.URL)
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GLEANN_REMOTE_ADDR", "off")
	t.Chdir(root)
	return e
}

// write creates a file (and its directories) under the docs directory.
func (e *cliEnv) write(rel, content string) string {
	e.t.Helper()
	p := filepath.Join(e.Docs, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// sampleDocs writes a small mixed corpus: two Go files and two Markdown files.
func (e *cliEnv) sampleDocs() {
	e.write("auth/token.go", "package auth\n\n// ValidateToken checks a bearer token.\nfunc ValidateToken(tok string) bool {\n\treturn tok != \"\" && checkSignature(tok)\n}\n\nfunc checkSignature(tok string) bool { return len(tok) > 3 }\n")
	e.write("server/main.go", "package main\n\nimport \"fmt\"\n\n// Serve starts the rate limiter and server.\nfunc Serve() { fmt.Println(\"serving\") }\n\nfunc main() { Serve() }\n")
	e.write("README.md", "# Demo project\n\n## Rate limiting\n\nThe token bucket rate limiter allows bursts of requests.\n\n## Authentication\n\nTokens are validated by ValidateToken before each request.\n")
	e.write("docs/guide.md", "# Guide\n\nDeploy the service with the deploy script. Banana bread is unrelated.\n")
}

// build indexes the docs directory under name through the CLI, failing the
// test when the build does.
func (e *cliEnv) build(name string, extra ...string) cliResult {
	e.t.Helper()
	args := append([]string{"index", "build", name, "--docs", e.Docs, "--no-report", "--no-agents"}, extra...)
	r := e.run(args...)
	if r.Code != 0 {
		e.t.Fatalf("index build %s failed (exit %d):\nstdout: %s\nstderr: %s", name, r.Code, r.Out, r.Err)
	}
	return r
}

// run executes `gleann <args...>` in-process with stdin empty.
func (e *cliEnv) run(args ...string) cliResult {
	e.t.Helper()
	return runCLIWithInput(e.t, "", args...)
}

// runIn executes `gleann <args...>` with the given standard input.
func (e *cliEnv) runIn(stdin string, args ...string) cliResult {
	e.t.Helper()
	return runCLIWithInput(e.t, stdin, args...)
}

// runCLIWithInput runs main() as `gleann <args...>`; see captureRun.
func runCLIWithInput(t *testing.T, stdin string, args ...string) cliResult {
	t.Helper()
	return captureRun(t, stdin, func() {
		orig := os.Args
		defer func() { os.Args = orig }()
		os.Args = append([]string{"gleann"}, args...)
		main()
	})
}

// captureRun runs fn with stdin fed from the given text, stdout and stderr
// captured (read concurrently, so large outputs cannot block) and exitFunc
// turned into a panic that ends fn and records the status.
func captureRun(t *testing.T, stdin string, fn func()) (res cliResult) {
	t.Helper()
	origExit, origOut, origErr, origIn := exitFunc, os.Stdout, os.Stderr, os.Stdin
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	inFile := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(inFile, []byte(stdin), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(inFile)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	outC, errC := make(chan string, 1), make(chan string, 1)
	go func() { b, _ := io.ReadAll(rOut); outC <- string(b) }()
	go func() { b, _ := io.ReadAll(rErr); errC <- string(b) }()

	os.Stdout, os.Stderr, os.Stdin = wOut, wErr, in
	exitFunc = func(c int) { panic(exitCalled{c}) }
	func() {
		defer func() {
			if r := recover(); r != nil {
				ec, ok := r.(exitCalled)
				if !ok {
					exitFunc, os.Stdout, os.Stderr, os.Stdin = origExit, origOut, origErr, origIn
					panic(r)
				}
				res.Code = ec.code
			}
		}()
		fn()
	}()
	exitFunc, os.Stdout, os.Stderr, os.Stdin = origExit, origOut, origErr, origIn
	_ = wOut.Close()
	_ = wErr.Close()
	res.Out, res.Err = <-outC, <-errC
	return res
}

// mustContain fails the test unless s contains every want.
func mustContain(t *testing.T, what, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("%s: missing %q in:\n%s", what, w, s)
		}
	}
}

// mustNotContain fails the test if s contains any of the given strings.
func mustNotContain(t *testing.T, what, s string, nots ...string) {
	t.Helper()
	for _, n := range nots {
		if strings.Contains(s, n) {
			t.Errorf("%s: unexpected %q in:\n%s", what, n, s)
		}
	}
}

// wantExit fails the test unless the run ended with the given status.
func wantExit(t *testing.T, r cliResult, code int) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("exit status %d, want %d\nstdout: %s\nstderr: %s", r.Code, code, r.Out, r.Err)
	}
}
