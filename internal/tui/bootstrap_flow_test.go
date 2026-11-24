package tui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// captureStdout runs fn with os.Stdout/os.Stderr redirected and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	func() {
		defer func() {
			os.Stdout, os.Stderr = oldOut, oldErr
			w.Close()
		}()
		fn()
	}()
	return <-done
}

// isolateHome gives the test its own HOME (and USERPROFILE, which is what
// os.UserHomeDir reads on Windows) so tests cannot see each other's config.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func fakeOllama(t *testing.T, tags string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		fmt.Fprint(w, tags)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBootstrap_DetectsOllamaAndPicksModels(t *testing.T) {
	isolateHome(t)
	srv := fakeOllama(t, `{"models":[
		{"name":"llama3:8b"},{"name":"gemma3:4b"},
		{"name":"nomic-embed-text"},{"name":"bge-m3"},
		{"name":"bge-reranker-v2"}]}`, 200)

	var res *OnboardResult
	var err error
	out := captureStdout(t, func() { res, err = BootstrapWithAPIKey(srv.URL, "", "code-m", "doc-m") })
	if err != nil {
		t.Fatal(err)
	}
	if res.EmbeddingModel != "bge-m3" || res.LLMModel != "gemma3:4b" {
		t.Errorf("preferred models not picked: emb=%s llm=%s", res.EmbeddingModel, res.LLMModel)
	}
	if res.RerankEnabled || res.RerankModel != "" {
		t.Errorf("an Ollama reranker has no rerank API and must not be enabled: %+v", res)
	}
	if !strings.Contains(out, "no rerank API") {
		t.Errorf("no hint about the reranker:\n%s", out)
	}
	if res.CodeEmbeddingModel != "code-m" || res.DocEmbeddingModel != "doc-m" {
		t.Errorf("dual models lost: %q %q", res.CodeEmbeddingModel, res.DocEmbeddingModel)
	}
	for _, want := range []string{"Ollama is running", "Found 5 models", "Code Model: code-m", "Docs Model: doc-m", "Configuration saved"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	saved := LoadSavedConfig()
	if saved == nil || !saved.Completed || saved.LLMModel != "gemma3:4b" || saved.OllamaHost != srv.URL {
		t.Errorf("config not persisted: %+v", saved)
	}
}

func TestBootstrap_Defaults(t *testing.T) {
	isolateHome(t)
	t.Run("unreachable keeps defaults", func(t *testing.T) {
		var res *OnboardResult
		var err error
		out := captureStdout(t, func() { res, err = BootstrapWithAPIKey("http://127.0.0.1:1", "k", "", "") })
		if err != nil {
			t.Fatal(err)
		}
		if res.EmbeddingModel != "bge-m3" || res.LLMModel != "gemma3:4b" || res.OllamaAPIKey != "k" {
			t.Errorf("defaults wrong: %+v", res)
		}
		if res.CodeEmbeddingModel != "" || res.DocEmbeddingModel != "" {
			t.Error("empty dual-model args must not set models")
		}
		if !strings.Contains(out, "not reachable") {
			t.Errorf("missing hint:\n%s", out)
		}
	})
	t.Run("no models listed", func(t *testing.T) {
		srv := fakeOllama(t, `{"models":[]}`, 200)
		var res *OnboardResult
		out := captureStdout(t, func() { res, _ = BootstrapWithAPIKey(srv.URL, "") })
		if res.LLMModel != "gemma3:4b" || !strings.Contains(out, "Could not list models") {
			t.Errorf("%+v\n%s", res, out)
		}
	})
	t.Run("server error is unreachable", func(t *testing.T) {
		srv := fakeOllama(t, `oops`, 500)
		if ollamaReachable(srv.URL, "") {
			t.Error("HTTP 500 must not count as reachable")
		}
	})
}

