package gleann

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fxHome points HOME/USERPROFILE and the config dir at a fresh directory.
func fxHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	return home
}

// startSleeper starts a long sleeping process and kills it on cleanup.
func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	requireUnixTools(t, "sleep")
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func TestLoadPluginsErrors(t *testing.T) {
	t.Run("no home", func(t *testing.T) {
		noHome(t)
		if _, err := LoadPlugins(); err == nil || !strings.Contains(err.Error(), "user home dir") {
			t.Fatalf("err = %v", err)
		}
		if _, err := NewPluginManager(); err == nil {
			t.Fatal("NewPluginManager must surface the registry error")
		}
	})
	t.Run("plugins.json unreadable", func(t *testing.T) {
		home := fxHome(t)
		if err := os.MkdirAll(filepath.Join(home, ".gleann", "plugins.json"), 0o755); err != nil {
			t.Fatal(err) // a directory where the file should be
		}
		if _, err := LoadPlugins(); err == nil || !strings.Contains(err.Error(), "read plugins file") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPluginManagerCloseEdgeCases(t *testing.T) {
	requireUnixTools(t, "sleep", "sh")
	old := pluginStopGrace
	pluginStopGrace = 150 * time.Millisecond
	defer func() { pluginStopGrace = old }()

	// A tracked process that was not started through EnsurePluginRunning has
	// no reaper: Close just kills it. An entry without a process is skipped.
	untracked := startSleeper(t)
	m := &PluginManager{
		activeCmds: map[string]*exec.Cmd{"unstarted": {}, "untracked": untracked},
		logFiles:   map[string]*os.File{"nil-log": nil},
		exited:     map[*exec.Cmd]chan struct{}{},
	}
	m.Close()
	if err := untracked.Wait(); err == nil {
		t.Error("untracked process should have been killed")
	}
	if len(m.activeCmds) != 0 {
		t.Error("Close must forget all processes")
	}

	// A plugin that ignores SIGINT is killed once the grace period ends.
	ready := filepath.Join(t.TempDir(), "ready")
	stubborn := exec.Command("sh", "-c", "trap '' INT; : > '"+ready+"'; while :; do sleep 1; done")
	if err := stubborn.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ { // the trap must be installed before SIGINT arrives
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() { _ = stubborn.Wait(); close(done) }()
	m = &PluginManager{
		activeCmds: map[string]*exec.Cmd{"stubborn": stubborn},
		exited:     map[*exec.Cmd]chan struct{}{stubborn: done},
	}
	start := time.Now()
	m.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stubborn plugin survived Close")
	}
	if time.Since(start) < pluginStopGrace {
		t.Errorf("Close returned before the grace period: %v", time.Since(start))
	}
}

func TestStopPluginLockedGivesUpWaitingForReaper(t *testing.T) {
	old := pluginStopGrace
	pluginStopGrace = 50 * time.Millisecond
	defer func() { pluginStopGrace = old }()
	cmd := startSleeper(t)
	never := make(chan struct{}) // the reaper never reports
	m := &PluginManager{
		activeCmds: map[string]*exec.Cmd{"p": cmd},
		exited:     map[*exec.Cmd]chan struct{}{cmd: never},
	}
	m.mu.Lock()
	start := time.Now()
	m.stopPluginLocked("p")
	m.mu.Unlock()
	if time.Since(start) < pluginStopGrace {
		t.Errorf("returned before the grace period: %v", time.Since(start))
	}
	if len(m.activeCmds) != 0 || len(m.exited) != 0 {
		t.Error("stopped plugin must be forgotten")
	}
}

func TestResolveMultimodalPluginEnv_NilAndDoclingModel(t *testing.T) {
	if a, v := (*PluginManager)(nil).ResolveMultimodalPluginEnv(); a != "" || v != "" {
		t.Errorf("nil manager = %q, %q", a, v)
	}
	if a, v := (&PluginManager{}).ResolveMultimodalPluginEnv(); a != "" || v != "" {
		t.Errorf("manager without registry = %q, %q", a, v)
	}

	home := fxHome(t)
	t.Setenv("GLEANN_DOCLING_VLM", "") // registers the restore of the value we set below
	t.Setenv("GLEANN_AUDIO_PLUGIN_URL", "x")
	t.Setenv("GLEANN_VISION_PLUGIN_URL", "x")
	model := filepath.Join(home, ".gleann", "models", "granite-docling", "granite_docling_bitnet_dense.eifm")
	fileAt(t, model)
	(&PluginManager{Registry: &PluginRegistry{}}).ResolveMultimodalPluginEnv()
	if got := os.Getenv("GLEANN_DOCLING_VLM"); got != "1" {
		t.Errorf("GLEANN_DOCLING_VLM = %q, want 1 when the model is installed", got)
	}
}

func TestFetchPluginInfoRequestErrors(t *testing.T) {
	m := &PluginManager{Registry: &PluginRegistry{}}
	if _, err := m.FetchPluginInfo(&Plugin{URL: "http://host\x7f"}); err == nil {
		t.Error("a URL with a control character must be rejected")
	}
	if _, err := m.FetchPluginInfo(&Plugin{URL: unusedURL(t)}); err == nil {
		t.Error("an unreachable plugin must be an error")
	}
}

func TestEnsurePluginRunning(t *testing.T) {
	t.Run("healthy on the second probe", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				if hj, ok := w.(http.Hijacker); ok {
					conn, _, _ := hj.Hijack()
					conn.Close()
					return
				}
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		m := &PluginManager{Registry: &PluginRegistry{}}
		if err := m.EnsurePluginRunning(&Plugin{Name: "flaky", URL: srv.URL}); err != nil {
			t.Fatalf("err = %v", err)
		}
		if calls.Load() < 2 {
			t.Errorf("health probed %d times, want the locked re-check", calls.Load())
		}
	})
	t.Run("a busy plugin that is still alive is left alone", func(t *testing.T) {
		cmd := startSleeper(t)
		m := &PluginManager{Registry: &PluginRegistry{}, activeCmds: map[string]*exec.Cmd{"busy": cmd}}
		if err := m.EnsurePluginRunning(&Plugin{Name: "busy", URL: unusedURL(t), Command: []string{"never-run"}}); err != nil {
			t.Fatalf("err = %v", err)
		}
		if m.activeCmds["busy"] != cmd {
			t.Error("the running process must not be replaced")
		}
	})
	t.Run("down without a start command", func(t *testing.T) {
		m := &PluginManager{Registry: &PluginRegistry{}}
		err := m.EnsurePluginRunning(&Plugin{Name: "gone", URL: unusedURL(t)})
		if err == nil || !strings.Contains(err.Error(), "no auto-start command") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("token cannot be created", func(t *testing.T) {
		home := fxHome(t)
		fileAt(t, filepath.Join(home, ".gleann", "auth")) // auth must be a directory
		m := &PluginManager{Registry: &PluginRegistry{}}
		err := m.EnsurePluginRunning(&Plugin{Name: "tok", URL: unusedURL(t), Command: []string{"never-run"}})
		if err == nil || !strings.Contains(err.Error(), "failed to auto-start plugin tok") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("command cannot be started", func(t *testing.T) {
		fxHome(t)
		m := &PluginManager{Registry: &PluginRegistry{}}
		missing := filepath.Join(t.TempDir(), "no-such-binary")
		err := m.EnsurePluginRunning(&Plugin{Name: "nostart", URL: unusedURL(t), Command: []string{missing}})
		if err == nil || !strings.Contains(err.Error(), "failed to auto-start plugin nostart") {
			t.Fatalf("err = %v", err)
		}
		if len(m.activeCmds) != 0 || len(m.logFiles) != 0 {
			t.Error("a plugin that never started must not be tracked")
		}
		m.tokMu.Lock()
		_, kept := m.tokens["nostart"]
		m.tokMu.Unlock()
		if kept {
			t.Error("the in-memory token of a failed start must be dropped")
		}
	})
	t.Run("log file cannot be opened", func(t *testing.T) {
		home := fxHome(t)
		// A directory where the plugin's log file belongs: logging is skipped
		// but the failed start is still reported without leaking a handle.
		if err := os.MkdirAll(filepath.Join(home, ".gleann", "logs", "nolog.log"), 0o755); err != nil {
			t.Fatal(err)
		}
		m := &PluginManager{Registry: &PluginRegistry{}}
		missing := filepath.Join(t.TempDir(), "no-such-binary")
		err := m.EnsurePluginRunning(&Plugin{Name: "nolog", URL: unusedURL(t), Command: []string{missing}})
		if err == nil || !strings.Contains(err.Error(), "failed to auto-start plugin nolog") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestProcessRawErrors(t *testing.T) {
	t.Run("plugin not running", func(t *testing.T) {
		m := &PluginManager{Registry: &PluginRegistry{}}
		_, err := m.processRaw(&Plugin{Name: "off", URL: unusedURL(t)}, "irrelevant")
		if err == nil || !strings.Contains(err.Error(), "ensure plugin off running") {
			t.Fatalf("err = %v", err)
		}
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	m := &PluginManager{Registry: &PluginRegistry{}}
	t.Run("directory instead of a file", func(t *testing.T) {
		_, err := m.processRaw(&Plugin{Name: "ok", URL: srv.URL}, t.TempDir())
		if err == nil {
			t.Fatal("reading a directory as the upload must fail")
		}
		if runtime.GOOS != "windows" && !strings.Contains(err.Error(), "copy file to form") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unusable endpoint", func(t *testing.T) {
		fxHome(t)
		cmd := startSleeper(t)
		mm := &PluginManager{Registry: &PluginRegistry{}, activeCmds: map[string]*exec.Cmd{"bad": cmd}}
		file := filepath.Join(t.TempDir(), "in.txt")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// The process is alive so the health check passes, but the URL cannot
		// be turned into a request.
		_, err := mm.processRaw(&Plugin{Name: "bad", URL: "http://host\x7f"}, file)
		if err == nil || !strings.Contains(err.Error(), "new http request") {
			t.Fatalf("err = %v", err)
		}
	})
}

// ── plugin stop grace ────────────────────────────────────────────────────

// startShellPlugin runs script with `sh -c`, passing args as $1, $2, ….
// It returns the command, a channel closed once the process has been reaped,
// and a channel that receives cmd.Wait's error. The process is killed on
// cleanup if the test leaves it running.
func startShellPlugin(t *testing.T, script string, args ...string) (*exec.Cmd, chan struct{}, <-chan error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"-c", script, "sh"}, args...)...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		waitErr <- err
		close(done)
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, done, waitErr
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func managerForPlugin(cmd *exec.Cmd, done chan struct{}) *PluginManager {
	return &PluginManager{
		activeCmds: map[string]*exec.Cmd{"p": cmd},
		exited:     map[*exec.Cmd]chan struct{}{cmd: done},
	}
}

// TestStopPluginLockedLetsPluginFinish guards the restart path: a plugin that
// traps SIGINT and needs a moment to finish its work must not be SIGKILLed the
// instant stopPluginLocked is called.
func TestStopPluginLockedLetsPluginFinish(t *testing.T) {
	requireUnixTools(t, "sh", "sleep")
	t.Setenv("GLEANN_PLUGIN_STOP_GRACE", "")
	old := pluginStopGrace
	pluginStopGrace = 5 * time.Second
	defer func() { pluginStopGrace = old }()

	dir := t.TempDir()
	ready, marker := filepath.Join(dir, "ready"), filepath.Join(dir, "marker")
	cmd, done, waitErr := startShellPlugin(t, `ready=$1; marker=$2
trap 'sleep 0.3; : > "$marker"; exit 0' INT
: > "$ready"
while :; do sleep 0.05; done`, ready, marker)
	waitForFile(t, ready)

	m := managerForPlugin(cmd, done)
	m.mu.Lock()
	m.stopPluginLocked("p")
	m.mu.Unlock()

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("plugin was killed before its SIGINT handler finished: %v", err)
	}
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("plugin exited uncleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plugin was not reaped")
	}
	if len(m.activeCmds) != 0 || len(m.exited) != 0 {
		t.Error("stopped plugin must be forgotten")
	}
}

// TestCloseLetsPluginFinishAfterLongGrace proves the shipped default grace is
// no longer the 2s that cut off an in-flight conversion: Close must let a
// plugin that needs 2.2s after SIGINT exit cleanly.
func TestCloseLetsPluginFinishAfterLongGrace(t *testing.T) {
	requireUnixTools(t, "sh", "sleep")
	if pluginStopGrace <= 2*time.Second {
		t.Fatalf("default plugin stop grace = %s, want more than the old 2s", pluginStopGrace)
	}
	t.Setenv("GLEANN_PLUGIN_STOP_GRACE", "")

	dir := t.TempDir()
	ready, marker := filepath.Join(dir, "ready"), filepath.Join(dir, "marker")
	cmd, done, waitErr := startShellPlugin(t, `ready=$1; marker=$2
trap 'sleep 2.2; : > "$marker"; exit 0' INT
: > "$ready"
while :; do sleep 0.05; done`, ready, marker)
	waitForFile(t, ready)

	m := managerForPlugin(cmd, done)
	m.Close()

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("Close killed the plugin before its SIGINT handler finished: %v", err)
	}
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("plugin exited uncleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plugin was not reaped")
	}
	if len(m.activeCmds) != 0 || len(m.exited) != 0 {
		t.Error("Close must forget every stopped plugin")
	}
}

// stopShellPluginWithGrace starts the standard SIGINT-trapping fake plugin,
// calls stopPluginLocked and reports the marker path, how long the stop took
// and the exit error.
func stopShellPluginWithGrace(t *testing.T, work string) (string, time.Duration, error) {
	t.Helper()
	dir := t.TempDir()
	ready, marker := filepath.Join(dir, "ready"), filepath.Join(dir, "marker")
	cmd, done, waitErr := startShellPlugin(t, `ready=$1; marker=$2; work=$3
trap 'sleep "$work"; : > "$marker"; exit 0' INT
: > "$ready"
while :; do sleep 0.05; done`, ready, marker, work)
	waitForFile(t, ready)

	m := managerForPlugin(cmd, done)
	start := time.Now()
	m.mu.Lock()
	m.stopPluginLocked("p")
	m.mu.Unlock()
	elapsed := time.Since(start)

	select {
	case err := <-waitErr:
		return marker, elapsed, err
	case <-time.After(10 * time.Second):
		t.Fatal("plugin was not reaped")
		return marker, elapsed, nil
	}
}

// TestPluginStopGraceEnvOverride checks GLEANN_PLUGIN_STOP_GRACE: a longer
// value gives the plugin time to finish, a tiny value force-kills it, and
// invalid values fall back to the built-in grace.
func TestPluginStopGraceEnvOverride(t *testing.T) {
	requireUnixTools(t, "sh", "sleep")
	old := pluginStopGrace
	pluginStopGrace = 10 * time.Second // fallback must be long enough for 200ms
	defer func() { pluginStopGrace = old }()

	t.Run("long value lets the plugin finish", func(t *testing.T) {
		t.Setenv("GLEANN_PLUGIN_STOP_GRACE", "2s")
		marker, _, err := stopShellPluginWithGrace(t, "0.3")
		if err != nil {
			t.Errorf("plugin exited uncleanly under a 2s override: %v", err)
		}
		if _, statErr := os.Stat(marker); statErr != nil {
			t.Errorf("plugin was killed before finishing under a 2s override: %v", statErr)
		}
	})
	t.Run("short value force-kills", func(t *testing.T) {
		t.Setenv("GLEANN_PLUGIN_STOP_GRACE", "100ms")
		marker, _, err := stopShellPluginWithGrace(t, "1")
		if err == nil {
			t.Error("plugin should have been killed before its 1s of work finished")
		}
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Error("the SIGINT handler finished although the grace period was 100ms")
		}
	})
	for _, bad := range []string{"bogus", "0s", "-1s"} {
		t.Run("invalid "+bad+" falls back", func(t *testing.T) {
			t.Setenv("GLEANN_PLUGIN_STOP_GRACE", bad)
			marker, _, err := stopShellPluginWithGrace(t, "0.2")
			if err != nil {
				t.Errorf("invalid value %q must fall back to the built-in grace, exit: %v", bad, err)
			}
			if _, statErr := os.Stat(marker); statErr != nil {
				t.Errorf("invalid value %q must fall back to the built-in grace: %v", bad, statErr)
			}
		})
	}
}

// TestStopPluginLockedKillsStubbornPlugin keeps the force-kill safety net: a
// plugin that ignores SIGINT is still killed and forgotten after the grace.
func TestStopPluginLockedKillsStubbornPlugin(t *testing.T) {
	requireUnixTools(t, "sh", "sleep")
	t.Setenv("GLEANN_PLUGIN_STOP_GRACE", "")
	old := pluginStopGrace
	pluginStopGrace = 100 * time.Millisecond
	defer func() { pluginStopGrace = old }()

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, done, waitErr := startShellPlugin(t, `ready=$1
trap '' INT
: > "$ready"
while :; do sleep 0.05; done`, ready)
	waitForFile(t, ready)

	m := managerForPlugin(cmd, done)
	start := time.Now()
	m.mu.Lock()
	m.stopPluginLocked("p")
	m.mu.Unlock()
	if time.Since(start) < 100*time.Millisecond {
		t.Errorf("stopPluginLocked returned before the grace period: %v", time.Since(start))
	}
	select {
	case err := <-waitErr:
		if err == nil {
			t.Error("a plugin that ignores SIGINT must be killed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stubborn plugin survived stopPluginLocked")
	}
	if len(m.activeCmds) != 0 || len(m.exited) != 0 || len(m.logFiles) != 0 {
		t.Error("stopped plugin must be forgotten")
	}
}

// ── plugin tokens ─────────────────────────────────────────────────────────

func TestPluginTokenErrors(t *testing.T) {
	t.Run("config dir unknown", func(t *testing.T) {
		noHome(t)
		t.Setenv("GLEANN_CONFIG_DIR", "")
		if _, err := pluginTokenPath("p"); err == nil || !strings.Contains(err.Error(), "locating gleann home") {
			t.Fatalf("err = %v", err)
		}
		if _, err := loadOrCreatePluginToken("p"); err == nil {
			t.Fatal("token creation without a home must fail")
		}
	})
	t.Run("invalid plugin name has no token", func(t *testing.T) {
		fxHome(t)
		m := &PluginManager{}
		if got := m.TokenFor(&Plugin{Name: "../evil"}); got != "" {
			t.Errorf("TokenFor = %q", got)
		}
		if _, err := m.launchToken(&Plugin{Name: "../evil"}); err == nil {
			t.Error("launchToken must reject an unsafe name")
		}
	})
	t.Run("token directory cannot be created", func(t *testing.T) {
		home := fxHome(t)
		fileAt(t, filepath.Join(home, ".gleann", "auth"))
		if _, err := loadOrCreatePluginToken("p"); err == nil || !strings.Contains(err.Error(), "creating") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("token file cannot be created in a read-only directory", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("permission bits are not enforced here")
		}
		home := fxHome(t)
		dir := filepath.Join(home, ".gleann", "auth", "plugins")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(dir, 0o700)
		if _, err := loadOrCreatePluginToken("p"); err == nil || !strings.Contains(err.Error(), "writing plugin token") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an empty token file is replaced", func(t *testing.T) {
		home := fxHome(t)
		path := filepath.Join(home, ".gleann", "auth", "plugins", "p.token")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		tok, err := loadOrCreatePluginToken("p")
		if err != nil || len(tok) != 64 {
			t.Fatalf("token = %q, %v", tok, err)
		}
		if again, _ := loadOrCreatePluginToken("p"); again != tok {
			t.Error("the published token must be stable")
		}
	})
	t.Run("token path is a directory", func(t *testing.T) {
		home := fxHome(t)
		path := filepath.Join(home, ".gleann", "auth", "plugins", "p.token")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := loadOrCreatePluginToken("p"); err == nil || !strings.Contains(err.Error(), "publishing plugin token") {
			t.Fatalf("err = %v", err)
		}
	})
}
