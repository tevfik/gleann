package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Tests of how the CLI assembles a gleann.Config: the vector-index tuning
// flags, host normalisation, GLEANN_MMAP, and the saved config.json merged
// underneath the flags.

// fSaveConfig writes raw JSON as the saved config of the current HOME.
func fSaveConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".gleann")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFGetConfigIndexTuningFlags(t *testing.T) {
	t.Setenv("GLEANN_MMAP", "")
	cfg := getConfig([]string{
		"--backend", "diskann",
		"--pq-dim", "16", "--pq-centroids", "128",
		"--search-l", "90", "--search-pq-rerank", "40",
		"--diskann-r", "48", "--diskann-l", "120", "--diskann-alpha", "1.4",
		"--storage", "recompute",
		"--ollama-api-key", "sekret",
		"--openai-base-url", "http://llm.local:8080/v1/",
	})
	d := cfg.DiskANNConfig
	if cfg.Backend != "diskann" || d.PQDim != 16 || d.PQCentroids != 128 || d.SearchL != 90 ||
		d.SearchPQRerank != 40 || d.R != 48 || d.L != 120 || d.Alpha < 1.39 || d.Alpha > 1.41 {
		t.Errorf("diskann flags not applied: backend=%q %+v", cfg.Backend, d)
	}
	if cfg.StorageMode != gleann.StorageRecompute || !cfg.HNSWConfig.PruneEmbeddings || cfg.HNSWConfig.PruneKeepFraction != 0 {
		t.Errorf("--storage recompute: mode=%q prune=%v keep=%v", cfg.StorageMode, cfg.HNSWConfig.PruneEmbeddings, cfg.HNSWConfig.PruneKeepFraction)
	}
	if cfg.OllamaAPIKey != "sekret" {
		t.Errorf("OllamaAPIKey = %q", cfg.OllamaAPIKey)
	}
	if cfg.OpenAIBaseURL != "http://llm.local:8080/v1" {
		t.Errorf("OpenAIBaseURL = %q, want the trailing slash trimmed", cfg.OpenAIBaseURL)
	}

	// A flag at the end of the line without its value is ignored.
	for _, f := range []string{"--backend", "--pq-dim", "--pq-centroids", "--search-l", "--search-pq-rerank",
		"--diskann-r", "--diskann-l", "--diskann-alpha", "--storage", "--ollama-api-key", "--openai-base-url", "--host"} {
		got := getConfig([]string{f})
		def := gleann.DefaultConfig()
		if got.Backend != def.Backend || got.DiskANNConfig != def.DiskANNConfig || got.StorageMode != def.StorageMode {
			t.Errorf("%s without a value changed the config", f)
		}
	}
}

func TestFGetConfigBadStorageExits(t *testing.T) {
	r := captureRun(t, "", func() { getConfig([]string{"--storage", "floppy"}) })
	wantExit(t, r, 1)
	mustContain(t, "bad storage", r.Err, "error: --storage:", `unknown storage mode "floppy"`)
}