func TestBootstrap_SaveFailure(t *testing.T) {
	home := isolateHome(t)
	// A regular file where ~/.gleann should be makes SaveConfig fail.
	if err := os.WriteFile(filepath.Join(home, ".gleann"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	captureStdout(t, func() { _, err = BootstrapWithAPIKey("http://127.0.0.1:1", "") })
	if err == nil || !strings.Contains(err.Error(), "save config") {
		t.Errorf("want save config error, got %v", err)
	}
}

func TestCheckSetupAndHint(t *testing.T) {
	isolateHome(t)
	var ok bool
	out := captureStdout(t, func() { ok = CheckSetup() })
	if ok || !strings.Contains(out, "not configured") {
		t.Errorf("unconfigured: ok=%v\n%s", ok, out)
	}
	if hint := captureStdout(t, PrintSetupHint); !strings.Contains(hint, "setup --bootstrap") {
		t.Errorf("hint expected when unconfigured: %q", hint)
	}

	srv := fakeOllama(t, `{"models":[]}`, 200)
	if err := SaveConfig(OnboardResult{Completed: true, EmbeddingModel: "e", EmbeddingProvider: "ollama",
		LLMModel: "l", LLMProvider: "ollama", OllamaHost: srv.URL, CodeEmbeddingModel: "cm", DocEmbeddingModel: "dm"}); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { ok = CheckSetup() })
	if !ok {
		t.Fatalf("configured, got false:\n%s", out)
	}
	for _, want := range []string{"is configured", "Code Model: cm", "Docs Model: dm", "reachable at " + srv.URL} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if hint := captureStdout(t, PrintSetupHint); hint != "" {
		t.Errorf("no hint when configured: %q", hint)
	}

	_ = SaveConfig(OnboardResult{Completed: true, OllamaHost: "http://127.0.0.1:1"})
	out = captureStdout(t, func() { CheckSetup() })
	if !strings.Contains(out, "not reachable") {
		t.Errorf("want not reachable:\n%s", out)
	}
}

func TestConfigErrorUnwrap(t *testing.T) {
	inner := errors.New("inner")
	ce := &ConfigError{Path: "p", Err: inner}
	if !errors.Is(ce, inner) {
		t.Error("ConfigError must unwrap to its cause")
	}
}

func TestBootstrap_ExplicitOptionsWin(t *testing.T) {
	isolateHome(t)
	srv := fakeOllama(t, `{"models":[{"name":"gemma3:4b"},{"name":"bge-m3"}]}`, 200)
	var res *OnboardResult
	var err error
	captureStdout(t, func() {
		res, err = Bootstrap(BootstrapOptions{OllamaHost: srv.URL, Provider: "llamacpp", Model: "my-embed", LLMProvider: "openai", LLMModel: "my-chat"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.EmbeddingModel != "my-embed" || res.EmbeddingProvider != "llamacpp" || res.LLMModel != "my-chat" || res.LLMProvider != "openai" {
		t.Errorf("explicit options lost: %+v", res)
	}
}

func TestBootstrap_RefusesCompletedConfigWithoutForce(t *testing.T) {
	isolateHome(t)
	captureStdout(t, func() {
		if _, err := Bootstrap(BootstrapOptions{OllamaHost: "http://127.0.0.1:1", Model: "first"}); err != nil {
			t.Fatal(err)
		}
		if _, err := Bootstrap(BootstrapOptions{OllamaHost: "http://127.0.0.1:1", Model: "second"}); !errors.Is(err, ErrConfigExists) {
			t.Fatalf("err = %v, want ErrConfigExists", err)
		}
		if got := LoadSavedConfig().EmbeddingModel; got != "first" {
			t.Fatalf("refused bootstrap changed the config: %q", got)
		}
		if _, err := Bootstrap(BootstrapOptions{OllamaHost: "http://127.0.0.1:1", Model: "second", Force: true}); err != nil {
			t.Fatal(err)
		}
		if got := LoadSavedConfig().EmbeddingModel; got != "second" {
			t.Fatalf("force did not replace: %q", got)
		}
	})
}

func TestBootstrap_ForceKeepsOlderBackup(t *testing.T) {
	isolateHome(t)
	captureStdout(t, func() {
		for _, m := range []string{"one", "two", "three"} {
			if _, err := Bootstrap(BootstrapOptions{OllamaHost: "http://127.0.0.1:1", Model: m, Force: true}); err != nil {
				t.Fatal(err)
			}
		}
	})
	baks, _ := filepath.Glob(configPath() + ".bak*")
	if len(baks) != 2 {
		t.Fatalf("want 2 backups (the second force keeps the first), got %v", baks)
	}
	first, err := os.ReadFile(configPath() + ".bak")
	if err != nil || !strings.Contains(string(first), `"one"`) {
		t.Fatalf("the original backup was overwritten: %v %s", err, first)
	}
	if fi, _ := os.Stat(baks[0]); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("backup mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestBackupConfigAt_SameSecondCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("gen%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := backupConfigAt(path, now); err != nil {
			t.Fatalf("backup %d: %v", i, err)
		}
	}
	want := map[string]string{
		path + ".bak":                   "gen0",
		path + ".bak-20261124-100000":   "gen1",
		path + ".bak-20261124-100000-1": "gen2",
		path + ".bak-20261124-100000-2": "gen3",
	}
	for name, content := range want {
		got, err := os.ReadFile(name)
		if err != nil || string(got) != content {
			t.Errorf("%s = %q (%v), want %q", name, got, err, content)
		}
	}
}
