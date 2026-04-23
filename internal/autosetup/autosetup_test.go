package autosetup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectOllama_Reachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{
					{"name": "bge-m3:latest"},
					{"name": "gemma3:4b"},
					{"name": "llama3:8b"},
				},
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	res := DetectOllama(srv.URL)
	if !res.OllamaFound {
		t.Fatal("expected OllamaFound=true")
	}
	if len(res.ModelsFound) != 3 {
		t.Fatalf("expected 3 models, got %d", len(res.ModelsFound))
	}
	if res.EmbeddingModel != "bge-m3:latest" {
		t.Errorf("expected bge-m3:latest, got %s", res.EmbeddingModel)
	}
	if res.LLMModel != "gemma3:4b" {
		t.Errorf("expected gemma3:4b, got %s", res.LLMModel)
	}
}

func TestDetectOllama_Unreachable(t *testing.T) {
	res := DetectOllama("http://localhost:19999")
	if res.OllamaFound {
		t.Fatal("expected OllamaFound=false for unreachable host")
	}
	if res.EmbeddingModel != "bge-m3" {
		t.Errorf("expected fallback bge-m3, got %s", res.EmbeddingModel)
	}
}

func TestPickModel(t *testing.T) {
	models := []string{"phi-4:latest", "gemma3:4b", "nomic-embed-text"}

	got := pickModel(models, []string{"gemma3", "llama3"}, "fallback")
	if got != "gemma3:4b" {
		t.Errorf("expected gemma3:4b, got %s", got)
	}

	got = pickModel(models, []string{"nonexistent"}, "fallback")
	if got != "fallback" {
		t.Errorf("expected fallback, got %s", got)
	}
}

func TestEnsureConfig_CreatesFile(t *testing.T) {
	// Use temp dir as HOME.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir) // Windows home directory var

	// Start a mock Ollama.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "bge-m3:latest"},
				{"name": "gemma3:4b"},
			},
		})
	}))
	defer srv.Close()

	bootstrapped, err := EnsureConfig(srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bootstrapped {
		t.Fatal("expected bootstrap to run")
	}

	// Verify file was created.
	cfgPath := filepath.Join(tmpDir, ".gleann", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal("config file not created:", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal("invalid json:", err)
	}
	if cfg["completed"] != true {
		t.Error("expected completed=true")
	}
	if cfg["embedding_model"] != "bge-m3:latest" {
		t.Errorf("expected bge-m3:latest, got %v", cfg["embedding_model"])
	}
}

func TestEnsureConfig_SkipsExisting(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir) // Windows home directory var

	// Create existing config.
	gleannDir := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannDir, 0o755)
	cfg := map[string]any{"completed": true, "embedding_model": "existing"}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(gleannDir, "config.json"), data, 0o644)

	bootstrapped, err := EnsureConfig("", true)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrapped {
		t.Fatal("expected bootstrap to be skipped for existing config")
	}
}

// ── ModelTier / EmbeddingTiers tests ──────────────────────────────────────

func TestEmbeddingTiers(t *testing.T) {
	tiers := EmbeddingTiers()
	if len(tiers) == 0 {
		t.Fatal("expected non-empty embedding tiers")
	}

	// Tier 1 should exist.
	var hasTier1 bool
	for _, tier := range tiers {
		if tier.Tier == 1 {
			hasTier1 = true
			break
		}
	}
	if !hasTier1 {
		t.Error("expected at least one tier-1 model")
	}

	// First entry should be nomic-embed-text (quick start).
	if tiers[0].Name != "nomic-embed-text" {
		t.Errorf("expected first tier to be nomic-embed-text, got %s", tiers[0].Name)
	}
	if tiers[0].Tier != 1 {
		t.Errorf("expected first tier to be 1, got %d", tiers[0].Tier)
	}
}

func TestQuickStartEmbeddingModel(t *testing.T) {
	m := QuickStartEmbeddingModel()
	if m != "nomic-embed-text" {
		t.Errorf("expected nomic-embed-text, got %s", m)
	}
}

// ── HasModel tests ────────────────────────────────────────────────────────

func TestHasModel_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "bge-m3:latest"},
				{"name": "gemma3:4b"},
			},
		})
	}))
	defer srv.Close()

	if !HasModel(srv.URL, "bge-m3") {
		t.Error("expected HasModel to find bge-m3")
	}
	if !HasModel(srv.URL, "bge-m3:latest") {
		t.Error("expected HasModel to find bge-m3:latest")
	}
	if !HasModel(srv.URL, "gemma3:4b") {
		t.Error("expected HasModel to find gemma3:4b")
	}
}

