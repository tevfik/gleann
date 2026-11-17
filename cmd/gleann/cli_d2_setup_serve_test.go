package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestCLID2SetupAutoBuildsIndex(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	r := e.run("setup", "--auto", "--yes", "--host", e.Ollama.URL, "--docs", e.Docs, "--name", "proj",
		"--code-model", "jina-code", "--doc-model", "bge-small", "--graph")
	wantExit(t, r, 0)
	mustContain(t, "setup --auto", r.Out,
		"auto-detected configuration", "✓ Configuration saved", "✓ All models available",
		`Building index "proj"`, "passages indexed", "AGENTS.md automatically generated", "What's next?", "gleann chat proj")

	cfg := d2ReadConfig(t, e)
	for k, want := range map[string]any{
		"ollama_host": e.Ollama.URL, "embedding_model": "bge-m3:latest", "llm_model": "gemma3:4b",
		"code_embedding_model": "jina-code", "doc_embedding_model": "bge-small", "completed": true,
	} {
		if cfg[k] != want {
			t.Errorf("saved config %s = %v, want %v", k, cfg[k], want)
		}
	}
	metas, err := gleann.ListIndexes(e.IndexDir)
	if err != nil || len(metas) != 1 || metas[0].Name != "proj" || metas[0].NumPassages == 0 {
		t.Fatalf("indexes after setup --auto = %+v (%v)", metas, err)
	}
	if b, err := os.ReadFile(filepath.Join(e.Docs, "AGENTS.md")); err != nil || !strings.Contains(string(b), "proj") {
		t.Errorf("AGENTS.md not written for the index: %v", err)
	}
	if !gleann.HasCodeGraph(e.IndexDir, "proj") {
		t.Error("setup --auto --graph built no code graph")
	}

	// A second run finds the index and skips the build.
	r = e.run("setup", "--auto", "-y", "--host", e.Ollama.URL, "--docs", e.Docs, "--name", "proj")
	wantExit(t, r, 0)
	mustContain(t, "setup --auto again", r.Out, `Index "proj" already exists`, "Skipping build")
}

func TestCLID2SetupAutoDefaultsNameFromDirAndPullsModels(t *testing.T) {
	e := newCLIEnv(t)
	docs := filepath.Join(e.Docs, "My Project.v2")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "notes.md"), []byte("# Notes\n\nSome notes about deployment.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only an LLM is installed: the small quick-start embedding model is pulled.
	e.Ollama.mu.Lock()
	e.Ollama.models = []string{"llama3.2:latest"}
	e.Ollama.mu.Unlock()
	t.Chdir(docs)

	r := e.run("setup", "--auto", "--yes", "--host", e.Ollama.URL)
	wantExit(t, r, 0)
	mustContain(t, "setup pull", r.Out, "Pulling nomic-embed-text", "✓ Pulled 1 model(s)", `Building index "my-project-v2"`)
	if e.Ollama.count("/api/pull") != 1 || !strings.Contains(e.Ollama.lastBody("/api/pull"), "nomic-embed-text") {
		t.Errorf("pull requests: %d, last body %q", e.Ollama.count("/api/pull"), e.Ollama.lastBody("/api/pull"))
	}
}

func TestCLID2SetupAutoFailures(t *testing.T) {
	e := newCLIEnv(t)

	// Ollama is not reachable.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	r := e.run("setup", "--auto", "--yes", "--host", deadURL)
	wantExit(t, r, 1)
	mustContain(t, "ollama down", r.Out, "Ollama is not running")
	if _, err := os.Stat(d2ConfigPath(e)); !os.IsNotExist(err) {
		t.Error("setup --auto saved a config although Ollama is down")
	}

	// Interactive confirmation: "e" points at the wizard, anything else cancels.
	r = e.runIn("e\n", "setup", "--auto", "--host", e.Ollama.URL)
	wantExit(t, r, 0)
	mustContain(t, "answer e", r.Out, "full advanced wizard")
	r = e.runIn("no\n", "setup", "--quick", "--host", e.Ollama.URL)
	wantExit(t, r, 0)
	mustContain(t, "answer no", r.Out, "Cancelled.")
	if _, err := os.Stat(d2ConfigPath(e)); !os.IsNotExist(err) {
		t.Error("a cancelled setup saved a config")
	}

	// Pressing enter accepts; an empty directory has nothing to index.
	empty := t.TempDir()
	r = e.runIn("\n", "setup", "--auto", "--host", e.Ollama.URL, "--docs", empty)
	wantExit(t, r, 1)
	mustContain(t, "empty docs", r.Err, "no documents found in "+empty)
	if _, err := os.Stat(d2ConfigPath(e)); err != nil {
		t.Errorf("accepted setup did not save the config: %v", err)
	}

	// A model that cannot be pulled stops the setup.
	pullFails := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_, _ = io.WriteString(w, `{"models":[]}`)
			return
		}
		http.Error(w, "disk full", http.StatusInternalServerError)
	}))
	defer pullFails.Close()
	r = e.run("setup", "--auto", "--yes", "--host", pullFails.URL)
	wantExit(t, r, 1)
	mustContain(t, "pull fails", r.Err, "failed to pull", "disk full")
	mustContain(t, "pull fails hint", r.Out, "ollama pull <model-name>")
}

