package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/tui"
)

// d1WriteConfig writes ~/.gleann/config.json of the environment from the
// given fields (completed is set unless the map says otherwise).
func d1WriteConfig(t *testing.T, e *cliEnv, fields map[string]any) {
	t.Helper()
	if _, ok := fields["completed"]; !ok {
		fields["completed"] = true
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "config.json"), string(data))
}

func d1WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// d1Script writes an executable shell script (Unix only).
func d1Script(t *testing.T, path, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script plugin binaries need a Unix shell")
	}
	d1WriteFile(t, path, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f *fakeOllama) d1SetModels(models ...string) {
	f.mu.Lock()
	f.models = models
	f.mu.Unlock()
}

func TestCLID1DoctorWithoutConfigFails(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("doctor")
	wantExit(t, r, 1)
	mustContain(t, "doctor", r.Out,
		"gleann doctor — system health check",
		"No config found",
		"Ollama reachable at "+e.Ollama.URL+" (no API key)",
		"Embedding model 'bge-m3' available",
		"LLM model 'llama3.2:3b' not found — run: ollama pull llama3.2:3b",
		"No indexes found",
		"Index directory:",
		"No plugins registered",
		"Run 'gleann setup --bootstrap'")
	if e.Ollama.count("/api/tags") != 2 {
		t.Errorf("doctor asked /api/tags %d times, want 2 (reachability + models)", e.Ollama.count("/api/tags"))
	}
}

func TestCLID1DoctorRejectsFlags(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("doctor", "--bogus")
	if r.Code == 0 {
		t.Fatalf("doctor --bogus succeeded: %s", r.combined())
	}
}

func TestCLID1DoctorMalformedConfig(t *testing.T) {
	e := newCLIEnv(t)
	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "config.json"), "{not json")
	r := e.run("doctor")
	wantExit(t, r, 1)
	mustContain(t, "doctor", r.Out, "Config is malformed")
}

func TestCLID1DoctorHealthyWithIndexesAndPlugins(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("good")
	e.Ollama.d1SetModels("bge-m3:latest", "qwen3:latest", "bge-reranker:latest")
	d1WriteConfig(t, e, map[string]any{
		"ollama_host": e.Ollama.URL, "embedding_provider": "ollama", "embedding_model": "bge-m3",
		"llm_provider": "ollama", "llm_model": "qwen3", "index_dir": e.IndexDir,
		"rerank_enabled": true, "rerank_model": "bge-reranker",
	})
	// A pending sync backup and an unimported v1.9 memory store.
	if err := os.MkdirAll(indexing.SyncBackupPath(e.IndexDir, "good"), 0o755); err != nil {
		t.Fatal(err)
	}
	d1WriteFile(t, filepath.Join(e.IndexDir, "old_memory", "data.kz"), "kuzu")

	present := filepath.Join(e.Home, "bin", "gleann-plugin-sound")
	d1WriteFile(t, present, "binary")
	reg := map[string]any{"plugins": []map[string]any{
		{"name": "gleann-plugin-sound", "command": []string{present}},
		{"name": "missing", "command": []string{filepath.Join(e.Home, "nope", "bin")}},
		{"name": "remote", "url": "http://127.0.0.1:1"},
		{"name": "bare"},
	}}
	data, _ := json.Marshal(reg)
	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "plugins.json"), string(data))

	r := e.run("doctor")
	wantExit(t, r, 0)
	mustContain(t, "doctor", r.Out,
		"Config loaded ("+filepath.Join(e.Home, ".gleann", "config.json")+")",
		"Embedding model 'bge-m3' available",
		"LLM model 'qwen3' available",
		"Reranker model 'bge-reranker' available",
		"1 index(es) in "+e.IndexDir,
		"good — ",
		`Index "good": a sync left a rollback copy`,
		`Entities of index "old" are in an old Kùzu store`,
		"Index storage:",
		"4 plugin(s) registered",
		"Plugin 'gleann-plugin-sound' binary exists",
		"Plugin 'missing' binary not found",
		"Plugin 'remote' configured via URL (http://127.0.0.1:1)",
		"Plugin 'bare' registered",
		"Summary: ")
	mustNotContain(t, "doctor", r.Out, "❌")
}

