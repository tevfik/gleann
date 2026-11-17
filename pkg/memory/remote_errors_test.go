package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gleannHealth answers the probe the way a gleann server does.
func gleannHealth(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`{"engine":"gleann-go","status":"ok"}`))
}

// isolateRemote points HOME at an empty temp dir and clears the cached
// client, so Remote() only sees what the test sets up.
func isolateRemote(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_API_TOKEN", "")
	ResetRemoteForTesting()
	t.Cleanup(ResetRemoteForTesting)
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// Every REST call reports the server's error, whether it is a JSON error
// envelope or a plain-text body, together with the status code.
func TestRemoteClient_ServerErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"json envelope", `{"error":"store is read-only"}`, "server 500: store is read-only"},
		{"plain text", "  boom  \n", "server 500: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			r := NewRemoteClient(ts.URL + "/")
			check := func(op string, err error) {
				t.Helper()
				if err == nil || err.Error() != tc.want {
					t.Errorf("%s: err = %v, want %q", op, err, tc.want)
				}
			}
			_, err := r.List("")
			check("List", err)
			_, err = r.Stats()
			check("Stats", err)
			_, err = r.Context("s")
			check("Context", err)
			_, err = r.Add(TierLong, "l", "c", nil)
			check("Add", err)
			_, err = r.Forget("x")
			check("Forget", err)
			_, err = r.Clear(TierShort)
			check("Clear", err)
			_, err = r.Compact(0.5)
			check("Compact", err)
		})
	}
}

// A 2xx answer whose body is not the expected JSON is a decode error, not an
// empty success.
func TestRemoteClient_UndecodableReplies(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/blocks" {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()
	r := NewRemoteClient(ts.URL)
	cases := map[string]error{}
	_, cases["decode blocks"] = r.Search("q")
	_, cases["decode stats"] = r.Stats()
	_, cases["decode block"] = r.AddBlock(&Block{Tier: TierLong, Content: "c"})
	_, cases["decode delete"] = r.Forget("id")
	_, cases["decode clear"] = r.Clear("")
	_, cases["decode compact"] = r.Compact(0)
	for want, err := range cases {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

// When the server is gone every call fails with the transport error.
func TestRemoteClient_ServerDown(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close()
	r := NewRemoteClient(url)
	if _, err := r.List(TierLong); err == nil {
		t.Error("List succeeded")
	}
	if _, err := r.Stats(); err == nil {
		t.Error("Stats succeeded")
	}
	if _, err := r.Context(""); err == nil {
		t.Error("Context succeeded")
	}
	if _, err := r.AddBlock(&Block{Content: "c"}); err == nil {
		t.Error("AddBlock succeeded")
	}
	if _, err := r.Forget("x"); err == nil {
		t.Error("Forget succeeded")
	}
	if _, err := r.Clear(""); err == nil {
		t.Error("Clear succeeded")
	}
	if _, err := r.Compact(0); err == nil {
		t.Error("Compact succeeded")
	}
	// An invalid base URL fails while building the DELETE request.
	bad := NewRemoteClient("http://bad host")
	if _, err := bad.Forget("x"); err == nil {
		t.Error("Forget with an invalid URL succeeded")
	}
	if _, err := bad.Clear(""); err == nil {
		t.Error("Clear with an invalid URL succeeded")
	}
}

// AddBlock sends the remaining lifetime of a block that expires in the
// future and the local bearer token; an expired block is sent without one.
func TestRemoteClient_AddBlockExpiryAndToken(t *testing.T) {
	isolateRemote(t)
	t.Setenv("GLEANN_API_TOKEN", "secret-token")
	var bodies []map[string]any
	var auths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		auths = append(auths, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Block{ID: "b1", Content: "c"})
	}))
	defer ts.Close()
	r := NewRemoteClient(ts.URL)
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	for _, exp := range []*time.Time{&future, &past} {
		got, err := r.AddBlock(&Block{Tier: TierShort, Content: "c", ExpiresAt: exp})
		if err != nil || got.ID != "b1" {
			t.Fatalf("AddBlock = %+v, %v", got, err)
		}
	}
	if _, ok := bodies[0]["expires_in"]; !ok {
		t.Error("future expiry not sent as expires_in")
	}
	if _, ok := bodies[1]["expires_in"]; ok {
		t.Error("past expiry sent as expires_in")
	}
	for _, a := range auths {
		if a != "Bearer secret-token" {
			t.Errorf("Authorization = %q", a)
		}
	}
}

