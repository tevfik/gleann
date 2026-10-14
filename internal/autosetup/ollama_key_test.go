package autosetup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAutosetupSendsOllamaBearer(t *testing.T) {
	var mu sync.Mutex
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"bge-m3:latest"}]}`))
		case "/api/pull":
			_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
		}
	}))
	defer srv.Close()
	t.Cleanup(func() { SetOllamaAPIKey("") })

	t.Setenv("OLLAMA_API_KEY", "")
	SetOllamaAPIKey("setup-key")
	if res := DetectOllama(srv.URL); !res.OllamaFound {
		t.Fatal("expected Ollama to be found")
	}
	if !HasModel(srv.URL, "bge-m3") {
		t.Error("HasModel should find bge-m3")
	}
	if err := PullModel(srv.URL, "x", func(string, int64, int64) {}); err != nil {
		t.Fatal(err)
	}

	// Env fallback when no explicit key is set.
	SetOllamaAPIKey("")
	t.Setenv("OLLAMA_API_KEY", "env-key")
	DetectOllama(srv.URL)

	mu.Lock()
	defer mu.Unlock()
	want := []string{"Bearer setup-key", "Bearer setup-key", "Bearer setup-key", "Bearer env-key"}
	if len(auths) != len(want) {
		t.Fatalf("requests = %v, want %v", auths, want)
	}
	for i := range want {
		if auths[i] != want[i] {
			t.Errorf("request %d Authorization = %q, want %q", i, auths[i], want[i])
		}
	}
}

func TestApplyDetectedConfigPersistsOllamaAPIKey(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := ApplyDetectedConfig(DetectedConfig{OllamaHost: "http://h:11434", OllamaAPIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, ".gleann", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["ollama_api_key"] != "k" {
		t.Errorf("ollama_api_key = %v, want k", m["ollama_api_key"])
	}
	// No key → field omitted.
	if err := ApplyDetectedConfig(DetectedConfig{OllamaHost: "http://h:11434"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(tmp, ".gleann", "config.json"))
	m = map[string]any{}
	_ = json.Unmarshal(data, &m)
	if _, ok := m["ollama_api_key"]; ok {
		t.Error("ollama_api_key should be omitted when empty")
	}
}
