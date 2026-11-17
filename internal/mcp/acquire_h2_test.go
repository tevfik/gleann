package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// h2SetMeta rewrites fields of an existing index meta file.
func h2SetMeta(t *testing.T, dir, name string, edit func(*gleann.IndexMeta)) {
	t.Helper()
	p := filepath.Join(dir, name, name+".meta.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m gleann.IndexMeta
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(&m)
	if data, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestH2AcquireSearcher_AutoResolvesName(t *testing.T) {
	srv, _ := h2LLMServer(t, nil)

	t.Setenv("GLEANN_INDEX", "idx")
	sr, release, err := srv.acquireSearcher("")
	if err != nil || sr == nil {
		t.Fatalf("GLEANN_INDEX: %v", err)
	}
	release()

	// Without the variable the single exposed index is chosen.
	t.Setenv("GLEANN_INDEX", "")
	sr, release, err = srv.acquireSearcher("")
	if err != nil || sr == nil {
		t.Fatalf("single index: %v", err)
	}
	release()

	// Two indexes, one named like the working directory: that one wins.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	h2WriteMeta(t, srv.config.IndexDir, gleann.IndexMeta{Name: filepath.Base(cwd), Backend: "hnsw"})
	if _, _, err := srv.acquireSearcher(""); err == nil || strings.Contains(err.Error(), "index name required") {
		t.Fatalf("expected the cwd-named (empty) index to be chosen and fail to load, got %v", err)
	}
	// Three indexes, none matching: no guess.
	if err := os.RemoveAll(filepath.Join(srv.config.IndexDir, filepath.Base(cwd))); err != nil {
		t.Fatal(err)
	}
	h2WriteMeta(t, srv.config.IndexDir, gleann.IndexMeta{Name: "other-a", Backend: "hnsw"})
	if _, _, err := srv.acquireSearcher(""); err == nil || !strings.Contains(err.Error(), "index name required") {
		t.Fatalf("ambiguous: err = %v", err)
	}
}

func TestH2AcquireSearcher_AccessControl(t *testing.T) {
	srv, _ := h2LLMServer(t, nil)
	t.Setenv("GLEANN_TAGS", "")

	h2SetMeta(t, srv.config.IndexDir, "idx", func(m *gleann.IndexMeta) {
		no := false
		m.MCPExposed = &no
		m.Tags = []string{"work"}
	})
	if _, _, err := srv.acquireSearcher("idx"); err == nil || !strings.Contains(err.Error(), "private and not exposed") {
		t.Fatalf("private index: %v", err)
	}

	h2SetMeta(t, srv.config.IndexDir, "idx", func(m *gleann.IndexMeta) { m.MCPExposed = nil })
	t.Setenv("GLEANN_TAGS", "personal,home")
	if _, _, err := srv.acquireSearcher("idx"); err == nil || !strings.Contains(err.Error(), "does not match required tags (personal,home)") {
		t.Fatalf("tag mismatch: %v", err)
	}
	t.Setenv("GLEANN_TAGS", "personal,work")
	_, release, err := srv.acquireSearcher("idx")
	if err != nil {
		t.Fatalf("tag match: %v", err)
	}
	release()
}

// h2EmbedServer answers the Ollama and OpenAI embedding endpoints with
// 8-dimensional vectors and records the requested model names.
type h2EmbedServer struct {
	mu     sync.Mutex
	models []string
	paths  []string
}

func (e *h2EmbedServer) handler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var inputs []string
	if json.Unmarshal(body.Input, &inputs) != nil {
		var one string
		_ = json.Unmarshal(body.Input, &one)
		inputs = []string{one}
	}
	e.mu.Lock()
	e.models = append(e.models, body.Model)
	e.paths = append(e.paths, r.URL.Path)
	e.mu.Unlock()
	vec := func(s string) []float32 {
		v := make([]float32, 8)
		for j := range v {
			v[j] = float32(len(s)+j) * 0.01
		}
		return v
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/embed":
		var embs [][]float32
		for _, s := range inputs {
			embs = append(embs, vec(s))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	case "/v1/embeddings":
		var data []map[string]any
		for i, s := range inputs {
			data = append(data, map[string]any{"index": i, "embedding": vec(s)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	default:
		http.NotFound(w, r)
	}
}

// The server's embedder serves another model than the index was built
// with: the searcher must embed queries with the index's model through the
// configured provider.
func TestH2AcquireSearcher_AdaptsToTheIndexEmbeddingModel(t *testing.T) {
	for _, provider := range []string{"ollama", "openai"} {
		t.Run(provider, func(t *testing.T) {
			emb := &h2EmbedServer{}
			hs := httptest.NewServer(http.HandlerFunc(emb.handler))
			t.Cleanup(hs.Close)
			srv, _ := h2LLMServer(t, func(c *Config) {
				c.EmbeddingProvider = provider
				c.EmbeddingModel = "server-default"
				c.OllamaHost = hs.URL
				c.OpenAIBaseURL = hs.URL
				c.OpenAIAPIKey = "sk-x"
			})
			srv.embedder = nil // as if the configured embedder were unavailable
			res := h2Call(t, srv.handleSearch, map[string]any{"index": "idx", "query": "JWT login"})
			if res.IsError {
				t.Fatalf("search: %s", extractText(res))
			}
			emb.mu.Lock()
			defer emb.mu.Unlock()
			if len(emb.models) == 0 {
				t.Fatal("the query was never embedded")
			}
			for i, m := range emb.models {
				if m != "test-model" {
					t.Errorf("request %d embedded with %q, want the index model test-model", i, m)
				}
			}
			wantPath := map[string]string{"ollama": "/api/embed", "openai": "/v1/embeddings"}[provider]
			if emb.paths[0] != wantPath {
				t.Errorf("embedding endpoint = %s, want %s", emb.paths[0], wantPath)
			}
		})
	}
}
