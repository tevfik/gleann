package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// h2aClosedURL returns the URL of a server that is no longer listening.
func h2aClosedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

// h2aTruncatedBody answers with an error status and a body shorter than its
// Content-Length, so reading the body fails.
func h2aTruncatedBody(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("par"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFetchModelsH2AConnectionErrors(t *testing.T) {
	url := h2aClosedURL(t)
	if _, err := fetchModels("ollama", url, ""); err == nil || !strings.Contains(err.Error(), "connect to Ollama") {
		t.Errorf("ollama: %v", err)
	}
	if _, err := fetchModels("openai", url, "sk"); err == nil || !strings.Contains(err.Error(), "connect to OpenAI") {
		t.Errorf("openai: %v", err)
	}
	if _, err := fetchOpenAIModels("http://bad host\x7f", "sk"); err == nil {
		t.Error("an invalid OpenAI URL was accepted")
	}
}

func TestFetchModelsH2AUnreadableErrorBody(t *testing.T) {
	url := h2aTruncatedBody(t)
	_, err := fetchModels("ollama", url, "")
	if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "(body unreadable)") {
		t.Errorf("ollama: %v", err)
	}
	_, err = fetchModels("openai", url, "sk")
	if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "(body unreadable)") {
		t.Errorf("openai: %v", err)
	}
}

func TestFetchModelsH2ALocalScan(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(dir, "b.gguf"), filepath.Join(sub, "a.EIFM"), filepath.Join(dir, "notes.txt")} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, prov := range []string{"llamacpp", "eif"} {
		got, err := fetchModels(prov, dir, "")
		if err != nil {
			t.Fatalf("%s: %v", prov, err)
		}
		if len(got) != 2 || got[0].Name != "a.EIFM" || got[1].Name != "b.gguf" || got[0].Tag != filepath.Join(sub, "a.EIFM") {
			t.Errorf("%s: %+v", prov, got)
		}
	}

	// A missing directory is skipped and reported as "no models".
	missing := filepath.Join(dir, "missing")
	for _, f := range []func(string) ([]ModelInfo, error){fetchLlamaCPPModels, fetchEIFModels} {
		if _, err := f(missing); err == nil || !strings.Contains(err.Error(), "no .gguf or .eifm models") {
			t.Errorf("missing dir: %v", err)
		}
	}
}

// Without a host the default model directories under HOME are scanned.
func TestFetchModelsH2AAutoScanDefaultDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", "")
	lm := filepath.Join(home, ".cache", "lm-studio", "models")
	if err := os.MkdirAll(lm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lm, "studio.gguf"), make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"", "auto-scan", "http://localhost:8080"} {
		got, err := fetchLlamaCPPModels(host)
		if err != nil || len(got) != 1 || got[0].Name != "studio.gguf" || got[0].Size != "2MB" {
			t.Errorf("llamacpp %q: %+v %v", host, got, err)
		}
		got, err = fetchEIFModels(host)
		if err != nil || len(got) != 1 {
			t.Errorf("eif %q: %+v %v", host, got, err)
		}
	}
}

// Without a home directory the default scan reports the error.
func TestFetchModelsH2ANoHome(t *testing.T) {
	if runtime.GOOS == "plan9" {
		t.Skip("home is not taken from the environment")
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	if _, err := fetchLlamaCPPModels(""); err == nil {
		t.Error("llamacpp: no error without a home directory")
	}
	if _, err := fetchEIFModels(""); err == nil {
		t.Error("eif: no error without a home directory")
	}
}

// An unreadable subdirectory is skipped; the rest is still listed.
func TestFetchModelsH2AUnreadableSubdirSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not restrict reading on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	for _, f := range []func(string) ([]ModelInfo, error){fetchLlamaCPPModels, fetchEIFModels} {
		got, err := f(dir)
		if err != nil || len(got) != 1 || got[0].Name != "ok.gguf" {
			t.Errorf("%+v %v", got, err)
		}
	}
}

