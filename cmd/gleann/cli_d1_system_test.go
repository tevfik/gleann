package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── gleann update ──────────────────────────────────────────────────────────

func d1SetVersion(t *testing.T, v string) {
	t.Helper()
	orig := version
	version = v
	t.Cleanup(func() { version = orig })
}

func d1StubRelease(t *testing.T, rel *gleann.Release, err error) *int {
	t.Helper()
	calls := 0
	orig := fetchLatestRelease
	t.Cleanup(func() { fetchLatestRelease = orig })
	fetchLatestRelease = func(_ context.Context, owner, repo string) (*gleann.Release, error) {
		calls++
		if owner != "tevfik" || repo != "gleann" {
			t.Errorf("release lookup for %s/%s", owner, repo)
		}
		return rel, err
	}
	return &calls
}

func d1TarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "dist/" + name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCLID1UpdateFlagsAndChecks(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("update", "--help")
	wantExit(t, r, 0)
	mustContain(t, "help", r.Out, "usage: gleann update [--check] [--force]")

	r = e.run("update", "--bogus")
	if r.Code == 0 {
		t.Fatal("update --bogus succeeded")
	}
	r = captureRun(t, "", func() { cmdUpdate([]string{"extra"}) })
	wantExit(t, r, 2)
	mustContain(t, "positional", r.Err, `unknown flag "extra"`)

	d1StubRelease(t, nil, errors.New("rate limit exceeded"))
	r = e.run("update", "--check")
	wantExit(t, r, 1)
	mustContain(t, "fetch error", r.Err, "cannot check for updates: rate limit exceeded")

	calls := d1StubRelease(t, &gleann.Release{Tag: "v1.9.0"}, nil)
	d1SetVersion(t, "dev")
	r = e.run("update")
	wantExit(t, r, 0)
	mustContain(t, "dev", r.Out, "installed dev, latest v1.9.0", "This is a development build; use --force")

	d1SetVersion(t, "v1.9.0")
	r = e.run("self-update")
	wantExit(t, r, 0)
	mustContain(t, "same", r.Out, "gleann is up to date.")

	d1SetVersion(t, "v1.8.0")
	r = e.run("upgrade", "--check")
	wantExit(t, r, 0)
	mustContain(t, "newer", r.Out, "A newer release is available: run `gleann update`.")
	if *calls != 3 {
		t.Errorf("release looked up %d times, want 3", *calls)
	}
}

func TestCLID1UpdateReplacesLauncher(t *testing.T) {
	pattern := runtime.GOOS + "-" + runtime.GOARCH
	switch pattern {
	case "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64":
	default:
		t.Skip("no published binaries for " + pattern)
	}
	e := newCLIEnv(t)
	launcher := filepath.Join(e.Home, "bin", "gleann")
	d1WriteFile(t, launcher, "old launcher")
	t.Setenv("GLEANN_LAUNCHER", launcher)

	ext := ".tar.gz"
	asset := "gleann-full-v2.0.0-" + pattern + ext
	archive := d1TarGz(t, "gleann-full", []byte("new launcher"))
	sum := sha256.Sum256(archive)
	badSums := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + asset:
			_, _ = w.Write(archive)
		case "/SHA256SUMS":
			s := hex.EncodeToString(sum[:])
			if badSums {
				s = strings.Repeat("0", 64)
			}
			fmt.Fprintf(w, "%s  %s\n", s, asset)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := &gleann.Release{Tag: "v2.0.0", Assets: []gleann.ReleaseAsset{
		{Name: "gleann-v2.0.0-" + pattern + ext, URL: srv.URL + "/wrong-variant"},
		{Name: asset, URL: srv.URL + "/" + asset},
		{Name: "SHA256SUMS", URL: srv.URL + "/SHA256SUMS"},
	}}
	d1StubRelease(t, rel, nil)
	d1SetVersion(t, "v1.0.0")

	r := e.run("update")
	wantExit(t, r, 0)
	mustContain(t, "update", r.Out, "Downloading "+asset, "Checksum verified.", "Updated "+launcher+" to v2.0.0.")
	if b, _ := os.ReadFile(launcher); string(b) != "new launcher" {
		t.Errorf("launcher holds %q after the update", b)
	}

	// Without a checksum file the download is still installed, with a note.
	rel.Assets = rel.Assets[:2]
	r = e.run("update", "--force")
	wantExit(t, r, 0)
	mustContain(t, "no sums", r.Out, "the release publishes no checksum file")

	// A wrong checksum aborts and leaves the launcher alone.
	d1WriteFile(t, launcher, "kept")
	rel.Assets = append(rel.Assets, gleann.ReleaseAsset{Name: "SHA256SUMS", URL: srv.URL + "/SHA256SUMS"})
	badSums = true
	r = e.run("update", "--force")
	wantExit(t, r, 1)
	mustContain(t, "bad sums", r.Err, "update failed:", "checksum mismatch")
	if b, _ := os.ReadFile(launcher); string(b) != "kept" {
		t.Errorf("a failed update changed the launcher to %q", b)
	}

	// No asset for this variant.
	rel.Assets = rel.Assets[:1]
	r = e.run("update", "--force")
	wantExit(t, r, 1)
	mustContain(t, "no asset", r.Err, "cannot update: release v2.0.0 has no gleann-full build")
}

