package gleann

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// This file drives the plugin installer end to end without the network:
// GitHub is an httptest server (githubAPIBase), plugin binaries, pip and the
// venv python are small shell scripts that log their arguments.

type tarEntry struct {
	name string
	body string
	dir  bool
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.dir {
			h = &tar.Header{Name: e.name, Mode: 0o755, Typeflag: tar.TypeDir}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractTarballToDir(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	data := makeTarGz(t, []tarEntry{
		{name: "acme-repo-1a2b3c/", dir: true},
		{name: "acme-repo-1a2b3c/main.go", body: "package main"},
		{name: "acme-repo-1a2b3c/cmd/", dir: true},
		{name: "acme-repo-1a2b3c/cmd/tool/tool.go", body: "package tool"},
	})
	if err := extractTarballToDir(bytes.NewReader(data), dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for path, want := range map[string]string{
		"main.go":          "package main",
		"cmd/tool/tool.go": "package tool",
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q with the top directory stripped", path, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "acme-repo-1a2b3c")); !os.IsNotExist(err) {
		t.Error("the archive's top directory was not stripped")
	}

	if err := extractTarballToDir(strings.NewReader("not gzip"), t.TempDir()); err == nil || !strings.Contains(err.Error(), "gzip") {
		t.Errorf("non-gzip input: %v", err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(strings.Repeat("garbage!", 100)))
	_ = zw.Close()
	if err := extractTarballToDir(bytes.NewReader(gz.Bytes()), t.TempDir()); err == nil || !strings.Contains(err.Error(), "tar read") {
		t.Errorf("gzip of a non-tar: %v", err)
	}
	// A destination that cannot be created.
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, nil, 0o644)
	if err := extractTarballToDir(bytes.NewReader(data), filepath.Join(blocker, "sub")); err == nil {
		t.Error("extracting under a regular file must fail")
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	data := makeZip(t, map[string]string{
		"dist/README.md":       "readme",
		"dist/gleann-plugin-x": "BINARY",
	})
	dest := filepath.Join(t.TempDir(), "bin")
	if err := extractBinaryFromZip(bytes.NewReader(data), dest, "gleann-plugin-x"); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "BINARY" {
		t.Errorf("extracted %q, want the binary entry", got)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(dest); fi.Mode()&0o111 == 0 {
			t.Error("the extracted binary is not executable")
		}
	}
	if err := extractBinaryFromZip(bytes.NewReader(data), dest, "other"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing binary: %v", err)
	}
	if err := extractBinaryFromZip(strings.NewReader("PK? no"), dest, "x"); err == nil || !strings.Contains(err.Error(), "zip reader") {
		t.Errorf("invalid zip: %v", err)
	}
	if err := extractBinaryFromZip(bytes.NewReader(data), filepath.Join(t.TempDir(), "no", "dir", "bin"), "gleann-plugin-x"); err == nil {
		t.Error("an uncreatable destination must fail")
	}
}

func TestFindGoBuildTarget(t *testing.T) {
	write := func(t *testing.T, root string, files ...string) {
		for _, f := range files {
			p := filepath.Join(root, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("package x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name   string
		files  []string
		target string
		ok     bool
	}{
		{"root package", []string{"main.go", "cmd/x/x.go"}, ".", true},
		{"cmd subdirectory", []string{"README.md", "cmd/notes.txt", "cmd/empty/readme", "cmd/tool/main.go"}, filepath.Join("cmd", "tool"), true},
		{"no sources", []string{"README.md"}, ".", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, tc.files...)
			target, ok := findGoBuildTarget(root)
			if target != tc.target || ok != tc.ok {
				t.Errorf("findGoBuildTarget = (%q, %v), want (%q, %v)", target, ok, tc.target, tc.ok)
			}
		})
	}
}

// installGitHub is a fake GitHub serving the latest release of any repo.
type installGitHub struct {
	mu        sync.Mutex
	srv       *httptest.Server
	apiStatus int
	tarball   []byte
	assets    map[string][]byte // name -> body
	hits      []string
}

func newInstallGitHub(t *testing.T) *installGitHub {
	t.Helper()
	g := &installGitHub{assets: map[string][]byte{}}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.hits = append(g.hits, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			if g.apiStatus != 0 {
				w.WriteHeader(g.apiStatus)
				return
			}
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			var as []asset
			for n := range g.assets {
				as = append(as, asset{Name: n, URL: g.srv.URL + "/dl/" + n})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name":    "v1.2.3",
				"tarball_url": g.srv.URL + "/tarball",
				"assets":      as,
			})
		case r.URL.Path == "/tarball" && g.tarball != nil:
			_, _ = w.Write(g.tarball)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			body, ok := g.assets[strings.TrimPrefix(r.URL.Path, "/dl/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	orig := githubAPIBase
	githubAPIBase = g.srv.URL
	t.Cleanup(func() {
		githubAPIBase = orig
		g.srv.Close()
	})
	return g
}

func TestDownloadSourceFromGitHub(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	g := newInstallGitHub(t)
	g.tarball = makeTarGz(t, []tarEntry{{name: "acme-x-123/main.go", body: "package main"}})
	dest := filepath.Join(t.TempDir(), "repo")
	ch := make(chan string, 16)
	collect := drain(ch)
	if err := downloadSourceFromGitHub("acme", "x", dest, ch); err != nil {
		t.Fatalf("download: %v", err)
	}
	msgs := collect()
	if got, _ := os.ReadFile(filepath.Join(dest, "main.go")); string(got) != "package main" {
		t.Errorf("source not extracted: %q", got)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "v1.2.3") {
		t.Errorf("progress does not name the release: %v", msgs)
	}

	// The tarball is missing.
	g.tarball = nil
	ch = make(chan string, 16)
	collect = drain(ch)
	err := downloadSourceFromGitHub("acme", "x", t.TempDir(), ch)
	collect()
	if err == nil || !strings.Contains(err.Error(), "download tarball") {
		t.Errorf("missing tarball: %v", err)
	}
	// The tarball is not an archive.
	g.tarball = []byte("not an archive")
	ch = make(chan string, 16)
	collect = drain(ch)
	err = downloadSourceFromGitHub("acme", "x", t.TempDir(), ch)
	collect()
	if err == nil || !strings.Contains(err.Error(), "extract tarball") {
		t.Errorf("corrupt tarball: %v", err)
	}
	// The API fails.
	g.apiStatus = http.StatusForbidden
	ch = make(chan string, 16)
	collect = drain(ch)
	err = downloadSourceFromGitHub("acme", "x", t.TempDir(), ch)
	collect()
	if err == nil || !strings.Contains(err.Error(), "fetch releases") {
		t.Errorf("API failure: %v", err)
	}
}

// ── Script-backed installs (unix shells only) ─────────────────────────────

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plugin binaries are shell scripts")
	}
}

// writeScript writes an executable shell script that appends its arguments
// to logPath and then runs body.
func writeScript(t *testing.T, path, logPath, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$(basename \"$0\") $*\" >> '" + logPath + "'\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	return string(data)
}

// installEnv isolates HOME, the config dir and the temp dir.
func installEnv(t *testing.T) (home, plugins, logPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	t.Setenv("GLEANN_PLUGIN_OWNER", "acme")
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	plugins = filepath.Join(home, ".gleann", "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, plugins, filepath.Join(t.TempDir(), "calls.log")
}

func runInstall(t *testing.T, meta PluginMeta) (string, []string, error) {
	t.Helper()
	ch := make(chan string, 64)
	collect := drain(ch)
	out, err := InstallPlugin(meta, ch)
	return out, collect(), err
}

func TestInstallPlugin_CloneFailureFallsBackToReleaseSource(t *testing.T) {
	requireShell(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	_, plugins, _ := installEnv(t)
	g := newInstallGitHub(t)
	g.tarball = makeTarGz(t, []tarEntry{
		{name: "acme-gleann-plugin-notes-1/", dir: true},
		{name: "acme-gleann-plugin-notes-1/README.md", body: "notes plugin"},
	})
	// A local path that does not exist: git fails at once, without network,
	// and the path still parses as github.com/<owner>/<repo>.
	repoURL := filepath.Join(t.TempDir(), "github.com", "acme", "gleann-plugin-notes")
	meta := PluginMeta{Name: "gleann-plugin-notes", RepoURL: repoURL}

	out, msgs, err := runInstall(t, meta)
	if err != nil {
		t.Fatalf("install: %v (%v)", err, msgs)
	}
	if out != "Installed gleann-plugin-notes" {
		t.Errorf("result = %q", out)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"Git clone failed", "Source downloaded from GitHub release", "Plugin directory linked"} {
		if !strings.Contains(joined, want) {
			t.Errorf("progress lacks %q:\n%s", want, joined)
		}
	}
	got, err := os.ReadFile(filepath.Join(plugins, "gleann-plugin-notes", "README.md"))
	if err != nil || string(got) != "notes plugin" {
		t.Fatalf("plugin dir does not expose the source: %q (%v)", got, err)
	}

	// Again: the plugin directory exists already.
	_, msgs, err = runInstall(t, meta)
	if err != nil || !strings.Contains(strings.Join(msgs, "\n"), "Plugin directory already exists") {
		t.Fatalf("second install: %v %v", err, msgs)
	}

	// A plugin entry that is a regular file is replaced; the repo is reused.
	pdir := filepath.Join(plugins, "gleann-plugin-notes")
	if err := os.Remove(pdir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pdir, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, msgs, err = runInstall(t, meta)
	joined = strings.Join(msgs, "\n")
	if err != nil || !strings.Contains(joined, "Removing invalid plugin entry") || !strings.Contains(joined, "Repository already exists") {
		t.Fatalf("reinstall over a file: %v\n%s", err, joined)
	}
	if fi, err := os.Stat(pdir); err != nil || !fi.IsDir() {
		t.Fatalf("plugin entry is not a directory after reinstall: %v", err)
	}

	// A dangling symlink entry is replaced too.
	if err := os.Remove(pdir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), pdir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runInstall(t, meta); err != nil {
		t.Fatalf("reinstall over a dangling link: %v", err)
	}

	// Neither git nor a GitHub URL: the install fails with git's output.
	_, _, err = runInstall(t, PluginMeta{Name: "lost", RepoURL: filepath.Join(t.TempDir(), "nowhere")})
	if err == nil || !strings.Contains(err.Error(), "git clone failed") {
		t.Errorf("unparsable repo URL: %v", err)
	}
	// git fails and the release download fails too.
	g.apiStatus = http.StatusNotFound
	_, _, err = runInstall(t, PluginMeta{Name: "gleann-plugin-other", RepoURL: filepath.Join(t.TempDir(), "github.com", "acme", "gleann-plugin-other")})
	if err == nil || !strings.Contains(err.Error(), "both git clone and release download failed") {
		t.Errorf("both sources failing: %v", err)
	}
}

func TestInstallPlugin_PythonPlugin(t *testing.T) {
	requireShell(t)
	_, plugins, logPath := installEnv(t)
	name := "gleann-plugin-speech"
	pdir := filepath.Join(plugins, name)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "requirements.txt"), []byte("markitdown\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No venv yet: findPython3 finds our python3 first in PATH, which creates
	// the venv with a pip and a python that rejects --name (old plugins).
	fakeBin := t.TempDir()
	venvPip := filepath.Join(pdir, ".venv", "bin", "pip")
	venvPy := filepath.Join(pdir, ".venv", "bin", "python")
	template := filepath.Join(t.TempDir(), "venv")
	writeScript(t, filepath.Join(template, "bin", "pip"), logPath, "exit 0")
	writeScript(t, filepath.Join(template, "bin", "python"), logPath, `case "$*" in *--name*) exit 1;; esac`)
	writeScript(t, filepath.Join(fakeBin, "python3"), logPath, `cp -R '`+template+`' "$3"`)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, msgs, err := runInstall(t, PluginMeta{Name: name, Language: "python", RepoURL: "unused"})
	if err != nil {
		t.Fatalf("install: %v\n%v", err, msgs)
	}
	if out != "Installed "+name+" (Python)" {
		t.Errorf("result = %q", out)
	}
	log := readLog(t, logPath)
	for _, want := range []string{
		"python3 -m venv " + filepath.Join(pdir, ".venv"),
		"pip install -r " + filepath.Join(pdir, "requirements.txt"),
		"python " + filepath.Join(pdir, "main.py") + " --install --name " + name,
		"python " + filepath.Join(pdir, "main.py") + " --install\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("call log lacks %q:\n%s", want, log)
		}
	}

	// Second install reuses the venv; pip now fails.
	writeScript(t, venvPip, logPath, "echo 'no network' >&2; exit 3")
	_, msgs, err = runInstall(t, PluginMeta{Name: name, Language: "python"})
	if err == nil || !strings.Contains(err.Error(), "pip install") {
		t.Fatalf("pip failure: %v", err)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "Virtual environment already exists") {
		t.Errorf("the existing venv was not reused: %v", msgs)
	}

	// pip works, but registration fails both ways.
	writeScript(t, venvPip, logPath, "exit 0")
	writeScript(t, venvPy, logPath, "echo broken; exit 1")
	_, _, err = runInstall(t, PluginMeta{Name: name, Language: "python"})
	if err == nil || !strings.Contains(err.Error(), "register plugin") {
		t.Fatalf("registration failure: %v", err)
	}

	// venv creation fails.
	if err := os.RemoveAll(filepath.Join(pdir, ".venv")); err != nil {
		t.Fatal(err)
	}
	writeScript(t, filepath.Join(fakeBin, "python3"), logPath, "echo 'no venv module'; exit 1")
	_, _, err = runInstall(t, PluginMeta{Name: name, Language: "python"})
	if err == nil || !strings.Contains(err.Error(), "create venv") {
		t.Fatalf("venv failure: %v", err)
	}
}

func TestInstallPlugin_GoBinaryPlugins(t *testing.T) {
	requireShell(t)
	home, plugins, logPath := installEnv(t)
	g := newInstallGitHub(t)

	t.Run("sound binary already installed", func(t *testing.T) {
		name := "gleann-plugin-sound"
		writeScript(t, filepath.Join(plugins, name, name), logPath, "exit 0")
		out, msgs, err := runInstall(t, PluginMeta{Name: name, Language: "go"})
		if err != nil || out != "Installed "+name+" (Go binary)" {
			t.Fatalf("install = %q, %v (%v)", out, err, msgs)
		}
		log := readLog(t, logPath)
		if !strings.Contains(log, name+" --install") || !strings.Contains(log, name+" models download --default") {
			t.Errorf("the plugin was not registered and asked for its models:\n%s", log)
		}
		if !strings.Contains(strings.Join(msgs, "\n"), "Speech recognition models ready") {
			t.Errorf("progress: %v", msgs)
		}
	})

	t.Run("docs binary fetches OCR and the docling model", func(t *testing.T) {
		name := "gleann-plugin-docs"
		// OCR setup fails: reported, not fatal.
		writeScript(t, filepath.Join(plugins, name, name), logPath, `case "$1" in models) exit 2;; esac; exit 0`)
		g.mu.Lock()
		g.assets = map[string][]byte{
			"granite_docling_bitnet_dense-q4.eifm": []byte("MODEL"),
			"tokenizer.bin":                        []byte("TOK"),
		}
		g.mu.Unlock()
		out, msgs, err := runInstall(t, PluginMeta{Name: name})
		if err != nil || !strings.Contains(out, "Go binary") {
			t.Fatalf("install = %q, %v", out, err)
		}
		joined := strings.Join(msgs, "\n")
		if !strings.Contains(joined, "OCR setup skipped") {
			t.Errorf("a failing OCR download must be reported: %s", joined)
		}
		modelDir := filepath.Join(home, ".gleann", "models", "granite-docling")
		if got, _ := os.ReadFile(filepath.Join(modelDir, "granite_docling_bitnet_dense.eifm")); string(got) != "MODEL" {
			t.Errorf("model not reachable under its canonical name: %q", got)
		}
		if got, _ := os.ReadFile(filepath.Join(modelDir, "tokenizer.bin")); string(got) != "TOK" {
			t.Errorf("tokenizer not downloaded: %q", got)
		}

		// Installing again finds the model present and downloads nothing.
		g.mu.Lock()
		g.hits = nil
		g.mu.Unlock()
		_, msgs, err = runInstall(t, PluginMeta{Name: name})
		if err != nil || !strings.Contains(strings.Join(msgs, "\n"), "model already present") {
			t.Fatalf("reinstall: %v %v", err, msgs)
		}
		g.mu.Lock()
		hits := len(g.hits)
		g.mu.Unlock()
		if hits != 0 {
			t.Errorf("a present model was fetched again (%d requests)", hits)
		}
	})

	t.Run("binary downloaded from the release", func(t *testing.T) {
		name := "gleann-plugin-fetch"
		if err := os.MkdirAll(filepath.Join(plugins, name), 0o755); err != nil {
			t.Fatal(err)
		}
		asset := name + "-" + runtime.GOOS + "-" + runtime.GOARCH
		g.mu.Lock()
		g.assets = map[string][]byte{asset: []byte("#!/bin/sh\necho \"fetched $*\" >> '" + logPath + "'\n")}
		g.mu.Unlock()
		out, msgs, err := runInstall(t, PluginMeta{Name: name, Language: "go"})
		if err != nil || !strings.Contains(out, "Go binary") {
			t.Fatalf("install = %q, %v (%v)", out, err, msgs)
		}
		if !strings.Contains(strings.Join(msgs, "\n"), "Binary downloaded successfully") {
			t.Errorf("progress: %v", msgs)
		}
		if !strings.Contains(readLog(t, logPath), "fetched --install") {
			t.Error("the downloaded binary was not run to register itself")
		}
	})

	t.Run("no release and no sources", func(t *testing.T) {
		name := "gleann-empty"
		if err := os.MkdirAll(filepath.Join(plugins, name), 0o755); err != nil {
			t.Fatal(err)
		}
		g.mu.Lock()
		g.assets = map[string][]byte{}
		g.mu.Unlock()
		_, msgs, err := runInstall(t, PluginMeta{Name: name, Language: "go"})
		if err == nil || !strings.Contains(err.Error(), "no binary in releases and no source files") {
			t.Fatalf("want the no-binary error, got %v", err)
		}
		if !strings.Contains(strings.Join(msgs, "\n"), "Download failed") {
			t.Errorf("the failed download is not reported: %v", msgs)
		}
	})

	t.Run("pre-built binary in the repository root", func(t *testing.T) {
		name := "gleann-plugin-root"
		repoRoot := filepath.Join(plugins, "_repos", "gleann-plugin-root-repo")
		bin := filepath.Join(repoRoot, name)
		writeScript(t, bin, logPath, "exit 0")
		// The repository is already cloned and its root holds the plugin
		// binary (no <name> subdirectory): the plugin entry links to the
		// repository root, where the binary is found without a download.
		out, msgs, err := runInstall(t, PluginMeta{Name: name, Language: "go", RepoURL: "https://example.invalid/acme/gleann-plugin-root-repo.git"})
		if err != nil || !strings.Contains(out, "Go binary") {
			t.Fatalf("install = %q, %v (%v)", out, err, msgs)
		}
		joined := strings.Join(msgs, "\n")
		if !strings.Contains(joined, "Repository already exists") || !strings.Contains(joined, "Binary already installed") || strings.Contains(joined, "Downloading") {
			t.Errorf("progress: %v", msgs)
		}
		target, err := filepath.EvalSymlinks(filepath.Join(plugins, name))
		want, _ := filepath.EvalSymlinks(repoRoot)
		if err != nil || target != want {
			t.Errorf("plugin entry resolves to %q, %v; want the repository root %q", target, err, want)
		}
		if !strings.Contains(readLog(t, logPath), name+" --install") {
			t.Error("the binary at the repository root was not run to register itself")
		}
	})

	t.Run("python language with a docs name builds as go", func(t *testing.T) {
		got, err := setupPythonPluginWithProgress(filepath.Join(plugins, "gleann-plugin-docs"), "gleann-plugin-docs", drainOnly(t))
		if err != nil || !strings.Contains(got, "Go binary") {
			t.Fatalf("docs plugin via the python path = %q, %v", got, err)
		}
	})
}

// drainOnly returns a progress channel whose messages are discarded.
func drainOnly(t *testing.T) chan<- string {
	t.Helper()
	ch := make(chan string, 64)
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	t.Cleanup(func() {
		close(ch)
		<-done
	})
	return ch
}
