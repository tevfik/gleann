package gleann

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cur, latest       string
		newer, comparable bool
	}{
		{"v1.8.6", "v1.8.7", true, true},
		{"v1.8.7", "v1.8.7", false, true},
		{"v1.9.0", "v1.8.7", false, true},
		{"v1.8.6-arm64-630cb0bd", "v1.10.0", true, true}, // numeric, not lexical
		{"gleann v1.2.3", "v1.2.3", false, true},
		{"dev", "v1.8.7", false, false},
		{"", "v1.8.7", false, false},
	}
	for _, c := range cases {
		n, ok := IsNewer(c.cur, c.latest)
		if n != c.newer || ok != c.comparable {
			t.Errorf("IsNewer(%q,%q) = %v,%v want %v,%v", c.cur, c.latest, n, ok, c.newer, c.comparable)
		}
	}
}

func TestSelectGleannAssetIsExact(t *testing.T) {
	names := []string{
		"gleann-eif-v1.9.0-linux-amd64.tar.gz",
		"gleann-full-v1.9.0-linux-amd64.tar.gz",
		"gleann-v1.9.0-linux-amd64.tar.gz",
		"gleann-v1.9.0-windows-amd64.zip",
	}
	i, err := SelectGleannAsset(names, "gleann", "v1.9.0", "linux", "amd64")
	if err != nil || names[i] != "gleann-v1.9.0-linux-amd64.tar.gz" {
		t.Fatalf("lite: %v %v", i, err)
	}
	i, err = SelectGleannAsset(names, "gleann-full", "v1.9.0", "linux", "amd64")
	if err != nil || names[i] != "gleann-full-v1.9.0-linux-amd64.tar.gz" {
		t.Fatalf("full: %v %v", i, err)
	}
	i, err = SelectGleannAsset(names, "gleann", "v1.9.0", "windows", "amd64")
	if err != nil || names[i] != "gleann-v1.9.0-windows-amd64.zip" {
		t.Fatalf("windows: %v %v", i, err)
	}
	if _, err := SelectGleannAsset(names, "gleann-full", "v1.9.0", "darwin", "amd64"); err == nil {
		t.Fatal("a variant without a build for the platform must be an error")
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "gleann")
	os.WriteFile(dest, []byte("old"), 0o755)
	nw := filepath.Join(dir, "new")
	os.WriteFile(nw, []byte("new"), 0o755)
	if err := ReplaceBinary(dest, nw); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "new" {
		t.Fatalf("dest = %q", b)
	}
	if _, err := os.Stat(dest + ".old"); !os.IsNotExist(err) {
		t.Fatal("old copy should be removed")
	}
	// A missing replacement leaves the installed binary untouched.
	if err := ReplaceBinary(dest, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error")
	}
	if b, _ := os.ReadFile(dest); string(b) != "new" {
		t.Fatalf("failed replace must restore the old binary, got %q", b)
	}
}