func TestCLID1UpdateTarget(t *testing.T) {
	t.Setenv("GLEANN_LAUNCHER", "")
	dest, variant, err := updateTarget()
	if err != nil || variant != "gleann" || dest == "" {
		t.Fatalf("updateTarget() = %q %q %v", dest, variant, err)
	}
	t.Setenv("GLEANN_LAUNCHER", "/opt/gleann/gleann")
	dest, variant, err = updateTarget()
	if err != nil || variant != "gleann-full" || dest != "/opt/gleann/gleann" {
		t.Fatalf("updateTarget() with launcher = %q %q %v", dest, variant, err)
	}
	if orUnknown("") != "unknown version" || orUnknown("v1") != "v1" {
		t.Error("orUnknown")
	}
}

// ── gleann models ──────────────────────────────────────────────────────────

func TestCLID1ModelsListAndDownload(t *testing.T) {
	e := newCLIEnv(t)
	var plain []modelcatalog.Entry
	for _, m := range modelcatalog.All() {
		if m.Pooler == nil && m.Bytes > 0 {
			plain = append(plain, m)
		}
	}
	if len(plain) < 2 {
		t.Skip("catalog has fewer than two plain models")
	}
	have, want := plain[0], plain[1]
	// An installed model: a (sparse) file of the catalog size.
	if err := os.MkdirAll(filepath.Dir(have.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(have.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(have.Bytes); err != nil {
		t.Fatal(err)
	}
	f.Close()

	r := e.run("models")
	wantExit(t, r, 0)
	mustContain(t, "list", r.Out, "Models for the llamacpp and eif providers (stored in "+modelcatalog.Dir()+")",
		"✓ "+have.ID, want.ID, "Download one with: gleann models download <id>")

	var got []string
	orig := downloadModel
	t.Cleanup(func() { downloadModel = orig })
	downloadModel = func(_ context.Context, m modelcatalog.Entry, progress func(done, total int64)) error {
		got = append(got, m.ID)
		progress(m.Bytes/2, m.Bytes)
		if m.ID == "boom" {
			return errors.New("never")
		}
		return nil
	}
	r = e.run("models", "download", have.ID, want.ID)
	wantExit(t, r, 0)
	mustContain(t, "download", r.Out, "✓ "+have.Name+" is already in "+have.Path(), "⬇  "+want.Name, " 50%", "✓ "+want.Path())
	if strings.Join(got, ",") != want.ID {
		t.Errorf("downloaded %v, want only %s", got, want.ID)
	}

	downloadModel = func(context.Context, modelcatalog.Entry, func(int64, int64)) error {
		return errors.New("HTTP 503")
	}
	r = e.run("models", "download", want.ID, "no-such-model")
	wantExit(t, r, 1)
	mustContain(t, "failures", r.Err, "✗ HTTP 503", `unknown model "no-such-model"`)

	for _, args := range [][]string{{"models", "download"}, {"models", "remove", "x"}} {
		r = e.run(args...)
		wantExit(t, r, 2)
		mustContain(t, strings.Join(args, " "), r.Err, "usage: gleann models [list | download <id>...]")
	}
}

// ── gleann service ─────────────────────────────────────────────────────────

func TestCLID1Service(t *testing.T) {
	e := newCLIEnv(t)
	var calls []string
	oi, ou, os_ := serviceInstall, serviceUninstall, serviceStart
	t.Cleanup(func() { serviceInstall, serviceUninstall, serviceStart = oi, ou, os_ })
	fail := false
	serviceInstall = func(bin, addr string) error {
		calls = append(calls, "install "+bin+" "+addr)
		if fail {
			return errors.New("systemctl missing")
		}
		return nil
	}
	serviceUninstall = func() error {
		calls = append(calls, "uninstall")
		if fail {
			return errors.New("not installed")
		}
		return nil
	}
	pidFile := filepath.Join(e.Home, ".gleann", "server.pid")
	serviceStart = func(bin, addr string) error {
		calls = append(calls, "start "+bin+" "+addr)
		if fail {
			return errors.New("port busy")
		}
		d1WriteFile(t, pidFile, `{"pid":99999999,"addr":"`+addr+`","started":"now"}`)
		return nil
	}

	r := e.run("service")
	wantExit(t, r, 1)
	mustContain(t, "usage", r.Out, "Usage: gleann service <command>")
	r = e.run("service", "bogus")
	wantExit(t, r, 1)
	mustContain(t, "unknown", r.Err, "unknown service command: bogus")

	r = e.run("service", "install", "--port", "9000", "--bin", "/x/gleann")
	wantExit(t, r, 0)
	mustContain(t, "install", r.Out, "gleann service installed")
	r = e.run("service", "start", "--addr", "127.0.0.1:9001")
	wantExit(t, r, 0)
	mustContain(t, "start", r.Out, "gleann server started (PID 99999999, 127.0.0.1:9001)")
	r = e.run("service", "restart", "-p", ":9002")
	wantExit(t, r, 0)
	mustContain(t, "restart", r.Out, "gleann server restarted (PID 99999999, :9002)")
	r = e.run("service", "uninstall")
	wantExit(t, r, 0)
	mustContain(t, "uninstall", r.Out, "gleann service uninstalled")
	want := []string{"install /x/gleann :9000", "start  127.0.0.1:9001", "start  :9002", "uninstall"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("service calls %q, want %q", calls, want)
	}

	// The recorded PID is not running: status reports it, stop refuses.
	r = e.run("service", "status")
	wantExit(t, r, 0)
	if r.Out == "" {
		t.Error("service status printed nothing")
	}
	r = e.run("service", "stop")
	wantExit(t, r, 1)
	mustContain(t, "stop", r.Err, "not running")

	r = e.run("service", "logs")
	wantExit(t, r, 1)
	mustContain(t, "no logs", r.Err, "no log file found")
	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "server.log"), "one\ntwo\nthree\n")
	r = e.run("service", "logs", "--lines", "2")
	wantExit(t, r, 0)
	if strings.Contains(r.Out, "one") || !strings.Contains(r.Out, "three") {
		t.Errorf("logs --lines 2 = %q", r.Out)
	}

	fail = true
	for _, sub := range []string{"install", "uninstall", "start", "restart"} {
		r = e.run("service", sub)
		wantExit(t, r, 1)
		if !strings.HasPrefix(r.Err, "error: ") {
			t.Errorf("service %s failure: %q", sub, r.Err)
		}
	}
}

func TestCLID1ResolveServiceAddr(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--addr", "0.0.0.0:1"}, "0.0.0.0:1"},
		{[]string{"--port", "8"}, ":8"},
		{[]string{"-p", ":9"}, ":9"},
		{[]string{"--addr", "h:1", "--port", "2"}, "h:1"},
	} {
		if got := resolveServiceAddr(c.args); got != c.want {
			t.Errorf("resolveServiceAddr(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// ── gleann uninstall ───────────────────────────────────────────────────────

func TestCLID1Uninstall(t *testing.T) {
	e := newCLIEnv(t)
	var runs []bool
	orig := runUninstall
	t.Cleanup(func() { runUninstall = orig })
	runUninstall = func(all bool) { runs = append(runs, all) }

	r := e.run("uninstall", "--help")
	wantExit(t, r, 0)
	mustContain(t, "help", r.Out, "Usage: gleann uninstall [flags]")

	r = e.runIn("n\n", "uninstall")
	wantExit(t, r, 0)
	mustContain(t, "cancel", r.Out, "Configuration and indexes in ~/.gleann will be kept", "Uninstall cancelled.")

	r = e.runIn("YES\n", "uninstall", "--all")
	wantExit(t, r, 0)
	mustContain(t, "all prompt", r.Out, "WARNING: This will permanently remove")

	r = e.run("uninstall", "--yes")
	wantExit(t, r, 0)
	mustNotContain(t, "--yes", r.Out, "Proceed with uninstall?")

	if fmt.Sprint(runs) != "[true false]" {
		t.Errorf("uninstall runs %v, want [true false]", runs)
	}
	if r := e.run("uninstall", "--bogus"); r.Code == 0 {
		t.Error("uninstall --bogus succeeded")
	}
}

// ── embedded llama.cpp ─────────────────────────────────────────────────────

type d1Runner struct {
	embedding bool
	startErr  error
	stopped   bool
}

func (r *d1Runner) SetEmbedding(b bool)         { r.embedding = b }
func (r *d1Runner) Start(context.Context) error { return r.startErr }
func (r *d1Runner) Stop() error                 { r.stopped = true; return nil }
func (r *d1Runner) BaseURL() string             { return "http://127.0.0.1:5555" }

func TestCLID1InitLlamaCPP(t *testing.T) {
	origNew, origGlobal := newLlamaRunner, globalLlamaRunner
	t.Cleanup(func() { newLlamaRunner, globalLlamaRunner = origNew, origGlobal })
	globalLlamaRunner = nil

	// Other providers are left alone.
	cfg := gleann.Config{EmbeddingProvider: "ollama", EmbeddingModel: "m.gguf"}
	if err := initLlamaCPP(context.Background(), &cfg); err != nil || cfg.EmbeddingProvider != "ollama" || globalLlamaRunner != nil {
		t.Fatalf("ollama config changed: %+v %v", cfg, err)
	}
	chat := gleann.ChatConfig{Provider: gleann.LLMOllama}
	applyLlamaChatOverride(&chat)
	if chat.Provider != gleann.LLMOllama {
		t.Error("chat overridden without a runner")
	}

	for _, m := range []string{"", "bge-m3"} {
		cfg = gleann.Config{EmbeddingProvider: "llamacpp", EmbeddingModel: m}
		if err := initLlamaCPP(context.Background(), &cfg); err == nil || !strings.Contains(err.Error(), "requires a .gguf model") {
			t.Errorf("model %q: err = %v", m, err)
		}
	}

	// The real runner reports a missing model file before starting anything.
	cfg = gleann.Config{EmbeddingProvider: "llamacpp", EmbeddingModel: filepath.Join(t.TempDir(), "absent.gguf")}
	r := captureRun(t, "", func() {
		if err := initLlamaCPP(context.Background(), &cfg); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("missing model: err = %v", err)
		}
	})
	mustContain(t, "starting", r.Err, "Starting embedded llama.cpp server")

	fake := &d1Runner{}
	newLlamaRunner = func(model string) llamaRunner {
		if model != "emb.gguf" {
			t.Errorf("runner for %q", model)
		}
		return fake
	}
	fake.startErr = errors.New("exec format error")
	cfg = gleann.Config{EmbeddingProvider: "llamacpp", EmbeddingModel: "emb.gguf"}
	if err := initLlamaCPP(context.Background(), &cfg); err == nil || !strings.Contains(err.Error(), "failed to start embedded llama-server: exec format error") {
		t.Errorf("start failure: %v", err)
	}

	fake.startErr = nil
	cfg = gleann.Config{EmbeddingProvider: "llamacpp", EmbeddingModel: "emb.gguf", BatchSize: 8}
	r = captureRun(t, "", func() {
		if err := initLlamaCPP(context.Background(), &cfg); err != nil {
			t.Errorf("init: %v", err)
		}
	})
	mustContain(t, "ready", r.Err, "Embedded llama-server is ready at http://127.0.0.1:5555")
	if !fake.embedding || cfg.EmbeddingProvider != "openai" || cfg.OpenAIBaseURL != fake.BaseURL() ||
		cfg.OllamaHost != fake.BaseURL() || cfg.OpenAIAPIKey != "gleann-embedded" || cfg.BatchSize != 8 || cfg.Concurrency != 2 {
		t.Errorf("config after init: %+v", cfg)
	}

	chat = gleann.ChatConfig{Provider: gleann.LLMOllama}
	applyLlamaChatOverride(&chat)
	if chat.Provider != gleann.LLMOpenAI || chat.BaseURL != fake.BaseURL() || chat.APIKey != "gleann-embedded" {
		t.Errorf("chat override: %+v", chat)
	}
	r = captureRun(t, "", cleanupLlamaCPP)
	if !fake.stopped || !strings.Contains(r.Err, "Shutting down embedded llama-server") {
		t.Errorf("cleanup: stopped=%v %q", fake.stopped, r.Err)
	}
}
