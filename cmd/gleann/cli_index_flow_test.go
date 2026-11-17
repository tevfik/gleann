package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// End-to-end CLI tests of the index lifecycle against the fake Ollama:
// build, list, info, search (text and JSON), tag, set, remove.

func TestCLIIndexBuildListInfoSearch(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	// list: text and JSON.
	r := e.run("index", "list")
	wantExit(t, r, 0)
	mustContain(t, "index list", r.Out, "Indexes (1)", "demo")

	r = e.run("index", "list", "--json")
	wantExit(t, r, 0)
	var metas []gleann.IndexMeta
	if err := json.Unmarshal([]byte(r.Out), &metas); err != nil {
		t.Fatalf("index list --json is not JSON: %v\n%s", err, r.Out)
	}
	if len(metas) != 1 || metas[0].Name != "demo" || metas[0].NumPassages == 0 {
		t.Fatalf("index list --json = %+v", metas)
	}

	// info: text and JSON.
	r = e.run("index", "info", "demo")
	wantExit(t, r, 0)
	mustContain(t, "index info", r.Out, "Index: demo", "Passages:", "Total:")
	r = e.run("index", "info", "demo", "--json")
	wantExit(t, r, 0)
	var meta gleann.IndexMeta
	if err := json.Unmarshal([]byte(r.Out), &meta); err != nil || meta.Name != "demo" {
		t.Fatalf("index info --json = %q (%v)", r.Out, err)
	}

	// search: text and JSON.
	r = e.run("search", "demo", "rate limiter token bucket")
	wantExit(t, r, 0)
	mustContain(t, "search", r.Out, `Results for "rate limiter token bucket"`, "[1] Score:")

	r = e.run("search", "demo", "ValidateToken", "--json", "--top-k", "2")
	wantExit(t, r, 0)
	var results []gleann.SearchResult
	if err := json.Unmarshal([]byte(r.Out), &results); err != nil {
		t.Fatalf("search --json is not JSON: %v\n%s", err, r.Out)
	}
	if len(results) == 0 || len(results) > 2 {
		t.Fatalf("search --json --top-k 2 returned %d results", len(results))
	}
	if !strings.Contains(results[0].Text, "ValidateToken") {
		t.Errorf("top result for ValidateToken = %q", results[0].Text)
	}
}