func TestCLID2SetupCheckAndBootstrap(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("setup", "--check")
	wantExit(t, r, 1)
	mustContain(t, "check unconfigured", r.Out, "gleann is not configured")

	r = e.run("setup", "--bootstrap", "--host", e.Ollama.URL, "--code-model", "jina-code", "--docs-model", "bge-small")
	wantExit(t, r, 0)
	mustContain(t, "bootstrap", r.Out, "Ollama is running", "Bootstrap Configuration")
	cfg := d2ReadConfig(t, e)
	if cfg["ollama_host"] != e.Ollama.URL || cfg["completed"] != true ||
		cfg["code_embedding_model"] != "jina-code" || cfg["doc_embedding_model"] != "bge-small" {
		t.Errorf("bootstrap saved %v", cfg)
	}

	r = e.run("setup", "--check")
	wantExit(t, r, 0)
	mustContain(t, "check configured", r.Out, "gleann is configured")

	wantExit(t, e.run("setup", "--bogus"), 2)
}

// d2FreePort returns a loopback port that was free a moment ago.
func d2FreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// d2Serve runs `gleann serve args...`; once /health answers it calls probe
// with the base URL and stops the server the way SIGINT does.
func d2Serve(t *testing.T, e *cliEnv, probe func(base string), args ...string) (cliResult, bool) {
	t.Helper()
	return d2ServeAt(t, e, "", probe, args...)
}

// d2ServeAt is d2Serve for a server listening on base (default: the --addr
// argument).
func d2ServeAt(t *testing.T, e *cliEnv, base string, probe func(base string), args ...string) (cliResult, bool) {
	t.Helper()
	stopC := make(chan chan<- os.Signal, 1)
	orig := serveNotify
	serveNotify = func(stop chan<- os.Signal) { stopC <- stop }
	defer func() { serveNotify = orig }()

	for i, a := range args {
		if base == "" && a == "--addr" && i+1 < len(args) {
			base = "http://" + args[i+1]
		}
	}
	served := make(chan bool, 1)
	go func() {
		var stop chan<- os.Signal
		select {
		case stop = <-stopC:
		case <-time.After(60 * time.Second):
			served <- false
			return
		}
		ok := false
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := http.Get(base + "/health")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					ok = true
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if ok {
			probe(base)
		}
		served <- ok
		stop <- os.Interrupt
	}()
	r := e.run(append([]string{"serve"}, args...)...)
	select {
	case ok := <-served:
		return r, ok
	default:
		return r, false
	}
}

func TestCLID2ServeStartsAndStops(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")
	addr := fmt.Sprintf("127.0.0.1:%d", d2FreePort(t))

	var indexes, settingsPut, settingsGet string
	r, ok := d2Serve(t, e, func(base string) {
		token := ""
		if b, err := os.ReadFile(authTokenPath()); err == nil {
			token = strings.TrimSpace(string(b))
		}
		do := func(method, path, body string) string {
			req, _ := http.NewRequest(method, base+path, bytes.NewBufferString(body))
			req.Header.Set("Authorization", "Bearer "+token)
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err.Error()
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return fmt.Sprintf("%d %s", resp.StatusCode, b)
		}
		indexes = do(http.MethodGet, "/api/indexes", "")
		settingsPut = do(http.MethodPut, "/api/settings", `{"values":{"top_k":7}}`)
		settingsGet = do(http.MethodGet, "/api/settings", "")
	}, "--addr", addr, "--host", e.Ollama.URL)
	wantExit(t, r, 0)
	if !ok {
		t.Fatalf("server never became healthy:\n%s\n%s", r.Out, r.Err)
	}
	mustContain(t, "serve banner", r.Out, "gleann server starting on "+addr, "bearer token required",
		"Web UI:    http://"+addr+"/?token=", "Index dir: "+e.IndexDir, "Host:      "+e.Ollama.URL,
		"Endpoints:", "Shutting down...")
	if !strings.HasPrefix(indexes, "200 ") || !strings.Contains(indexes, "demo") {
		t.Errorf("GET /api/indexes = %s", indexes)
	}
	if !strings.HasPrefix(settingsPut, "200 ") || !strings.Contains(settingsPut, "top_k") {
		t.Errorf("PUT /api/settings = %s", settingsPut)
	}
	if !strings.HasPrefix(settingsGet, "200 ") {
		t.Errorf("GET /api/settings = %s", settingsGet)
	}
	if v, ok := d2ReadConfig(t, e)["top_k"].(float64); !ok || v != 7 {
		t.Errorf("settings save did not persist top_k=7: %v", d2ReadConfig(t, e)["top_k"])
	}
	// The startup auto-setup wrote a config pointing at the fake Ollama.
	if d2ReadConfig(t, e)["ollama_host"] != e.Ollama.URL {
		t.Errorf("serve auto-setup config: %v", d2ReadConfig(t, e))
	}
}

