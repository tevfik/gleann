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

// E-02/S-04: a UI Save must not persist start-up-only runtime overrides.
func TestConfigSave_DoesNotPersistRuntimeOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gleann")
	_ = os.MkdirAll(dir, 0o700)
	cfgPath := filepath.Join(dir, "config.json")
	orig := `{"embedding_provider":"llamacpp","embedding_model":"my.gguf","ollama_host":"http://localhost:11434","custom_unknown":"keep-me","completed":true}`
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded := gleann.DefaultConfig()
	loaded.EmbeddingProvider, loaded.EmbeddingModel, loaded.OllamaHost = "llamacpp", "my.gguf", "http://localhost:11434"
	runtime := loaded
	runtime.EmbeddingProvider, runtime.OllamaHost = "openai", "http://127.0.0.1:55555" // what initLlamaCPP does
	runtime.OpenAIBaseURL = "http://127.0.0.1:55555"

	s := &Server{config: runtime, searchers: make(map[string]*gleann.LeannSearcher)}
	s.SetRuntimeOverrides(gleann.ChangedKeys(loaded, runtime))

	// GET exposes the persisted values of the overridden keys.
	w := httptest.NewRecorder()
	s.handleGetConfig(w, httptest.NewRequest("GET", "/api/config", nil))
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	ov, _ := got["runtime_overrides"].(map[string]any)
	if ov["embedding_provider"] != "llamacpp" {
		t.Fatalf("runtime_overrides missing persisted provider: %v", got["runtime_overrides"])
	}

	// The UI loads the effective config, changes an unrelated field, saves.
	body := runtime
	body.BatchSize = 7
	raw, _ := json.Marshal(body)
	w = httptest.NewRecorder()
	s.handleUpdateConfig(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(string(raw))))
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(cfgPath)
	var disk map[string]any
	_ = json.Unmarshal(data, &disk)
	if disk["embedding_provider"] != "llamacpp" || disk["ollama_host"] != "http://localhost:11434" {
		t.Fatalf("runtime override leaked to disk: %v", disk)
	}
	if _, leaked := disk["openai_base_url"]; leaked {
		t.Fatalf("openai_base_url (embedded server) leaked to disk: %v", disk)
	}
	if disk["custom_unknown"] != "keep-me" || disk["batch_size"] != float64(7) {
		t.Fatalf("unrelated keys wrong: %v", disk)
	}

	// An explicit change of an overridden key IS persisted.
	body.EmbeddingModel = "other.gguf"
	body.EmbeddingProvider = "ollama"
	raw, _ = json.Marshal(body)
	w = httptest.NewRecorder()
	s.handleUpdateConfig(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(string(raw))))
	data, _ = os.ReadFile(cfgPath)
	_ = json.Unmarshal(data, &disk)
	if disk["embedding_provider"] != "ollama" || disk["embedding_model"] != "other.gguf" {
		t.Fatalf("explicit change not persisted: %v", disk)
	}
}
