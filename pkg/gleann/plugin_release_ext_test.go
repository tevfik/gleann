package gleann

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// ── Release asset selection ───────────────────────────────────────────────

func TestSelectReleaseAsset(t *testing.T) {
	tests := []struct {
		name         string
		assets       []string
		goos, goarch string
		want         string
		wantErr      bool
	}{
		{
			name:   "prefers onnx variant",
			assets: []string{"p-v1.0.0-linux-amd64.tar.gz", "p-onnx-linux-amd64.tar.gz"},
			goos:   "linux", goarch: "amd64",
			want: "p-onnx-linux-amd64.tar.gz",
		},
		{
			name:   "skips checksum listed first",
			assets: []string{"p-v1.0.0-linux-amd64.tar.gz.sha256", "checksums.txt", "p-v1.0.0-linux-amd64.tar.gz"},
			goos:   "linux", goarch: "amd64",
			want: "p-v1.0.0-linux-amd64.tar.gz",
		},
		{
			name:   "skips signatures in generic fallback",
			assets: []string{"p_linux-arm64.sig", "p_linux-arm64"},
			goos:   "linux", goarch: "arm64",
			want: "p_linux-arm64",
		},
		{
			name:   "tie keeps first listed",
			assets: []string{"a-v1-darwin-arm64.zip", "b-v1-darwin-arm64.zip"},
			goos:   "darwin", goarch: "arm64",
			want: "a-v1-darwin-arm64.zip",
		},
		{
			name:   "stub when nothing better",
			assets: []string{"p-stub-windows-amd64.zip", "p-linux-amd64"},
			goos:   "windows", goarch: "amd64",
			want: "p-stub-windows-amd64.zip",
		},
		{
			name:   "other platform only",
			assets: []string{"p-v1-linux-amd64.tar.gz"},
			goos:   "darwin", goarch: "amd64",
			wantErr: true,
		},
		{
			name:   "unsupported arch does not fall back to amd64",
			assets: []string{"p-v1-linux-amd64.tar.gz"},
			goos:   "linux", goarch: "386",
			wantErr: true,
		},
		{
			name:   "windows arm64 unsupported",
			assets: []string{"p-v1-windows-amd64.zip"},
			goos:   "windows", goarch: "arm64",
			wantErr: true,
		},
		{name: "no assets", goos: "linux", goarch: "amd64", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, err := selectReleaseAsset(tt.assets, tt.goos, tt.goarch)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", tt.assets[idx])
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.assets[idx] != tt.want {
				t.Fatalf("selected %q, want %q", tt.assets[idx], tt.want)
			}
		})
	}
}

// ── Download behaviour against a fake GitHub ──────────────────────────────

type fakeRelease struct {
	mu      sync.Mutex
	assets  map[string]http.HandlerFunc
	order   []string
	apiCode int
}

func newFakeGitHub(t *testing.T, fr *fakeRelease) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fr.mu.Lock()
		defer fr.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			if fr.apiCode != 0 && fr.apiCode != 200 {
				w.WriteHeader(fr.apiCode)
				_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
				return
			}
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			var as []asset
			for _, n := range fr.order {
				as = append(as, asset{Name: n, URL: srv.URL + "/dl/" + n})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.9.9", "assets": as})
			return
		}
		if h, ok := fr.assets[strings.TrimPrefix(r.URL.Path, "/dl/")]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	orig := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() {
		githubAPIBase = orig
		srv.Close()
	})
	return srv
}

func drain(ch chan string) func() []string {
	var msgs []string
	done := make(chan struct{})
	go func() {
		for m := range ch {
			msgs = append(msgs, m)
		}
		close(done)
	}()
	return func() []string {
		close(ch)
		<-done
		return msgs
	}
}

func serveBytes(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) }
}

// serveTruncated advertises full length but sends only part of the body,
// simulating a dropped connection mid-download.
func serveTruncated(full []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(full)))
		_, _ = w.Write(full[:len(full)/3])
	}
}

