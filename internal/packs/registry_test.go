package packs

import (
	"os"
	"path/filepath"
	"testing"
)

func writePack(t *testing.T, dir, id, manifest, content string) {
	t.Helper()
	pdir := filepath.Join(dir, id)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "pack.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "items.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReloadAndGet(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, "demo", `id: demo
version: 0.1.0
schema_version: 1
locale: en
title: Demo
content_files: [items.yaml]
`, "- id: a\n  name: Alpha\n- id: b\n  name: Beta\n")

	r := New(dir)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := len(r.List()); got != 1 {
		t.Fatalf("want 1 pack, got %d", got)
	}
	p, err := r.Get("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 2 {
		t.Errorf("items=%d", len(p.Items))
	}
	if p.ETag == "" {
		t.Error("etag empty")
	}
	if _, err := r.Item("demo", "a"); err != nil {
		t.Errorf("Item(a): %v", err)
	}
	hits, err := r.Search("demo", "alpha", 10)
	if err != nil || len(hits) != 1 {
		t.Errorf("search alpha: hits=%d err=%v", len(hits), err)
	}
}

func TestRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, "bad", `id: bad
version: 0.1.0
schema_version: 1
locale: en
title: Bad
content_files: ["../escape.yaml"]
`, "")
	r := New(dir)
	err := r.Reload()
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestEmptyDirIsOK(t *testing.T) {
	r := New("")
	if err := r.Reload(); err != nil {
		t.Errorf("empty dir reload: %v", err)
	}
	if got := len(r.List()); got != 0 {
		t.Errorf("got %d packs", got)
	}
}