func tgzWith(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	tw.Write([]byte(content))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// serveFakeRelease serves a GitHub-like release of repo with one plugin archive
// and, optionally, a SHA256SUMS file (sum overrides the real hash).
func serveFakeRelease(t *testing.T, repo, tag, script string, sums bool, sumOverride string) {
	t.Helper()
	asset := fmt.Sprintf("%s-%s-%s-%s.tar.gz", repo, tag, runtime.GOOS, runtime.GOARCH)
	archive := tgzWith(t, repo, script)
	hash := sha256.Sum256(archive)
	sum := hex.EncodeToString(hash[:])
	if sumOverride != "" {
		sum = sumOverride
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/repos/tevfik/"+repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		assets := fmt.Sprintf(`{"name":%q,"browser_download_url":%q}`, asset, srv.URL+"/dl/"+asset)
		if sums {
			assets += fmt.Sprintf(`,{"name":"SHA256SUMS","browser_download_url":%q}`, srv.URL+"/dl/SHA256SUMS")
		}
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[%s]}`, tag, assets)
	})
	mux.HandleFunc("/dl/"+asset, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/dl/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, asset)
	})
	old := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = old })
}

func fakePluginHome(t *testing.T, name, installedVersion string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".gleann", "plugins", name)
	os.MkdirAll(dir, 0o755)
	bin := filepath.Join(dir, name)
	os.WriteFile(bin, []byte(pluginScript(installedVersion)), 0o755)
	return bin
}

func pluginScript(version string) string {
	return "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --version) echo 'plugin v" + version + " (schema_version=2)';;\n" +
		"  --install) echo registered > \"$(dirname \"$0\")/registered\";;\n" +
		"esac\nexit 0\n"
}

func drainProgress() (chan string, func()) {
	ch := make(chan string, 64)
	go func() {
		for range ch {
		}
	}()
	return ch, func() { close(ch) }
}

func TestUpdatePluginReplacesOlderBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugin")
	}
	name := "gleann-plugin-docs"
	bin := fakePluginHome(t, name, "1.3.0")
	serveFakeRelease(t, name, "v1.4.0", pluginScript("1.4.0"), true, "")
	meta := PluginMeta{Name: name, RepoURL: "https://github.com/tevfik/" + name}

	res, _, err := CheckPluginUpdate(context.Background(), meta)
	if err != nil || !res.Newer || res.Installed != "v1.3.0" || res.Latest != "v1.4.0" {
		t.Fatalf("check: %+v %v", res, err)
	}

	ch, done := drainProgress()
	res, err = UpdatePlugin(context.Background(), meta, false, ch)
	done()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified {
		t.Error("checksum from SHA256SUMS should have been verified")
	}
	if v := installedPluginVersion(bin); v != "v1.4.0" {
		t.Fatalf("installed version after update = %q", v)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(bin), "registered")); err != nil {
		t.Error("--install was not run after the update")
	}

	// Already current: nothing is downloaded or changed.
	ch, done = drainProgress()
	res, err = UpdatePlugin(context.Background(), meta, false, ch)
	done()
	if err != nil || res.Newer {
		t.Fatalf("second run: %+v %v", res, err)
	}
}

func TestUpdatePluginRejectsChecksumMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugin")
	}
	name := "gleann-plugin-docs"
	bin := fakePluginHome(t, name, "1.3.0")
	serveFakeRelease(t, name, "v1.4.0", pluginScript("1.4.0"), true, strings.Repeat("0", 64))
	meta := PluginMeta{Name: name, RepoURL: "https://github.com/tevfik/" + name}

	ch, done := drainProgress()
	_, err := UpdatePlugin(context.Background(), meta, false, ch)
	done()
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if v := installedPluginVersion(bin); v != "v1.3.0" {
		t.Fatalf("a rejected download must leave the old binary, got %q", v)
	}
	entries, _ := os.ReadDir(filepath.Dir(bin))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".new") || strings.HasSuffix(e.Name(), ".old") {
			t.Errorf("leftover %s", e.Name())
		}
	}
}

func TestUpdatePluginNotInstalled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ch, done := drainProgress()
	defer done()
	_, err := UpdatePlugin(context.Background(), PluginMeta{Name: "gleann-plugin-docs", RepoURL: "https://github.com/tevfik/gleann-plugin-docs"}, false, ch)
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("got %v", err)
	}
}

// A plugin installed with `go install` / a copy in ~/.local/bin is only known
// through plugins.json; the updater must find it there.
func TestUpdatePluginFindsBinaryFromRegistry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugin")
	}
	name := "gleann-plugin-docs"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	bin := filepath.Join(home, "bin", name)
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte(pluginScript("1.3.0")), 0o755)
	os.MkdirAll(filepath.Join(home, ".gleann"), 0o755)
	reg := fmt.Sprintf(`{"plugins":[{"name":%q,"command":[%q,"--serve"]}]}`, name, bin)
	os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), []byte(reg), 0o644)

	serveFakeRelease(t, name, "v1.4.0", pluginScript("1.4.0"), false, "")
	meta := PluginMeta{Name: name, RepoURL: "https://github.com/tevfik/" + name}
	ch, done := drainProgress()
	res, err := UpdatePlugin(context.Background(), meta, false, ch)
	done()
	if err != nil {
		t.Fatal(err)
	}
	if res.Verified {
		t.Error("no checksum file was published, nothing to verify")
	}
	if v := installedPluginVersion(bin); v != "v1.4.0" {
		t.Fatalf("registered binary not updated: %q", v)
	}
}