func platformOrSkip(t *testing.T) string {
	p := releasePlatformPattern(runtime.GOOS, runtime.GOARCH)
	if p == "" {
		t.Skipf("no release platform for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return p
}

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		n string
		b []byte
	}{{"README.md", []byte("readme")}, {name, content}} {
		if err := tw.WriteHeader(&tar.Header{Name: "dist/" + f.n, Mode: 0o755, Size: int64(len(f.b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(f.b)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestDownloadBinaryFromGitHub_TarGzAndChecksumSkipped(t *testing.T) {
	plat := platformOrSkip(t)
	bin := []byte("#!/bin/sh\necho plugin\n")
	archive := "gleann-plugin-x-v1-" + plat + ".tar.gz"
	fr := &fakeRelease{
		order: []string{archive + ".sha256", archive},
		assets: map[string]http.HandlerFunc{
			archive + ".sha256": serveBytes([]byte("deadbeef  " + archive)),
			archive:             serveBytes(tarGz(t, "gleann-plugin-x", bin)),
		},
	}
	newFakeGitHub(t, fr)
	dest := filepath.Join(t.TempDir(), "gleann-plugin-x")
	ch := make(chan string, 16)
	stop := drain(ch)
	err := downloadBinaryFromGitHub("o", "gleann-plugin-x", dest, ch)
	msgs := stop()
	if err != nil {
		t.Fatalf("download: %v (%v)", err, msgs)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, bin) {
		t.Fatalf("installed binary = %q, want %q", got, bin)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("temporary file left behind")
	}
}

func TestDownloadBinaryFromGitHub_PartialDownloadLeavesNoBinary(t *testing.T) {
	// Regression: a truncated transfer left a partial file at destPath, and
	// the next install reported "Binary already installed".
	plat := platformOrSkip(t)
	raw := "gleann-plugin-y-v1-" + plat
	fr := &fakeRelease{
		order:  []string{raw},
		assets: map[string]http.HandlerFunc{raw: serveTruncated(bytes.Repeat([]byte("B"), 64<<10))},
	}
	newFakeGitHub(t, fr)
	dest := filepath.Join(t.TempDir(), "gleann-plugin-y")
	ch := make(chan string, 16)
	stop := drain(ch)
	err := downloadBinaryFromGitHub("o", "gleann-plugin-y", dest, ch)
	stop()
	if err == nil {
		t.Fatal("expected error for truncated download")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("partial binary must not be installed at %s", dest)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("temporary file left behind")
	}
}

func TestDownloadBinaryFromGitHub_APIErrors(t *testing.T) {
	platformOrSkip(t)
	t.Run("non-200", func(t *testing.T) {
		newFakeGitHub(t, &fakeRelease{apiCode: 403})
		ch := make(chan string, 16)
		stop := drain(ch)
		err := downloadBinaryFromGitHub("o", "r", filepath.Join(t.TempDir(), "r"), ch)
		stop()
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("expected 403 error, got %v", err)
		}
	})
	t.Run("no assets", func(t *testing.T) {
		newFakeGitHub(t, &fakeRelease{})
		ch := make(chan string, 16)
		stop := drain(ch)
		err := downloadBinaryFromGitHub("o", "r", filepath.Join(t.TempDir(), "r"), ch)
		stop()
		if err == nil {
			t.Fatal("expected error for release without assets")
		}
	})
	t.Run("asset 404", func(t *testing.T) {
		plat := releasePlatformPattern(runtime.GOOS, runtime.GOARCH)
		newFakeGitHub(t, &fakeRelease{order: []string{"r-v1-" + plat}})
		dest := filepath.Join(t.TempDir(), "r")
		ch := make(chan string, 16)
		stop := drain(ch)
		err := downloadBinaryFromGitHub("o", "r", dest, ch)
		stop()
		if err == nil {
			t.Fatal("expected error for 404 asset")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("no file should be created on 404")
		}
	})
}

// ── Docling model download ────────────────────────────────────────────────

func TestEnsureDoclingModel_DownloadPartialAndRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	model := bytes.Repeat([]byte("M"), 32<<10)
	modelName := "granite_docling_bitnet_dense.eifm"

	fr := &fakeRelease{
		order: []string{modelName, "tokenizer.bin", "unrelated.eifm"},
		assets: map[string]http.HandlerFunc{
			modelName:       serveTruncated(model),
			"tokenizer.bin": serveBytes([]byte("tok")),
		},
	}
	newFakeGitHub(t, fr)
	modelPath := filepath.Join(home, ".gleann", "models", "granite-docling", modelName)

	// 1st attempt: model transfer is truncated → no model file may remain.
	ch := make(chan string, 32)
	stop := drain(ch)
	ensureDoclingModel("o", "gleann-plugin-docs", ch)
	msgs := stop()
	if _, err := os.Stat(modelPath); !os.IsNotExist(err) {
		t.Fatalf("truncated model must not be kept (msgs=%v)", msgs)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "Model download failed") {
		t.Fatalf("expected failure message, got %v", msgs)
	}
	if b, _ := os.ReadFile(filepath.Join(filepath.Dir(modelPath), "tokenizer.bin")); string(b) != "tok" {
		t.Fatalf("tokenizer should be downloaded, got %q", b)
	}

	// 2nd attempt: server healthy → model is downloaded rather than skipped.
	fr.mu.Lock()
	fr.assets[modelName] = serveBytes(model)
	fr.mu.Unlock()
	ch = make(chan string, 32)
	stop = drain(ch)
	ensureDoclingModel("o", "gleann-plugin-docs", ch)
	stop()
	got, err := os.ReadFile(modelPath)
	if err != nil || !bytes.Equal(got, model) {
		t.Fatalf("model not downloaded on retry: err=%v len=%d", err, len(got))
	}

	// 3rd call: already present → no network needed.
	fr.mu.Lock()
	fr.apiCode = 500
	fr.mu.Unlock()
	ch = make(chan string, 32)
	stop = drain(ch)
	ensureDoclingModel("o", "gleann-plugin-docs", ch)
	msgs = stop()
	if !strings.Contains(strings.Join(msgs, "\n"), "already present") {
		t.Fatalf("expected already-present short-circuit, got %v", msgs)
	}
}

func TestEnsureDoclingModel_VersionedAssetAndAPIError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	fr := &fakeRelease{apiCode: 403}
	newFakeGitHub(t, fr)
	ch := make(chan string, 32)
	stop := drain(ch)
	ensureDoclingModel("o", "r", ch)
	msgs := stop()
	if !strings.Contains(strings.Join(msgs, "\n"), "status 403") {
		t.Fatalf("API error should be reported, got %v", msgs)
	}

	versioned := "granite_docling_bitnet_dense_v2.eifm"
	fr.mu.Lock()
	fr.apiCode = 0
	fr.order = []string{versioned}
	fr.assets = map[string]http.HandlerFunc{versioned: serveBytes([]byte("v2"))}
	fr.mu.Unlock()
	ch = make(chan string, 32)
	stop = drain(ch)
	ensureDoclingModel("o", "r", ch)
	stop()
	dir := filepath.Join(home, ".gleann", "models", "granite-docling")
	if runtime.GOOS != "windows" {
		b, err := os.ReadFile(filepath.Join(dir, "granite_docling_bitnet_dense.eifm"))
		if err != nil || string(b) != "v2" {
			t.Fatalf("canonical symlink should resolve to versioned model: %q %v", b, err)
		}
	}
}

// ── Plugin registry: null safety and priority ─────────────────────────────

func TestLoadPlugins_NullSafety(t *testing.T) {
	tests := []struct {
		name string
		json string
		n    int
	}{
		{"null plugins", `{"plugins": null}`, 0},
		{"missing plugins", `{}`, 0},
		{"null fields in entry", `{"plugins":[{"name":"p","url":null,"command":null,"capabilities":null,"extensions":null}]}`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), []byte(tt.json), 0o644); err != nil {
				t.Fatal(err)
			}
			reg, err := LoadPlugins()
			if err != nil {
				t.Fatal(err)
			}
			if reg.Plugins == nil || len(reg.Plugins) != tt.n {
				t.Fatalf("Plugins = %#v, want non-nil len %d", reg.Plugins, tt.n)
			}
			b, _ := json.Marshal(reg)
			if strings.Contains(string(b), `"plugins":null`) {
				t.Fatalf("registry would be re-serialised with null plugins: %s", b)
			}
			m := &PluginManager{Registry: reg}
			if p := m.FindDocumentExtractor(".pdf"); p != nil {
				t.Fatalf("plugin without capabilities must not match: %+v", p)
			}
		})
	}
	t.Run("corrupt file errors", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		_ = os.MkdirAll(filepath.Join(home, ".gleann"), 0o755)
		_ = os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), []byte(`{"plugins":[`), 0o644)
		if _, err := LoadPlugins(); err == nil {
			t.Fatal("expected parse error")
		}
	})
}

