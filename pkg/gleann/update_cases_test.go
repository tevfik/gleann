package gleann

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// releaseServer serves GET /repos/*/releases/latest with the given tag and
// assets (name -> body). Names mapped to a nil body are listed but 404 on
// download. Extra files can be added to files without being listed.
func releaseServer(t *testing.T, tag string, assets map[string][]byte) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			list := []asset{}
			for n := range assets {
				list = append(list, asset{n, srv.URL + "/dl/" + n})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "assets": list})
			return
		}
		if body, ok := assets[strings.TrimPrefix(r.URL.Path, "/dl/")]; ok && body != nil {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	old := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() {
		githubAPIBase = old
		srv.Close()
	})
	return srv
}

func TestFetchLatestReleaseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/notag/") {
			fmt.Fprint(w, `{"assets":[]}`)
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	old := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = old }()

	if _, err := FetchLatestRelease(context.Background(), "o", "r"); err == nil || !strings.Contains(err.Error(), "latest release of o/r") {
		t.Errorf("HTTP failure: err = %v", err)
	}
	if _, err := FetchLatestRelease(context.Background(), "notag", "r"); err == nil || !strings.Contains(err.Error(), "has no tag") {
		t.Errorf("tagless release: err = %v", err)
	}
}

func TestSelectGleannAssetUnsupportedPlatform(t *testing.T) {
	if _, err := SelectGleannAsset([]string{"gleann-v1-linux-386.tar.gz"}, "gleann", "v1", "linux", "386"); err == nil || !strings.Contains(err.Error(), "no published binaries") {
		t.Errorf("err = %v", err)
	}
}

func TestReplaceBinaryFailures(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "gleann")
	newBin := filepath.Join(dir, "new")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The ".old" slot is a non-empty directory, so the old binary cannot be
	// moved aside: the running binary must be left untouched.
	if err := os.MkdirAll(filepath.Join(dest+".old", "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceBinary(dest, newBin); err == nil || !strings.Contains(err.Error(), "move old binary aside") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "old" {
		t.Errorf("the installed binary changed: %q", b)
	}
}

func TestReplaceBinaryFirstInstallAndRestore(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "gleann")
	newBin := filepath.Join(dir, "new")
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceBinary(dest, newBin); err != nil { // nothing to move aside
		t.Fatalf("first install: %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "new" {
		t.Errorf("dest = %q", b)
	}
	// The replacement is missing: the previous binary is put back.
	err := ReplaceBinary(dest, filepath.Join(dir, "gone"))
	if err == nil || !strings.Contains(err.Error(), "install new binary") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "new" {
		t.Errorf("old binary was not restored: %q", b)
	}
}

