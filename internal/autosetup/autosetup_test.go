package autosetup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
