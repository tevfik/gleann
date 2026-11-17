package packs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const goodManifest = "id: %s\nversion: \"1\"\ncontent_files: [items.yaml]\n"

func TestReloadDirIsAFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows reports a path below a file as not found, which Reload treats as a missing packs dir")
	}
	file := filepath.Join(t.TempDir(), "packs")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := New(file).Reload(); err == nil {
		t.Fatal("a packs path that is a file must be an error")
	}
	if err := New(filepath.Join(t.TempDir(), "missing")).Reload(); err != nil {
		t.Fatalf("a missing packs dir is not an error: %v", err)
	}
}

func TestReloadInvalidPacks(t *testing.T) {
	dir := t.TempDir()
	// Valid packs, listed in ID order.
	writePack(t, dir, "zeta", strings.ReplaceAll(goodManifest, "%s", "zeta"), "- id: z\n")
	writePack(t, dir, "alpha", strings.ReplaceAll(goodManifest, "%s", "alpha"), "- id: a\n")
	// A stray file next to the pack directories is ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// One broken pack per loadPack failure.
	if err := os.MkdirAll(filepath.Join(dir, "no-manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, dir, "bad-yaml", "id: [unclosed\n", "")
	writePack(t, dir, "no-version", "id: nv\ncontent_files: [items.yaml]\n", "")
	writePack(t, dir, "no-content", "id: nc\nversion: \"1\"\n", "")
	writePack(t, dir, "missing-file", "id: mf\nversion: \"1\"\ncontent_files: [other.yaml]\n", "")
	writePack(t, dir, "bad-items", "id: bi\nversion: \"1\"\ncontent_files: [items.yaml]\n", "- just a string\n")

	r := New(dir)
	err := r.Reload()
	if err == nil {
		t.Fatal("broken packs must be reported")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid in %v", err)
	}
	for _, name := range []string{"no-manifest", "bad-yaml", "no-version", "no-content", "missing-file", "bad-items"} {
		if !strings.Contains(err.Error(), name+":") {
			t.Errorf("error does not name pack %q: %v", name, err)
		}
	}
	list := r.List()
	if len(list) != 2 || list[0].Manifest.ID != "alpha" || list[1].Manifest.ID != "zeta" {
		t.Fatalf("valid packs must still load, sorted: %+v", list)
	}
}

func TestItemAndSearchEdgeCases(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, "p", strings.ReplaceAll(goodManifest, "%s", "p"), "- id: a\n- id: b\n- id: c\n")
	r := New(dir)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Item("unknown", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Item on unknown pack: %v", err)
	}
	// An empty query returns every item, capped at the limit.
	got, err := r.Search("p", "  ", 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("empty query with limit 2: %d %v", len(got), err)
	}
}