func TestFGetConfigHostForms(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"11434", "http://localhost:11434"},
		{"gpu-box:11434", "http://gpu-box:11434"},
		{"https://ollama.example", "https://ollama.example"},
	} {
		if got := getConfig([]string{"--host", tc.in}).OllamaHost; got != tc.want {
			t.Errorf("--host %s: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFGetConfigMmapEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want *bool
	}{
		{"off", new(bool)},
		{"No", new(bool)},
		{"on", ptrTo(true)},
		{"YES", ptrTo(true)},
		{"maybe", nil}, // unrecognised values leave the default
	} {
		t.Setenv("GLEANN_MMAP", tc.env)
		cfg := getConfig(nil)
		def := gleann.DefaultConfig()
		switch {
		case tc.want == nil:
			if (cfg.UseMmap == nil) != (def.UseMmap == nil) || cfg.HNSWConfig.UseMmap != def.HNSWConfig.UseMmap {
				t.Errorf("GLEANN_MMAP=%s changed the default", tc.env)
			}
		case cfg.UseMmap == nil || *cfg.UseMmap != *tc.want || cfg.HNSWConfig.UseMmap != *tc.want || cfg.DiskANNConfig.UseMmap != *tc.want:
			t.Errorf("GLEANN_MMAP=%s: UseMmap=%v hnsw=%v diskann=%v", tc.env, cfg.UseMmap, cfg.HNSWConfig.UseMmap, cfg.DiskANNConfig.UseMmap)
		}
	}
}

func ptrTo[T any](v T) *T { return &v }

// Without a saved config, OLLAMA_HOST is normalised like --host.
func TestFApplySavedConfigEnvHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct{ env, want string }{
		{"11500", "http://localhost:11500"},
		{"box:11434", "http://box:11434"},
		{"http://box:1", "http://box:1"},
	} {
		t.Setenv("OLLAMA_HOST", tc.env)
		cfg := gleann.DefaultConfig()
		applySavedConfig(&cfg, nil)
		if cfg.OllamaHost != tc.want {
			t.Errorf("OLLAMA_HOST=%s: got %q, want %q", tc.env, cfg.OllamaHost, tc.want)
		}
		// --host wins over the environment.
		cfg = gleann.DefaultConfig()
		cfg.OllamaHost = "http://flag:1"
		applySavedConfig(&cfg, []string{"--host", "flag:1"})
		if cfg.OllamaHost != "http://flag:1" {
			t.Errorf("--host overridden by OLLAMA_HOST: %q", cfg.OllamaHost)
		}
	}

	// With a saved config that has no host, OLLAMA_HOST still applies.
	fSaveConfig(t, home, `{"embedding_provider": "ollama"}`)
	t.Setenv("OLLAMA_HOST", "envbox:7")
	cfg := gleann.DefaultConfig()
	applySavedConfig(&cfg, nil)
	if cfg.OllamaHost != "http://envbox:7" {
		t.Errorf("saved config without host: got %q", cfg.OllamaHost)
	}
	for _, tc := range []struct{ saved, want string }{
		{"7777", "http://localhost:7777"},
		{"saved:1", "http://saved:1"},
	} {
		fSaveConfig(t, home, `{"ollama_host": "`+tc.saved+`"}`)
		cfg := gleann.DefaultConfig()
		applySavedConfig(&cfg, nil)
		if cfg.OllamaHost != tc.want {
			t.Errorf("saved host %s: got %q, want %q", tc.saved, cfg.OllamaHost, tc.want)
		}
	}
}

