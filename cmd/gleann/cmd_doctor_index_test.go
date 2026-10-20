package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestDescribeIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "docs.index"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	got := describeIndex(dir, gleann.IndexMeta{
		Name: "docs", NumPassages: 12, Backend: "hnsw", EmbeddingModel: "nomic-embed-text", Dimensions: 768, VectorReady: true,
	})
	want := "docs — 12 passages, hnsw, nomic-embed-text, 768 dims, 2.0 KB"
	if got != want {
		t.Errorf("vector index:\n got %q\nwant %q", got, want)
	}

	got = describeIndex(dir, gleann.IndexMeta{Name: "docs", NumPassages: 3, LexicalOnly: true})
	if !strings.Contains(got, "lexical-only") || strings.Contains(got, "dims") {
		t.Errorf("lexical-only index: %q", got)
	}

	// An index whose directory is missing reports 0 B rather than failing.
	if got := describeIndex(dir, gleann.IndexMeta{Name: "ghost", VectorReady: true}); !strings.HasSuffix(got, "0 B") {
		t.Errorf("missing directory: %q", got)
	}
}

func TestCheckDocsRuntimeReportsMissingRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugin")
	}
	dir := t.TempDir()
	script := func(body string) string {
		p := filepath.Join(dir, "gleann-plugin-docs")
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	var oks, warns []string
	ok := func(s string) { oks = append(oks, s) }
	warn := func(s string) { warns = append(warns, s) }

	checkDocsRuntime(script("echo 'ok   x'\nexit 0\n"), ok, warn)
	if len(oks) != 1 || len(warns) != 0 {
		t.Fatalf("healthy plugin: ok=%v warn=%v", oks, warns)
	}

	oks, warns = nil, nil
	checkDocsRuntime(script("echo 'FAIL onnxruntime: not found'\nexit 1\n"), ok, warn)
	if len(oks) != 0 || len(warns) != 2 || !strings.Contains(warns[0], "onnxruntime") || !strings.Contains(warns[1], "models download") {
		t.Fatalf("broken plugin: ok=%v warn=%v", oks, warns)
	}
}
