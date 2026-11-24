package indexing

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain gives the whole package a private home directory. Builds use the
// embedding disk cache under $HOME/.gleann/cache/embeddings, and newEnv picks
// a fresh model name per test, so on a shared HOME every test run added
// entries that were never reused. NewCachedComputer scans that directory on
// every build, so the suite got slower with every run (with half a million
// entries a single build spent about two seconds in the scan under -race).
// The Go toolchain locations are pinned first so exact resolution (go list)
// keeps using the real module and build caches.
func TestMain(m *testing.M) {
	pinGoEnv()
	home, err := os.MkdirTemp("", "gleann-indexing-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// pinGoEnv exports the toolchain paths that default to locations under HOME.
func pinGoEnv() {
	names := []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"}
	out, err := exec.Command("go", append([]string{"env"}, names...)...).Output()
	if err != nil {
		return
	}
	vals := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(vals) != len(names) {
		return
	}
	for i, n := range names {
		if v := strings.TrimSpace(vals[i]); v != "" && v != "off" {
			os.Setenv(n, v)
		}
	}
}