func TestFindByCapability_Priority(t *testing.T) {
	docCap := []string{CapDocumentExtraction}
	mk := func(name string, prio int, exts ...string) Plugin {
		return Plugin{Name: name, Capabilities: docCap, Extensions: exts, Priority: prio}
	}
	tests := []struct {
		name    string
		plugins []Plugin
		ext     string
		want    string
	}{
		{"higher priority wins regardless of order", []Plugin{mk("a", 0, ".pdf"), mk("b", 10, ".pdf")}, ".pdf", "b"},
		{"tie keeps first registered", []Plugin{mk("a", 5, ".pdf"), mk("b", 5, ".pdf")}, ".pdf", "a"},
		{"zero priority legacy first match", []Plugin{mk("a", 0, ".pdf"), mk("b", 0, ".pdf")}, ".pdf", "a"},
		{"negative priority still selectable", []Plugin{mk("a", -5, ".pdf")}, ".pdf", "a"},
		{"negative loses to default", []Plugin{mk("a", -5, ".pdf"), mk("b", 0, ".pdf")}, ".pdf", "b"},
		{"extension case-insensitive", []Plugin{mk("a", 0, ".PDF")}, ".Pdf", "a"},
		{"non-matching ext skipped even with high priority", []Plugin{mk("a", 100, ".docx"), mk("b", 0, ".pdf")}, ".pdf", "b"},
		{"empty extensions act as wildcard", []Plugin{mk("wild", 0), mk("pdf", 0, ".pdf")}, ".go", "wild"},
		{"schema too new skipped", []Plugin{{Name: "new", Capabilities: docCap, Extensions: []string{".pdf"}, SchemaVersion: MaxSupportedSchemaVersion + 1, Priority: 99}, mk("old", 0, ".pdf")}, ".pdf", "old"},
		{"wrong capability skipped", []Plugin{{Name: "audio", Capabilities: []string{CapAudioTranscription}, Extensions: []string{".pdf"}, Priority: 99}}, ".pdf", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &PluginManager{Registry: &PluginRegistry{Plugins: tt.plugins}}
			p := m.FindByCapability(CapDocumentExtraction, tt.ext)
			got := ""
			if p != nil {
				got = p.Name
			}
			if got != tt.want {
				t.Fatalf("selected %q, want %q", got, tt.want)
			}
		})
	}
	// Returned pointer aliases the registry entry (callers may rely on it).
	m := &PluginManager{Registry: &PluginRegistry{Plugins: []Plugin{mk("a", 0, ".pdf")}}}
	if p := m.FindByCapability(CapDocumentExtraction, ".pdf"); p != &m.Registry.Plugins[0] {
		t.Fatal("expected pointer into registry slice")
	}
}

