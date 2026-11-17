package memory

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// truncatedBody serves a response that announces more bytes than it sends and
// then drops the connection, so reading the body fails midway.
func truncatedBody(t *testing.T, contentType string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: " + contentType + "\r\nContent-Length: 1000\r\n\r\n{\"engine\":")
		_ = buf.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProbeAddress_TruncatedBody(t *testing.T) {
	if probeAddress(truncatedBody(t, "application/json").URL) {
		t.Fatal("a health answer cut off mid-body was accepted")
	}
}

func TestRemoteClient_ContextBodyReadFailure(t *testing.T) {
	c := NewRemoteClient(truncatedBody(t, "application/xml").URL)
	_, err := c.Context("repo")
	wantErr(t, "Context", err, "read context")
}

// Without a home directory there is no config.json or server.pid to read.
func TestRemoteDiscovery_NoHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if a := readConfigServerAddr(); a != "" {
		t.Errorf("readConfigServerAddr = %q", a)
	}
	if a := readPIDServerAddr(); a != "" {
		t.Errorf("readPIDServerAddr = %q", a)
	}
}

// The same server named by config.json and server.pid is probed once.
func TestRemote_DuplicateCandidatesCollapse(t *testing.T) {
	var probes atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable) // never healthy: every candidate is tried
	}))
	defer ts.Close()

	home := isolateRemote(t)
	t.Setenv("GLEANN_REMOTE_ADDR", "")
	cfg := `{"server_addr":"` + ts.URL + `"}`
	if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	pid := `{"addr":"` + strings.TrimPrefix(ts.URL, "http://") + `/"}`
	if err := os.WriteFile(filepath.Join(home, ".gleann", "server.pid"), []byte(pid), 0o644); err != nil {
		t.Fatal(err)
	}
	// The built-in default is probed after these; whatever it answers does
	// not matter here, only how often the configured server was asked.
	_ = Remote()
	if n := probes.Load(); n != 1 {
		t.Fatalf("server probed %d times, want once", n)
	}
}

// fakeSpawn replaces the daemon launcher with one that runs cmd, and records
// the listen address it was asked for.
func fakeSpawn(t *testing.T, cmd func() *exec.Cmd) *string {
	t.Helper()
	var gotAddr string
	old := spawnDaemon
	spawnDaemon = func(_, serveAddr string) *exec.Cmd {
		gotAddr = serveAddr
		return cmd()
	}
	t.Cleanup(func() { spawnDaemon = old })
	return &gotAddr
}

// exitingCommand is a process that starts and ends at once: the test binary
// itself with no tests selected.
func exitingCommand() *exec.Cmd { return exec.Command(os.Args[0], "-test.run=^$") }

func missingBinary(t *testing.T) func() *exec.Cmd {
	return func() *exec.Cmd { return exec.Command(filepath.Join(t.TempDir(), "no-such-binary")) }
}

// deadAddr returns a loopback address nothing listens on.
func deadAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestEnsureDaemon_SpawnsAndAdoptsHealthyServer(t *testing.T) {
	// The "daemon" the spawned command stands for is this test server: it is
	// unknown to Remote() until its address is passed explicitly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gleannHealth(w) }))
	defer srv.Close()

	isolateRemote(t)
	// Remote() only probes this dead address, wherever a developer's own
	// server runs.
	t.Setenv("GLEANN_REMOTE_ADDR", deadAddr(t))
	got := fakeSpawn(t, exitingCommand)
	c, err := EnsureDaemon(srv.URL)
	if err != nil || c == nil || c.base != srv.URL {
		t.Fatalf("EnsureDaemon = %+v, %v", c, err)
	}
	if *got != strings.TrimPrefix(srv.URL, "http://") {
		t.Errorf("daemon asked to listen on %q, want the host:port of %s", *got, srv.URL)
	}
	if again := Remote(); again != c {
		t.Error("the adopted daemon was not cached")
	}
}

func TestEnsureDaemon_SpawnFailureAndTimeout(t *testing.T) {
	isolateRemote(t)
	t.Setenv("GLEANN_REMOTE_ADDR", deadAddr(t))

	fakeSpawn(t, missingBinary(t))
	if c, err := EnsureDaemon("http://127.0.0.1:1"); c != nil || err == nil || !strings.Contains(err.Error(), "auto-spawn daemon") {
		t.Fatalf("EnsureDaemon with an unstartable binary = %+v, %v", c, err)
	}

	// A daemon that never answers: the https scheme is stripped for the
	// listen address and the wait ends with a health error.
	dead := deadAddr(t)
	got := fakeSpawn(t, exitingCommand)
	c, err := EnsureDaemon("https://" + dead)
	if c != nil || err == nil || !strings.Contains(err.Error(), "failed to become healthy") {
		t.Fatalf("EnsureDaemon = %+v, %v; want a health timeout", c, err)
	}
	if *got != dead {
		t.Errorf("daemon asked to listen on %q, want %q", *got, dead)
	}
}

// With no address given the daemon address falls back to the environment,
// then config.json, then the built-in default.
func TestEnsureDaemon_AddressFallbacks(t *testing.T) {
	home := isolateRemote(t)
	got := fakeSpawn(t, missingBinary(t))

	envAddr := deadAddr(t)
	t.Setenv("GLEANN_REMOTE_ADDR", envAddr)
	if _, err := EnsureDaemon(""); err == nil {
		t.Fatal("EnsureDaemon with an unstartable binary succeeded")
	}
	if *got != envAddr {
		t.Errorf("env address: daemon asked to listen on %q, want %q", *got, envAddr)
	}

	// The remaining fallbacks only apply when Remote() finds no server on
	// the built-in default address either.
	t.Setenv("GLEANN_REMOTE_ADDR", "")
	if Remote() != nil {
		t.Skip("a gleann server is running on this machine")
	}

	if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), []byte(`{"server_addr":"localhost:9"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetRemoteForTesting()
	if _, err := EnsureDaemon(""); err == nil {
		t.Fatal("EnsureDaemon with an unstartable binary succeeded")
	}
	if *got != "localhost:9" {
		t.Errorf("config address: daemon asked to listen on %q, want localhost:9", *got)
	}

	if err := os.Remove(filepath.Join(home, ".gleann", "config.json")); err != nil {
		t.Fatal(err)
	}
	ResetRemoteForTesting()
	if _, err := EnsureDaemon(""); err == nil {
		t.Fatal("EnsureDaemon with an unstartable binary succeeded")
	}
	if want := strings.TrimPrefix(DefaultRemoteAddr, "http://"); *got != want {
		t.Errorf("default address: daemon asked to listen on %q, want %q", *got, want)
	}
}
