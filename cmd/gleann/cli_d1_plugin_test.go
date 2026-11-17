package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// d1StubPlugins replaces the catalog and the release-based plugin calls for
// the duration of the test.
type d1PluginStub struct {
	catalog   []gleann.PluginMeta
	checks    map[string]gleann.PluginUpdate
	checkErr  map[string]error
	updateErr map[string]error
	installed []string
	updated   []string
}

func d1StubPlugins(t *testing.T, s *d1PluginStub) {
	t.Helper()
	oc, ock, ou, oi := pluginCatalog, checkPluginUpdate, updatePlugin, installPlugin
	t.Cleanup(func() { pluginCatalog, checkPluginUpdate, updatePlugin, installPlugin = oc, ock, ou, oi })
	pluginCatalog = func() []gleann.PluginMeta { return s.catalog }
	checkPluginUpdate = func(_ context.Context, m gleann.PluginMeta) (gleann.PluginUpdate, *gleann.Release, error) {
		if err := s.checkErr[m.Name]; err != nil {
			return gleann.PluginUpdate{}, nil, err
		}
		return s.checks[m.Name], nil, nil
	}
	updatePlugin = func(_ context.Context, m gleann.PluginMeta, force bool, progress chan<- string) (gleann.PluginUpdate, error) {
		if err := s.updateErr[m.Name]; err != nil {
			return gleann.PluginUpdate{}, err
		}
		progress <- "downloading " + m.Name
		s.updated = append(s.updated, m.Name)
		return s.checks[m.Name], nil
	}
	installPlugin = func(m gleann.PluginMeta, progress chan<- string) (string, error) {
		if m.Name == "gleann-plugin-broken" {
			return "", errors.New("no release for this platform")
		}
		progress <- "fetching " + m.Name
		s.installed = append(s.installed, m.Name)
		return m.Name + " installed", nil
	}
}

func d1NeedGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func d1Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// d1Origin creates a local repository with one commit (and a tag when tag
// is set) holding the given files.
func d1Origin(t *testing.T, files map[string]string, tag string) string {
	t.Helper()
	dir := t.TempDir()
	d1Git(t, dir, "init", "-q", "-b", "main")
	for name, body := range files {
		d1WriteFile(t, filepath.Join(dir, name), body)
		if strings.HasPrefix(name, ".venv/") {
			_ = os.Chmod(filepath.Join(dir, name), 0o755)
		}
	}
	d1Git(t, dir, "add", "-A")
	d1Git(t, dir, "commit", "-q", "-m", "init")
	if tag != "" {
		d1Git(t, dir, "tag", tag)
	}
	return dir
}

func TestCLID1PluginUsageAndArgumentErrors(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("plugin")
	wantExit(t, r, 1)
	mustContain(t, "plugin", r.Out, "gleann plugin — manage installed plugins")

	r = e.run("plugins", "help")
	wantExit(t, r, 0)
	mustContain(t, "plugin help", r.Out, "gleann plugin install <name>")

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"plugin", "install"}, "usage: gleann plugin install <name>"},
		{[]string{"plugin", "info"}, "usage: gleann plugin info <name>"},
		{[]string{"plugin", "models"}, "usage: gleann plugin models <name>"},
		{[]string{"plugin", "frobnicate"}, "unknown plugin subcommand: frobnicate"},
	} {
		r := e.run(c.args...)
		wantExit(t, r, 1)
		mustContain(t, strings.Join(c.args, " "), r.Err, c.want)
	}
	r = e.run("plugin", "list", "--bogus")
	if r.Code == 0 {
		t.Error("plugin list --bogus succeeded")
	}
}