func TestChecksumFor(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/single":
			fmt.Fprint(w, "\n  ABCDEF  tool.tar.gz\n")
		case "/sums":
			fmt.Fprint(w, "\n111  other.tar.gz\n222 *tool.tar.gz\n")
		case "/sums-without":
			fmt.Fprint(w, "111  other.tar.gz\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got, err := checksumFor(ctx, &Release{Assets: []ReleaseAsset{{Name: "tool.tar.gz.sha256", URL: srv.URL + "/single"}}}, "tool.tar.gz")
	if err != nil || got != "abcdef" {
		t.Errorf("single .sha256 = %q, %v", got, err)
	}
	got, err = checksumFor(ctx, &Release{Assets: []ReleaseAsset{{Name: "SHA256SUMS", URL: srv.URL + "/sums"}}}, "tool.tar.gz")
	if err != nil || got != "222" {
		t.Errorf("SHA256SUMS = %q, %v", got, err)
	}
	got, err = checksumFor(ctx, &Release{Assets: []ReleaseAsset{{Name: "checksums.txt", URL: srv.URL + "/sums-without"}}}, "tool.tar.gz")
	if err != nil || got != "" {
		t.Errorf("asset absent from the sums = %q, %v", got, err)
	}
	if _, err := checksumFor(ctx, &Release{Assets: []ReleaseAsset{{Name: "SHA256SUMS", URL: srv.URL + "/404"}}}, "tool.tar.gz"); err == nil {
		t.Error("an unreachable checksum file must be an error, not a silent skip")
	}
}

func TestDownloadReleaseBinaryFailures(t *testing.T) {
	pattern := platformOrSkip(t)
	ctx := context.Background()
	archive := fxTarGz(t, false, tarEntry{name: "tool", body: "BIN"})
	sum := sha256.Sum256(archive)

	t.Run("download fails", func(t *testing.T) {
		srv := releaseServer(t, "v1", map[string][]byte{"a.tar.gz": nil})
		rel := &Release{Tag: "v1", Assets: []ReleaseAsset{{Name: "a.tar.gz", URL: srv.URL + "/dl/a.tar.gz"}}}
		_, _, err := DownloadReleaseBinary(ctx, rel, 0, "tool", filepath.Join(t.TempDir(), "tool"))
		if err == nil || !strings.Contains(err.Error(), "download a.tar.gz") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("checksum file unreachable", func(t *testing.T) {
		srv := releaseServer(t, "v1", map[string][]byte{"a.tar.gz": archive, "SHA256SUMS": nil})
		rel := &Release{Tag: "v1", Assets: []ReleaseAsset{
			{Name: "a.tar.gz", URL: srv.URL + "/dl/a.tar.gz"},
			{Name: "SHA256SUMS", URL: srv.URL + "/dl/SHA256SUMS"},
		}}
		_, _, err := DownloadReleaseBinary(ctx, rel, 0, "tool", filepath.Join(t.TempDir(), "tool"))
		if err == nil || !strings.Contains(err.Error(), "checksum file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("zip archive is extracted and verified", func(t *testing.T) {
		zipBody := makeZip(t, map[string]string{"tool": "ZIPBIN"})
		zsum := sha256.Sum256(zipBody)
		srv := releaseServer(t, "v1", map[string][]byte{
			"a-" + pattern + ".zip": zipBody,
			"a.zip.sha256":          []byte(hex.EncodeToString(zsum[:]) + "\n"),
		})
		rel := &Release{Tag: "v1", Assets: []ReleaseAsset{
			{Name: "a-" + pattern + ".zip", URL: srv.URL + "/dl/a-" + pattern + ".zip"},
			{Name: "a-" + pattern + ".zip.sha256", URL: srv.URL + "/dl/a.zip.sha256"},
		}}
		dest := filepath.Join(t.TempDir(), "tool")
		path, verified, err := DownloadReleaseBinary(ctx, rel, 0, "tool", dest)
		if err != nil || !verified {
			t.Fatalf("path=%q verified=%v err=%v", path, verified, err)
		}
		if b, _ := os.ReadFile(path); string(b) != "ZIPBIN" {
			t.Errorf("extracted = %q", b)
		}
	})
	t.Run("archive without the binary", func(t *testing.T) {
		other := fxTarGz(t, false, tarEntry{name: "README", body: "x"})
		srv := releaseServer(t, "v1", map[string][]byte{"a.tar.gz": other})
		rel := &Release{Tag: "v1", Assets: []ReleaseAsset{{Name: "a.tar.gz", URL: srv.URL + "/dl/a.tar.gz"}}}
		dest := filepath.Join(t.TempDir(), "tool")
		if _, _, err := DownloadReleaseBinary(ctx, rel, 0, "tool", dest); err == nil || !strings.Contains(err.Error(), "extract a.tar.gz") {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dest), ".tool.new")); !os.IsNotExist(err) {
			t.Errorf("partial extraction left behind: %v", err)
		}
	})
	t.Run("checksum mismatch", func(t *testing.T) {
		bad := strings.Repeat("0", len(hex.EncodeToString(sum[:])))
		srv := releaseServer(t, "v1", map[string][]byte{"a.tar.gz": archive, "SHA256SUMS": []byte(bad + "  a.tar.gz\n")})
		rel := &Release{Tag: "v1", Assets: []ReleaseAsset{
			{Name: "a.tar.gz", URL: srv.URL + "/dl/a.tar.gz"},
			{Name: "SHA256SUMS", URL: srv.URL + "/dl/SHA256SUMS"},
		}}
		if _, _, err := DownloadReleaseBinary(ctx, rel, 0, "tool", filepath.Join(t.TempDir(), "tool")); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("err = %v", err)
		}
	})
}

// ── plugin updates ────────────────────────────────────────────────────────

func TestPluginBinaryLookup(t *testing.T) {
	t.Run("no home", func(t *testing.T) {
		noHome(t)
		if _, err := pluginBinary("p"); err == nil {
			t.Fatal("expected an error without a home directory")
		}
	})
	t.Run("registered command with ~ expansion", func(t *testing.T) {
		home := fxHome(t)
		bin := filepath.Join(home, "tools", "myplug")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		reg := `{"plugins":[{"name":"myplug","command":["~/tools/myplug"]}]}`
		fileAt(t, filepath.Join(home, ".gleann", "plugins.json"))
		if err := os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), []byte(reg), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := pluginBinary("myplug")
		if err != nil || got != bin {
			t.Fatalf("pluginBinary = %q, %v", got, err)
		}
		if _, err := pluginBinary("other"); err == nil || !strings.Contains(err.Error(), "not installed as a binary") {
			t.Errorf("unregistered plugin: err = %v", err)
		}
	})
	if got := expandHome("~", "/h"); got != filepath.Join("/h") {
		t.Errorf("expandHome(~) = %q", got)
	}
}

