package tui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	t.Setenv("HOME", t.TempDir())
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
	if !res.RerankEnabled || res.RerankModel != "bge-reranker-v2" {
		t.Errorf("reranker should be enabled: %+v", res)
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
	t.Setenv("HOME", t.TempDir())
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
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", t.TempDir())
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
