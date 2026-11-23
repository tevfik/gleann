package indexer

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The environment overrides every route out of the offline, read-only load
// and keeps the user's non-conflicting GOFLAGS (unit level; the end-to-end
// proofs are the three tests below).
func TestExactLoadEnvOfflineOverrides(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"GOPRIVATE=private.example.com/*",
		"GONOPROXY=private.example.com/*",
		"GONOSUMDB=private.example.com/*",
		"GOPROXY=https://proxy.example.com",
		"GOFLAGS=-mod=mod -tags=probeTag",
		"GOTOOLCHAIN=auto",
	}
	env := exactLoadEnv(in, t.TempDir())
	last := make(map[string]string)
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			last[k] = v
		}
	}
	for k, want := range map[string]string{
		"GOPROXY":     "off",
		"GOPRIVATE":   "",
		"GONOPROXY":   "none",
		"GONOSUMDB":   "*",
		"GOTOOLCHAIN": "auto",
		"PATH":        "/usr/bin",
	} {
		if last[k] != want {
			t.Errorf("%s = %q, want %q", k, last[k], want)
		}
	}
	flags := strings.Fields(last["GOFLAGS"])
	if !slices.Contains(flags, "-tags=probeTag") {
		t.Errorf("GOFLAGS lost the user's -tags: %q", last["GOFLAGS"])
	}
	if !slices.Contains(flags, "-mod=readonly") || slices.Contains(flags, "-mod=mod") {
		t.Errorf("GOFLAGS must replace -mod=mod with -mod=readonly: %q", last["GOFLAGS"])
	}
	if len(in) != 7 {
		t.Errorf("input slice changed: %v", in)
	}
	// A vendored tree loads with -mod=vendor instead.
	vroot := writeTree(t, map[string]string{"vendor/modules.txt": "# example.com/dep v1.0.0\n"})
	if got := exactLoadEnv(nil, vroot); !slices.Contains(got, "GOFLAGS=-mod=vendor") {
		t.Errorf("vendored tree: %v", got)
	}
}

// The load environment must keep the user's GOFLAGS choices that do not
// conflict with offline read-only indexing — -tags above all: replacing
// GOFLAGS wholesale drops them and silently changes which files the load
// type-checks (review G4).
func TestExactLoadEnvKeepsUserBuildTags(t *testing.T) {
	needGo(t)
	root := writeTree(t, map[string]string{
		"go.mod":    "module example.com/tagtest\n\ngo 1.21\n",
		"app.go":    "package tagtest\n\n// Base is always built.\nfunc Base() int { return 1 }\n",
		"tagged.go": "//go:build probeTag\n\npackage tagtest\n\n// Tagged is built with -tags=probeTag.\nfunc Tagged() int { return 2 }\n",
	})
	t.Setenv("GOFLAGS", "-tags=probeTag")
	r, err := LoadExactDirs("example.com/tagtest", root, []string{"."})
	if err != nil {
		t.Fatalf("LoadExactDirs: %v", err)
	}
	if !r.HasFile("tagged.go") {
		t.Errorf("the user's -tags=probeTag was dropped: tagged.go not type-checked (files: app.go=%v)", r.HasFile("app.go"))
	}
}

// A vendored module must load from vendor/ when its dependencies are not in
// the module cache: -mod=readonly cannot (review G4).
func TestExactLoadEnvVendoredModule(t *testing.T) {
	needGo(t)
	root := writeTree(t, map[string]string{
		"go.mod":                        "module example.com/vendtest\n\ngo 1.21\n\nrequire example.com/dep v1.0.0\n",
		"app.go":                        "package vendtest\n\nimport \"example.com/dep\"\n\n// Use reads the vendored constant.\nfunc Use() int { return dep.Value }\n",
		"vendor/example.com/dep/dep.go": "package dep\n\n// Value is vendored.\nconst Value = 7\n",
		"vendor/modules.txt":            "# example.com/dep v1.0.0\n## explicit; go 1.21\nexample.com/dep\n",
	})
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "mod"))
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))
	r, err := LoadExactDirs("example.com/vendtest", root, []string{"."})
	if err != nil {
		t.Fatalf("LoadExactDirs of a vendored module: %v", err)
	}
	if !r.HasFile("app.go") {
		t.Error("app.go of the vendored module not type-checked")
	}
}

// With GOPRIVATE/GONOPROXY set, a private-looking module missing from the
// cache must NOT be fetched: GOPROXY=off alone does not stop the direct VCS
// route those patterns open (review G4). The detectors: a local logging proxy
// that contains and records any HTTP attempt (go-get discovery goes through
// it), a git shim that logs invocations, the module cache contents, and the
// go.mod/go.sum bytes.
func TestExactLoadEnvNeverFetchesPrivateModules(t *testing.T) {
	needGo(t)
	const modSrc = "module example.com/nettest\n\ngo 1.21\n\nrequire private.example.com/org/lib v0.0.0\n"
	const sumSrc = "private.example.com/org/lib v0.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nprivate.example.com/org/lib v0.0.0/go.mod h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=\n"
	root := writeTree(t, map[string]string{
		"go.mod": modSrc,
		"go.sum": sumSrc,
		"app.go": "package nettest\n\nimport \"private.example.com/org/lib\"\n\n// Use calls the missing private module.\nfunc Use() int { return lib.Value }\n",
	})
	modcache := filepath.Join(t.TempDir(), "mod")
	t.Setenv("GOMODCACHE", modcache)
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))
	t.Setenv("GOPRIVATE", "*")
	t.Setenv("GOFLAGS", "-mod=mod")

	// Any HTTP attempt (go-get meta discovery, a proxy fetch) hits this local
	// proxy and is recorded; nothing reaches the real network.
	var mu sync.Mutex
	var attempts []string
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts = append(attempts, r.Method+" "+r.Host+r.URL.RequestURI())
		mu.Unlock()
		w.WriteHeader(http.StatusBadGateway)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	proxyURL := "http://" + ln.Addr().String()
	t.Setenv("HTTP_PROXY", proxyURL)
	t.Setenv("HTTPS_PROXY", proxyURL)

	gitLog := filepath.Join(t.TempDir(), "git.log")
	if runtime.GOOS != "windows" {
		shim := t.TempDir()
		script := "#!/bin/sh\necho \"git $*\" >> " + gitLog + "\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	_, err = LoadExactDirs("example.com/nettest", root, []string{"."})
	if err == nil {
		t.Fatal("the missing private module type-checked?!")
	}
	mu.Lock()
	got := append([]string(nil), attempts...)
	mu.Unlock()
	if len(got) > 0 {
		t.Errorf("the load contacted the network (via the logging proxy): %v", got)
	}
	if b, rerr := os.ReadFile(gitLog); rerr == nil && len(b) > 0 {
		t.Errorf("git was invoked: %s", b)
	}
	// go writes an empty transient .lock under cache/download even when the
	// lookup is disabled; module CONTENT (info/mod/zip/ziphash, extracted
	// directories) must never appear.
	var content []string
	_ = filepath.WalkDir(modcache, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasSuffix(p, ".lock") {
			content = append(content, p)
		}
		return nil
	})
	if len(content) > 0 {
		t.Errorf("module content was written to the cache: %v", content)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "go.mod")); string(b) != modSrc {
		t.Errorf("go.mod changed: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "go.sum")); string(b) != sumSrc {
		t.Errorf("go.sum changed: %s", b)
	}
}
