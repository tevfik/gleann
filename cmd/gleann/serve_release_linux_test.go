//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/vault"
)

// `gleann serve` opens the default vault in a background goroutine (to prune
// upload records). When serve returns after its signal handler stopped the
// server, nothing under HOME may stay open: on Windows an open vault.db makes
// the removal of the home directory fail.
func TestCLID2ServeReleasesVault(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("GLEANN_AUTH", "off")
	addr := "127.0.0.1:" + strconv.Itoa(d2FreePort(t))

	// Hold the vault's exclusive lock so the prune goroutine is parked in its
	// open (holding a descriptor) when the server is stopped.
	holder, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	time.AfterFunc(500*time.Millisecond, func() { _ = holder.Close() })
	holderFDs := openFDsUnder(t, e.Home) // the holder's own descriptors

	r, ok := d2Serve(t, e, func(string) {}, "--addr", addr, "--quiet", "--host", e.Ollama.URL)
	wantExit(t, r, 0)
	if !ok {
		t.Fatalf("server never became healthy:\n%s\n%s", r.Out, r.Err)
	}
	// The holder may still be open here when the fix is missing (serve returns
	// at once); only descriptors the holder did not have count.
	for fd, target := range openFDsUnder(t, e.Home) {
		if _, isHolder := holderFDs[fd]; !isHolder {
			t.Errorf("handle %s (fd %s) still open after serve returned", target, fd)
		}
	}
}

func openFDsUnder(t testing.TB, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}
	out := map[string]string{}
	for _, ent := range ents {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", ent.Name()))
		if err == nil && strings.HasPrefix(target, dir+string(filepath.Separator)) {
			out[ent.Name()] = target
		}
	}
	return out
}
