package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// isolateHome points HOME/USERPROFILE at a fresh directory and returns the
// path of its config.json.
func isolateHome(t *testing.T) (home, cfgPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home, filepath.Join(home, ".gleann", "config.json")
}

func postConfig(s *Server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.handleUpdateConfig(w, httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body)))
	return w
}

func TestHandleUpdateConfig_Rejections(t *testing.T) {
	isolateHome(t)
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}

	tests := []struct {
		name, body, want string
	}{
		{"invalid json", `{`, "invalid config JSON"},
		{"non-vision multimodal model", `{"multimodal_model":"plain-text-model"}`, "does not support vision"},
		{"unknown storage mode", `{"storage_mode":"sideways"}`, "unknown storage mode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := postConfig(s, tc.body)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleUpdateConfig_SaveFailureIs500(t *testing.T) {
	home, _ := isolateHome(t)
	// ~/.gleann exists as a regular file, so the config directory cannot be created.
	if err := os.WriteFile(filepath.Join(home, ".gleann"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	w := postConfig(s, `{}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "failed to save config") {
		t.Errorf("got %d %s", w.Code, w.Body.String())
	}
}

func TestSaveServerConfig_Persistence(t *testing.T) {
	_, cfgPath := isolateHome(t)

	cfg := gleann.DefaultConfig()
	cfg.SearchConfig.UseReranker = true
	cfg.SearchConfig.RerankerConfig.Model = "rerank-m"
	cfg.SearchConfig.RerankerConfig.Provider = "ollama"
	if err := saveServerConfig(cfg); err != nil {
		t.Fatal(err)
	}
	var disk map[string]any
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		t.Fatal(err)
	}
	if disk["rerank_enabled"] != true || disk["rerank_model"] != "rerank-m" || disk["rerank_provider"] != "ollama" || disk["completed"] != true {
		t.Errorf("rerank/completed keys not persisted: %v", disk)
	}

	// Clearing the reranker removes its keys instead of leaving stale values.
	cfg.SearchConfig.RerankerConfig.Model, cfg.SearchConfig.RerankerConfig.Provider = "", ""
	if err := saveServerConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cfgPath)
	disk = map[string]any{}
	_ = json.Unmarshal(data, &disk)
	if _, ok := disk["rerank_model"]; ok {
		t.Errorf("stale rerank_model kept: %v", disk)
	}
}

func TestSaveServerConfig_Errors(t *testing.T) {
	t.Run("existing file is not JSON", func(t *testing.T) {
		_, cfgPath := isolateHome(t)
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, []byte("not json at all"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := saveServerConfig(gleann.DefaultConfig())
		if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
			t.Errorf("got %v", err)
		}
		// The unreadable file must be left untouched.
		if data, _ := os.ReadFile(cfgPath); string(data) != "not json at all" {
			t.Errorf("config overwritten: %q", data)
		}
	})
	t.Run("config path is a directory", func(t *testing.T) {
		_, cfgPath := isolateHome(t)
		if err := os.MkdirAll(cfgPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := saveServerConfig(gleann.DefaultConfig()); err == nil {
			t.Error("a directory in place of config.json must fail")
		}
	})
	t.Run("no home directory", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		if _, err := serverConfigPath(); err == nil {
			t.Skip("platform resolves a home directory without HOME")
		}
		if err := saveServerConfig(gleann.DefaultConfig()); err == nil {
			t.Error("saving without a home directory must fail")
		}
	})
	t.Run("whitespace-only file is treated as empty", func(t *testing.T) {
		_, cfgPath := isolateHome(t)
		_ = os.MkdirAll(filepath.Dir(cfgPath), 0o700)
		_ = os.WriteFile(cfgPath, []byte("  \n"), 0o600)
		if err := saveServerConfig(gleann.DefaultConfig()); err != nil {
			t.Errorf("got %v", err)
		}
	})
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.json")
	if err := writeFileAtomic(target, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(target, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "v2" {
		t.Errorf("content = %q", data)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".out.json.*.tmp")); len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}

	if err := writeFileAtomic(filepath.Join(dir, "missing", "x.json"), []byte("x"), 0o600); err == nil ||
		!strings.Contains(err.Error(), "create temp file") {
		t.Errorf("missing directory: %v", err)
	}

	// Renaming over a non-empty directory fails and still cleans up the temp file.
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(blocked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(blocked, []byte("x"), 0o600); err == nil || !strings.Contains(err.Error(), "replace") {
		t.Errorf("rename over directory: %v", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".blocked.*.tmp")); len(leftovers) != 0 {
		t.Errorf("temp files left behind after failed rename: %v", leftovers)
	}
}

func TestHandleGetConfig_RedactsSecretOverrides(t *testing.T) {
	_, cfgPath := isolateHome(t)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"openai_api_key":"sk-disk-secret","embedding_model":"disk-model"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	s.SetRuntimeOverrides([]string{"openai_api_key", "embedding_model"})

	w := httptest.NewRecorder()
	s.handleGetConfig(w, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if strings.Contains(w.Body.String(), "sk-disk-secret") {
		t.Fatalf("persisted secret leaked: %s", w.Body.String())
	}
	var got struct {
		Overrides map[string]any `json:"runtime_overrides"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Overrides["openai_api_key"] != gleann.RedactedSecret || got.Overrides["embedding_model"] != "disk-model" {
		t.Errorf("runtime_overrides = %v", got.Overrides)
	}
}

func TestIsSecretKey(t *testing.T) {
	for k, want := range map[string]bool{"openai_api_key": true, "ollama_api_key": true, "embedding_model": false, "": false} {
		if isSecretKey(k) != want {
			t.Errorf("isSecretKey(%q) != %v", k, want)
		}
	}
}

func TestApplyConfig_UsesOpenAIBaseURLForOpenAIEmbeddings(t *testing.T) {
	isolateHome(t)
	s := &Server{searchers: map[string]*gleann.LeannSearcher{}}
	cfg := gleann.DefaultConfig()
	cfg.EmbeddingProvider = "openai"
	cfg.EmbeddingModel = "text-embedding-x"
	cfg.OpenAIBaseURL = "http://127.0.0.1:1/v1"
	s.ApplyConfig(cfg)
	if got := s.CurrentConfig().EmbeddingModel; got != "text-embedding-x" {
		t.Errorf("config not applied: %q", got)
	}
	if _, emb := s.cfgAndEmbedder(); emb == nil || emb.ModelName() != "text-embedding-x" {
		t.Errorf("embedder not rebuilt: %v", emb)
	}
}

func TestQueryExpander_ConfigOverrides(t *testing.T) {
	s := &Server{}
	cfg := gleann.DefaultConfig()
	cfg.LLMModel = "chat-m"
	cfg.SearchConfig.ExpandModel = "expand-m"
	cfg.LLMProvider = "openai"
	cfg.OpenAIBaseURL = "http://127.0.0.1:2/v1"
	cfg.OllamaHost = "http://127.0.0.1:3"
	a := s.queryExpander(cfg)
	if a == nil {
		t.Fatal("nil expander")
	}
	// The same settings share one expander (its cache is reused); other settings do not.
	if b := s.queryExpander(cfg); a != b {
		t.Error("identical settings must reuse the expander")
	}
	cfg.SearchConfig.ExpandModel = "other-expand"
	if c := s.queryExpander(cfg); c == a {
		t.Error("a different expand model must yield a different expander")
	}
}

func TestNewLogBuffer_KeepsNewestLines(t *testing.T) {
	b := NewLogBuffer(2)
	for _, l := range []string{"one", "two", "three"} {
		if n, err := b.Write([]byte(l)); err != nil || n != len(l) {
			t.Fatalf("write %q: %d %v", l, n, err)
		}
	}
	got := b.GetLogs()
	if len(got) != 2 || got[0] != "two" || got[1] != "three" {
		t.Errorf("logs = %v, want [two three]", got)
	}
	got[0] = "mutated"
	if b.GetLogs()[0] != "two" {
		t.Error("GetLogs must return a copy")
	}
}

// POST /api/config applies the body on top of the running config. It used to
// decode into a zero Config, so a body with one field reset every other
// setting, the HNSW graph settings included (heuristic off).
func TestHandleUpdateConfig_PartialBodyKeepsTheRest(t *testing.T) {
	isolateHome(t)
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.OllamaHost = "http://ollama.test:11434"
	cfg.OpenAIAPIKey = "sk-keep"
	s := &Server{config: cfg, searchers: map[string]*gleann.LeannSearcher{}}

	if w := postConfig(s, `{"llm_model":"m2"}`); w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got := s.cfg()
	if got.LLMModel != "m2" {
		t.Errorf("llm_model = %q", got.LLMModel)
	}
	if got.HNSWConfig != cfg.HNSWConfig || !got.HNSWConfig.UseHeuristic {
		t.Errorf("hnsw config changed: %+v", got.HNSWConfig)
	}
	if got.IndexDir != cfg.IndexDir || got.OllamaHost != cfg.OllamaHost || got.OpenAIAPIKey != "sk-keep" {
		t.Errorf("other settings changed: index_dir %q ollama %q key %q", got.IndexDir, got.OllamaHost, got.OpenAIAPIKey)
	}

	// An explicit value still replaces, an explicit "" still clears.
	if w := postConfig(s, `{"openai_api_key":"","hnsw_config":{"m":16,"ef_construction":100,"ef_search":64,"use_heuristic":false}}`); w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got = s.cfg()
	if got.OpenAIAPIKey != "" || got.HNSWConfig.M != 16 || got.HNSWConfig.UseHeuristic {
		t.Errorf("explicit values not applied: key %q hnsw %+v", got.OpenAIAPIKey, got.HNSWConfig)
	}
	if got.LLMModel != "m2" {
		t.Errorf("an earlier update was lost: llm_model %q", got.LLMModel)
	}
}