func TestFindDocumentExtractor_NilSafetyAndMedia(t *testing.T) {
	var nilMgr *PluginManager
	if nilMgr.FindByCapability(CapDocumentExtraction, ".pdf") != nil {
		t.Fatal("nil manager must return nil")
	}
	if (&PluginManager{}).FindDocumentExtractor(".pdf") != nil {
		t.Fatal("nil registry must return nil")
	}
	m := &PluginManager{Registry: &PluginRegistry{Plugins: []Plugin{
		{Name: "docs", Capabilities: []string{CapDocumentExtraction}, Extensions: []string{".pdf", ".png", ".JPG"}},
	}}}
	for _, ext := range []string{".mp3", ".wav"} {
		if p := m.FindDocumentExtractor(ext); p != nil {
			t.Errorf("audio media ext %s must not match document extraction", ext)
		}
	}
	if p := m.FindDocumentExtractor(".PDF"); p == nil || p.Name != "docs" {
		t.Fatal("expected docs plugin for .PDF")
	}
	if p := m.FindDocumentExtractor(".png"); p == nil || p.Name != "docs" {
		t.Fatal("expected docs plugin for .png")
	}
	if p := m.FindDocumentExtractor(".jpg"); p == nil || p.Name != "docs" {
		t.Fatal("expected docs plugin for .jpg")
	}
}

