package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// CLI tests of `gleann mcp install` (config files under the temp home and
// working directory) and `gleann multimodal analyze` against the fake Ollama.

func TestCLIMCPInstallWritesEveryTarget(t *testing.T) {
	e := newCLIEnv(t)
	// Optional targets are configured only when their directories exist.
	for _, d := range []string{
		filepath.Join(e.Home, ".config", "Claude"),
		filepath.Join(e.Home, ".gemini", "antigravity-ide"),
		filepath.Join(e.Home, ".config", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings"),
		".cursor",
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := e.run("mcp", "install", "--bin", "/opt/gleann/bin/gleann", "--name", "gleann-test")
	wantExit(t, r, 0)
	mustContain(t, "mcp install", r.Out,
		"Configuring gleann MCP server (binary: /opt/gleann/bin/gleann)",
		"✓ Claude Code", "✓ Claude Desktop", "✓ Cursor (User)", "✓ Cursor (Workspace)",
		"✓ Google Antigravity / Gemini", "✓ Antigravity IDE", "✓ VS Code (Workspace)", "✓ Roo Code",
		"✓ OpenCode", "Done! Configured 9 MCP target(s).")

	for _, p := range []string{
		filepath.Join(e.Home, ".claude.json"),
		filepath.Join(e.Home, ".cursor", "mcp.json"),
		filepath.Join(".vscode", "mcp.json"),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			MCPServers map[string]struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		s, ok := cfg.MCPServers["gleann-test"]
		if !ok || s.Command != "/opt/gleann/bin/gleann" || len(s.Args) != 1 || s.Args[0] != "mcp" {
			t.Errorf("%s: server entry = %+v", p, cfg.MCPServers)
		}
	}

	// A single target only touches that target. Invalid JSON is replaced
	// (a .bak keeps the old content); a path that cannot be written is
	// reported and not counted.
	if err := os.WriteFile(filepath.Join(e.Home, ".cursor", "mcp.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(".cursor", "mcp.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(".cursor", "mcp.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	r = e.run("mcp", "install", "--target", "cursor", "--bin", "/x/gleann")
	wantExit(t, r, 0)
	mustContain(t, "cursor only", r.Out, "✓ Cursor (User)", "✗ Cursor (Workspace)", "Configured 1 MCP target(s).")
	mustNotContain(t, "cursor only", r.Out, "Claude Code")
	if b, err := os.ReadFile(filepath.Join(e.Home, ".cursor", "mcp.json.bak")); err != nil || string(b) != "{not json" {
		t.Errorf("backup of the invalid config = %q (%v)", b, err)
	}
}

func TestCLIMultimodalAnalyze(t *testing.T) {
	e := newCLIEnv(t)
	e.Ollama.setReply("A small red square.")
	img := e.write("pics/red.png", "\x89PNG\r\n\x1a\nfake image bytes")

	r := e.run("multimodal")
	wantExit(t, r, 1)
	mustContain(t, "no sub", r.Out, "Usage: gleann multimodal analyze")
	r = e.run("multimodal", "explode")
	wantExit(t, r, 1)
	mustContain(t, "unknown sub", r.Err, "unknown multimodal sub-command: explode")
	wantExit(t, e.run("multimodal", "help"), 0)
	wantExit(t, e.run("multimodal", "analyze", "--help"), 0)
	r = e.run("multimodal", "analyze")
	wantExit(t, r, 1)
	mustContain(t, "no target", r.Err, "usage: gleann multimodal analyze")
	r = e.run("multimodal", "analyze", filepath.Join(e.Docs, "missing.png"), "--model", "gemma3:4b")
	wantExit(t, r, 1)
	mustContain(t, "missing file", r.Err, "error:")

	// One file, model given.
	r = e.run("multimodal", "analyze", img, "--model", "gemma3:4b")
	wantExit(t, r, 0)
	mustContain(t, "file", r.Out, "Analyzing: "+img+" (model: gemma3:4b)", "A small red square.")

	// A directory, model auto-detected from /api/tags.
	e.Ollama.mu.Lock()
	e.Ollama.models = append(e.Ollama.models, "llava:7b")
	e.Ollama.mu.Unlock()
	r = e.run("multimodal", "analyze", filepath.Dir(img))
	wantExit(t, r, 0)
	mustContain(t, "dir", r.Out, "Scanning directory:", "(model: llava:7b)", "red.png", "Processed 1 multimodal files", "A small red square.")

	// No multimodal model installed.
	e.Ollama.mu.Lock()
	e.Ollama.models = []string{"bge-m3:latest"}
	e.Ollama.mu.Unlock()
	r = e.run("multimodal", "analyze", img)
	wantExit(t, r, 1)
	mustContain(t, "no model", r.Err, "no multimodal model found")

	// The model fails: the error is reported. (A new file: the result of
	// red.png is cached.)
	e.Ollama.mu.Lock()
	e.Ollama.failChat = true
	e.Ollama.mu.Unlock()
	blue := e.write("blue.png", "\x89PNG\r\n\x1a\nother image bytes")
	r = e.run("multimodal", "analyze", blue, "--model", "gemma3:4b")
	wantExit(t, r, 1)
	mustContain(t, "model error", r.Err, "error:")
}