func TestCLID1PluginListAndInfo(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("plugin", "list")
	wantExit(t, r, 0)
	mustContain(t, "empty list", r.Out, "No plugins installed")

	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok","ready":true,"schema_version":3,"version":"1.4.0"}`))
		case "/info":
			_, _ = w.Write([]byte(`{"name":"gleann-plugin-docs","version":"1.4.0","schema_version":3,"capabilities":["convert","ocr"],"extensions":[".pdf",".docx"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ready.Close()
	loading := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ready":false}`))
	}))
	defer loading.Close()
	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "plugins.json"), `{"plugins":[
		{"name":"gleann-plugin-docs","url":"`+ready.URL+`","capabilities":["convert"]},
		{"name":"gleann-plugin-sound","url":"`+loading.URL+`","version":"0.9.0","schema_version":2},
		{"name":"offline"}]}`)

	r = e.run("plugin", "list")
	wantExit(t, r, 0)
	lines := strings.Split(strings.TrimSpace(r.Out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "NAME") {
		t.Fatalf("plugin list output:\n%s", r.Out)
	}
	// Rows are sorted by name.
	mustContain(t, "docs row", lines[1], "gleann-plugin-docs", "ready", "schema=v3", "version=1.4.0", "convert")
	mustContain(t, "sound row", lines[2], "gleann-plugin-sound", "loading", "schema=v2", "version=0.9.0")
	mustContain(t, "offline row", lines[3], "offline", "stopped", "schema=?", "version=?")

	r = e.run("plugin", "info", "gleann-plugin-docs")
	wantExit(t, r, 0)
	mustContain(t, "info", r.Out, "Name           gleann-plugin-docs", "SchemaVersion  3",
		"Capabilities   convert, ocr", "Extensions     .pdf, .docx")

	r = e.run("plugin", "info", "nope")
	wantExit(t, r, 1)
	mustContain(t, "info unknown", r.Err, `plugin "nope" not registered`)

	r = e.run("plugin", "info", "offline")
	wantExit(t, r, 1)
	mustContain(t, "info offline", r.Err, "could not reach offline", "start it manually")

	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "plugins.json"), "{bad")
	for _, args := range [][]string{{"plugin", "list"}, {"plugin", "info", "x"}} {
		r = e.run(args...)
		wantExit(t, r, 1)
		mustContain(t, strings.Join(args, " "), r.Err, "could not load plugins")
	}
}

func TestCLID1PluginInstall(t *testing.T) {
	e := newCLIEnv(t)
	stub := &d1PluginStub{catalog: []gleann.PluginMeta{{Name: "gleann-plugin-docs"}, {Name: "gleann-plugin-broken"}}}
	d1StubPlugins(t, stub)

	// Catalog plugins go through the release installer.
	r := e.run("plugin", "install", "docs", "gleann-plugin-broken", "nonsense")
	wantExit(t, r, 0)
	mustContain(t, "install out", r.Out, "[install] fetching gleann-plugin-docs", "✓ gleann-plugin-docs installed")
	mustContain(t, "install err", r.Err, "failed to install gleann-plugin-broken: no release for this platform",
		`Unknown plugin "nonsense"`)
	if strings.Join(stub.installed, ",") != "gleann-plugin-docs" {
		t.Errorf("installed %v", stub.installed)
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".gleann", "plugins", "_repos")); err != nil {
		t.Errorf("install must create the repos directory: %v", err)
	}

	// A workspace checkout is used as is.
	ws := filepath.Join(e.Home, ".openclaw", "workspace", "gleann-plugin-sound")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	r = e.run("plugin", "install", "sound")
	wantExit(t, r, 0)
	mustNotContain(t, "workspace", r.Out, "Cloning")
}