func TestCheckAndUpdatePluginErrors(t *testing.T) {
	requireShell(t)
	pattern := platformOrSkip(t)
	ctx := context.Background()
	meta := PluginMeta{Name: "covcplug", RepoURL: "https://github.com/acme/covcplug"}

	t.Run("not installed", func(t *testing.T) {
		fxHome(t)
		if _, _, err := CheckPluginUpdate(ctx, meta); err == nil {
			t.Fatal("expected an error")
		}
		ch, stop := drainProgress()
		defer stop()
		if _, err := UpdatePlugin(ctx, meta, false, ch); err == nil {
			t.Fatal("UpdatePlugin must surface the check error")
		}
	})
	t.Run("unparsable repository URL", func(t *testing.T) {
		fxHome(t)
		fakePluginHome(t, meta.Name, "1.0.0")
		_, _, err := CheckPluginUpdate(ctx, PluginMeta{Name: meta.Name, RepoURL: "https://example.invalid/x"})
		if err == nil || !strings.Contains(err.Error(), "cannot parse repository URL") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("release lookup fails", func(t *testing.T) {
		fakePluginHome(t, meta.Name, "1.0.0")
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		old := githubAPIBase
		githubAPIBase = srv.URL
		defer func() { githubAPIBase = old }()
		if _, _, err := CheckPluginUpdate(ctx, meta); err == nil {
			t.Fatal("expected a release lookup error")
		}
	})
	t.Run("binary without --version counts as outdated", func(t *testing.T) {
		bin := fakePluginHome(t, meta.Name, "1.0.0")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		releaseServer(t, "v2.0.0", map[string][]byte{})
		res, rel, err := CheckPluginUpdate(ctx, meta)
		if err != nil || !res.Newer || res.Installed != "" || rel == nil || res.Latest != "v2.0.0" {
			t.Fatalf("res = %+v, rel = %v, err = %v", res, rel, err)
		}
	})
	t.Run("already up to date", func(t *testing.T) {
		fakePluginHome(t, meta.Name, "2.0.0")
		releaseServer(t, "v2.0.0", map[string][]byte{})
		ch := make(chan string, 8)
		res, err := UpdatePlugin(ctx, meta, false, ch)
		if err != nil || res.Newer {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		if msg := <-ch; !strings.Contains(msg, "up to date") {
			t.Errorf("progress = %q", msg)
		}
	})
	t.Run("release has no build for this platform", func(t *testing.T) {
		fakePluginHome(t, meta.Name, "1.0.0")
		releaseServer(t, "v2.0.0", map[string][]byte{"covcplug-v2.0.0-plan9-arm.tar.gz": []byte("x")})
		ch, stop := drainProgress()
		defer stop()
		if _, err := UpdatePlugin(ctx, meta, false, ch); err == nil || !strings.Contains(err.Error(), "no binary found") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("download fails", func(t *testing.T) {
		fakePluginHome(t, meta.Name, "1.0.0")
		releaseServer(t, "v2.0.0", map[string][]byte{"covcplug-v2.0.0-" + pattern + ".tar.gz": nil})
		ch, stop := drainProgress()
		defer stop()
		if _, err := UpdatePlugin(ctx, meta, false, ch); err == nil || !strings.Contains(err.Error(), "download") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("old binary cannot be moved aside", func(t *testing.T) {
		bin := fakePluginHome(t, meta.Name, "1.0.0")
		if err := os.MkdirAll(filepath.Join(bin+".old", "keep"), 0o755); err != nil {
			t.Fatal(err)
		}
		archive := tgzWith(t, "covcplug", pluginScript("2.0.0"))
		releaseServer(t, "v2.0.0", map[string][]byte{"covcplug-v2.0.0-" + pattern + ".tar.gz": archive})
		ch, stop := drainProgress()
		defer stop()
		if _, err := UpdatePlugin(ctx, meta, false, ch); err == nil || !strings.Contains(err.Error(), "move old binary aside") {
			t.Fatalf("err = %v", err)
		}
		leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(bin), ".covcplug.new"))
		if len(leftovers) != 0 {
			t.Errorf("the downloaded binary must be cleaned up: %v", leftovers)
		}
	})
	t.Run("forced update of the docs plugin refreshes OCR", func(t *testing.T) {
		docs := PluginMeta{Name: "covc-docs", RepoURL: "https://github.com/acme/covc-docs"}
		bin := fakePluginHome(t, docs.Name, "2.0.0")
		log := filepath.Join(t.TempDir(), "log")
		script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\ncase \"$1\" in --version) echo 'v2.0.0';; esac\nexit 0\n"
		archive := tgzWith(t, "covc-docs", script)
		releaseServer(t, "v2.0.0", map[string][]byte{"covc-docs-v2.0.0-" + pattern + ".tar.gz": archive})
		ch, stop := drainProgress()
		defer stop()
		res, err := UpdatePlugin(ctx, docs, true, ch)
		if err != nil || res.Newer {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		if got := readLog(t, log); !strings.Contains(got, "models download") || !strings.Contains(got, "--install") {
			t.Errorf("the new binary was not registered / asked for OCR models:\n%s", got)
		}
		if b, _ := os.ReadFile(bin); !strings.Contains(string(b), "v2.0.0") {
			t.Errorf("binary was not replaced: %q", b)
		}
	})
}

// ── plugin catalog ────────────────────────────────────────────────────────

func TestFetchPluginCatalogStaleCacheSurvivesBadFetches(t *testing.T) {
	origURL, origTTL := RegistryURL, catalogTTL
	defer func() {
		RegistryURL, catalogTTL = origURL, origTTL
		InvalidatePluginCatalog()
	}()

	var body string
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	RegistryURL = srv.URL

	InvalidatePluginCatalog()
	catalogTTL = time.Nanosecond
	status, body = 200, `[{"name":"cached-one","description":"d"}]`
	if got := FetchPluginCatalog(); len(got) != 1 || got[0].Name != "cached-one" {
		t.Fatalf("initial fetch = %+v", got)
	}
	time.Sleep(2 * time.Millisecond) // the cache is now stale

	for _, tc := range []struct {
		name   string
		status int
		body   string
		url    string
	}{
		{"server error", 500, "oops", ""},
		{"invalid JSON", 200, "{not json", ""},
		{"empty catalog", 200, "[]", ""},
		{"unreachable", 200, "", "http://127.0.0.1:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body = tc.status, tc.body
			RegistryURL = srv.URL
			if tc.url != "" {
				RegistryURL = tc.url
			}
			got := FetchPluginCatalog()
			if len(got) != 1 || got[0].Name != "cached-one" {
				t.Errorf("a failing refresh must keep serving the stale cache, got %+v", got)
			}
		})
	}
}

// ── plugin downloads ──────────────────────────────────────────────────────

func TestHTTPGetAndFetchErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := httpGet(ctx, "http://host\x7f/x"); err == nil {
		t.Error("an invalid URL must fail before any request")
	}
	if _, err := httpGet(ctx, unusedURL(t)); err == nil {
		t.Error("connection refused must be an error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/short":
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("only a little"))
		default:
			fmt.Fprint(w, "{broken")
		}
	}))
	defer srv.Close()

	var v map[string]any
	if err := fetchJSON(ctx, srv.URL+"/json", &v); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("fetchJSON on a broken body: %v", err)
	}
	if err := fetchJSON(ctx, srv.URL+"/x\x7f", &v); err == nil {
		t.Error("fetchJSON must propagate request errors")
	}

	dir := t.TempDir()
	dest := filepath.Join(dir, "out")
	if err := fetchToFile(ctx, srv.URL+"/short", dest); err == nil {
		t.Error("a body shorter than Content-Length must not be installed")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("truncated download left at dest: %v", err)
	}
	// The destination directory does not exist: the temp file cannot be made.
	if err := fetchToFile(ctx, srv.URL+"/json", filepath.Join(dir, "no", "such", "out")); err == nil {
		t.Error("fetchToFile into a missing directory must fail")
	}
	if err := fetchToFile(ctx, "http://host\x7f/x", dest); err == nil {
		t.Error("fetchToFile must propagate request errors")
	}
	// fetchToTemp: a failing download leaves no temp file.
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	t.Setenv("TMP", tmpDir)
	t.Setenv("TEMP", tmpDir)
	if _, err := fetchToTemp(ctx, srv.URL+"/short"); err == nil {
		t.Error("fetchToTemp of a truncated body must fail")
	}
	if entries, _ := os.ReadDir(tmpDir); len(entries) != 0 && runtime.GOOS != "windows" {
		t.Errorf("temporary files left behind: %v", entries)
	}
	t.Setenv("TMPDIR", filepath.Join(tmpDir, "missing"))
	t.Setenv("TMP", filepath.Join(tmpDir, "missing"))
	t.Setenv("TEMP", filepath.Join(tmpDir, "missing"))
	if _, err := fetchToTemp(ctx, srv.URL+"/json"); err == nil {
		t.Error("fetchToTemp without a usable temp dir must fail")
	}
}
