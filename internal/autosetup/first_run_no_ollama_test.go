package autosetup

// T1 (v1.22): first run without Ollama must inform the user, persist
// actionable auto-download guidance in the generated config and perform
// ZERO background downloads. These tests pin HOME, XDG_* and GLEANN_* to
// t.TempDir() and count every request a fake "no Ollama" server receives.

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/internal/testenv"
	"github.com/tevfik/gleann/pkg/gleann"
)

// isolateFirstRunEnv points every path gleann reads at a throwaway home so
// the test can neither see nor write the developer's real configuration.
func isolateFirstRunEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	t.Setenv("GLEANN_INDEX_DIR", filepath.Join(home, ".gleann", "indexes"))
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(home, ".gleann", "memory"))
	t.Setenv("OLLAMA_HOST", testenv.DeadOllamaHost)
	t.Setenv("OLLAMA_API_KEY", "")
	return home
}

// deadOllamaFake stands in for an absent Ollama: /api/tags answers 503 and
// every request is counted, so a test can prove no download was attempted.
type deadOllamaFake struct {
	*httptest.Server
	mu       sync.Mutex
	requests map[string]int
}

func newDeadOllamaFake(t *testing.T) *deadOllamaFake {
	t.Helper()
	f := &deadOllamaFake{requests: map[string]int{}}
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

func (f *deadOllamaFake) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

func (f *deadOllamaFake) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.requests {
		n += c
	}
	return n
}

// readFirstRunConfig reads the generated config.json of an isolated home.
func readFirstRunConfig(t *testing.T, home string) map[string]any {
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

// noOllamaGuidanceWants are the actionable parts every no-Ollama notice must
// carry: the explicit no-background-download statement and the ways to get
// models (Ollama install, manual llamacpp download, opt-in auto download).
var noOllamaGuidanceWants = []string{
	"No models were downloaded in the background",
	"https://ollama.com/download",
	"gleann models download",
	"auto_download",
	"foreground",
	"llamacpp",
}

func TestFirstRunBootstrap_NoOllama_NoticeGuidanceZeroDownloads(t *testing.T) {
	home := isolateFirstRunEnv(t)
	f := newDeadOllamaFake(t)

	var did bool
	var err error
	out := captureStdout(t, func() { did, err = EnsureConfig(f.URL, false) })
	if err != nil || !did {
		t.Fatalf("EnsureConfig = %v, %v", did, err)
	}

	for _, want := range append([]string{"Ollama not found at " + f.URL}, noOllamaGuidanceWants...) {
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

	cfg := readFirstRunConfig(t, home)
	if cfg["completed"] != true {
		t.Errorf("completed = %v, want true", cfg["completed"])
	}
	notice, _ := cfg["setup_notice"].(string)
	if notice == "" {
		t.Fatal("config must persist the auto-download guidance (setup_notice)")
	}
	for _, want := range noOllamaGuidanceWants {
		if !strings.Contains(notice, want) {
			t.Errorf("setup_notice lacks %q:\n%s", want, notice)
		}
	}
}

func TestFirstRunBootstrap_NoOllama_QuietStillPersistsGuidance(t *testing.T) {
	home := isolateFirstRunEnv(t)
	f := newDeadOllamaFake(t)

	out := captureStdout(t, func() {
		if _, err := EnsureConfig(f.URL, true); err != nil {
			t.Errorf("EnsureConfig: %v", err)
		}
	})
	if out != "" {
		t.Errorf("quiet mode printed:\n%s", out)
	}
	if n := f.count("/api/pull"); n != 0 {
		t.Errorf("%d pull requests, want 0", n)
	}
	cfg := readFirstRunConfig(t, home)
	if notice, _ := cfg["setup_notice"].(string); !strings.Contains(notice, "gleann models download") {
		t.Errorf("quiet bootstrap must still persist guidance, got %v", cfg["setup_notice"])
	}
}

func TestFirstRunBootstrap_OllamaFound_NoNotice(t *testing.T) {
	home := isolateFirstRunEnv(t)
	srv := tagsServer(t, http.StatusOK, `{"models":[{"name":"bge-m3:latest"},{"name":"gemma3:4b"}]}`)

	out := captureStdout(t, func() {
		if _, err := EnsureConfig(srv.URL, false); err != nil {
			t.Errorf("EnsureConfig: %v", err)
		}
	})
	if strings.Contains(out, "No models were downloaded") {
		t.Errorf("no-Ollama notice printed although Ollama was found:\n%s", out)
	}
	cfg := readFirstRunConfig(t, home)
	if _, on := cfg["setup_notice"]; on {
		t.Errorf("setup_notice persisted although Ollama was found: %v", cfg["setup_notice"])
	}
}

// The 'gleann models download' hint carries a concrete, downloadable id (the
// default embedding model), not a bare command that only prints usage.
func TestNoOllamaGuidance_ShowsConcreteDownloadID(t *testing.T) {
	id := downloadExampleID()
	if id == "" || id == "<id>" {
		t.Fatalf("no concrete model id for the hint: %q", id)
	}
	if e, ok := modelcatalog.Find(id); !ok || e.Kind != modelcatalog.Embedding {
		t.Fatalf("hint id %q is not a downloadable embedding model", id)
	}
	if id != gleann.DefaultConfig().EmbeddingModel {
		t.Errorf("hint id = %q, want the default embedding model %q", id, gleann.DefaultConfig().EmbeddingModel)
	}
	g := NoOllamaGuidance()
	if !strings.Contains(g, "gleann models download "+id+"'") || !strings.Contains(g, "gleann models list") {
		t.Errorf("guidance must name a concrete id and how to list models:\n%s", g)
	}
	for _, line := range strings.Split(g, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "gleann models download'") {
			t.Errorf("bare 'gleann models download' hint: %q", line)
		}
	}
}

// Quiet mode (serve --quiet) prints nothing on stdout but leaves exactly one
// log line saying that no model was downloaded.
func TestFirstRunBootstrap_NoOllama_QuietLogsOneNotice(t *testing.T) {
	isolateFirstRunEnv(t)
	f := newDeadOllamaFake(t)

	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	if _, err := EnsureConfig(f.URL, true); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "no models were downloaded") || !strings.Contains(lines[0], f.URL) {
		t.Errorf("want one log line about the missing Ollama, got %q", buf.String())
	}
}

