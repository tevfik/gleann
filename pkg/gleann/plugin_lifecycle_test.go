package gleann

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func requireUnixTools(t *testing.T, tools ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires unix tools")
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
}

// unusedURL returns an http URL on a port nothing listens on.
func unusedURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: process was not reaped", what)
	}
}

func TestEnsurePluginRunning_UnhealthyIsKilledAndUnregistered(t *testing.T) {
	requireUnixTools(t, "sleep")
	winHome := t.TempDir()
	t.Setenv("HOME", winHome)
	t.Setenv("USERPROFILE", winHome)
	old := pluginStartTimeout
	pluginStartTimeout = 1200 * time.Millisecond
	defer func() { pluginStartTimeout = old }()

	m := &PluginManager{Registry: &PluginRegistry{}}
	p := &Plugin{Name: "never-healthy", URL: unusedURL(t), Command: []string{"sleep", "30"}}

	var reaped <-chan struct{}
	pluginStartedHook = func(_ *exec.Cmd, exited <-chan struct{}) { reaped = exited }
	defer func() { pluginStartedHook = nil }()

	if err := m.EnsurePluginRunning(p); err == nil {
		t.Fatal("expected health-check failure")
	}
	m.mu.Lock()
	n, nl, ne := len(m.activeCmds), len(m.logFiles), len(m.exited)
	m.mu.Unlock()
	if n != 0 || nl != 0 || ne != 0 {
		t.Fatalf("plugin still tracked after failure: cmds=%d logs=%d exited=%d", n, nl, ne)
	}
	if reaped == nil {
		t.Fatal("plugin process was never started")
	}
	waitClosed(t, reaped, "unhealthy plugin")
}

func TestEnsurePluginRunning_EarlyExitIsReaped(t *testing.T) {
	requireUnixTools(t, "true")
	winHome := t.TempDir()
	t.Setenv("HOME", winHome)
	t.Setenv("USERPROFILE", winHome)

	m := &PluginManager{Registry: &PluginRegistry{}}
	p := &Plugin{Name: "exits", URL: unusedURL(t), Command: []string{"true"}}

	start := time.Now()
	err := m.EnsurePluginRunning(p)
	if err == nil {
		t.Fatal("expected error for plugin that exits immediately")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("early exit should be detected quickly, took %s", time.Since(start))
	}
	if len(m.activeCmds) != 0 || len(m.logFiles) != 0 {
		t.Fatalf("exited plugin still tracked")
	}
}

func TestPluginManagerClose_KillsAndReaps(t *testing.T) {
	requireUnixTools(t, "sleep")
	winHome := t.TempDir()
	t.Setenv("HOME", winHome)
	t.Setenv("USERPROFILE", winHome)

	// Health endpoint that fails (connection reset) for the first two
	// probes, so EnsurePluginRunning spawns the command, then succeeds.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := &PluginManager{Registry: &PluginRegistry{}}
	p := &Plugin{Name: "sleeper", URL: srv.URL, Command: []string{"sleep", "30"}}
	if err := m.EnsurePluginRunning(p); err != nil {
		t.Fatalf("EnsurePluginRunning: %v", err)
	}

	m.mu.Lock()
	cmd := m.activeCmds["sleeper"]
	done := m.exited[cmd]
	logF := m.logFiles["sleeper"]
	m.mu.Unlock()
	if cmd == nil || done == nil {
		t.Fatal("expected spawned plugin to be tracked")
	}
	if logF == nil {
		t.Fatal("expected log file to be tracked")
	}

	m.Close()
	waitClosed(t, done, "Close")
	if cmd.ProcessState == nil {
		t.Error("expected process to be waited on")
	}
	if len(m.activeCmds) != 0 || len(m.logFiles) != 0 || len(m.exited) != 0 {
		t.Error("maps should be reset after Close")
	}
	// The log file must have been closed.
	if _, err := logF.Write([]byte("x")); err == nil {
		t.Error("expected log file to be closed")
	}
}
