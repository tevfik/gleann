package main

import (
	"os"
	"path/filepath"
	"testing"
)

// CLI tests of `gleann index remove|tag`, `gleann tui` and the benchmark
// failure paths.

func TestCLIEManageFailures(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	// A name that is not an index name is tried literally and refused.
	r := e.run("index", "remove", "../x")
	mustContain(t, "literal name", r.Err, `error removing "../x"`)

	// Tags: the metadata of the index is unreadable.
	meta := filepath.Join(e.IndexDir, "demo", "demo.meta.json")
	if err := os.WriteFile(meta, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("index", "tag", "demo", "--add", "x")
	wantExit(t, r, 1)
	mustContain(t, "tag failure", r.Err, `error updating tags for "demo"`)

	// The index directory is a file: indexes cannot be listed.
	file := filepath.Join(e.Home, "indexes-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_INDEX_DIR", file)
	r = e.run("index", "remove", "demo")
	wantExit(t, r, 1)
	mustContain(t, "list failure", r.Err, "error listing indexes")
}

func TestCLIETUIRequiresTerminal(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("tui")
	wantExit(t, r, 1)
	mustContain(t, "tui guard", r.Err, "gleann tui requires an interactive terminal")
}

func TestCLIERipgrepSearchTerms(t *testing.T) {
	for _, q := range []string{"", "   ", `"" ()`} {
		paths, tokens, err := runRipgrepSearch(q, 3)
		if err != nil || len(paths) != 0 || tokens != 0 {
			t.Errorf("runRipgrepSearch(%q) = %v %d %v", q, paths, tokens, err)
		}
	}
}