func writeCfg(t *testing.T, home string, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(home, ".gleann")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A stale setup_notice is removed once Ollama answers; the rest of the config
// is untouched.
func TestEnsureConfig_ClearsStaleNoticeWhenOllamaReachable(t *testing.T) {
	home := isolateFirstRunEnv(t)
	writeCfg(t, home, map[string]any{
		"completed": true, "embedding_provider": "ollama", "embedding_model": "bge-m3",
		"top_k": 7, "setup_notice": "Ollama not detected at x.",
	})
	srv := tagsServer(t, http.StatusOK, `{"models":[{"name":"bge-m3:latest"}]}`)
	if did, err := EnsureConfig(srv.URL, true); err != nil || did {
		t.Fatalf("EnsureConfig = %v, %v", did, err)
	}
	cfg := readFirstRunConfig(t, home)
	if _, on := cfg["setup_notice"]; on {
		t.Errorf("stale setup_notice kept although Ollama is reachable: %v", cfg["setup_notice"])
	}
	if cfg["top_k"] != float64(7) || cfg["embedding_model"] != "bge-m3" {
		t.Errorf("other keys changed: %v", cfg)
	}
}

// The notice stays while Ollama is still down and no local model is present.
func TestEnsureConfig_KeepsNoticeWhileStillMissing(t *testing.T) {
	home := isolateFirstRunEnv(t)
	writeCfg(t, home, map[string]any{
		"completed": true, "embedding_provider": "ollama", "embedding_model": "bge-m3",
		"setup_notice": "still relevant",
	})
	f := newDeadOllamaFake(t)
	if _, err := EnsureConfig(f.URL, true); err != nil {
		t.Fatal(err)
	}
	if got := readFirstRunConfig(t, home)["setup_notice"]; got != "still relevant" {
		t.Errorf("setup_notice = %v, want it kept", got)
	}
}

// ... and goes once the configured local model file is installed.
func TestEnsureConfig_ClearsNoticeWhenLocalModelPresent(t *testing.T) {
	home := isolateFirstRunEnv(t)
	writeCfg(t, home, map[string]any{
		"completed": true, "embedding_provider": "llamacpp", "embedding_model": "mine.gguf",
		"setup_notice": "download a model",
	})
	f := newDeadOllamaFake(t)
	if _, err := EnsureConfig(f.URL, true); err != nil {
		t.Fatal(err)
	}
	if _, on := readFirstRunConfig(t, home)["setup_notice"]; !on {
		t.Fatal("notice must stay while the model file is missing")
	}
	models := filepath.Join(home, ".gleann", "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "mine.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureConfig(f.URL, true); err != nil {
		t.Fatal(err)
	}
	if _, on := readFirstRunConfig(t, home)["setup_notice"]; on {
		t.Error("setup_notice kept although the local model is present")
	}
}

// OLLAMA_HOST decides the host for every auto-setup probe and download: with
// it set, a nil/empty host argument reaches the fake, never localhost:11434.
func TestAutoSetupHonoursOllamaHost(t *testing.T) {
	home := isolateFirstRunEnv(t)
	var mu sync.Mutex
	seen := map[string]int{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/api/tags" {
			_, _ = w.Write([]byte(`{"models":[{"name":"other:1"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer fake.Close()
	t.Setenv("OLLAMA_HOST", fake.URL)

	det := DetectOllama("")
	if !det.OllamaFound || det.OllamaHost != fake.URL {
		t.Fatalf("DetectOllama(\"\") = found %v host %q, want the fake %s", det.OllamaFound, det.OllamaHost, fake.URL)
	}
	if !HasModel("", "other") {
		t.Errorf("HasModel(\"\") did not reach the fake")
	}
	if _, err := EnsureConfig("", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".gleann", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	if cfg["ollama_host"] != fake.URL {
		t.Errorf("config ollama_host = %v, want %s", cfg["ollama_host"], fake.URL)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["/api/tags"] < 3 {
		t.Errorf("fake saw %d /api/tags probes, want at least 3: %v", seen["/api/tags"], seen)
	}
	if seen["/api/pull"] != 0 {
		t.Errorf("EnsureConfig pulled: %v", seen)
	}
}

// MissingModels reads, never pulls; the notice names every missing model.
func TestMissingModelsNeverPulls(t *testing.T) {
	isolateFirstRunEnv(t)
	var pulls int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"have:1"}]}`))
		case "/api/pull":
			pulls++
		}
	}))
	defer fake.Close()
	miss := MissingModels(fake.URL, "have", "gone-a", "gone-b")
	if strings.Join(miss, ",") != "gone-a,gone-b" {
		t.Fatalf("missing = %v", miss)
	}
	note := MissingModelsNotice(fake.URL, miss)
	for _, w := range []string{"gone-a", "ollama pull gone-b", "Nothing was downloaded"} {
		if !strings.Contains(note, w) {
			t.Errorf("notice lacks %q:\n%s", w, note)
		}
	}
	if pulls != 0 || MissingModelsNotice(fake.URL, nil) != "" {
		t.Errorf("pulls=%d or non-empty notice for no missing models", pulls)
	}
}
