package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// newBuildServer returns a server whose config points at a fake Ollama and
// a temp index dir; builds run in-process via internal/indexing.
func newBuildServer(t *testing.T) (*Server, http.Handler, gleann.Config) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ollama := newFakeOllama(t)
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.EmbeddingProvider = "ollama"
	// Unique model: the on-disk embedding cache is keyed by model + text.
	cfg.EmbeddingModel = fmt.Sprintf("test-model-%d", time.Now().UnixNano())
	cfg.OllamaHost = ollama.URL
	emb := embedding.NewComputer(embedding.Options{
		Provider: embedding.ProviderOllama,
		Model:    cfg.EmbeddingModel,
		BaseURL:  cfg.OllamaHost,
	})
	s := &Server{config: cfg, embedder: emb, searchers: make(map[string]*gleann.LeannSearcher)}
	// Registered after the TempDirs above, so it runs before they are removed:
	// Windows cannot delete index files a cached searcher still holds open.
	t.Cleanup(s.invalidateAllSearchers)
	return s, s.buildMux(), cfg
}

func TestUploadBuildsInProcess(t *testing.T) {
	_, h, cfg := newBuildServer(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "../../notes.md")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(fw, "# Notes\n\nGleann builds indexes in-process now.\n")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/indexes/up/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "indexed" || resp["file"] != "notes.md" {
		t.Fatalf("unexpected response: %v", resp)
	}
	if _, err := gleann.GetIndexMeta(cfg.IndexDir, "up"); err != nil {
		t.Fatalf("index not built: %v", err)
	}

	// Search sees the freshly built index.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/indexes/up/search", strings.NewReader(`{"query":"in-process","top_k":1}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
}

func TestUploadEmptyFileFails(t *testing.T) {
	_, h, _ := newBuildServer(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if _, err := mw.CreateFormFile("file", "empty.txt"); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/empty/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "failed to index file") {
		t.Fatalf("expected index failure, got %d %s", w.Code, w.Body.String())
	}
}

func TestIndexPathBuildsInProcess(t *testing.T) {
	s, h, cfg := newBuildServer(t)
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "README.md"), []byte("# Readme\n\nDocs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	post := func(name, payload string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/indexes/"+name+"/index-path", strings.NewReader(payload)))
		if w.Code != http.StatusOK {
			t.Fatalf("index-path: %d %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp
	}

	resp := post("p", fmt.Sprintf(`{"path":%q,"tags":["work"],"description":"demo","mcp_exposed":false}`, docs))
	if resp["status"] != "indexing_started" || resp["index"] != "p" || resp["path"] != docs {
		t.Fatalf("unexpected response: %v", resp)
	}
	resp = post("dual", fmt.Sprintf(`{"path":%q,"code_model":"code-m","doc_model":"doc-m"}`, docs))
	if resp["status"] != "indexing_started" {
		t.Fatalf("unexpected response: %v", resp)
	}
	s.bgWG.Wait()

	meta, err := gleann.GetIndexMeta(cfg.IndexDir, "p")
	if err != nil {
		t.Fatalf("index not built: %v", err)
	}
	if !meta.HasTag("work") || meta.Description != "demo" || meta.MCPExposed == nil || *meta.MCPExposed {
		t.Errorf("meta not applied: %+v", meta)
	}
	for _, n := range []string{"dual-code", "dual-docs"} {
		if _, err := gleann.GetIndexMeta(cfg.IndexDir, n); err != nil {
			t.Errorf("dual-model index %s missing: %v", n, err)
		}
	}
}
