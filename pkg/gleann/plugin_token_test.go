package gleann

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewPluginTokenIsRandom(t *testing.T) {
	a, err := newPluginToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newPluginToken()
	if len(a) != 64 || a == b {
		t.Fatalf("tokens should be 64 hex chars and unique: %q %q", a, b)
	}
}

func TestTokenFor(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	m := &PluginManager{}
	p := &Plugin{Name: "x", Token: "configured"}
	if got := m.TokenFor(p); got != "configured" {
		t.Fatalf("configured token: got %q", got)
	}
	stored, err := loadOrCreatePluginToken("x")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.TokenFor(p); got != "configured" {
		t.Fatalf("configured token must win over the stored one: got %q", got)
	}
	if got := m.TokenFor(&Plugin{Name: "x"}); got != stored {
		t.Fatalf("stored token: got %q want %q", got, stored)
	}
	if got := (*PluginManager)(nil).TokenFor(p); got != "configured" {
		t.Fatalf("nil manager: got %q", got)
	}
	if got := m.TokenFor(&Plugin{Name: "other"}); got != "" {
		t.Fatalf("unprotected plugin must have no token, got %q", got)
	}
}

func TestProcessRawSendsBearerToken(t *testing.T) {
	var mu sync.Mutex
	var convertAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/convert" {
			mu.Lock()
			convertAuth = r.Header.Get("Authorization")
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"markdown":"ok"}`))
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &PluginManager{Registry: &PluginRegistry{}}
	p := &Plugin{Name: "tok", URL: srv.URL, Token: "s3cret"}
	if _, err := m.processRaw(p, file); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if convertAuth != "Bearer s3cret" {
		t.Fatalf("Authorization = %q, want Bearer s3cret", convertAuth)
	}
}

func TestFetchPluginInfoSendsBearerToken(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"name":"x"}`))
	}))
	defer srv.Close()
	m := &PluginManager{}
	if _, err := m.FetchPluginInfo(&Plugin{Name: "x", URL: srv.URL, Token: "abc"}); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer abc" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestEnsurePluginRunningPassesTokenEnv(t *testing.T) {
	requireUnixTools(t, "sleep")
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	old := pluginStartTimeout
	pluginStartTimeout = 1200 * time.Millisecond
	defer func() { pluginStartTimeout = old }()

	m := &PluginManager{Registry: &PluginRegistry{}}
	p := &Plugin{Name: "tokplug", URL: unusedURL(t), Command: []string{"sleep", "30"}}

	var envTok, mgrTok string
	pluginStartedHook = func(cmd *exec.Cmd, _ <-chan struct{}) {
		for _, kv := range cmd.Env {
			if v, ok := strings.CutPrefix(kv, PluginTokenEnv+"="); ok {
				envTok = v
			}
		}
		mgrTok = m.TokenFor(p)
	}
	defer func() { pluginStartedHook = nil }()

	_ = m.EnsurePluginRunning(p) // never healthy; only the launch matters
	if len(envTok) != 64 {
		t.Fatalf("%s not passed to the process: %q", PluginTokenEnv, envTok)
	}
	if mgrTok != envTok {
		t.Fatalf("manager token %q differs from the one given to the process %q", mgrTok, envTok)
	}
	// Stopping the process drops the in-memory copy but not the stored token.
	if got := m.TokenFor(p); got != envTok {
		t.Fatalf("stored token must survive a stop: got %q want %q", got, envTok)
	}
}

func TestLaunchTokenUsesConfiguredToken(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	m := &PluginManager{}
	tok, err := m.launchToken(&Plugin{Name: "x", Token: "fixed"})
	if err != nil || tok != "fixed" {
		t.Fatalf("got %q, %v", tok, err)
	}
}

func TestExportEnvToken(t *testing.T) {
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", "http://127.0.0.1:9/")
	t.Setenv(AudioPluginTokenEnv, "")
	m := &PluginManager{}
	m.exportEnvToken(&Plugin{Name: "snd", URL: "http://127.0.0.1:9", Token: "t1", Capabilities: []string{CapAudioTranscription}})
	if got := os.Getenv(AudioPluginTokenEnv); got != "t1" {
		t.Fatalf("audio token env = %q", got)
	}
	t.Setenv(AudioPluginTokenEnv, "")
	m.exportEnvToken(&Plugin{Name: "other", URL: "http://127.0.0.1:10", Token: "t2", Capabilities: []string{CapAudioTranscription}})
	if got := os.Getenv(AudioPluginTokenEnv); got != "" {
		t.Fatalf("token exported for a different URL: %q", got)
	}
}

// Two managers stand for two gleann processes (serve + mcp): the one that did
// not launch the plugin must still authenticate against it.
func TestSecondProcessReachesPluginLaunchedByFirst(t *testing.T) {
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
	first := &PluginManager{}
	tok, err := first.launchToken(&Plugin{Name: "shared-plug"})
	if err != nil || tok == "" {
		t.Fatalf("launchToken: %q %v", tok, err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && r.Header.Get("Authorization") != "Bearer "+tok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"markdown":"ok","name":"x"}`))
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := &PluginManager{Registry: &PluginRegistry{}} // never launched anything
	p := &Plugin{Name: "shared-plug", URL: srv.URL}
	if _, err := second.processRaw(p, file); err != nil {
		t.Fatalf("second process: %v", err)
	}
	if _, err := second.FetchPluginInfo(p); err != nil {
		t.Fatalf("second process /info: %v", err)
	}
	// A third launch reuses the stored token instead of rotating it.
	third := &PluginManager{}
	if again, _ := third.launchToken(&Plugin{Name: "shared-plug"}); again != tok {
		t.Fatalf("token rotated: %q != %q", again, tok)
	}
}

func TestPluginTokenFilePermissionsAndName(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("GLEANN_CONFIG_DIR", cfg)
	if _, err := loadOrCreatePluginToken("perm-plug"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "auth", "plugins", "perm-plug.token")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if st.Mode().Perm() != 0o600 {
			t.Errorf("file mode %v, want 0600", st.Mode().Perm())
		}
		if d, _ := os.Stat(filepath.Dir(path)); d.Mode().Perm() != 0o700 {
			t.Errorf("dir mode %v, want 0700", d.Mode().Perm())
		}
	}
	if ents, _ := os.ReadDir(filepath.Dir(path)); len(ents) != 1 {
		t.Errorf("temp files left behind: %v", ents)
	}
	for _, bad := range []string{"../evil", "a/b", "", ".."} {
		if _, err := loadOrCreatePluginToken(bad); err == nil {
			t.Errorf("name %q accepted", bad)
		}
		if got := (&PluginManager{}).TokenFor(&Plugin{Name: bad}); got != "" {
			t.Errorf("TokenFor(%q) = %q", bad, got)
		}
	}
}
