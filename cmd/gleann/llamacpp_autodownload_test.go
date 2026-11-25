package main

// Opt-in foreground download (llamacpp_config.auto_download): with the flag
// true a missing catalogue model is downloaded once through the modelcatalog
// path; false or unset never makes a request; a checksum mismatch or a
// cancel leaves no file. The "catalogue" is an httptest model server.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/pkg/gleann"
)

type modelServer struct {
	*httptest.Server
	hits atomic.Int32
}

func newModelServer(t *testing.T, payload []byte, handler func(w http.ResponseWriter, r *http.Request)) *modelServer {
	t.Helper()
	m := &modelServer{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.hits.Add(1)
		if handler != nil {
			handler(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(m.Close)
	return m
}

// autoDownloadEnv isolates the models dir and points the catalogue lookup at
// one entry served by srv. It returns the entry and the config to use.
func autoDownloadEnv(t *testing.T, srv *modelServer, payload []byte, sha string, autoDownload bool) (modelcatalog.Entry, *gleann.Config) {
	t.Helper()
	e, cfg := autoDownloadEnvID(t, srv, payload, sha, autoDownload, "tiny-test")
	cfg.EmbeddingModel = e.File
	return e, cfg
}

// autoDownloadEnvID is autoDownloadEnv with the catalogue id of the entry; the
// config keeps the id as its embedding model, like the first-run config does.
func autoDownloadEnvID(t *testing.T, srv *modelServer, payload []byte, sha string, autoDownload bool, id string) (modelcatalog.Entry, *gleann.Config) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	e := modelcatalog.Entry{
		ID: id, Name: "Tiny Test", Kind: modelcatalog.Embedding, File: id + "-Q4.gguf",
		URL: srv.URL + "/" + id + ".gguf", Bytes: int64(len(payload)), SHA256: sha,
	}
	orig := llamaCatalogLookup
	llamaCatalogLookup = func(name string) (modelcatalog.Entry, bool) {
		if name == e.ID || name == e.File {
			return e, true
		}
		return modelcatalog.Entry{}, false
	}
	t.Cleanup(func() { llamaCatalogLookup = orig })
	cfg := gleann.DefaultConfig()
	cfg.EmbeddingProvider = "llamacpp"
	cfg.EmbeddingModel = id
	cfg.LlamaCPPConfig.AutoDownload = autoDownload
	return e, &cfg
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func noModelFiles(t *testing.T, e modelcatalog.Entry) {
	t.Helper()
	for _, p := range []string{e.Path(), e.Path() + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists after a failed or disabled download (err=%v)", p, err)
		}
	}
}

func TestAutoDownloadTrueDownloadsOnceAndIsIdempotent(t *testing.T) {
	payload := []byte(strings.Repeat("gguf-bytes-", 1000))
	srv := newModelServer(t, payload, nil)
	e, cfg := autoDownloadEnv(t, srv, payload, sum(payload), true)

	if err := ensureLlamaCPPModels(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(e.Path())
	if err != nil || string(got) != string(payload) {
		t.Fatalf("model not installed correctly: %v", err)
	}
	if srv.hits.Load() != 1 {
		t.Fatalf("requests = %d, want 1", srv.hits.Load())
	}
	if err := ensureLlamaCPPModels(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if srv.hits.Load() != 1 {
		t.Errorf("second call downloaded again (%d requests)", srv.hits.Load())
	}
	if _, err := os.Stat(e.Path() + ".part"); !os.IsNotExist(err) {
		t.Error(".part file left behind")
	}
}

func TestAutoDownloadFalseOrUnsetNeverRequests(t *testing.T) {
	payload := []byte("x")
	srv := newModelServer(t, payload, nil)
	e, cfg := autoDownloadEnv(t, srv, payload, sum(payload), false)
	if err := ensureLlamaCPPModels(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	// Unset: the default config value.
	def := gleann.DefaultConfig()
	if def.LlamaCPPConfig.AutoDownload {
		t.Fatal("auto_download must default to false")
	}
	cfg.LlamaCPPConfig = def.LlamaCPPConfig
	if err := ensureLlamaCPPModels(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if srv.hits.Load() != 0 {
		t.Errorf("%d requests with auto_download false/unset, want 0", srv.hits.Load())
	}
	noModelFiles(t, e)
}

func TestAutoDownloadChecksumMismatchLeavesNoFile(t *testing.T) {
	payload := []byte(strings.Repeat("good", 100))
	srv := newModelServer(t, payload, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("evil", 100))) // same size, other bytes
	})
	e, cfg := autoDownloadEnv(t, srv, payload, sum(payload), true)

	err := ensureLlamaCPPModels(context.Background(), cfg, true)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	noModelFiles(t, e)
}

func TestAutoDownloadCancelLeavesNoFile(t *testing.T) {
	payload := []byte(strings.Repeat("x", 4096))
	srv := newModelServer(t, payload, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		_, _ = w.Write(payload[:1024])
		w.(http.Flusher).Flush()
		<-r.Context().Done() // stall until the client gives up
	})
	e, cfg := autoDownloadEnv(t, srv, payload, sum(payload), true)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	err := ensureLlamaCPPModels(ctx, cfg, true)
	if err == nil {
		t.Fatal("a cancelled download must fail")
	}
	noModelFiles(t, e)
}

// initLlamaCPPQuiet downloads before the runner starts, and not at all
// without the flag.
func TestInitLlamaCPPAutoDownloadsBeforeStart(t *testing.T) {
	payload := []byte(strings.Repeat("m", 2048))
	srv := newModelServer(t, payload, nil)
	e, cfg := autoDownloadEnv(t, srv, payload, sum(payload), true)

	var presentAtStart bool
	orig := newLlamaRunner
	newLlamaRunner = func(string) llamaRunner {
		return &fakeRunnerStart{onStart: func() { _, err := os.Stat(e.Path()); presentAtStart = err == nil }}
	}
	defer func() { newLlamaRunner = orig; cleanupLlamaCPP() }()

	if err := initLlamaCPPQuiet(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if !presentAtStart || srv.hits.Load() != 1 {
		t.Errorf("model present at runner start = %v, requests = %d", presentAtStart, srv.hits.Load())
	}
}

type fakeRunnerStart struct{ onStart func() }

func (f *fakeRunnerStart) SetEmbedding(bool)           {}
func (f *fakeRunnerStart) Start(context.Context) error { f.onStart(); return nil }
func (f *fakeRunnerStart) Stop() error                 { return nil }
func (f *fakeRunnerStart) BaseURL() string             { return "http://127.0.0.1:1" }

// R1: the first-run config holds the Ollama-style default "bge-m3". With
// auto_download the runner must get the downloaded file's path, and the
// user's config must keep saying "bge-m3".
func TestFirstRunConfigWithAutoDownloadDownloadsThenStartsRunner(t *testing.T) {
	payload := []byte(strings.Repeat("bge", 1500))
	srv := newModelServer(t, payload, nil)
	e, cfg := autoDownloadEnvID(t, srv, payload, sum(payload), true, "bge-m3")
	if cfg.EmbeddingModel != "bge-m3" {
		t.Fatalf("test setup: model = %q", cfg.EmbeddingModel)
	}

	var gotModel string
	var presentAtStart bool
	orig := newLlamaRunner
	newLlamaRunner = func(model string) llamaRunner {
		gotModel = model
		return &fakeRunnerStart{onStart: func() { _, err := os.Stat(model); presentAtStart = err == nil }}
	}
	defer func() { newLlamaRunner = orig; cleanupLlamaCPP() }()

	if err := initLlamaCPPQuiet(context.Background(), cfg, true); err != nil {
		t.Fatalf("init with the first-run config: %v", err)
	}
	if gotModel != e.Path() || !presentAtStart {
		t.Errorf("runner model = %q (present at start: %v), want %q", gotModel, presentAtStart, e.Path())
	}
	if srv.hits.Load() != 1 {
		t.Errorf("requests = %d, want 1", srv.hits.Load())
	}
	if cfg.EmbeddingModel != "bge-m3" {
		t.Errorf("config model rewritten to %q", cfg.EmbeddingModel)
	}
	if cfg.EmbeddingProvider != "openai" {
		t.Errorf("provider = %q, want the embedded server's openai", cfg.EmbeddingProvider)
	}
	// Ollama-style tag, and a second start: no new download.
	cfg2 := *cfg
	cfg2.EmbeddingProvider, cfg2.EmbeddingModel = "llamacpp", "bge-m3:latest"
	cleanupLlamaCPP()
	if err := initLlamaCPPQuiet(context.Background(), &cfg2, true); err != nil {
		t.Fatal(err)
	}
	if gotModel != e.Path() || srv.hits.Load() != 1 {
		t.Errorf("tagged name: runner %q, requests %d", gotModel, srv.hits.Load())
	}
}

// Without the flag a catalogue id that is not on disk fails clearly, with no
// request and no runner; an unknown name names the alternatives.
func TestInitLlamaCPPCatalogueIDWithoutFlagOrUnknown(t *testing.T) {
	payload := []byte("x")
	srv := newModelServer(t, payload, nil)
	_, cfg := autoDownloadEnvID(t, srv, payload, sum(payload), false, "bge-m3")
	started := false
	orig := newLlamaRunner
	newLlamaRunner = func(string) llamaRunner { started = true; return &fakeRunnerStart{onStart: func() {}} }
	defer func() { newLlamaRunner = orig; cleanupLlamaCPP() }()

	err := initLlamaCPPQuiet(context.Background(), cfg, true)
	if err == nil || !strings.Contains(err.Error(), "gleann models download bge-m3") || !strings.Contains(err.Error(), "auto_download") {
		t.Fatalf("err = %v, want download/auto_download hints", err)
	}
	cfg.EmbeddingModel = "no-such-model"
	err = initLlamaCPPQuiet(context.Background(), cfg, true)
	if err == nil || !strings.Contains(err.Error(), "catalogue id") || !strings.Contains(err.Error(), "ollama") {
		t.Fatalf("err = %v, want the alternatives", err)
	}
	if srv.hits.Load() != 0 || started {
		t.Errorf("requests = %d, runner started = %v; want 0, false", srv.hits.Load(), started)
	}
}

// The first-run default model and the id the guidance tells users to download
// are real catalogue entries, so the guided path resolves.
func TestFirstRunDefaultModelIsInTheCatalogue(t *testing.T) {
	for _, name := range []string{gleann.DefaultConfig().EmbeddingModel, gleann.DefaultConfig().EmbeddingModel + ":latest"} {
		if e, ok := catalogEntryFor(name); !ok || e.Kind != modelcatalog.Embedding {
			t.Errorf("%q does not resolve to a catalogue embedding model", name)
		}
	}
}
