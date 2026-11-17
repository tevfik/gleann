package main

import (
	"reflect"
	"testing"
)

func TestFlagEqualsSpelling(t *testing.T) {
	args := []string{"q", "--top-k=3", "--graph=false", "--json", "--mcp=true", "--", "--index-dir=/x"}
	if got := getFlag(args, "--top-k"); got != "3" {
		t.Errorf("getFlag --top-k = %q, want 3", got)
	}
	if got := getFlag(args, "--mcp"); got != "true" {
		t.Errorf("getFlag --mcp = %q, want true", got)
	}
	if got := getFlag(args, "--index-dir"); got != "" {
		t.Errorf("getFlag read a flag after --: %q", got)
	}
	if hasFlag(args, "--graph") {
		t.Error("--graph=false counted as set")
	}
	if !hasFlag(args, "--json") || !hasFlag([]string{"--json=true"}, "--json") {
		t.Error("--json / --json=true not seen")
	}
	if hasFlag([]string{"--", "--json"}, "--json") {
		t.Error("flag after -- counted")
	}
}

func TestExpandFlagValues(t *testing.T) {
	known := flagSet{"--top-k": true, "--no-mmap": false}
	got := expandFlagValues([]string{"--top-k=3", "--no-mmap=true", "--no-mmap=0", "--other=1", "--", "--top-k=9"}, known)
	want := []string{"--top-k", "3", "--no-mmap", "--other=1", "--", "--top-k=9"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expandFlagValues = %q, want %q", got, want)
	}
}

func TestGetConfigEqualsSpelling(t *testing.T) {
	a := getConfig([]string{"--top-k=7", "--index-dir=/tmp/idx-eq"})
	b := getConfig([]string{"--top-k", "7", "--index-dir", "/tmp/idx-eq"})
	if a.SearchConfig.TopK != 7 || a.SearchConfig.TopK != b.SearchConfig.TopK || a.IndexDir != b.IndexDir {
		t.Errorf("--flag=value: top_k %d index_dir %q; --flag value: top_k %d index_dir %q",
			a.SearchConfig.TopK, a.IndexDir, b.SearchConfig.TopK, b.IndexDir)
	}
}