func TestCLID1DoctorOldFormatAndMigrationLeftovers(t *testing.T) {
	e := newCLIEnv(t)
	cmdMigrateFixture(t, e.IndexDir, "legacy", false)
	cmdMigrateFixture(t, e.IndexDir, "moved", false)
	// cmdMigrateFixture points HOME elsewhere; restore the environment's.
	t.Setenv("HOME", e.Home)
	t.Setenv("USERPROFILE", e.Home)
	if r := e.run("index", "migrate", "moved"); r.Code != 0 {
		t.Fatalf("migrate: %s", r.combined())
	}
	d1WriteConfig(t, e, map[string]any{"ollama_host": e.Ollama.URL, "embedding_provider": "ollama",
		"llm_provider": "ollama", "llm_model": "llama3.2", "index_dir": e.IndexDir})

	r := e.run("doctor")
	mustContain(t, "doctor", r.Out,
		"2 index(es)",
		`Index "legacy": `,
		"gleann index migrate legacy",
		`Index "moved": `, "old-format file(s) left after the migration",
		"gleann index migrate moved --cleanup")
	mustNotContain(t, "doctor", r.Out, "repair it with: gleann index repair legacy")
}

func TestCLID1DoctorOllamaAuthAndUnreachable(t *testing.T) {
	e := newCLIEnv(t)
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer auth.Close()

	// Ollama used and answering 401: a warning about the key plus a failure.
	d1WriteConfig(t, e, map[string]any{"ollama_host": strings.TrimPrefix(auth.URL, "http://"),
		"embedding_provider": "ollama", "llm_provider": "ollama", "index_dir": e.IndexDir})
	r := e.run("doctor")
	wantExit(t, r, 1)
	mustContain(t, "doctor 401", r.Out, "requires authentication (no API key)", "Ollama returned HTTP 401 at "+auth.URL)

	// Not used: only optional warnings.
	d1WriteConfig(t, e, map[string]any{"ollama_host": auth.URL, "embedding_provider": "openai",
		"llm_provider": "openai", "index_dir": e.IndexDir, "openai_api_key": "k"})
	r = e.run("doctor")
	mustContain(t, "doctor optional 401", r.Out, "Ollama returned HTTP 401 at "+auth.URL+" (optional)")

	// Unreachable.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	d1WriteConfig(t, e, map[string]any{"ollama_host": deadURL, "embedding_provider": "openai",
		"llm_provider": "anthropic", "index_dir": e.IndexDir})
	r = e.run("doctor")
	mustContain(t, "doctor unreachable optional", r.Out,
		"Cannot reach Ollama at "+deadURL+" (optional — current providers: emb=openai, llm=anthropic)")

	d1WriteConfig(t, e, map[string]any{"ollama_host": deadURL, "embedding_provider": "ollama",
		"llm_provider": "ollama", "index_dir": e.IndexDir})
	r = e.run("doctor")
	wantExit(t, r, 1)
	mustContain(t, "doctor unreachable", r.Out, "Cannot reach Ollama at "+deadURL+" — is it running?", "Fix: ollama serve")
}

func TestCLID1DoctorCheckModelsProviders(t *testing.T) {
	e := newCLIEnv(t)
	models := filepath.Join(e.Home, ".gleann", "models")
	d1WriteFile(t, filepath.Join(models, "emb.gguf"), "gguf")
	d1WriteFile(t, filepath.Join(models, "rr.gguf"), "gguf")

	collect := func(cfg map[string]any) (oks, warns, fails []string) {
		data, _ := json.Marshal(cfg)
		d1WriteFile(t, filepath.Join(e.Home, ".gleann", "config.json"), string(data))
		saved, err := tui.LoadSavedConfigChecked()
		if err != nil {
			t.Fatal(err)
		}
		checkModels(saved, e.Ollama.URL,
			func(s string) { oks = append(oks, s) },
			func(s string) { warns = append(warns, s) },
			func(s string) { fails = append(fails, s) })
		return
	}
	join := func(ss []string) string { return strings.Join(ss, "\n") }

	oks, warns, fails := collect(map[string]any{"embedding_provider": "llamacpp", "embedding_model": "emb.gguf",
		"llm_provider": "llamacpp", "llm_model": "missing.gguf", "rerank_enabled": true, "rerank_model": "rr.gguf"})
	mustContain(t, "local ok", join(oks), "Embedding model 'emb.gguf' available (local file)", "Reranker model 'rr.gguf' available (local file)")
	mustContain(t, "local fail", join(fails), "LLM model file not found at "+filepath.Join(models, "missing.gguf"))
	if len(warns) != 0 {
		t.Errorf("unexpected warnings %v", warns)
	}

	oks, warns, fails = collect(map[string]any{"embedding_provider": "eif", "embedding_model": "gone.eifm",
		"llm_provider": "eif", "llm_model": "emb.gguf", "rerank_enabled": true, "rerank_model": "gone.gguf"})
	mustContain(t, "eif", join(fails), "Embedding model file not found")
	mustContain(t, "eif llm", join(oks), "LLM model 'emb.gguf' available (local file)")
	mustContain(t, "eif rerank", join(warns), "Reranker model file not found")

	oks, _, _ = collect(map[string]any{"llm_provider": "openai", "llm_model": "gpt", "openai_base_url": "http://x"})
	mustContain(t, "openai", join(oks), "LLM model 'gpt' via OpenAI-compatible endpoint (http://x, no API key)")
	oks, _, _ = collect(map[string]any{"llm_provider": "openai", "llm_model": "gpt", "openai_api_key": "k"})
	mustContain(t, "openai key", join(oks), "(https://api.openai.com, API key configured)")
	oks, warns, _ = collect(map[string]any{"llm_provider": "anthropic", "llm_model": "claude", "rerank_enabled": true, "rerank_model": "nope"})
	mustContain(t, "anthropic", join(oks), "LLM model 'claude' via Anthropic API")
	mustContain(t, "rerank missing", join(warns), "Reranker model 'nope' not found")

	// An unparsable model list.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer bad.Close()
	var w []string
	checkModels(nil, bad.URL, func(string) {}, func(s string) { w = append(w, s) }, func(string) {})
	mustContain(t, "bad tags", join(w), "Cannot parse Ollama model list")
	// Unreachable: no output at all.
	bad.Close()
	called := false
	checkModels(nil, bad.URL, func(string) { called = true }, func(string) { called = true }, func(string) { called = true })
	if called {
		t.Error("checkModels reported on an unreachable host")
	}
}

