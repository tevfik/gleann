package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	return NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})
}

// serverWithSource returns a server whose only index, "src", has a source
// directory; it is the directory gleann_read and gleann_read_full_document
// may read from.
func serverWithSource(t *testing.T) (*Server, string) {
	t.Helper()
	srv := testServer(t)
	src := t.TempDir()
	idxDir := filepath.Join(srv.config.IndexDir, "src")
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(gleann.IndexMeta{Name: "src", SourceDir: src})
	if err := os.WriteFile(filepath.Join(idxDir, "src.meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	return srv, src
}

// ── gleann_shell tests ────────────────────────────────────────────────────

func TestShellTool_Build(t *testing.T) {
	srv := testServer(t)
	tool := srv.buildShellTool()
	if tool.Name != "gleann_shell" {
		t.Errorf("expected gleann_shell, got %s", tool.Name)
	}
	if !strings.Contains(tool.Description, "compression") {
		t.Error("description should mention compression")
	}
}

func TestShellTool_HandleShell_GitStatus(t *testing.T) {
	srv := testServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"command": "git status",
		"output":  "On branch main\n  modified: file.go\n  modified: file2.go\n",
	}

	result, err := srv.handleShell(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	text := result.Content[0].(mcp.TextContent).Text
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if parsed["pattern_used"] != "git_status" {
		t.Errorf("expected git_status pattern, got %v", parsed["pattern_used"])
	}
}

func TestShellTool_HandleShell_MissingCommand(t *testing.T) {
	srv := testServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"output": "some output",
	}

	result, err := srv.handleShell(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "missing") {
		t.Error("should report missing command")
	}
}

func TestShellTool_HandleShell_InvalidArgs(t *testing.T) {
	srv := testServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = "not a map"

	result, err := srv.handleShell(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "invalid") {
		t.Error("should report invalid arguments")
	}
}

// ── gleann_read tests ─────────────────────────────────────────────────────

func TestReadTool_Build(t *testing.T) {
	srv := testServer(t)
	tool := srv.buildReadTool()
	if tool.Name != "gleann_read" {
		t.Errorf("expected gleann_read, got %s", tool.Name)
	}
	if !strings.Contains(tool.Description, "10 modes") {
		t.Error("description should mention 10 modes")
	}
}

func TestReadTool_HandleRead_Auto(t *testing.T) {
	srv, src := serverWithSource(t)

	tmpFile := filepath.Join(src, "test.go")
	os.WriteFile(tmpFile, []byte("package main\n\nfunc main() {}\n"), 0o644)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"path": tmpFile,
		"mode": "auto",
	}

	result, err := srv.handleRead(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "mode=auto") {
		t.Error("header should show mode=auto")
	}
	if !strings.Contains(text, "package main") {
		t.Error("should contain file content")
	}
}

func TestReadTool_HandleRead_Map(t *testing.T) {
	srv, src := serverWithSource(t)

	// Create a file with structure
	content := "package main\n\ntype Foo struct {}\n\nfunc Bar() {}\n"
	tmpFile := filepath.Join(src, "app.go")
	os.WriteFile(tmpFile, []byte(content), 0o644)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"path": tmpFile,
		"mode": "map",
	}

	result, err := srv.handleRead(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "Foo struct") {
		t.Error("map mode should show struct")
	}
}

func TestReadTool_HandleRead_Lines(t *testing.T) {
	srv, src := serverWithSource(t)

	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line " + string(rune('A'+i))
	}
	tmpFile := filepath.Join(src, "data.txt")
	os.WriteFile(tmpFile, []byte(strings.Join(lines, "\n")), 0o644)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"path":  tmpFile,
		"mode":  "lines",
		"lines": "5:10",
	}

	result, err := srv.handleRead(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "line E") {
		t.Error("should contain line 5")
	}
}

func TestReadTool_HandleRead_MissingPath(t *testing.T) {
	srv := testServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"mode": "full",
	}

	result, err := srv.handleRead(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "missing") {
		t.Error("should report missing path")
	}
}

func TestReadTool_HandleRead_FileNotFound(t *testing.T) {
	srv := testServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"path": "/nonexistent/file.go",
	}

	result, err := srv.handleRead(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "error") {
		t.Error("should report read error")
	}
}

// ── gleann_gain tests ─────────────────────────────────────────────────────

func TestGainTool_Build(t *testing.T) {
	srv := testServer(t)
	tool := srv.buildGainTool()
	if tool.Name != "gleann_gain" {
		t.Errorf("expected gleann_gain, got %s", tool.Name)
	}
}

func TestGainTool_HandleGain_Report(t *testing.T) {
	srv := testServer(t)

	// Reset first
	sessionGain = gleann.TokenGain{}
	sessionGain.Add(1000, 300)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}

	result, err := srv.handleGain(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "1000") {
		t.Error("should show raw tokens")
	}

	// Reset
	sessionGain = gleann.TokenGain{}
}

func TestGainTool_HandleGain_Reset(t *testing.T) {
	srv := testServer(t)

	sessionGain = gleann.TokenGain{}
	sessionGain.Add(500, 100)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"reset": true,
	}

	result, err := srv.handleGain(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "reset") {
		t.Error("should confirm reset")
	}
	if sessionGain.Calls != 0 {
		t.Error("gain should be reset to zero")
	}
}

func TestReadTool_RefusesPathsOutsideSource(t *testing.T) {
	srv, src := serverWithSource(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := []string{outside, filepath.Join(src, "..", filepath.Base(filepath.Dir(outside)), "secret.txt"), "../secret.txt"}
	if err := os.Symlink(outside, filepath.Join(src, "link.txt")); err == nil {
		paths = append(paths, filepath.Join(src, "link.txt"), "link.txt")
	}
	for _, p := range paths {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"path": p, "mode": "full"}
		res, err := srv.handleRead(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(mcp.TextContent).Text
		if !res.IsError || strings.Contains(text, "TOP-SECRET") {
			t.Errorf("path %q was served: %q", p, text)
		}
	}
}

func TestReadTool_RelativeToSource(t *testing.T) {
	srv, src := serverWithSource(t)
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"path": "a.txt", "mode": "full"}
	res, _ := srv.handleRead(context.Background(), req)
	if res.IsError || !strings.Contains(res.Content[0].(mcp.TextContent).Text, "inside") {
		t.Errorf("relative read failed: %v", res.Content)
	}
}

func TestReadTool_WorkingDirectory(t *testing.T) {
	srv, _ := serverWithSource(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main // in-project"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	for _, p := range []string{"main.go", filepath.Join(project, "main.go")} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"path": p, "mode": "full"}
		res, _ := srv.handleRead(context.Background(), req)
		if res.IsError || !strings.Contains(res.Content[0].(mcp.TextContent).Text, "in-project") {
			t.Errorf("read of %q inside the working directory failed: %v", p, res.Content)
		}
	}

	// A home directory as working directory must not open everything below it.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "secret.txt"), []byte("HOME-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(home)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"path": "secret.txt", "mode": "full"}
	res, _ := srv.handleRead(context.Background(), req)
	if !res.IsError || strings.Contains(res.Content[0].(mcp.TextContent).Text, "HOME-SECRET") {
		t.Errorf("home directory as working directory was readable: %v", res.Content)
	}
}
