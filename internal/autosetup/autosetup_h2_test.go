package autosetup

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func unsetHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "") // plan9
}

// tagsServer serves /api/tags with the given status and body.
func tagsServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDetectOllama_BadResponses(t *testing.T) {
	// Non-200: not found, defaults kept.
	res := DetectOllama(tagsServer(t, http.StatusInternalServerError, "").URL + "/")
	if res.OllamaFound || res.EmbeddingModel != "bge-m3" || res.LLMModel != "gemma3:4b" {
		t.Fatalf("500 response: %+v", res)
	}
	if strings.HasSuffix(res.OllamaHost, "/") {
		t.Errorf("trailing slash not trimmed: %q", res.OllamaHost)
	}
	// 200 with a body that is not JSON: Ollama is up, models unknown.
	res = DetectOllama(tagsServer(t, http.StatusOK, "not json").URL)
	if !res.OllamaFound || len(res.ModelsFound) != 0 || res.EmbeddingModel != "bge-m3" {
		t.Fatalf("garbage body: %+v", res)
	}
}

func TestHasModel_BadJSON(t *testing.T) {
	if HasModel(tagsServer(t, http.StatusOK, "{").URL, "bge-m3") {
		t.Fatal("an undecodable tag list must not report the model")
	}
}

func TestEnsureConfig_NoHome(t *testing.T) {
	unsetHome(t)
	if _, err := EnsureConfig("http://127.0.0.1:1", true); err == nil {
		t.Fatal("EnsureConfig without a home directory must fail")
	}
}

func TestEnsureConfig_UnreadableConfig(t *testing.T) {
	home := setHome(t)
	// A directory in place of config.json: ReadFile fails with something
	// other than "not exist", which must be reported, not overwritten.
	if err := os.MkdirAll(filepath.Join(home, ".gleann", "config.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureConfig("http://127.0.0.1:1", true); err == nil {
		t.Fatal("an unreadable config must be an error")
	}
}

func TestEnsureConfig_VerboseFoundWithReranker(t *testing.T) {
	home := setHome(t)
	srv := tagsServer(t, http.StatusOK, `{"models":[{"name":"bge-m3:latest"},{"name":"qwen3:8b"},{"name":"bge-reranker-v2-m3"}]}`)
	var did bool
	var err error
	out := captureStdout(t, func() { did, err = EnsureConfig(srv.URL, false) })
	if err != nil || !did {
		t.Fatalf("EnsureConfig = %v, %v", did, err)
	}
	for _, want := range []string{"auto-detecting", "Ollama found", "(3 models)", "Embedding: bge-m3:latest", "LLM: qwen3:8b", "Config saved"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, ".gleann", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, on := cfg["rerank_enabled"]; on || cfg["rerank_model"] != nil {
		t.Fatalf("an Ollama reranker has no rerank API and must not be enabled: %v", cfg)
	}
}

func TestEnsureConfig_VerboseNotFound(t *testing.T) {
	setHome(t)
	srv := tagsServer(t, http.StatusServiceUnavailable, "")
	out := captureStdout(t, func() {
		if _, err := EnsureConfig(srv.URL, false); err != nil {
			t.Errorf("EnsureConfig: %v", err)
		}
	})
	if !strings.Contains(out, "Ollama not found at "+srv.URL) {
		t.Fatalf("output:\n%s", out)
	}
}

func TestEnsureConfig_KeepsBackupOfPreviousFile(t *testing.T) {
	home := setHome(t)
	path := filepath.Join(home, ".gleann", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := `{"index_dir":"/custom"}`
	if err := os.WriteFile(path, []byte(prev), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureConfig(tagsServer(t, http.StatusOK, `{"models":[]}`).URL, true); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil || string(bak) != prev {
		t.Fatalf(".bak = %q, %v", bak, err)
	}
}

func TestPullModel_DefaultProgressOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, line := range []string{
			`{"status":"pulling manifest"}`,
			`not json`,
			`{"status":"pulling abc","total":1000,"completed":250}`,
			`{"status":"pulling abc","total":1000,"completed":1000}`,
			`{"status":"success"}`,
		} {
			fmt.Fprintln(w, line)
		}
	}))
	defer srv.Close()
	var err error
	out := captureStdout(t, func() { err = PullModel(srv.URL+"/", "m", nil) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pulling manifest", " 25.0%", "success"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %q", want, out)
		}
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("missing final newline: %q", out)
	}
}

func TestEnsureModels_VerboseAndPullFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			io.WriteString(w, `{"models":[{"name":"have:latest"}]}`)
		case "/api/pull":
			var req struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Name == "broken" {
				http.Error(w, "manifest unknown", http.StatusNotFound)
				return
			}
			io.WriteString(w, `{"status":"success"}`+"\n")
		}
	}))
	defer srv.Close()

	var pulled []string
	var err error
	out := captureStdout(t, func() { pulled, err = EnsureModels(srv.URL, false, "have", "fresh") })
	if err != nil {
		t.Fatal(err)
	}
	if len(pulled) != 1 || pulled[0] != "fresh" {
		t.Fatalf("pulled %v", pulled)
	}
	for _, want := range []string{"have (available)", "Pulling fresh", "fresh (installed)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	pulled, err = EnsureModels(srv.URL, true, "fresh", "broken")
	if err == nil || !strings.Contains(err.Error(), "failed to pull broken") || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("err = %v", err)
	}
	if len(pulled) != 1 || pulled[0] != "fresh" {
		t.Fatalf("models pulled before the failure must be returned, got %v", pulled)
	}
}

func TestEnsureModels_NoModelsNoNetwork(t *testing.T) {
	// The default host is filled in but never contacted without models.
	pulled, err := EnsureModels("", true)
	if err != nil || len(pulled) != 0 {
		t.Fatalf("EnsureModels() = %v, %v", pulled, err)
	}
}

func TestFormatDetectedConfig_SplitEmbeddings(t *testing.T) {
	out := FormatDetectedConfig(DetectedConfig{
		OllamaHost:         "http://h",
		EmbeddingModel:     "bge-m3",
		CodeEmbeddingModel: "code-embed",
		DocEmbeddingModel:  "doc-embed",
	})
	for _, want := range []string{"Code Embed : code-embed", "Docs Embed : doc-embed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestApplyDetectedConfig_AllOptionalFields(t *testing.T) {
	home := setHome(t)
	err := ApplyDetectedConfig(DetectedConfig{
		OllamaHost:         "http://h",
		OllamaAPIKey:       "secret",
		EmbeddingModel:     "bge-m3",
		CodeEmbeddingModel: "code-embed",
		DocEmbeddingModel:  "doc-embed",
		LLMModel:           "qwen3",
		IndexDir:           filepath.Join(home, "idx"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".gleann", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"ollama_api_key":       "secret",
		"code_embedding_model": "code-embed",
		"doc_embedding_model":  "doc-embed",
	} {
		if cfg[k] != want {
			t.Errorf("%s = %v, want %s", k, cfg[k], want)
		}
	}
}

func TestApplyDetectedConfig_Errors(t *testing.T) {
	unsetHome(t)
	if err := ApplyDetectedConfig(DetectedConfig{}); err == nil {
		t.Fatal("no home directory must be an error")
	}
	home := setHome(t)
	// .gleann is a file: the directory cannot be created.
	if err := os.WriteFile(filepath.Join(home, ".gleann"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDetectedConfig(DetectedConfig{}); err == nil {
		t.Fatal("an uncreatable config dir must be an error")
	}
}
