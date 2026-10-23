package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

func newFakeOllamaMCP(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		embs := make([][]float32, len(req.Input))
		for i, txt := range req.Input {
			e := make([]float32, 8)
			for j := range e {
				e[j] = float32((len(txt)+j*5)%11) / 11
			}
			e[i%8]++
			embs[i] = e
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDefaultSyncRunnerInProcess exercises the real (non-mocked) runner:
// build a new index, then sync it, entirely in-process.
func TestDefaultSyncRunnerInProcess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	indexDir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	ollama := newFakeOllamaMCP(t)

	srv := NewServer(Config{
		IndexDir:          indexDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    fmt.Sprintf("fake-%d", time.Now().UnixNano()),
		OllamaHost:        ollama.URL,
		Version:           "test",
	})
	defer srv.Close()

	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var progressCalls atomic.Int64
	out, err := srv.defaultSyncRunner(context.Background(), SyncOptions{
		IndexName: "mcpidx",
		DocsDir:   docs,
		Mode:      "code",
		NoPlugins: true,
		IsNew:     true,
		OnProgress: func(phase string, pct float64, done, total int, rate, etaSec float64) {
			progressCalls.Add(1)
		},
	})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if !strings.Contains(out, `Vector Index "mcpidx" built`) || !strings.Contains(out, "gleann-progress") {
		t.Errorf("unexpected build output:\n%s", out)
	}
	if progressCalls.Load() == 0 {
		t.Error("expected progress callbacks")
	}
	if _, err := gleann.GetIndexMeta(indexDir, "mcpidx"); err != nil {
		t.Fatalf("index not created: %v", err)
	}

	out, err = srv.defaultSyncRunner(context.Background(), SyncOptions{
		IndexName: "mcpidx", DocsDir: docs, Mode: "code", NoPlugins: true,
	})
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("expected up-to-date sync, got:\n%s", out)
	}

	// Close cancels the lifetime context used for background builds.
	srv.Close()
	if !errors.Is(srv.lifetime().Err(), context.Canceled) {
		t.Error("expected lifetime context to be cancelled by Close")
	}
}

// TestDefaultSyncRunnerHonoursCodeModel verifies that a new build uses
// code_embedding_model for mode "code". Before this, gleann_sync embedded
// every code index with the general embedding_model and only honoured the
// specialised model through the CLI --code-model flag.
func TestDefaultSyncRunnerHonoursCodeModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	indexDir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	ollama := newFakeOllamaMCP(t)

	buildCfg := gleann.DefaultConfig()
	buildCfg.IndexDir = indexDir
	buildCfg.EmbeddingProvider = "ollama"
	buildCfg.OllamaHost = ollama.URL
	buildCfg.EmbeddingModel = "general-model"
	buildCfg.CodeEmbeddingModel = "code-model"
	buildCfg.DocEmbeddingModel = "docs-model"

	srv := NewServer(Config{
		IndexDir:          indexDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "general-model",
		OllamaHost:        ollama.URL,
		Version:           "test",
		BuildConfig:       &buildCfg,
	})
	defer srv.Close()

	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.defaultSyncRunner(context.Background(), SyncOptions{
		IndexName: "codeidx", DocsDir: docs, Mode: "code", NoPlugins: true, IsNew: true,
	}); err != nil {
		t.Fatalf("code build: %v", err)
	}
	meta, err := gleann.GetIndexMeta(indexDir, "codeidx")
	if err != nil {
		t.Fatalf("index not created: %v", err)
	}
	if meta.EmbeddingModel != "code-model" {
		t.Errorf("code build recorded %q, want code-model", meta.EmbeddingModel)
	}

	// Mode "docs" must use the documentation model, not the code model.
	docsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(docsDir, "note.md"), []byte("# note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.defaultSyncRunner(context.Background(), SyncOptions{
		IndexName: "docidx", DocsDir: docsDir, Mode: "docs", NoPlugins: true, IsNew: true,
	}); err != nil {
		t.Fatalf("docs build: %v", err)
	}
	meta, err = gleann.GetIndexMeta(indexDir, "docidx")
	if err != nil {
		t.Fatalf("docs index not created: %v", err)
	}
	if meta.EmbeddingModel != "docs-model" {
		t.Errorf("docs build recorded %q, want docs-model", meta.EmbeddingModel)
	}
}