func TestCLID1PluginInstallClonesAndRegisters(t *testing.T) {
	d1NeedGit(t)
	e := newCLIEnv(t)
	d1StubPlugins(t, &d1PluginStub{})
	origin := d1Origin(t, map[string]string{
		"main.py":          "print('plugin')\n",
		".venv/bin/python": "#!/bin/sh\necho \"registering $1 $2\"\n",
	}, "")
	if _, err := os.Stat(filepath.Join(origin, ".venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("the venv python stub is a shell script")
	}
	failing := d1Origin(t, map[string]string{"main.py": "x", ".venv/bin/python": "#!/bin/sh\nexit 4\n"}, "")
	orig := map[string]string{}
	for k, v := range knownPluginRepos {
		orig[k] = v
	}
	t.Cleanup(func() {
		for k := range knownPluginRepos {
			delete(knownPluginRepos, k)
		}
		for k, v := range orig {
			knownPluginRepos[k] = v
		}
	})
	knownPluginRepos["sound"] = origin
	knownPluginRepos["marker"] = failing
	knownPluginRepos["gleann-plugin-gone"] = filepath.Join(t.TempDir(), "missing-repo")

	target := filepath.Join(e.Home, ".gleann", "plugins", "_repos", "gleann-plugin-sound")
	r := e.run("plugin", "install", "sound")
	wantExit(t, r, 0)
	mustContain(t, "clone", r.Out, "[install] Cloning "+origin+" into "+target,
		"registering main.py --install", "✓ Plugin gleann-plugin-sound registered successfully!")

	r = e.run("plugin", "install", "sound")
	mustContain(t, "again", r.Out, "Plugin repository already present at "+target)

	r = e.run("plugin", "install", "marker", "gleann-plugin-gone")
	mustContain(t, "archived", r.Err, "gleann-plugin-marker is archived",
		"failed to register plugin gleann-plugin-marker", "failed to clone "+knownPluginRepos["gleann-plugin-gone"])
}

func TestCLID1PluginUpdateReleases(t *testing.T) {
	e := newCLIEnv(t)
	stub := &d1PluginStub{
		catalog: []gleann.PluginMeta{{Name: "gleann-plugin-docs"}, {Name: "gleann-plugin-sound"}, {Name: "gleann-plugin-x"}},
		checks: map[string]gleann.PluginUpdate{
			"gleann-plugin-docs":  {Installed: "", Latest: "v2.0.0", Newer: true},
			"gleann-plugin-sound": {Installed: "v1.0.0", Latest: "v1.0.0"},
		},
		checkErr:  map[string]error{"gleann-plugin-x": errors.New("plugin gleann-plugin-x is not installed as a binary")},
		updateErr: map[string]error{"gleann-plugin-x": errors.New("not installed")},
	}
	d1StubPlugins(t, stub)

	r := e.run("plugin", "update", "--check")
	wantExit(t, r, 0)
	mustContain(t, "check", r.Out, "[check]  gleann-plugin-docs: unknown version → v2.0.0 available",
		"[check]  gleann-plugin-sound: up to date (v1.0.0)")
	mustNotContain(t, "check", r.Out, "gleann-plugin-x")
	if len(stub.updated) != 0 {
		t.Fatalf("--check updated %v", stub.updated)
	}

	stub.checkErr["gleann-plugin-sound"] = errors.New("rate limited")
	r = e.run("plugin", "update", "--check", "sound")
	mustContain(t, "check sound", r.Out, "[check]  gleann-plugin-sound: rate limited")
	mustNotContain(t, "check sound", r.Out, "gleann-plugin-docs")

	// Update: handled plugins, no repos directory: success.
	stub.updateErr["gleann-plugin-sound"] = errors.New("checksum mismatch")
	r = e.run("plugin", "update")
	wantExit(t, r, 0)
	mustContain(t, "update", r.Out, "[update] downloading gleann-plugin-docs",
		"[update] gleann-plugin-docs updated to v2.0.0")
	mustContain(t, "update err", r.Err, "[update] gleann-plugin-sound failed: checksum mismatch")

	// --force re-announces even when nothing is newer.
	delete(stub.updateErr, "gleann-plugin-sound")
	r = e.run("plugin", "upgrade", "--force", "gleann-plugin-sound")
	wantExit(t, r, 0)
	mustContain(t, "force", r.Out, "[update] gleann-plugin-sound updated to v1.0.0")

	// Nothing handled and no repos: an error.
	stub.catalog = nil
	r = e.run("plugin", "update")
	wantExit(t, r, 1)
	mustContain(t, "no repos", r.Err, "no plugin repos found under "+filepath.Join(e.Home, ".gleann", "plugins", "_repos"))
}

func TestCLID1PluginUpdateGitRepos(t *testing.T) {
	d1NeedGit(t)
	e := newCLIEnv(t)
	d1StubPlugins(t, &d1PluginStub{})
	repos := filepath.Join(e.Home, ".gleann", "plugins", "_repos")

	if err := os.MkdirAll(repos, 0o755); err != nil {
		t.Fatal(err)
	}
	tagged := d1Origin(t, map[string]string{"a.txt": "a"}, "v1.2.3")
	untagged := d1Origin(t, map[string]string{"b.txt": "b"}, "")
	d1Git(t, repos, "clone", "-q", tagged, filepath.Join(repos, "gleann-plugin-tagged"))
	d1Git(t, repos, "clone", "-q", untagged, filepath.Join(repos, "gleann-plugin-plain"))
	if err := os.MkdirAll(filepath.Join(repos, "gleann-plugin-notgit"), 0o755); err != nil {
		t.Fatal(err)
	}
	d1WriteFile(t, filepath.Join(repos, "README"), "a file, not a repo")
	// A checkout whose remote is gone: fetch fails.
	gone := d1Origin(t, map[string]string{"c.txt": "c"}, "")
	d1Git(t, repos, "clone", "-q", gone, filepath.Join(repos, "gleann-plugin-broken"))
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	r := e.run("plugin", "update")
	wantExit(t, r, 0)
	mustContain(t, "update repos", r.Out,
		"[skip]   gleann-plugin-notgit — not a git checkout",
		"[update] gleann-plugin-tagged", "checked out tag v1.2.3",
		"[update] gleann-plugin-plain",
		"[update] gleann-plugin-broken",
		"Done. 2 updated, 2 skipped.")
	mustContain(t, "fetch failed", r.Err, "fetch failed")

	// Only the named plugin (short alias accepted).
	r = e.run("plugin", "update", "gleann-plain")
	wantExit(t, r, 0)
	mustContain(t, "named", r.Out, "[update] gleann-plugin-plain", "Done. 1 updated, 0 skipped.")
	mustNotContain(t, "named", r.Out, "gleann-plugin-tagged")
}

func TestCLID1PluginModels(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("plugin", "models", "docs")
	wantExit(t, r, 1)
	mustContain(t, "missing", r.Err, `plugin binary for "docs" not found`)

	// Found through the registry; the plugin's exit status is passed through.
	bin := d1Script(t, filepath.Join(e.Home, "pbin", "gleann-plugin-docs"), `echo "args: $*"; [ "$2" = "verify" ] && exit 3; exit 0`)
	reg, _ := json.Marshal(map[string]any{"plugins": []map[string]any{{"name": "gleann-plugin-docs", "command": []string{bin}}}})
	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "plugins.json"), string(reg))
	r = e.run("plugin", "models", "docs")
	wantExit(t, r, 0)
	mustContain(t, "list default", r.Out, "args: models list")
	r = e.run("plugin", "models", "gleann-plugin-docs", "verify", "--json")
	wantExit(t, r, 3)
	mustContain(t, "verify", r.Out, "args: models verify --json")

	// Found in a candidate location.
	d1Script(t, filepath.Join(e.Home, ".gleann", "plugins", "gleann-plugin-sound", "bin", "gleann-plugin-sound"), `echo "sound $*"`)
	r = e.run("plugin", "models", "sound", "download", "tiny")
	wantExit(t, r, 0)
	mustContain(t, "candidate", r.Out, "sound models download tiny")

	// A file that cannot be executed.
	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "plugins", "gleann-plugin-noexec", "gleann-plugin-noexec"), "not a program")
	r = e.run("plugin", "models", "noexec")
	wantExit(t, r, 1)
	mustContain(t, "noexec", r.Err, "failed to run gleann-plugin-noexec models")
}
