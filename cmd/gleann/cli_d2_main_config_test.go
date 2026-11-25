package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// d2ConfigPath is ~/.gleann/config.json of the test environment.
func d2ConfigPath(e *cliEnv) string { return filepath.Join(e.Home, ".gleann", "config.json") }

// d2WriteConfig writes raw text as the saved config.
func d2WriteConfig(t *testing.T, e *cliEnv, raw string) {
	t.Helper()
	p := d2ConfigPath(e)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

// d2ReadConfig returns the saved config as a map.
func d2ReadConfig(t *testing.T, e *cliEnv) map[string]any {
	t.Helper()
	b, err := os.ReadFile(d2ConfigPath(e))
	if err != nil {
		t.Fatalf("reading saved config: %v", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("saved config is not JSON: %v\n%s", err, b)
	}
	return m
}

func TestCLID2MainDispatch(t *testing.T) {
	e := newCLIEnv(t)

	// No arguments prints the usage and returns.
	r := e.run()
	wantExit(t, r, 0)
	mustContain(t, "gleann (no args)", r.Out, "Usage:", "Core Commands:", "gleann index build core")

	for _, h := range []string{"help", "--help", "-h"} {
		r = e.run(h)
		wantExit(t, r, 0)
		mustContain(t, "gleann "+h, r.Out, "Usage:\n  gleann <command>")
	}

	r = e.run("version")
	wantExit(t, r, 0)
	if r.Out != "gleann "+version+"\n" {
		t.Errorf("version output = %q", r.Out)
	}
	wantExit(t, e.run("version", "--bogus"), 2)

	r = e.run("frobnicate")
	wantExit(t, r, 1)
	mustContain(t, "unknown command", r.Err, "unknown command: frobnicate")
	mustContain(t, "unknown command usage", r.Out, "Usage:")

	for _, dep := range []string{"quickstart", "go"} {
		r = e.run(dep)
		wantExit(t, r, 1)
		mustContain(t, dep, r.Err, "deprecated", "gleann setup --auto")
	}
}

func TestCLID2MalformedConfigGate(t *testing.T) {
	e := newCLIEnv(t)
	d2WriteConfig(t, e, "{\n  \"llm_model\": \"x\",\n  oops\n}\n")

	// A command that reads the config is refused with the location of the error.
	r := e.run("index", "list")
	wantExit(t, r, 1)
	mustContain(t, "gated command", r.Err, "error: config:")

	// Exempt commands still run.
	wantExit(t, e.run("version"), 0)
	r = e.run("config", "path")
	wantExit(t, r, 0)
	if strings.TrimSpace(r.Out) != d2ConfigPath(e) {
		t.Errorf("config path = %q, want %q", r.Out, d2ConfigPath(e))
	}
	// show and set are gated inside cmdConfig.
	wantExit(t, e.run("config", "show"), 1)
	wantExit(t, e.run("config", "set", "llm_model", "y"), 1)
	// validate reports the error and fails.
	r = e.run("config", "validate")
	wantExit(t, r, 1)
	mustContain(t, "validate malformed", r.Err, "Invalid config")

	// The file was not modified by any of them.
	b, _ := os.ReadFile(d2ConfigPath(e))
	if !strings.Contains(string(b), "oops") {
		t.Errorf("a gated command rewrote the malformed config: %s", b)
	}
}

func TestCLID2Completion(t *testing.T) {
	e := newCLIEnv(t)
	for shell, marker := range map[string]string{"bash": "complete", "zsh": "compdef", "fish": "complete -c gleann"} {
		r := e.run("completion", shell)
		wantExit(t, r, 0)
		if !strings.Contains(r.Out, marker) || !strings.Contains(r.Out, "gleann") {
			t.Errorf("completion %s lacks %q:\n%.300s", shell, marker, r.Out)
		}
	}
	r := e.run("completion")
	wantExit(t, r, 1)
	mustContain(t, "completion usage", r.Err, "Supported shells:", "bash", "zsh", "fish")
	r = e.run("completion", "powershell")
	wantExit(t, r, 1)
	mustContain(t, "unsupported shell", r.Err, "unsupported shell: powershell")
}

func TestCLID2ConfigShowSetAndMasking(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("config")
	wantExit(t, r, 0)
	mustContain(t, "config show (none)", r.Out, "No configuration found")

	steps := []struct {
		key, val, display, field, stored string
	}{
		{"ollama_host", "11435", "http://localhost:11435", "ollama_host", "http://localhost:11435"},
		{"host", "gpu-box:11434", "http://gpu-box:11434", "ollama_host", "http://gpu-box:11434"},
		{"OLLAMA_HOST", "https://ollama.example", "https://ollama.example", "ollama_host", "https://ollama.example"},
		{"embedding_provider", "openai", "openai", "embedding_provider", "openai"},
		{"embedding_model", "bge-m3", "bge-m3", "embedding_model", "bge-m3"},
		{"code_model", "jina-code", "jina-code", "code_embedding_model", "jina-code"},
		{"docs-model", "bge-small", "bge-small", "doc_embedding_model", "bge-small"},
		{"llm_provider", "ollama", "ollama", "llm_provider", "ollama"},
		{"llm_model", "qwen2.5:7b", "qwen2.5:7b", "llm_model", "qwen2.5:7b"},
		{"openai_base_url", "http://127.0.0.1:9/v1", "http://127.0.0.1:9/v1", "openai_base_url", "http://127.0.0.1:9/v1"},
		{"index_dir", e.IndexDir, e.IndexDir, "index_dir", e.IndexDir},
		{"server_addr", "127.0.0.1:9000", "127.0.0.1:9000", "server_addr", "127.0.0.1:9000"},
		{"backend", "hnsw", "hnsw", "backend", "hnsw"},
	}
	for _, s := range steps {
		r = e.run("config", "set", s.key, s.val)
		wantExit(t, r, 0)
		if want := "✅ Updated " + strings.ToLower(s.key) + " = " + s.display + "\n"; r.Out != want {
			t.Errorf("config set %s: output %q, want %q", s.key, r.Out, want)
		}
		if got := d2ReadConfig(t, e)[s.field]; got != s.stored {
			t.Errorf("after config set %s: %s = %v, want %q", s.key, s.field, got, s.stored)
		}
	}

	// Secrets are stored in full but displayed masked, by set and by show.
	secrets := map[string]string{
		"openai_api_key":    "sk-openai-0123456789abcdef",
		"anthropic_api_key": "sk-ant-0123456789abcdef",
		"ollama_api_key":    "ollama-0123456789abcdef",
	}
	for key, val := range secrets {
		r = e.run("config", "set", key, val)
		wantExit(t, r, 0)
		mustNotContain(t, "config set "+key, r.Out, val)
	}
	saved := d2ReadConfig(t, e)
	if saved["openai_api_key"] != secrets["openai_api_key"] || saved["ollama_api_key"] != secrets["ollama_api_key"] {
		t.Errorf("secrets not stored in full: %v", saved)
	}
	r = e.run("config", "show")
	wantExit(t, r, 0)
	var shown map[string]any
	if err := json.Unmarshal([]byte(r.Out), &shown); err != nil {
		t.Fatalf("config show is not JSON: %v\n%s", err, r.Out)
	}
	for _, val := range secrets {
		mustNotContain(t, "config show", r.Out, val)
	}
	if shown["llm_model"] != "qwen2.5:7b" || shown["openai_api_key"] == "" {
		t.Errorf("config show = %v", shown)
	}

	// "--" ends the flags, so a value may start with a dash.
	r = e.run("config", "set", "--", "llm_model", "-odd-name")
	wantExit(t, r, 0)
	if d2ReadConfig(t, e)["llm_model"] != "-odd-name" {
		t.Errorf("config set -- llm_model -odd-name not stored: %s", r.Out)
	}

	// An empty value displays as "".
	r = e.run("config", "set", "backend", "")
	wantExit(t, r, 0)
	mustContain(t, "empty value", r.Out, `backend = ""`)
}

func TestCLID2ConfigErrors(t *testing.T) {
	e := newCLIEnv(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"config", "set"}, "usage: gleann config set <key> <value>"},
		{[]string{"config", "set", "llm_model"}, "examples:"},
		{[]string{"config", "set", "--", "llm_model"}, "usage: gleann config set <key> <value>"},
		{[]string{"config", "set", "no_such_key", "v"}, "unknown config key: no_such_key"},
		{[]string{"config", "frob"}, "unknown config subcommand: frob"},
	}
	r := e.run("config", "show", "--bogus")
	wantExit(t, r, 2)
	mustContain(t, "config show --bogus", r.Err, "unknown flag --bogus")
	for _, c := range cases {
		r := e.run(c.args...)
		wantExit(t, r, 1)
		mustContain(t, strings.Join(c.args, " "), r.Err, c.want)
	}
	if _, err := os.Stat(d2ConfigPath(e)); !os.IsNotExist(err) {
		t.Errorf("a failed config set wrote the config file (err=%v)", err)
	}
}