func TestCLID2ServeAuthOffAndPortFlag(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("GLEANN_AUTH", "off")
	port := d2FreePort(t)
	var health string
	stopC := make(chan chan<- os.Signal, 1)
	orig := serveNotify
	serveNotify = func(stop chan<- os.Signal) { stopC <- stop }
	defer func() { serveNotify = orig }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		stop := <-stopC
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/indexes", port))
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				health = fmt.Sprintf("%d %s", resp.StatusCode, b)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		stop <- os.Interrupt
	}()
	r := e.run("serve", "--port", fmt.Sprint(port), "--quiet", "--host", e.Ollama.URL)
	<-done
	wantExit(t, r, 0)
	mustContain(t, "serve auth off", r.Out, fmt.Sprintf("starting on 127.0.0.1:%d", port), "Auth:      DISABLED (loopback only)")
	if !strings.HasPrefix(health, "200") {
		t.Errorf("unauthenticated GET /api/indexes with auth off = %q", health)
	}
	if i := strings.IndexByte(health, ' '); i < 0 || !json.Valid([]byte(health[i+1:])) {
		t.Errorf("GET /api/indexes body is not JSON: %q", health)
	}
}

func TestCLID2ServeErrors(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("serve", "--help")
	wantExit(t, r, 0)
	mustContain(t, "serve --help", r.Out, "Usage: gleann serve", "--addr <host:port>")
	wantExit(t, e.run("serve", "--bogus"), 2)

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--addr", "127.0.0.1:http0"}, `invalid port number "http0"`},
		{[]string{"--addr", "[::1"}, "invalid address"},
		{[]string{"--port", "70000"}, `invalid port number "70000"`},
		{[]string{"-p", ":0"}, `invalid port number "0"`},
	} {
		r := e.run(append([]string{"serve", "--quiet", "--host", e.Ollama.URL}, c.args...)...)
		wantExit(t, r, 1)
		mustContain(t, strings.Join(c.args, " "), r.Err, c.want)
	}

	// A port that is already taken is a server error.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	orig := serveNotify
	serveNotify = func(chan<- os.Signal) {}
	defer func() { serveNotify = orig }()
	r = e.run("serve", "--quiet", "--host", e.Ollama.URL, "--addr", l.Addr().String())
	wantExit(t, r, 1)
	mustContain(t, "port in use", r.Err, "server error:")

	// auth=off on a non-loopback address is refused before listening.
	t.Setenv("GLEANN_AUTH", "off")
	r = e.run("serve", "--quiet", "--host", e.Ollama.URL, "--addr", "0.0.0.0:1")
	wantExit(t, r, 1)
	mustContain(t, "auth off exposed", r.Err, "refusing to expose an unauthenticated API")
}

