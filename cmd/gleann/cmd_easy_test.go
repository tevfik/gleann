package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout reroutes os.Stdout for the duration of fn and returns whatever
// was written. Helpful for testing CLI commands that print directly.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

func TestCmdConfigPath_PrintsPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	out := captureStdout(t, func() { cmdConfigPath() })
	want := filepath.Join(tmp, ".gleann", "config.json")
	if !strings.Contains(out, want) {
		t.Errorf("output %q missing %q", out, want)
	}
}

func TestCmdConfigShow_NoConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	out := captureStdout(t, func() { cmdConfigShow() })
	if !strings.Contains(out, "No configuration") {
		t.Errorf("expected 'No configuration' message, got %q", out)
	}
}

func TestCmdConfigValidate_NoFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	out := captureStdout(t, func() { cmdConfigValidate() })
	if !strings.Contains(out, "No config file found") {
		t.Errorf("expected missing-file message, got %q", out)
	}
}

func TestCmdConfigValidate_OK(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfgDir := filepath.Join(tmp, ".gleann")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"server": map[string]any{"addr": ":8080"}})
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { cmdConfigValidate() })
	// Validate prints success or summary; just make sure it didn't print an error keyword.
	if strings.Contains(strings.ToLower(out), "invalid json") {
		t.Errorf("unexpected validation error in output: %q", out)
	}
}

func TestCmdConfig_DispatchShow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	out := captureStdout(t, func() { cmdConfig([]string{"show"}) })
	if !strings.Contains(out, "No configuration") {
		t.Errorf("dispatch to show failed, output=%q", out)
	}
}

func TestCmdConfig_DispatchPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	out := captureStdout(t, func() { cmdConfig([]string{"path"}) })
	if !strings.Contains(out, ".gleann") {
		t.Errorf("dispatch to path failed, output=%q", out)
	}
}

func TestCmdConfig_DispatchValidate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	out := captureStdout(t, func() { cmdConfig([]string{"validate"}) })
	if !strings.Contains(out, "No config file") {
		t.Errorf("dispatch to validate failed, output=%q", out)
	}
}

func TestPrintIndexUsage(t *testing.T) {
	out := captureStdout(t, func() { printIndexUsage() })
	if !strings.Contains(out, "gleann index") {
		t.Errorf("usage missing header, got %q", out)
	}
	for _, want := range []string{"list", "build", "remove", "rebuild"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage missing %q", want)
		}
	}
}

func TestCmdCompletion_Bash(t *testing.T) {
	out := captureStdout(t, func() { cmdCompletion([]string{"bash"}) })
	if len(out) == 0 {
		t.Fatal("expected non-empty bash completion script")
	}
}

func TestCmdCompletion_Zsh(t *testing.T) {
	out := captureStdout(t, func() { cmdCompletion([]string{"zsh"}) })
	if len(out) == 0 {
		t.Fatal("expected non-empty zsh completion script")
	}
}

func TestCmdCompletion_Fish(t *testing.T) {
	out := captureStdout(t, func() { cmdCompletion([]string{"fish"}) })
	if len(out) == 0 {
		t.Fatal("expected non-empty fish completion script")
	}
}