// Every saved setting reaches the config unless a flag (already applied to
// config) says otherwise.
func TestFApplySavedConfigCopiesSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_INDEX_DIR", "")
	idx := filepath.Join(home, "saved-indexes")
	fSaveConfig(t, home, `{
		"embedding_provider": "openai", "embedding_model": "text-embedding-3-small",
		"code_embedding_model": "code-emb", "doc_embedding_model": "doc-emb",
		"openai_api_key": "sk-saved", "ollama_api_key": "ok-saved", "openai_base_url": "http://saved/v1",
		"index_dir": "`+filepath.ToSlash(idx)+`",
		"llm_provider": "openai", "llm_model": "gpt-x", "top_k": 17,
		"storage_mode": "recompute", "backend": "diskann", "multimodal_model": "llava",
		"rerank_enabled": true, "rerank_model": "bge-reranker", "rerank_provider": "jina",
		"expand": true, "expand_model": "small",
		"auth": "token", "cors_origins": ["https://a.example"], "allowed_roots": ["/srv"],
		"rate_limit": 5, "rate_burst": 9,
		"proxy_mode": "inject", "proxy_context_intro": "Context:", "proxy_cover": true,
		"proxy_cover_margin": 0.25, "proxy_cover_share": 0.5, "proxy_cover_floor": 0.1
	}`)
	cfg := gleann.DefaultConfig()
	applySavedConfig(&cfg, nil)

	checks := []struct {
		name      string
		got, want any
	}{
		{"provider", cfg.EmbeddingProvider, "openai"},
		{"model", cfg.EmbeddingModel, "text-embedding-3-small"},
		{"code model", cfg.CodeEmbeddingModel, "code-emb"},
		{"doc model", cfg.DocEmbeddingModel, "doc-emb"},
		{"openai key", cfg.OpenAIAPIKey, "sk-saved"},
		{"ollama key", cfg.OllamaAPIKey, "ok-saved"},
		{"openai base", cfg.OpenAIBaseURL, "http://saved/v1"},
		{"index dir", filepath.Clean(cfg.IndexDir), filepath.Clean(idx)},
		{"llm provider", cfg.LLMProvider, "openai"},
		{"llm model", cfg.LLMModel, "gpt-x"},
		{"top k", cfg.SearchConfig.TopK, 17},
		{"storage", cfg.StorageMode, gleann.StorageRecompute},
		{"prune", cfg.HNSWConfig.PruneEmbeddings, true},
		{"backend", cfg.Backend, "diskann"},
		{"multimodal", cfg.MultimodalModel, "llava"},
		{"rerank", cfg.SearchConfig.UseReranker, true},
		{"rerank model", cfg.SearchConfig.RerankerConfig.Model, "bge-reranker"},
		{"rerank provider", string(cfg.SearchConfig.RerankerConfig.Provider), "jina"},
		{"expand", cfg.SearchConfig.Expand, true},
		{"expand model", cfg.SearchConfig.ExpandModel, "small"},
		{"auth", cfg.Auth, "token"},
		{"cors", len(cfg.CORSOrigins), 1},
		{"roots", len(cfg.AllowedRoots), 1},
		{"rate", cfg.RateLimit, 5},
		{"burst", cfg.RateBurst, 9},
		{"proxy mode", cfg.ProxyMode, "inject"},
		{"proxy intro", cfg.ProxyContextIntro, "Context:"},
		{"proxy cover", cfg.ProxyCover, true},
		{"cover margin", cfg.ProxyCoverMargin, float32(0.25)},
		{"cover share", cfg.ProxyCoverShare, float32(0.5)},
		{"cover floor", cfg.ProxyCoverFloor, float32(0.1)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}

	// Flags win: provider, model, top-k, storage, backend, LLM and index dir.
	cfg = gleann.DefaultConfig()
	cfg.EmbeddingProvider, cfg.EmbeddingModel = "ollama", "bge-m3"
	cfg.OpenAIAPIKey = "sk-flag"
	args := []string{"--provider", "ollama", "--model", "bge-m3", "--top-k", "3", "--storage", "store",
		"--backend", "hnsw", "--llm-provider", "ollama", "--llm-model", "llama", "--index-dir", "x"}
	cfg.SearchConfig.TopK, cfg.Backend, cfg.LLMProvider, cfg.LLMModel = 3, "hnsw", "ollama", "llama"
	applySavedConfig(&cfg, args)
	if cfg.EmbeddingProvider != "ollama" || cfg.EmbeddingModel != "bge-m3" || cfg.SearchConfig.TopK != 3 ||
		cfg.Backend != "hnsw" || cfg.LLMProvider != "ollama" || cfg.LLMModel != "llama" ||
		cfg.OpenAIAPIKey != "sk-flag" || cfg.StorageMode == gleann.StorageRecompute || filepath.Clean(cfg.IndexDir) == filepath.Clean(idx) {
		t.Errorf("saved config overrode flags: %+v", cfg)
	}

	// resolveOllamaAPIKey: config value, then the saved key.
	if got := resolveOllamaAPIKey(gleann.Config{OllamaAPIKey: "explicit"}); got != "explicit" {
		t.Errorf("resolveOllamaAPIKey explicit = %q", got)
	}
	if got := resolveOllamaAPIKey(gleann.Config{}); got != "ok-saved" {
		t.Errorf("resolveOllamaAPIKey saved = %q", got)
	}
}

func TestFDefaultEmbedderResolverCaches(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.OllamaHost = "http://127.0.0.1:1"
	res := defaultEmbedderResolver(cfg)
	a, err := res.ResolveEmbedder(t.Context(), "model-a", 8)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := res.ResolveEmbedder(t.Context(), "model-a", 8)
	c, _ := res.ResolveEmbedder(t.Context(), "model-b", 8)
	if a != b {
		t.Error("same model resolved to different embedders")
	}
	if a == c {
		t.Error("different models share an embedder")
	}
}
