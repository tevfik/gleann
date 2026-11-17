package gleann

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGoOnPath puts a `go` script first on PATH. `go build -o <out> ...`
// logs its arguments and writes <out> as a shell script that logs its own
// calls (so the plugin can register itself); fail makes the build fail.
func fakeGoOnPath(t *testing.T, logPath string, fail bool) {
	t.Helper()
	bin := t.TempDir()
	body := `out=""; prev=""
for a in "$@"; do if [ "$prev" = "-o" ]; then out="$a"; fi; prev="$a"; done
`
	if fail {
		body += `echo "main.go:3: undefined: thing"; exit 1`
	} else {
		body += `printf '#!/bin/sh\necho "built $*" >> '"'` + logPath + `'"'\n' > "$out"; chmod +x "$out"`
	}
	writeScript(t, filepath.Join(bin, "go"), logPath, body)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestInstallPlugin_BuildsFromSourceWhenNoRelease(t *testing.T) {
	requireShell(t)
	_, plugins, logPath := installEnv(t)
	newInstallGitHub(t) // no release assets: the download fails

	for _, tc := range []struct {
		name, dir string
		wantArgs  string
	}{
		{"gleann-plugin-notes", ".", "build -o"},
		{"gleann-plugin-sound", "cmd/server", "-tags onnx ./cmd/server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeGoOnPath(t, logPath, false)
			src := filepath.Join(plugins, tc.name, tc.dir, "main.go")
			if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, msgs, err := runInstall(t, PluginMeta{Name: tc.name, Language: "go"})
			if err != nil || !strings.Contains(out, "Go binary") {
				t.Fatalf("install = %q, %v (%v)", out, err, msgs)
			}
			joined := strings.Join(msgs, "\n")
			if !strings.Contains(joined, "Building Go binary from source") || !strings.Contains(joined, "Binary built successfully") {
				t.Errorf("progress: %v", msgs)
			}
			log := readLog(t, logPath)
			if !strings.Contains(log, tc.wantArgs) {
				t.Errorf("go build arguments lack %q:\n%s", tc.wantArgs, log)
			}
			if !strings.Contains(log, "built --install") {
				t.Errorf("the built binary was not run to register itself:\n%s", log)
			}
		})
	}
}

func TestInstallPlugin_SourceBuildFailure(t *testing.T) {
	requireShell(t)
	_, plugins, logPath := installEnv(t)
	newInstallGitHub(t)
	fakeGoOnPath(t, logPath, true)
	name := "gleann-plugin-broken"
	if err := os.MkdirAll(filepath.Join(plugins, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins, name, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runInstall(t, PluginMeta{Name: name, Language: "go"})
	if err == nil || !strings.Contains(err.Error(), "go build:") || !strings.Contains(err.Error(), "undefined: thing") {
		t.Fatalf("err = %v, want the compiler output", err)
	}
	if _, err := os.Stat(filepath.Join(plugins, name, name)); !os.IsNotExist(err) {
		t.Fatal("a failed build left a binary behind")
	}
}

func TestUninstallPluginRemovesFilesAndRegistration(t *testing.T) {
	home, plugins, _ := installEnv(t)
	for _, n := range []string{"gleann-plugin-a", "gleann-plugin-b"} {
		if err := os.MkdirAll(filepath.Join(plugins, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	reg := PluginRegistry{Plugins: []Plugin{{Name: "gleann-plugin-a", URL: "http://127.0.0.1:1"}, {Name: "gleann-plugin-b", URL: "http://127.0.0.1:2"}}}
	data, _ := json.Marshal(reg)
	if err := os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallPlugin("../etc"); err == nil {
		t.Fatal("a path name was accepted")
	}
	out, err := UninstallPlugin("gleann-plugin-a")
	if err != nil || out != "Uninstalled gleann-plugin-a" {
		t.Fatalf("uninstall = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(plugins, "gleann-plugin-a")); !os.IsNotExist(err) {
		t.Fatal("the plugin directory survived")
	}
	if _, err := os.Stat(filepath.Join(plugins, "gleann-plugin-b")); err != nil {
		t.Fatal("another plugin was removed")
	}
	left, err := LoadPlugins()
	if err != nil || len(left.Plugins) != 1 || left.Plugins[0].Name != "gleann-plugin-b" {
		t.Fatalf("registry after uninstall = %+v, %v", left, err)
	}
}
