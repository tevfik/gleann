//go:build linux

package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

// openFilesUnder lists this process's open file descriptors for files under
// dir. On Windows t.TempDir cleanup fails when any file below it is still
// open; on Linux the removal succeeds anyway, so /proc/self/fd is the proof.
func openFilesUnder(t testing.TB, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}
	var out []string
	for _, e := range ents {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && strings.HasPrefix(target, dir+string(filepath.Separator)) {
			out = append(out, target)
		}
	}
	return out
}

// openFDsUnder maps this process's open descriptors on files under dir to
// their targets.
func openFDsUnder(t testing.TB, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}
	out := map[string]string{}
	for _, e := range ents {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && strings.HasPrefix(target, dir+string(filepath.Separator)) {
			out[e.Name()] = target
		}
	}
	return out
}

// Start launches a goroutine that opens the default vault (to prune upload
// records) and releases it when done. Stop must wait for it: otherwise a
// server that is stopped right after start returns while vault.db is still
// open, and the home directory cannot be removed on Windows.
func TestStop_WaitsForVaultPrune(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_AUTH", "off")

	// Hold the vault's exclusive lock so the prune goroutine blocks inside
	// its open (the window in which it holds a file descriptor).
	holder, err := vault.NewTracker(vault.DefaultDBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() }) // idempotent
	holderFDs := openFDsUnder(t, home)
	if len(holderFDs) == 0 {
		t.Fatal("holder has no descriptor under home")
	}

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := NewServer(cfg, "127.0.0.1:0", "test")
	started := make(chan error, 1)
	go func() { started <- s.Start() }()

	// Wait until the prune goroutine has opened its own descriptor (the
	// holder's is the first), and remember the holder's descriptors so the
	// final check looks at the prune goroutine's only.
	deadline := time.Now().Add(10 * time.Second)
	for len(openFDsUnder(t, home)) <= len(holderFDs) {
		if time.Now().After(deadline) {
			t.Fatalf("prune goroutine never opened the vault: %v", openFDsUnder(t, home))
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Let the lock go shortly after Stop begins.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = holder.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Logf("Stop: %v", err)
	}
	<-started
	// The holder may still be open here (without the fix Stop returns at
	// once); only descriptors the holder did not have count.
	for fd, target := range openFDsUnder(t, home) {
		if _, isHolder := holderFDs[fd]; !isHolder {
			t.Errorf("prune goroutine still holds %s (fd %s) after Stop", target, fd)
		}
	}
}

// Stop may run while Start is still setting up (the serve signal handler runs
// concurrently with Start). Under -race this must not report a data race on
// the server fields, and Start must end with http.ErrServerClosed.
func TestStartStopConcurrent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_AUTH", "off")
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := NewServer(cfg, "127.0.0.1:0", "test")
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	// A Stop that wins the race against Start's set-up is a no-op for it, so
	// keep stopping until Start has returned.
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = s.Stop(ctx)
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Fatalf("Start = %v, want http.ErrServerClosed", err)
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Deleting an index while searches are in flight must leave no handle on the
// index files once the searches have returned (Windows cannot remove open
// files, and a leaked handle also pins the deleted index's disk space).
func TestDeleteIndexDuringSearch_ReleasesHandles(t *testing.T) {
	for round := 0; round < 5; round++ {
		s, h, cfg := newIndexedServer(t)
		if code := doSearch(h); code != http.StatusOK {
			t.Fatalf("initial search: %d", code)
		}
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						doSearch(h)
					}
				}
			}()
		}
		time.Sleep(20 * time.Millisecond)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/indexes/idx", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
		close(stop)
		wg.Wait()
		// No cleanup involved: the handles must be gone right now.
		if open := openFilesUnder(t, cfg.IndexDir); len(open) > 0 {
			t.Fatalf("round %d: index handles still open after delete+searches: %v (cache %d)", round, open, len(s.searchers))
		}
	}
}

// A Stop that runs before Start has taken its set-up lock must not be undone:
// Start returns http.ErrServerClosed at once, listens on nothing and starts no
// background task.
func TestStopBeforeStart_StartRefuses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_AUTH", "off")
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	// Reserve a port so that a listener could be detected afterwards.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	s := NewServer(cfg, addr, "test")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Start after Stop = %v, want http.ErrServerClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start after Stop is serving")
	}
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		c.Close()
		t.Error("something listens on the address after Stop+Start")
	}
	if open := openFilesUnder(t, home); len(open) > 0 {
		t.Errorf("Start after Stop opened files: %v", open)
	}
}