func TestCLID1DoctorCheckPlugins(t *testing.T) {
	e := newCLIEnv(t)
	plugins := filepath.Join(e.Home, ".gleann", "plugins.json")
	run := func() (oks, warns string) {
		var o, w []string
		checkPlugins(func(s string) { o = append(o, s) }, func(s string) { w = append(w, s) })
		return strings.Join(o, "\n"), strings.Join(w, "\n")
	}

	d1WriteFile(t, plugins, "{broken")
	_, w := run()
	mustContain(t, "invalid", w, "Invalid plugins.json")

	d1WriteFile(t, plugins, `[{"name":"legacy","url":"http://h"}]`)
	o, _ := run()
	mustContain(t, "legacy array", o, "1 plugin(s) registered", "Plugin 'legacy' configured via URL")

	d1WriteFile(t, plugins, `{"plugins":[]}`)
	o, _ = run()
	mustContain(t, "empty", o, "No plugins registered")

	// plugins.json is a directory: unreadable.
	if err := os.Remove(plugins); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	_, w = run()
	mustContain(t, "unreadable", w, "Cannot read "+plugins)
}

func TestCLID1DoctorDocsPluginRuntime(t *testing.T) {
	e := newCLIEnv(t)
	bin := d1Script(t, filepath.Join(e.Home, "plugins", "gleann-plugin-docs"),
		`if [ "$1 $2" = "models verify" ]; then
  if [ -f "$0.ok" ]; then echo OK; exit 0; fi
  echo "OK  onnxruntime"; echo "FAIL ocr model missing"; exit 1
fi
exit 2`)
	d1WriteFile(t, filepath.Join(e.Home, ".gleann", "plugins.json"),
		`{"plugins":[{"name":"docs","command":["`+bin+`"]}]}`)
	run := func() (string, string) {
		var o, w []string
		checkPlugins(func(s string) { o = append(o, s) }, func(s string) { w = append(w, s) })
		return strings.Join(o, "\n"), strings.Join(w, "\n")
	}
	_, w := run()
	mustContain(t, "docs runtime missing", w, "Plugin 'docs': FAIL ocr model missing", "Fix: gleann-plugin-docs models download")
	mustNotContain(t, "docs runtime missing", w, "onnxruntime")

	d1WriteFile(t, bin+".ok", "")
	o, w := run()
	mustContain(t, "docs runtime ok", o, "Plugin 'docs' OCR runtime and models are installed")
	if w != "" {
		t.Errorf("unexpected warnings: %s", w)
	}
}

func TestCLID1DoctorUnreadableIndexDirAndDiskSpace(t *testing.T) {
	e := newCLIEnv(t)
	file := filepath.Join(e.Home, "not-a-dir")
	d1WriteFile(t, file, "x")
	d1WriteConfig(t, e, map[string]any{"ollama_host": e.Ollama.URL, "index_dir": filepath.Join(file, "sub")})
	r := e.run("doctor")
	mustContain(t, "doctor", r.Out, "Index directory: "+filepath.Join(file, "sub")+" (empty)")

	var warns []string
	checkDiskSpace(string(filepath.Separator)+filepath.Join("nonexistent-d1", "x"), func(string) {}, func(s string) { warns = append(warns, s) })
	if len(warns) != 0 {
		t.Errorf("an existing ancestor (the root) should make the check pass, got %v", warns)
	}
	if got := formatBytes(512); got != "512 B" {
		t.Errorf("formatBytes(512) = %q", got)
	}
	if got := formatBytes(3 << 30); got != "3.0 GB" {
		t.Errorf("formatBytes(3GiB) = %q", got)
	}
}