func TestHasModel_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "bge-m3:latest"},
			},
		})
	}))
	defer srv.Close()

	if HasModel(srv.URL, "nomic-embed-text") {
		t.Error("expected HasModel to not find nomic-embed-text")
	}
}

func TestHasModel_Unreachable(t *testing.T) {
	if HasModel("http://localhost:19999", "bge-m3") {
		t.Error("expected HasModel to return false for unreachable host")
	}
}

// ── PullModel tests ───────────────────────────────────────────────────────

func TestPullModel_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pull" && r.Method == "POST" {
			// Simulate streaming pull response.
			w.Header().Set("Content-Type", "application/x-ndjson")
			lines := []string{
				`{"status":"pulling manifest"}`,
				`{"status":"pulling abc123","digest":"sha256:abc123","total":1000,"completed":500}`,
				`{"status":"pulling abc123","digest":"sha256:abc123","total":1000,"completed":1000}`,
				`{"status":"verifying sha256 digest"}`,
				`{"status":"success"}`,
			}
			for _, line := range lines {
				fmt.Fprintln(w, line)
			}
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	var statuses []string
	err := PullModel(srv.URL, "test-model", func(status string, completed, total int64) {
		statuses = append(statuses, status)
	})
	if err != nil {
		t.Fatalf("PullModel failed: %v", err)
	}
	if len(statuses) == 0 {
		t.Error("expected progress callbacks")
	}
	// Should contain "success".
	found := false
	for _, s := range statuses {
		if s == "success" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'success' status in callbacks, got %v", statuses)
	}
}

func TestPullModel_Unreachable(t *testing.T) {
	err := PullModel("http://localhost:19999", "test-model", nil)
	if err == nil {
		t.Error("expected error for unreachable host")
	}
}

func TestPullModel_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	err := PullModel(srv.URL, "nonexistent-model", nil)
	if err == nil {
		t.Error("expected error for HTTP 404")
	}
}

// ── EnsureModels tests ────────────────────────────────────────────────────

func TestEnsureModels_AllAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "bge-m3:latest"},
				{"name": "gemma3:4b"},
			},
		})
	}))
	defer srv.Close()

	pulled, err := EnsureModels(srv.URL, true, "bge-m3", "gemma3:4b")
	if err != nil {
		t.Fatalf("EnsureModels failed: %v", err)
	}
	if len(pulled) != 0 {
		t.Errorf("expected no models pulled, got %v", pulled)
	}
}

