package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})
	// Close releases the block-memory store (and any cached searcher) the
	// handlers open under temp dirs; without it t.TempDir cleanup fails on
	// Windows ("file is being used by another process").
	t.Cleanup(srv.Close)
	return srv
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

	// Seed the server's tracker directly.
	srv.gain = gleann.TokenGain{}
	srv.gain.Add(1000, 300)

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
	srv.gain = gleann.TokenGain{}
}

func TestGainTool_HandleGain_Reset(t *testing.T) {
	srv := testServer(t)

	srv.gain = gleann.TokenGain{}
	srv.gain.Add(500, 100)

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
	if srv.gain.Calls != 0 {
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

// ── gleann_gain recording tests (gleann-gain-dead) ─────────────────────────
//
// Gain definition under test: every successful gleann_shell call records
// EstimateTokens(raw output) vs EstimateTokens(compressed payload); every
// successful gleann_read call records EstimateTokens(whole file) vs
// EstimateTokens(mode-filtered payload, excluding the metadata header).
// The counters accumulate per server until reset.

// gainReport calls gleann_gain and decodes its JSON report.
func gainReport(t *testing.T, srv *Server) gleann.TokenGain {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	res, err := srv.handleGain(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var g gleann.TokenGain
	if err := json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &g); err != nil {
		t.Fatalf("gain report is not valid JSON: %v", err)
	}
	return g
}

// shellCall runs gleann_shell and returns the compressed payload it reports.
func shellCall(t *testing.T, srv *Server, command, output string) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"command": command, "output": output}
	res, err := srv.handleShell(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("gleann_shell failed: %v", res.Content)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &parsed); err != nil {
		t.Fatalf("shell response is not valid JSON: %v", err)
	}
	payload, _ := parsed["output"].(string)
	return payload
}

// readCall runs gleann_read and returns the payload without the metadata
// header line ("# path (mode=..., ~N tokens)").
func readCall(t *testing.T, srv *Server, args map[string]interface{}) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := srv.handleRead(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("gleann_read failed: %v", res.Content)
	}
	text := res.Content[0].(mcp.TextContent).Text
	i := strings.Index(text, "\n")
	if i < 0 {
		t.Fatalf("read response has no metadata header: %q", text)
	}
	return text[i+1:]
}

// noisyGitStatus is a git status output the git_status pattern compresses.
func noisyGitStatus() string {
	return "On branch main\nChanges not staged for commit:\n" +
		strings.Repeat("  modified:   pkg/gleann/file.go\n", 40)
}

func TestGainTool_ShellCallRecordsGain(t *testing.T) {
	srv := testServer(t)
	raw := noisyGitStatus()
	payload := shellCall(t, srv, "git status", raw)

	g := gainReport(t, srv)
	if g.Calls != 1 {
		t.Errorf("after one gleann_shell call: calls = %d, want 1", g.Calls)
	}
	if want := gleann.EstimateTokens(raw); g.RawTokens != want {
		t.Errorf("raw_tokens = %d, want %d", g.RawTokens, want)
	}
	if want := gleann.EstimateTokens(payload); g.CompressedTokens != want {
		t.Errorf("compressed_tokens = %d, want %d", g.CompressedTokens, want)
	}
	if g.SavedTokens != g.RawTokens-g.CompressedTokens {
		t.Errorf("saved_tokens = %d, want raw-compressed = %d", g.SavedTokens, g.RawTokens-g.CompressedTokens)
	}
	if g.SavedTokens <= 0 {
		t.Errorf("git status compression should save tokens, saved = %d", g.SavedTokens)
	}
}

func TestGainTool_ReadCallRecordsGain(t *testing.T) {
	srv, src := serverWithSource(t)
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d with some content", i+1)
	}
	raw := strings.Join(lines, "\n")
	tmpFile := filepath.Join(src, "data.txt")
	if err := os.WriteFile(tmpFile, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := readCall(t, srv, map[string]interface{}{"path": tmpFile, "mode": "lines", "lines": "5:10"})

	g := gainReport(t, srv)
	if g.Calls != 1 {
		t.Errorf("after one gleann_read call: calls = %d, want 1", g.Calls)
	}
	if want := gleann.EstimateTokens(raw); g.RawTokens != want {
		t.Errorf("raw_tokens = %d, want whole-file estimate %d", g.RawTokens, want)
	}
	if want := gleann.EstimateTokens(payload); g.CompressedTokens != want {
		t.Errorf("compressed_tokens = %d, want payload estimate %d", g.CompressedTokens, want)
	}
	if g.SavedTokens != g.RawTokens-g.CompressedTokens {
		t.Errorf("saved_tokens = %d, want raw-compressed = %d", g.SavedTokens, g.RawTokens-g.CompressedTokens)
	}
	if g.SavedTokens <= 0 {
		t.Errorf("a 6-line slice of a 40-line file should save tokens, saved = %d", g.SavedTokens)
	}
}

