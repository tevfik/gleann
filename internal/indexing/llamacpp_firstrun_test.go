package indexing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/pkg/gleann"
)

type fakeEnvRunner struct{ started bool }

func (f *fakeEnvRunner) SetEmbedding(bool)           {}
func (f *fakeEnvRunner) Start(context.Context) error { f.started = true; return nil }
func (f *fakeEnvRunner) Stop() error                 { return nil }
func (f *fakeEnvRunner) BaseURL() string             { return "http://127.0.0.1:1" }

// An MCP-style sync (gleann_sync) builds its embedder through startLlamaCPP
// with the saved config. The first-run config holds llamacpp + "bge-m3"; with
// the catalogue model downloaded the embedded runner must start on its file.
func TestStartLlamaCPP_FirstRunConfigWithDownloadedModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	e, ok := modelcatalog.Find("bge-m3")
	if !ok {
		t.Fatal("bge-m3 is not in the catalogue")
	}
	size := e.Bytes
	if e.Pooler != nil {
		size = e.Pooler.Bytes
	}
	if err := os.MkdirAll(modelcatalog.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Path(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(e.Path(), size); err != nil { // sparse stand-in for the download
		t.Fatal(err)
	}

	var gotModel string
	fake := &fakeEnvRunner{}
	orig := newLlamaRunner
	newLlamaRunner = func(m string) llamaRunner { gotModel = m; return fake }
	defer func() { newLlamaRunner = orig }()

	cfg := gleann.DefaultConfig()
	cfg.EmbeddingProvider = "llamacpp" // EmbeddingModel keeps the first-run "bge-m3"
	stop, err := startLlamaCPP(context.Background(), &cfg, newPrinter(nil, nil))
	if err != nil {
		t.Fatalf("startLlamaCPP with the first-run config: %v", err)
	}
	defer stop()
	if !fake.started || gotModel != e.Path() {
		t.Errorf("started=%v model=%q, want the downloaded file %q", fake.started, gotModel, e.Path())
	}
	if cfg.EmbeddingProvider != "openai" || cfg.EmbeddingModel != "bge-m3" {
		t.Errorf("provider=%q model=%q", cfg.EmbeddingProvider, cfg.EmbeddingModel)
	}
}

// Not downloaded and no opt-in: a clear error, no runner.
func TestStartLlamaCPP_CatalogueModelMissingWithoutOptIn(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	started := false
	orig := newLlamaRunner
	newLlamaRunner = func(string) llamaRunner { started = true; return &fakeEnvRunner{} }
	defer func() { newLlamaRunner = orig }()
	cfg := gleann.DefaultConfig()
	cfg.EmbeddingProvider = "llamacpp"
	_, err := startLlamaCPP(context.Background(), &cfg, newPrinter(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "gleann models download bge-m3") || started {
		t.Fatalf("err=%v started=%v", err, started)
	}
}