func TestFilterEmbeddingModelsH2AEIFMAlwaysKept(t *testing.T) {
	got := filterEmbeddingModels(models("my-model.eifm", "llama3", "bge-reranker-v2-m3"))
	if len(got) != 1 || got[0].Name != "my-model.eifm" {
		t.Errorf("%+v", got)
	}
}

// ── config.go ──────────────────────────────────────────────────

func TestNewConfigErrorH2APositions(t *testing.T) {
	data := []byte("{\n  \"a\": 1,\n  x\n}")
	cases := []struct {
		name         string
		err          error
		line, column int
	}{
		{"no offset", errors.New("boom"), 0, 0},
		{"offset zero", &json.SyntaxError{Offset: 0}, 1, 1},
		{"past the end", &json.SyntaxError{Offset: int64(len(data)) + 10}, 4, 2},
		{"third line", &json.UnmarshalTypeError{Offset: 15}, 3, 3},
	}
	for _, c := range cases {
		ce := newConfigError("/cfg.json", data, c.err)
		if ce.Line != c.line || ce.Column != c.column {
			t.Errorf("%s: line %d col %d, want %d %d", c.name, ce.Line, ce.Column, c.line, c.column)
		}
		if !errors.Is(ce, c.err) {
			t.Errorf("%s: Unwrap lost the cause", c.name)
		}
	}
	msg := newConfigError("/cfg.json", data, errors.New("boom")).Error()
	if !strings.Contains(msg, "/cfg.json is not valid JSON: boom") || strings.Contains(msg, "line") {
		t.Errorf("message without position: %q", msg)
	}
}

// saveExact reports a config directory that cannot be created.
func TestSaveExactH2AParentIsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveExact(filepath.Join(file, "sub", "config.json"), OnboardResult{}); err == nil {
		t.Error("saveExact under a file succeeded")
	}
}

// ── onboard_catalog.go ─────────────────────────────────────────

// Reranker files are downloaded with a local embedder, and also with a
// server embedder (the built-in reranker runs GGUF files); one entry is
// downloaded once. A failed download is reported with the manual command.
func TestDownloadChosenModelsH2AReportsFailures(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no network: every download fails at once

	cases := []struct {
		name string
		r    OnboardResult
		ids  []string
	}{
		{"local embedder with reranker", OnboardResult{
			EmbeddingProvider: "llamacpp", EmbeddingModel: "bge-m3-Q4_K_M.gguf", CodeEmbeddingModel: "bge-m3-Q4_K_M.gguf",
			RerankEnabled: true, RerankModel: "bge-reranker-v2-m3-Q8_0.gguf",
		}, []string{"bge-m3", "bge-reranker-v2-m3"}},
		{"server embedder with reranker", OnboardResult{
			EmbeddingProvider: "ollama", EmbeddingModel: "bge-m3",
			LLMProvider: "eif", LLMModel: "/elsewhere/qwen2.5-0.5b-instruct-q4_k_m.gguf",
			RerankEnabled: true, RerankModel: "ms-marco-MiniLM-L6-v2-pooler-Q8_0.gguf",
		}, []string{"qwen2.5-0.5b-instruct", "ms-marco-minilm-l6"}},
	}
	for _, c := range cases {
		var out bytes.Buffer
		if !downloadChosenModels(ctx, c.r, &out) {
			t.Errorf("%s: nothing attempted", c.name)
		}
		s := out.String()
		for _, id := range c.ids {
			if strings.Count(s, "gleann models download "+id+"\n") != 1 {
				t.Errorf("%s: %s not reported once:\n%s", c.name, id, s)
			}
		}
		if strings.Count(s, "⬇  Downloading") != len(c.ids) {
			t.Errorf("%s: %d downloads, want %d:\n%s", c.name, strings.Count(s, "⬇  Downloading"), len(c.ids), s)
		}
	}
}