func TestGainTool_FullReadRecordsCallWithZeroSaving(t *testing.T) {
	srv, src := serverWithSource(t)
	raw := "package main\n\nfunc main() {}\n"
	tmpFile := filepath.Join(src, "main.go")
	if err := os.WriteFile(tmpFile, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := readCall(t, srv, map[string]interface{}{"path": tmpFile, "mode": "full"})
	if payload != raw {
		t.Fatalf("full mode payload = %q, want the whole file %q", payload, raw)
	}

	g := gainReport(t, srv)
	if g.Calls != 1 {
		t.Errorf("a full read is still a recorded call: calls = %d, want 1", g.Calls)
	}
	if want := gleann.EstimateTokens(raw); g.RawTokens != want || g.CompressedTokens != want {
		t.Errorf("full read: raw = %d, compressed = %d, want both %d", g.RawTokens, g.CompressedTokens, want)
	}
	if g.SavedTokens != 0 {
		t.Errorf("full read saves nothing: saved = %d, want 0", g.SavedTokens)
	}
}

func TestGainTool_AccumulatesAcrossCalls(t *testing.T) {
	srv, src := serverWithSource(t)
	raw := strings.Repeat("func body line with content\n", 30)
	tmpFile := filepath.Join(src, "big.go")
	if err := os.WriteFile(tmpFile, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	shellRaw := noisyGitStatus()
	shellPayload := shellCall(t, srv, "git status", shellRaw)
	linesPayload := readCall(t, srv, map[string]interface{}{"path": tmpFile, "mode": "lines", "lines": "1:5"})
	fullPayload := readCall(t, srv, map[string]interface{}{"path": tmpFile, "mode": "full"})

	wantRaw := gleann.EstimateTokens(shellRaw) + gleann.EstimateTokens(raw) + gleann.EstimateTokens(raw)
	wantComp := gleann.EstimateTokens(shellPayload) + gleann.EstimateTokens(linesPayload) + gleann.EstimateTokens(fullPayload)

	g := gainReport(t, srv)
	if g.Calls != 3 {
		t.Errorf("calls = %d, want 3", g.Calls)
	}
	if g.RawTokens != wantRaw {
		t.Errorf("raw_tokens = %d, want %d", g.RawTokens, wantRaw)
	}
	if g.CompressedTokens != wantComp {
		t.Errorf("compressed_tokens = %d, want %d", g.CompressedTokens, wantComp)
	}
	if g.SavedTokens != wantRaw-wantComp {
		t.Errorf("saved_tokens = %d, want %d", g.SavedTokens, wantRaw-wantComp)
	}
	if wantRatio := float64(wantRaw-wantComp) / float64(wantRaw); g.Ratio != wantRatio {
		t.Errorf("ratio = %v, want %v", g.Ratio, wantRatio)
	}
}

func TestGainTool_ResetClearsRecordedGain(t *testing.T) {
	srv := testServer(t)
	shellCall(t, srv, "git status", noisyGitStatus())
	if g := gainReport(t, srv); g.Calls == 0 {
		t.Fatal("precondition: shell call was not recorded")
	}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"reset": true}
	res, err := srv.handleGain(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content[0].(mcp.TextContent).Text, "reset") {
		t.Error("should confirm reset")
	}
	if g := gainReport(t, srv); g != (gleann.TokenGain{}) {
		t.Errorf("after reset the report should be all zero, got %+v", g)
	}
}

func TestGainTool_TrackerIsPerServer(t *testing.T) {
	srvA := testServer(t)
	srvB := testServer(t)
	shellCall(t, srvA, "git status", noisyGitStatus())

	if g := gainReport(t, srvA); g.Calls != 1 {
		t.Errorf("server A: calls = %d, want 1", g.Calls)
	}
	if g := gainReport(t, srvB); g != (gleann.TokenGain{}) {
		t.Errorf("server B must not see server A's gain, got %+v", g)
	}
}

func TestGainTool_ConcurrentShellCalls(t *testing.T) {
	srv := testServer(t)
	raw := noisyGitStatus()
	payload := shellCall(t, srv, "git status", raw) // warm-up also gives the per-call numbers

	const goroutines, callsEach = 8, 5
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsEach; j++ {
				req := mcp.CallToolRequest{}
				req.Params.Arguments = map[string]interface{}{"command": "git status", "output": raw}
				if res, err := srv.handleShell(context.Background(), req); err != nil || res.IsError {
					t.Errorf("concurrent gleann_shell failed: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	g := gainReport(t, srv)
	wantCalls := 1 + goroutines*callsEach
	if g.Calls != wantCalls {
		t.Errorf("calls = %d, want %d", g.Calls, wantCalls)
	}
	if want := wantCalls * gleann.EstimateTokens(raw); g.RawTokens != want {
		t.Errorf("raw_tokens = %d, want %d", g.RawTokens, want)
	}
	if want := wantCalls * (gleann.EstimateTokens(raw) - gleann.EstimateTokens(payload)); g.SavedTokens != want {
		t.Errorf("saved_tokens = %d, want %d", g.SavedTokens, want)
	}
}

func TestGainTool_DescriptionStatesGainDefinition(t *testing.T) {
	srv := testServer(t)
	desc := srv.buildGainTool().Description
	for _, want := range []string{"gleann_shell", "gleann_read", "4 characters per token"} {
		if !strings.Contains(desc, want) {
			t.Errorf("gain tool description should state the definition (missing %q): %s", want, desc)
		}
	}
}
