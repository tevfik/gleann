package gleann

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func drainMsgs(ch chan string) []string {
	close(ch)
	var out []string
	for s := range ch {
		out = append(out, s)
	}
	return out
}

func TestPrepareDocsOCRRunsModelsDownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugin")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "gleann-plugin-docs")
	os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" > "+log+"\n"), 0o755)

	ch := make(chan string, 8)
	prepareDocsOCR(bin, ch)
	msgs := drainMsgs(ch)
	got, _ := os.ReadFile(log)
	if strings.TrimSpace(string(got)) != "models download" {
		t.Fatalf("plugin called with %q", got)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "ready") {
		t.Fatalf("messages: %v", msgs)
	}
}

func TestPrepareDocsOCRFailureIsNotFatal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugin")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "gleann-plugin-docs")
	os.WriteFile(bin, []byte("#!/bin/sh\nexit 2\n"), 0o755) // an older plugin: unknown subcommand
	ch := make(chan string, 8)
	prepareDocsOCR(bin, ch)
	msgs := strings.Join(drainMsgs(ch), "\n")
	if !strings.Contains(msgs, "skipped") {
		t.Fatalf("messages: %s", msgs)
	}
}
