package tui

// T1 (v1.22): `setup --bootstrap` without Ollama must inform the user,
// persist actionable auto-download guidance and perform ZERO background
// downloads; explicit --model/--provider/--llm-model choices still win.
// HOME, XDG_* and GLEANN_* are pinned to t.TempDir() and every request the
// fake "no Ollama" server receives is counted.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// isolateNoOllamaEnv is isolateHome plus the XDG/GLEANN/Ollama variables, so
// nothing outside the throwaway home can leak into the test.
func isolateNoOllamaEnv(t *testing.T) string {
	t.Helper()
	home := isolateHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	t.Setenv("GLEANN_INDEX_DIR", filepath.Join(home, ".gleann", "indexes"))
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(home, ".gleann", "memory"))
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_API_KEY", "")
	return home
}

// noOllamaFake answers /api/tags with 503 (Ollama absent) and counts every
// request so a test can assert that no download was attempted.
type noOllamaFake struct {
	*httptest.Server
	mu       sync.Mutex
	requests map[string]int
}

func newNoOllamaFake(t *testing.T) *noOllamaFake {
	t.Helper()
	f := &noOllamaFake{requests: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests[r.URL.Path]++
		f.mu.Unlock()
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *noOllamaFake) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

func (f *noOllamaFake) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.requests {
		n += c
	}
	return n
}

// readRawConfig reads config.json of the isolated home as a generic map.
func readRawConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".gleann", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	return cfg
}

var noOllamaBootstrapWants = []string{
	"not reachable",
	"No models were downloaded in the background",
	"https://ollama.com/download",
	"gleann models download",
	"auto_download",
	"foreground",
	"llamacpp",
}

func TestBootstrap_NoOllama_NoticeGuidanceZeroDownloads(t *testing.T) {
	home := isolateNoOllamaEnv(t)
	f := newNoOllamaFake(t)

	var res *OnboardResult
	var err error
	out := captureStdout(t, func() { res, err = Bootstrap(BootstrapOptions{OllamaHost: f.URL}) })
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed {
		t.Error("bootstrap must still complete with defaults")
	}
	for _, want := range noOllamaBootstrapWants {
		if !strings.Contains(out, want) {
			t.Errorf("notice lacks %q:\n%s", want, out)
		}
	}

	// Zero downloads: only the single /api/tags probe may have happened.
	if n := f.count("/api/pull"); n != 0 {
		t.Errorf("%d pull requests against a dead Ollama, want 0", n)
	}
	if n := f.total(); n != 1 {
		t.Errorf("want exactly the /api/tags probe, got %d requests: %v", n, f.requests)
	}

	cfg := readRawConfig(t, home)
	notice, _ := cfg["setup_notice"].(string)
	if notice == "" {
		t.Fatal("config must persist the auto-download guidance (setup_notice)")
	}
	for _, want := range []string{"gleann models download", "auto_download", "foreground", "llamacpp"} {
		if !strings.Contains(notice, want) {
			t.Errorf("setup_notice lacks %q:\n%s", want, notice)
		}
	}
}

func TestBootstrap_NoOllama_ExplicitOptionsWin(t *testing.T) {
	home := isolateNoOllamaEnv(t)
	f := newNoOllamaFake(t)

	var res *OnboardResult
	var err error
	out := captureStdout(t, func() {
		res, err = Bootstrap(BootstrapOptions{
			OllamaHost:  f.URL,
			Provider:    "llamacpp",
			Model:       "my-embed",
			LLMProvider: "openai",
			LLMModel:    "my-chat",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.EmbeddingProvider != "llamacpp" || res.EmbeddingModel != "my-embed" ||
		res.LLMProvider != "openai" || res.LLMModel != "my-chat" {
		t.Errorf("explicit options lost without Ollama: %+v", res)
	}
	// The notice is still shown and persisted, and nothing was downloaded.
	for _, want := range noOllamaBootstrapWants {
		if !strings.Contains(out, want) {
			t.Errorf("notice lacks %q:\n%s", want, out)
		}
	}
	if n := f.total(); n != 1 {
		t.Errorf("want exactly the /api/tags probe, got %d requests: %v", n, f.requests)
	}
	cfg := readRawConfig(t, home)
	if cfg["embedding_provider"] != "llamacpp" || cfg["embedding_model"] != "my-embed" ||
		cfg["llm_provider"] != "openai" || cfg["llm_model"] != "my-chat" {
		t.Errorf("explicit options not persisted: %v", cfg)
	}
	if notice, _ := cfg["setup_notice"].(string); notice == "" {
		t.Error("setup_notice missing although Ollama was absent")
	}
}

func TestBootstrap_OllamaFound_ClearsStaleNotice(t *testing.T) {
	home := isolateNoOllamaEnv(t)
	dead := newNoOllamaFake(t)
	captureStdout(t, func() {
		if _, err := Bootstrap(BootstrapOptions{OllamaHost: dead.URL}); err != nil {
			t.Fatal(err)
		}
	})
	if notice, _ := readRawConfig(t, home)["setup_notice"].(string); notice == "" {
		t.Fatal("first bootstrap without Ollama did not persist guidance")
	}

	live := fakeOllama(t, `{"models":[{"name":"bge-m3"},{"name":"gemma3:4b"}]}`, 200)
	var res *OnboardResult
	out := captureStdout(t, func() {
		var err error
		res, err = Bootstrap(BootstrapOptions{OllamaHost: live.URL, Force: true})
		if err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Ollama is running") {
		t.Errorf("live bootstrap output:\n%s", out)
	}
	if strings.Contains(out, "No models were downloaded") {
		t.Errorf("no-Ollama notice printed although Ollama was found:\n%s", out)
	}
	if res.EmbeddingModel != "bge-m3" {
		t.Errorf("models not picked from the live list: %+v", res)
	}
	if _, on := readRawConfig(t, home)["setup_notice"]; on {
		t.Error("a bootstrap that finds Ollama must clear the stale setup_notice")
	}
}