// ── Catalog robustness ────────────────────────────────────────────────────

func TestFetchPluginCatalog_FallbackIsACopyAndNullEntriesDropped(t *testing.T) {
	orig := RegistryURL
	t.Cleanup(func() {
		RegistryURL = orig
		InvalidatePluginCatalog()
	})

	InvalidatePluginCatalog()
	RegistryURL = "http://127.0.0.1:1"
	c := FetchPluginCatalog()
	if len(c) == 0 {
		t.Fatal("expected default catalog")
	}
	name := c[0].Name
	c[0].Name = "mutated"
	if again := FetchPluginCatalog(); again[0].Name != name {
		// Regression: the fallback returned the shared defaultCatalog slice.
		t.Fatalf("mutating the returned catalog changed the built-in default: %q", again[0].Name)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[null, {"name": ""}, {"name": "real", "extensions": null, "settings_cmd": null}]`))
	}))
	defer srv.Close()
	InvalidatePluginCatalog()
	RegistryURL = srv.URL
	c = FetchPluginCatalog()
	if len(c) != 1 || c[0].Name != "real" {
		t.Fatalf("expected only the valid entry, got %+v", c)
	}

	srvAllNull := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[null]`))
	}))
	defer srvAllNull.Close()
	InvalidatePluginCatalog()
	RegistryURL = srvAllNull.URL
	if c := FetchPluginCatalog(); len(c) == 0 || c[0].Name != name {
		t.Fatalf("all-null remote catalog should fall back to default, got %+v", c)
	}
}

func TestRepoPluginsRegistryJSONIsValid(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "plugins-registry.json"))
	if err != nil {
		t.Skipf("registry file not found: %v", err)
	}
	var metas []*PluginMeta
	if err := json.Unmarshal(data, &metas); err != nil {
		t.Fatalf("plugins-registry.json is not valid: %v", err)
	}
	seen := map[string]bool{}
	for i, m := range metas {
		if m == nil || m.Name == "" || m.RepoURL == "" {
			t.Fatalf("entry %d missing name/repo_url: %+v", i, m)
		}
		if seen[m.Name] {
			t.Fatalf("duplicate plugin %q", m.Name)
		}
		seen[m.Name] = true
		if owner, repo := parseGitHubURL(m.RepoURL); owner == "" || repo == "" {
			t.Errorf("%s: unparsable repo_url %q", m.Name, m.RepoURL)
		}
		for _, e := range m.Extensions {
			if !strings.HasPrefix(e, ".") || strings.ToLower(e) != e {
				t.Errorf("%s: extension %q should be lowercase with a leading dot", m.Name, e)
			}
		}
	}
}