func TestCLID2ConfigValidate(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("OLLAMA_HOST", "") // the saved host is under test; the environment would override it

	r := e.run("config", "validate")
	wantExit(t, r, 0)
	mustContain(t, "validate (none)", r.Out, "No config file found", "gleann setup")

	// Empty file.
	d2WriteConfig(t, e, "")
	r = e.run("config", "validate")
	wantExit(t, r, 1)
	mustContain(t, "validate empty", r.Err, "is empty")

	// A valid config whose Ollama answers and whose index dir is writable.
	cfg := map[string]any{
		"embedding_provider": "ollama", "embedding_model": "bge-m3", "llm_provider": "ollama",
		"llm_model": "gemma3:4b", "ollama_host": e.Ollama.URL, "ollama_api_key": "ollama-0123456789abcdef",
		"index_dir": e.IndexDir, "roles": map[string][]string{"reviewer": {"You review code."}},
	}
	write := func(m map[string]any) {
		b, _ := json.Marshal(m)
		d2WriteConfig(t, e, string(b))
	}
	write(cfg)
	r = e.run("config", "validate")
	wantExit(t, r, 0)
	mustContain(t, "validate ok", r.Out, "Config is valid", "Provider:  ollama", "Model:     bge-m3",
		"LLM Model: gemma3:4b", "Roles:     1 custom", "Ollama Key:")
	mustNotContain(t, "validate ok", r.Out, "ollama-0123456789abcdef", "⚠️")
	if e.Ollama.count("/api/tags") == 0 {
		t.Error("validate did not probe Ollama")
	}
	if _, err := os.Stat(filepath.Join(e.IndexDir, ".gleann_write_test")); !os.IsNotExist(err) {
		t.Error("validate left its write probe behind")
	}

	// Each problem produces its own warning but the config itself stays valid.
	missingDir := filepath.Join(e.Home, "nope")
	aFile := filepath.Join(e.Home, "file.txt")
	if err := os.WriteFile(aFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer unauthorized.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	for _, c := range []struct {
		name string
		mod  func(m map[string]any)
		want string
	}{
		{"missing index dir", func(m map[string]any) { m["index_dir"] = missingDir }, "Index Directory does not exist: " + missingDir},
		{"index dir is a file", func(m map[string]any) { m["index_dir"] = aFile }, "path is a file, not a directory"},
		{"ollama down", func(m map[string]any) { m["ollama_host"] = deadURL }, "is unreachable"},
		{"ollama 401", func(m map[string]any) { m["ollama_host"] = unauthorized.URL }, "rejected the request (HTTP 401)"},
		{"eif model missing", func(m map[string]any) {
			m["embedding_provider"], m["llm_provider"] = "eif", "openai"
			m["embedding_model"] = filepath.Join(e.Home, "missing.gguf")
		}, "EIF model file not found"},
	} {
		m := map[string]any{}
		for k, v := range cfg {
			m[k] = v
		}
		c.mod(m)
		write(m)
		r = e.run("config", "validate")
		wantExit(t, r, 0)
		mustContain(t, c.name, r.Out, "Config is valid", c.want)
	}
}

func TestCLID2ConfigEdit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX true/false commands as the editor")
	}
	trueBin, err1 := exec.LookPath("true")
	falseBin, err2 := exec.LookPath("false")
	if err1 != nil || err2 != nil {
		t.Skip("true/false not on PATH")
	}
	e := newCLIEnv(t)
	t.Setenv("VISUAL", "")

	// A missing config is created as {} before the editor opens it.
	t.Setenv("EDITOR", trueBin)
	r := e.run("config", "edit")
	wantExit(t, r, 0)
	mustContain(t, "config edit", r.Out, "Creating default at "+d2ConfigPath(e))
	if b, err := os.ReadFile(d2ConfigPath(e)); err != nil || string(b) != "{}\n" {
		t.Errorf("config edit created %q (%v), want {}", b, err)
	}

	// VISUAL is used when EDITOR is empty; an existing file is left alone.
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", trueBin)
	d2WriteConfig(t, e, `{"llm_model":"keep"}`)
	r = e.run("config", "edit")
	wantExit(t, r, 0)
	mustNotContain(t, "config edit existing", r.Out, "Creating default")
	if d2ReadConfig(t, e)["llm_model"] != "keep" {
		t.Error("config edit modified an existing config")
	}

	// A failing editor is an error.
	t.Setenv("VISUAL", falseBin)
	r = e.run("config", "edit")
	wantExit(t, r, 1)
	mustContain(t, "failing editor", r.Err, "error running editor")

	// No editor anywhere.
	t.Setenv("VISUAL", "")
	t.Setenv("PATH", t.TempDir())
	r = e.run("config", "edit")
	wantExit(t, r, 1)
	mustContain(t, "no editor", r.Err, "no editor found")
}