// Without GLEANN_REMOTE_ADDR, Remote() finds the server through
// ~/.gleann/config.json first and ~/.gleann/server.pid second, and caches the
// client it found.
func TestRemote_DiscoversFromConfigAndPIDFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			gleannHealth(w)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	t.Run("config", func(t *testing.T) {
		home := isolateRemote(t)
		t.Setenv("GLEANN_REMOTE_ADDR", "")
		cfg := `{"server_addr":"` + ts.URL + `/"}`
		if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		c := Remote()
		if c == nil || c.base != ts.URL {
			t.Fatalf("Remote() = %+v, want client for %s", c, ts.URL)
		}
		if again := Remote(); again != c {
			t.Error("second Remote() within the TTL did not reuse the cached client")
		}
	})
	t.Run("pid file", func(t *testing.T) {
		home := isolateRemote(t)
		t.Setenv("GLEANN_REMOTE_ADDR", "")
		// A broken config.json is ignored.
		if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		pid := `{"addr":"` + strings.TrimPrefix(ts.URL, "http://") + `"}`
		if err := os.WriteFile(filepath.Join(home, ".gleann", "server.pid"), []byte(pid), 0o644); err != nil {
			t.Fatal(err)
		}
		c := Remote()
		if c == nil || c.base != ts.URL {
			t.Fatalf("Remote() = %+v, want client for %s", c, ts.URL)
		}
		// EnsureDaemon returns the reachable server without spawning anything.
		got, err := EnsureDaemon("")
		if err != nil || got == nil || got.base != ts.URL {
			t.Fatalf("EnsureDaemon = %+v, %v", got, err)
		}
	})
	t.Run("unreadable files", func(t *testing.T) {
		home := isolateRemote(t)
		if err := os.WriteFile(filepath.Join(home, ".gleann", "server.pid"), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if a := readConfigServerAddr(); a != "" {
			t.Errorf("readConfigServerAddr with no config = %q", a)
		}
		if a := readPIDServerAddr(); a != "" {
			t.Errorf("readPIDServerAddr with a broken pid file = %q", a)
		}
	})
}

// The probe accepts only a gleann /health answer: a non-200 status, a body
// that is not JSON or an unreachable address are all "no server".
func TestProbeAddress(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"service":"gleann"}`))
	}))
	defer healthy.Close()
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>`))
	}))
	defer garbled.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()

	for _, tc := range []struct {
		addr string
		want bool
	}{
		{healthy.URL, true},
		{ts.URL, false}, // 404
		{garbled.URL, false},
		{downURL, false},
	} {
		if got := probeAddress(tc.addr); got != tc.want {
			t.Errorf("probeAddress(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// With remote access switched off, EnsureDaemon refuses to spawn a server.
func TestEnsureDaemon_Disabled(t *testing.T) {
	isolateRemote(t)
	t.Setenv("GLEANN_REMOTE_ADDR", "off")
	c, err := EnsureDaemon("http://127.0.0.1:1")
	if c != nil || err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("EnsureDaemon = %+v, %v; want disabled error", c, err)
	}
}

// spawnDaemonCmd runs `<bin> serve --addr <addr> --quiet` in its own process
// group, so the daemon outlives the CLI that started it.
func TestSpawnDaemonCmd(t *testing.T) {
	cmd := spawnDaemonCmd("/usr/bin/gleann", "localhost:9999")
	want := []string{"/usr/bin/gleann", "serve", "--addr", "localhost:9999", "--quiet"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", cmd.Args, want)
	}
	if cmd.Process != nil {
		t.Fatal("spawnDaemonCmd started the process")
	}
}
