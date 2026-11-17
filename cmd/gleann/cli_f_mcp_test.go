package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CLI tests of `gleann mcp`: the stdio server answering a scripted session,
// its configuration sources, and `gleann mcp install` edge cases.

const fMCPSession = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`

func TestCLIFMCPServeStdio(t *testing.T) {
	e := newCLIEnv(t)
	fSaveConfig(t, e.Home, `{"embedding_provider": "ollama", "embedding_model": "bge-m3", "ollama_host": "`+e.Ollama.URL+`",
		"openai_api_key": "sk-x", "ollama_api_key": "ok-x", "openai_base_url": "http://127.0.0.1:1/v1",
		"llm_provider": "ollama", "llm_model": "llama3.2", "index_dir": "`+filepath.ToSlash(e.IndexDir)+`"}`)
	t.Setenv("GLEANN_LLM_PROVIDER", "ollama")
	t.Setenv("GLEANN_LLM_MODEL", "gemma3:4b")

	r := e.runIn(fMCPSession, "mcp", "--llm-model", "llama3.2", "--llm-provider", "ollama")
	wantExit(t, r, 0)
	mustContain(t, "initialize", r.Out, `"id":1`, `"serverInfo"`)
	mustContain(t, "tools/list", r.Out, `"id":2`, `"gleann_search"`)

	// --clean-names drops the gleann_ prefix; GLEANN_MCP_CLEAN_NAMES does too.
	r = e.runIn(fMCPSession, "mcp", "--clean-names")
	wantExit(t, r, 0)
	mustContain(t, "clean names", r.Out, `"name":"search"`)
	mustNotContain(t, "clean names", r.Out, `"gleann_search"`)

	t.Setenv("GLEANN_MCP_CLEAN_NAMES", "1")
	r = e.runIn(fMCPSession, "mcp", "--tools", "full")
	wantExit(t, r, 0)
	mustNotContain(t, "env clean names", r.Out, `"gleann_search"`)
}

func TestCLIFMCPUsageAndInstallTargets(t *testing.T) {
	e := newCLIEnv(t)
	for _, a := range []string{"help", "--help", "-h"} {
		r := e.run("mcp", a)
		wantExit(t, r, 0)
		mustContain(t, "usage "+a, r.Out, "gleann mcp install [options]", "--clean-names")
	}

	r := e.run("mcp", "--nope")
	if r.Code == 0 {
		t.Fatalf("unknown mcp flag accepted: %s", r.combined())
	}

	// An unknown target configures nothing; without --bin the binary is
	// looked up.
	r = e.run("mcp", "install", "--target", "emacs")
	wantExit(t, r, 0)
	mustContain(t, "unknown target", r.Out, "Configuring gleann MCP server (binary: ", "Configured 0 MCP target(s)")

	// A config path that cannot be written is reported per target.
	if err := os.MkdirAll(filepath.Join(e.Home, ".claude.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	r = e.run("mcp", "install", "--target", "claude", "--bin", "/x/gleann")
	wantExit(t, r, 0)
	mustContain(t, "claude error", r.Out, "✗ Claude Code", "Configured 0 MCP target(s)")

	// A broken OpenCode config is reported and left in place.
	oc := filepath.Join(e.Home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(oc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oc, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("mcp", "install", "--target", "opencode", "--bin", "/x/gleann")
	wantExit(t, r, 0)
	mustContain(t, "opencode error", r.Out, "✗ OpenCode", "invalid JSON", "Configured 0 MCP target(s)")
	if s := fReadFile(t, oc); s != "{broken" {
		t.Errorf("broken opencode.json rewritten: %q", s)
	}
}

func TestFMergeMCPServerConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "mcp.json")

	// A new file is created with the server entry.
	if err := MergeMCPServerConfig(path, "gleann", "/bin/gleann", []string{"mcp"}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, "new", fReadFile(t, path), `"mcpServers"`, `"gleann"`, `"/bin/gleann"`)

	// Other servers survive; the entry is replaced, not duplicated.
	if err := os.WriteFile(path, []byte(`{"mcpServers": {"other": {"command": "o"}}, "theme": "dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MergeMCPServerConfig(path, "gleann", "/bin/g2", nil); err != nil {
		t.Fatal(err)
	}
	s := fReadFile(t, path)
	mustContain(t, "merge", s, `"other"`, `"theme": "dark"`, `"/bin/g2"`)

	// Invalid JSON is backed up and replaced.
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MergeMCPServerConfig(path, "gleann", "/bin/g3", nil); err != nil {
		t.Fatal(err)
	}
	if b := fReadFile(t, path+".bak"); b != "not json" {
		t.Errorf("backup = %q", b)
	}
	mustContain(t, "after invalid", fReadFile(t, path), `"/bin/g3"`)

	// A directory that cannot be created is an error.
	if err := MergeMCPServerConfig(filepath.Join(fBlockedPath(t), "mcp.json"), "gleann", "g", nil); err == nil ||
		!strings.Contains(err.Error(), "creating directory") {
		t.Errorf("blocked dir: %v", err)
	}
}