func TestEnsureModels_PullsMissing(t *testing.T) {
	pullCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{
					{"name": "gemma3:4b"},
				},
			})
			return
		}
		if r.URL.Path == "/api/pull" && r.Method == "POST" {
			pullCount++
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintln(w, `{"status":"success"}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	pulled, err := EnsureModels(srv.URL, true, "bge-m3", "gemma3:4b")
	if err != nil {
		t.Fatalf("EnsureModels failed: %v", err)
	}
	if len(pulled) != 1 {
		t.Errorf("expected 1 model pulled, got %d", len(pulled))
	}
	if pulled[0] != "bge-m3" {
		t.Errorf("expected bge-m3, got %s", pulled[0])
	}
}

func TestEnsureModels_EmptyStringsIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{},
		})
	}))
	defer srv.Close()

	pulled, err := EnsureModels(srv.URL, true, "", "")
	if err != nil {
		t.Fatalf("EnsureModels failed: %v", err)
	}
	if len(pulled) != 0 {
		t.Errorf("expected no models pulled for empty strings, got %v", pulled)
	}
}

// ── DetectAll tests ───────────────────────────────────────────────────────

func TestDetectAll_WithModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "bge-m3:latest"},
				{"name": "gemma3:4b"},
			},
		})
	}))
	defer srv.Close()

	dc := DetectAll(srv.URL)
	if !dc.OllamaRunning {
		t.Error("expected OllamaRunning=true")
	}
	if dc.EmbeddingModel != "bge-m3:latest" {
		t.Errorf("expected bge-m3:latest, got %s", dc.EmbeddingModel)
	}
	if dc.LLMModel != "gemma3:4b" {
		t.Errorf("expected gemma3:4b, got %s", dc.LLMModel)
	}
}

func TestDetectAll_QuickStart_NoEmbeddingAvailable(t *testing.T) {
	// When no embedding model exists and quick-start is true,
	// should use nomic-embed-text (tier 1) instead of bge-m3.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "gemma3:4b"},
			},
		})
	}))
	defer srv.Close()

	dc := DetectAll(srv.URL, true)
	if dc.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("expected nomic-embed-text in quick-start mode, got %s", dc.EmbeddingModel)
	}
}

func TestDetectAll_QuickStart_EmbeddingAlreadyAvailable(t *testing.T) {
	// When an embedding model is already available, quick-start should use it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "bge-m3:latest"},
				{"name": "gemma3:4b"},
			},
		})
	}))
	defer srv.Close()

	dc := DetectAll(srv.URL, true)
	if dc.EmbeddingModel != "bge-m3:latest" {
		t.Errorf("expected bge-m3:latest (already available), got %s", dc.EmbeddingModel)
	}
}

// ── FormatDetectedConfig tests ────────────────────────────────────────────

func TestFormatDetectedConfig(t *testing.T) {
	dc := DetectedConfig{
		OllamaHost:     "http://localhost:11434",
		OllamaRunning:  true,
		EmbeddingModel: "bge-m3",
		LLMModel:       "gemma3:4b",
		RerankModel:    "bge-reranker",
		IndexDir:       "/home/test/.gleann/indexes",
		MCPEnabled:     true,
		ServerEnabled:  false,
	}

	s := FormatDetectedConfig(dc)
	if s == "" {
		t.Fatal("expected non-empty formatted config")
	}
	// Check key content is present.
	for _, expected := range []string{"bge-m3", "gemma3:4b", "bge-reranker", "running", "Accept"} {
		if !strings.Contains(s, expected) {
			t.Errorf("expected formatted config to contain %q", expected)
		}
	}
}

func TestFormatDetectedConfig_NoRerank(t *testing.T) {
	dc := DetectedConfig{
		OllamaHost:     "http://localhost:11434",
		OllamaRunning:  true,
		EmbeddingModel: "bge-m3",
		LLMModel:       "gemma3:4b",
		IndexDir:       "/home/test/.gleann/indexes",
	}

	s := FormatDetectedConfig(dc)
	if !strings.Contains(s, "(none)") {
		t.Error("expected '(none)' for empty reranker")
	}
}

// ── ApplyDetectedConfig tests ─────────────────────────────────────────────

func TestApplyDetectedConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	dc := DetectedConfig{
		OllamaHost:     "http://localhost:11434",
		OllamaRunning:  true,
		EmbeddingModel: "bge-m3",
		LLMModel:       "gemma3:4b",
		RerankModel:    "bge-reranker",
		IndexDir:       filepath.Join(tmpDir, ".gleann", "indexes"),
		MCPEnabled:     true,
		ServerEnabled:  false,
	}

	if err := ApplyDetectedConfig(dc); err != nil {
		t.Fatal(err)
	}

	// Verify file was created.
	cfgPath := filepath.Join(tmpDir, ".gleann", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal("config file not created:", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal("invalid json:", err)
	}
	if cfg["embedding_model"] != "bge-m3" {
		t.Errorf("expected embedding_model=bge-m3, got %v", cfg["embedding_model"])
	}
	if cfg["llm_model"] != "gemma3:4b" {
		t.Errorf("expected llm_model=gemma3:4b, got %v", cfg["llm_model"])
	}
	if cfg["rerank_model"] != "bge-reranker" {
		t.Errorf("expected rerank_model=bge-reranker, got %v", cfg["rerank_model"])
	}
	if cfg["completed"] != true {
		t.Error("expected completed=true")
	}
}

func TestApplyDetectedConfig_NoRerank(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	dc := DetectedConfig{
		OllamaHost:     "http://localhost:11434",
		EmbeddingModel: "bge-m3",
		LLMModel:       "gemma3:4b",
		IndexDir:       filepath.Join(tmpDir, ".gleann", "indexes"),
	}

	if err := ApplyDetectedConfig(dc); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, ".gleann", "config.json"))
	var cfg map[string]any
	json.Unmarshal(data, &cfg)

	if _, ok := cfg["rerank_model"]; ok {
		t.Error("expected no rerank_model when RerankModel is empty")
	}
	if _, ok := cfg["rerank_enabled"]; ok {
		t.Error("expected no rerank_enabled when RerankModel is empty")
	}
}
