package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CLI tests of `gleann index repair` and `gleann index storage`: healthy,
// damaged and unrepairable indexes, --all, --check and --force, and the
// restore-on-failure path of a storage switch.

// fIndexFile returns the path of <name>/<name><suffix> in the index dir.
func (e *cliEnv) fIndexFile(name, suffix string) string {
	return filepath.Join(e.IndexDir, name, name+suffix)
}

// fDropStoreFormat removes store_format from an index's meta.json: the
// health check reports it and a repair restores it.
func (e *cliEnv) fDropStoreFormat(name string) {
	e.t.Helper()
	p := e.fIndexFile(name, ".meta.json")
	var m map[string]any
	if err := json.Unmarshal([]byte(fReadFile(e.t, p)), &m); err != nil {
		e.t.Fatal(err)
	}
	delete(m, "store_format")
	b, _ := json.Marshal(m)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestCLIFRepair(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("index", "repair")
	wantExit(t, r, 1)
	mustContain(t, "no args", r.Err, "usage: gleann index repair")
	r = e.run("index", "repair", "--check")
	wantExit(t, r, 1)

	r = e.run("index", "repair", "--all")
	wantExit(t, r, 0)
	mustContain(t, "no indexes", r.Out, "No indexes found in")

	e.sampleDocs()
	e.build("alpha")
	e.build("beta")

	// Healthy indexes are reported as such.
	r = e.run("index", "repair", "--all", "--check")
	wantExit(t, r, 0)
	mustContain(t, "healthy", r.Out, "✅ alpha: healthy", "✅ beta: healthy")

	// --check reports a damaged index and exits 1 without changing it.
	e.fDropStoreFormat("beta")
	r = e.run("index", "repair", "--all", "--check")
	wantExit(t, r, 1)
	mustContain(t, "check", r.Out, "⚠  beta: needs repair", "store_format", "1 index(es) need repair")
	if strings.Contains(fReadFile(t, e.fIndexFile("beta", ".meta.json")), "store_format") {
		t.Fatal("--check modified the index")
	}

	// --all --force rebuilds both and prints a summary.
	r = e.run("index", "repair", "--all", "--force", "--progress", "none")
	wantExit(t, r, 0)
	mustContain(t, "repaired", r.Out, "Repaired 2 of 2 index(es)", "re-embedded")
	r = e.run("index", "repair", "beta", "--check")
	wantExit(t, r, 0)
	mustContain(t, "after repair", r.Out, "✅ beta: healthy")

	// An index whose passage store is gone cannot be repaired.
	if err := os.Remove(e.fIndexFile("alpha", ".db")); err != nil {
		t.Fatal(err)
	}
	r = e.run("index", "repair", "alpha")
	wantExit(t, r, 1)
	mustContain(t, "not repairable", r.Err, "❌ alpha:", "gleann index rebuild alpha --docs <dir>")

	// The index directory must be readable for --all.
	notDir := filepath.Join(e.Home, "indexes-file")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_INDEX_DIR", notDir)
	r = e.run("index", "repair", "--all")
	wantExit(t, r, 1)
	mustContain(t, "list error", r.Err, "cannot list indexes")
	r = e.run("index", "storage")
	wantExit(t, r, 1)
	mustContain(t, "storage list error", r.Err, "error:")
}

func TestCLIFStorage(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("hnswidx", "--backend", "hnsw")
	e.build("diskidx", "--backend", "diskann")

	// The table lists one index when named.
	r := e.run("index", "storage", "hnswidx")
	wantExit(t, r, 0)
	mustContain(t, "named", r.Out, "INDEX", "hnswidx")
	mustNotContain(t, "named", r.Out, "diskidx")

	// DiskANN always stores vectors.
	r = e.run("index", "storage", "diskidx", "--set", "recompute")
	wantExit(t, r, 1)
	mustContain(t, "diskann", r.Err, `index "diskidx" (diskann backend`, "cannot be switched")

	// A failing rebuild puts the previous files back.
	before := fReadFile(t, e.fIndexFile("hnswidx", ".meta.json"))
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = true
	e.Ollama.mu.Unlock()
	// Empty the embedding cache so the rebuild has to call the embedder.
	if err := os.RemoveAll(filepath.Join(e.Home, ".gleann", "cache", "embeddings")); err != nil {
		t.Fatal(err)
	}
	r = e.run("index", "storage", "hnswidx", "--set", "recompute", "--progress", "none")
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = false
	e.Ollama.mu.Unlock()
	wantExit(t, r, 1)
	mustContain(t, "restored", r.Err, "previous index files restored")
	if after := fReadFile(t, e.fIndexFile("hnswidx", ".meta.json")); after != before {
		t.Errorf("meta.json not restored:\nbefore %s\nafter  %s", before, after)
	}
	r = e.run("search", "hnswidx", "token bucket")
	wantExit(t, r, 0)
}

func TestFHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1536, "1.5 KB"},
		{5 << 20, "5.0 MB"},
		{3 << 30, "3.00 GB"},
	} {
		if got := humanBytes(tc.n); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestFCopyFileErrors(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "dst")); err == nil {
		t.Error("copying a missing file succeeded")
	}
	if err := copyFile(src, filepath.Join(fBlockedPath(t), "dst")); err == nil {
		t.Error("copying into a blocked directory succeeded")
	}
	// Reading a directory as a file fails part-way.
	if err := copyFile(dir, filepath.Join(t.TempDir(), "dst")); err == nil {
		t.Error("copying a directory succeeded")
	}
	dst := filepath.Join(dir, "dst")
	if err := copyFile(src, dst); err != nil || fReadFile(t, dst) != "data" {
		t.Errorf("copyFile: %v", err)
	}
}