func TestCLID2ServeAddrHelpers(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8081": true, "localhost:1": true, "[::1]:80": true,
		"0.0.0.0:8081": false, ":8081": false, "10.1.2.3:80": false, "nonsense": false,
	} {
		if got := bindIsLoopbackAddr(addr); got != want {
			t.Errorf("bindIsLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
	for addr, want := range map[string]string{
		":8081": "localhost:8081", "0.0.0.0:9": "localhost:9", "[::]:7": "localhost:7",
		"127.0.0.1:5": "127.0.0.1:5", "bad": "bad",
	} {
		if got := uiHost(addr); got != want {
			t.Errorf("uiHost(%q) = %q, want %q", addr, got, want)
		}
	}
}

// The admin token line printed by serve is the token in authTokenPath.
var d2TokenRE = regexp.MustCompile(`\?token=(\S+)`)

func TestCLID2ServeTokenMatchesFile(t *testing.T) {
	e := newCLIEnv(t)
	addr := fmt.Sprintf("127.0.0.1:%d", d2FreePort(t))
	r, ok := d2Serve(t, e, func(string) {}, "--addr", addr, "--quiet", "--host", e.Ollama.URL)
	wantExit(t, r, 0)
	if !ok {
		t.Fatalf("server never became healthy:\n%s\n%s", r.Out, r.Err)
	}
	m := d2TokenRE.FindStringSubmatch(r.Out)
	b, err := os.ReadFile(authTokenPath())
	if m == nil || err != nil || strings.TrimSpace(string(b)) != m[1] {
		t.Errorf("printed token %v does not match %s (%v)", m, authTokenPath(), err)
	}
}

func TestCLID2ServeSavedAddrAndLlamaFallback(t *testing.T) {
	e := newCLIEnv(t)
	port := d2FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	// The saved config asks for the embedded llama.cpp server, which cannot
	// start here: serve falls back to Ollama in memory only.
	cfg := map[string]any{
		"embedding_provider": "llamacpp", "embedding_model": "bge-m3", "llm_provider": "llamacpp",
		"llm_model": "chat.gguf", "ollama_host": e.Ollama.URL, "server_addr": addr, "completed": true,
	}
	b, _ := json.Marshal(cfg)
	d2WriteConfig(t, e, string(b))

	var put string
	r, ok := d2ServeAt(t, e, "http://"+addr, func(base string) {
		tok, _ := os.ReadFile(authTokenPath())
		req, _ := http.NewRequest(http.MethodPut, base+"/api/settings", strings.NewReader(`{"values":{"top_k":3}}`))
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
		req.Header.Set("Content-Type", "application/json")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			put = fmt.Sprintf("%d %s", resp.StatusCode, body)
		}
	}, "--quiet")
	wantExit(t, r, 0)
	if !ok {
		t.Fatalf("server on the saved address never became healthy:\n%s\n%s", r.Out, r.Err)
	}
	mustContain(t, "llamacpp fallback", r.Err, "failed to start embedded llamacpp", "Falling back to Ollama")
	mustContain(t, "serve banner", r.Out, "starting on "+addr, "Model:     bge-m3 (ollama)")
	if !strings.HasPrefix(put, "200 ") {
		t.Fatalf("PUT /api/settings = %q", put)
	}
	// The save wrote top_k but not the in-memory fallback provider/model.
	saved := d2ReadConfig(t, e)
	if saved["top_k"] != float64(3) || saved["llm_provider"] != "llamacpp" || saved["llm_model"] != "chat.gguf" ||
		saved["embedding_provider"] != "llamacpp" {
		t.Errorf("settings save persisted runtime overrides or lost top_k: %v", saved)
	}
}

func TestCLID2KeepRuntimeOverrides(t *testing.T) {
	asLoaded := gleann.Config{LLMProvider: "llamacpp", LLMModel: "chat.gguf", OllamaHost: "http://disk:1"}
	running := gleann.Config{LLMProvider: "ollama", LLMModel: "nemotron-3-nano:4b"}
	// On disk: llm_provider was changed by the save, llm_model and ollama_host were not.
	fresh := gleann.Config{LLMProvider: "openai", LLMModel: "chat.gguf", OllamaHost: "http://disk:1", IndexDir: "/idx"}

	got := keepRuntimeOverrides(fresh, running, asLoaded, []string{"llm_model", "llm_provider", "ollama_host"})
	if got.LLMProvider != "openai" {
		t.Errorf("a key the save changed must take the disk value: llm_provider = %q", got.LLMProvider)
	}
	if got.LLMModel != "nemotron-3-nano:4b" {
		t.Errorf("an unchanged override must keep its running value: llm_model = %q", got.LLMModel)
	}
	if got.OllamaHost != "" {
		t.Errorf("an override that is unset while running must stay unset: ollama_host = %q", got.OllamaHost)
	}
	if got.IndexDir != "/idx" || got.EmbedderResolver == nil {
		t.Errorf("non-override keys or the embedder resolver lost: %+v", got)
	}

	plain := keepRuntimeOverrides(fresh, running, asLoaded, nil)
	if plain.LLMProvider != "openai" || plain.LLMModel != "chat.gguf" || plain.EmbedderResolver == nil {
		t.Errorf("without overrides the fresh config is used as is: %+v", plain)
	}
}
