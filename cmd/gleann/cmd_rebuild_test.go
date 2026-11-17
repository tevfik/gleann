package main

import (
	"os"
	"path/filepath"
	"testing"
)

// `index rebuild X` takes the source directory, model, backend, tags,
// description and MCP exposure from the index unless a flag sets them
// (RFC 004 B3).
func TestRebuildArgsFromMeta(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"name":"x","backend":"hnsw","embedding_model":"bge-m3","source_dir":"/src/x",` +
		`"tags":["work","go"],"description":"my code","mcp_exposed":false,"vector_ready":true}`
	if err := os.WriteFile(filepath.Join(dir, "x", "x.meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	got := rebuildArgs([]string{"x", "--model", "other"}, dir, "x")
	for flag, want := range map[string]string{
		"--docs": "/src/x", "--model": "other", "--backend": "hnsw",
		"--tags": "work,go", "--desc": "my code", "--mcp": "false",
	} {
		if v := getFlag(got, flag); v != want {
			t.Errorf("%s = %q, want %q (args %v)", flag, v, want, got)
		}
	}
	if hasFlag(got, "--graph") {
		t.Errorf("an index without a code graph must not get --graph: %v", got)
	}
	// An old Kùzu graph next to the index asks for the graph.
	if err := os.MkdirAll(filepath.Join(dir, "x_graph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := rebuildArgs([]string{"x"}, dir, "x"); !hasFlag(got, "--graph") {
		t.Errorf("an index that had <name>_graph must keep its graph: %v", got)
	}
	// No index: the arguments stay as given.
	if got := rebuildArgs([]string{"y", "--docs", "/d"}, dir, "y"); len(got) != 3 {
		t.Errorf("rebuildArgs without an index = %v", got)
	}
}
