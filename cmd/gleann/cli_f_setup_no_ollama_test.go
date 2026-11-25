package main

// T1 (v1.22): `gleann setup --bootstrap` / `--auto` without Ollama inform the
// user, persist auto-download guidance (bootstrap) and never download in the
// background. The cliEnv harness isolates HOME/GLEANN_*/OLLAMA_* per test.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// deadOllamaServer answers /api/tags with 503 and counts every request.
func deadOllamaServer(t *testing.T) (*httptest.Server, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	requests := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return requests[path]
	}
}

func TestCLIFSetupBootstrapNoOllamaNoticeAndGuidance(t *testing.T) {
	e := newCLIEnv(t)
	dead, count := deadOllamaServer(t)

	r := e.run("setup", "--bootstrap", "--host", dead.URL)
	wantExit(t, r, 0)
	mustContain(t, "no-ollama bootstrap", r.Out,
		"not reachable",
		"No models were downloaded in the background",
		"https://ollama.com/download",
		"gleann models download",
		"auto_download",
		"foreground",
		"llamacpp")
	if n := count("/api/pull"); n != 0 {
		t.Errorf("%d pull requests against a dead Ollama, want 0", n)
	}
	if n := count("/api/tags"); n != 1 {
		t.Errorf("want exactly one /api/tags probe, got %d", n)
	}

	cfg := d2ReadConfig(t, e)
	notice, _ := cfg["setup_notice"].(string)
	if !strings.Contains(notice, "gleann models download") || !strings.Contains(notice, "auto_download") || !strings.Contains(notice, "foreground") {
		t.Errorf("setup_notice must carry the auto-download guidance, got %q", notice)
	}
	if cfg["completed"] != true {
		t.Errorf("completed = %v, want true", cfg["completed"])
	}
}

func TestCLIFSetupBootstrapNoOllamaExplicitFlagsWin(t *testing.T) {
	e := newCLIEnv(t)
	dead, count := deadOllamaServer(t)

	r := e.run("setup", "--bootstrap", "--host", dead.URL,
		"--provider", "llamacpp", "--model", "my-embed",
		"--llm-provider", "openai", "--llm-model", "my-chat")
	wantExit(t, r, 0)
	mustContain(t, "explicit flags notice", r.Out, "No models were downloaded in the background")
	if n := count("/api/pull"); n != 0 {
		t.Errorf("%d pull requests, want 0", n)
	}
	cfg := d2ReadConfig(t, e)
	for k, want := range map[string]any{
		"embedding_provider": "llamacpp",
		"embedding_model":    "my-embed",
		"llm_provider":       "openai",
		"llm_model":          "my-chat",
	} {
		if cfg[k] != want {
			t.Errorf("explicit flag lost: config[%s] = %v, want %v", k, cfg[k], want)
		}
	}
	if notice, _ := cfg["setup_notice"].(string); notice == "" {
		t.Error("setup_notice missing although Ollama was absent")
	}
}

func TestCLIFSetupAutoNoOllamaGuidanceNoConfig(t *testing.T) {
	e := newCLIEnv(t)
	dead, count := deadOllamaServer(t)

	r := e.run("setup", "--auto", "--yes", "--host", dead.URL)
	wantExit(t, r, 1)
	mustContain(t, "no-ollama auto", r.Out,
		"Ollama is not running",
		"No models were downloaded in the background",
		"gleann models download",
		"auto_download",
		"foreground")
	if n := count("/api/pull"); n != 0 {
		t.Errorf("%d pull requests against a dead Ollama, want 0", n)
	}
	if _, err := os.Stat(d2ConfigPath(e)); !os.IsNotExist(err) {
		t.Error("setup --auto saved a config although Ollama is down")
	}
}

// `gleann serve` with a reachable Ollama that lacks the models and fails the
// pull reports the failure as a warning instead of dropping it silently.
func TestCLIFServeReportsFailedEnsureModels(t *testing.T) {
	e := newCLIEnv(t)
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest"}]}`))
		case "/api/pull":
			pulls.Add(1)
			http.Error(w, "pull refused", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	addr := fmt.Sprintf("127.0.0.1:%d", d2FreePort(t))

	r, ok := d2Serve(t, e, func(string) {}, "--addr", addr, "--host", srv.URL)
	if !ok {
		t.Fatalf("server never became healthy:\n%s\n%s", r.Out, r.Err)
	}
	mustContain(t, "serve warning", r.Err, "warning: could not ensure Ollama models")
	if pulls.Load() == 0 {
		t.Error("the fake Ollama never saw a pull attempt; the test does not exercise EnsureModels")
	}
}

// Non-quiet `gleann serve` prints the no-Ollama guidance once on first run
// (EnsureConfig), not twice, and once again on a later start.
func TestCLIFServeFirstRunPrintsGuidanceOnce(t *testing.T) {
	e := newCLIEnv(t)
	dead, _ := deadOllamaServer(t)
	const marker = "No models were downloaded in the background"

	run := func() cliResult {
		addr := fmt.Sprintf("127.0.0.1:%d", d2FreePort(t))
		r, ok := d2Serve(t, e, func(string) {}, "--addr", addr, "--host", dead.URL)
		if !ok {
			t.Fatalf("server never became healthy:\n%s\n%s", r.Out, r.Err)
		}
		return r
	}
	if n := strings.Count(run().Out, marker); n != 1 {
		t.Errorf("first run printed the guidance %d times, want 1", n)
	}
	if n := strings.Count(run().Out, marker); n != 1 {
		t.Errorf("later start printed the guidance %d times, want 1", n)
	}
}
