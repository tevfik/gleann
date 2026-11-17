package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/testutil/format1"
	"github.com/tevfik/gleann/pkg/gleann"
)

// CLI tests of index management: usage errors, tags, MCP exposure, removal
// patterns, storage switching, rebuild, sync, watch and the refusal of an
// index in the old store format.

func TestCLIIndexUsageErrors(t *testing.T) {
	e := newCLIEnv(t)
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no subcommand", []string{"index"}, 1, "gleann index"},
		{"unknown subcommand", []string{"index", "frobnicate"}, 1, "unknown index subcommand: frobnicate"},
		{"info without name", []string{"index", "info"}, 1, "usage: gleann info <name>"},
		{"info of missing index", []string{"index", "info", "nope"}, 1, `index "nope" not found`},
		{"tag without name", []string{"index", "tag"}, 1, "usage: gleann index tag"},
		{"tag of missing index", []string{"index", "tag", "nope"}, 1, "error"},
		{"set without name", []string{"index", "set"}, 1, "usage: gleann index set"},
		{"set without change", []string{"index", "set", "nope"}, 1, "usage: gleann index set"},
		{"set of missing index", []string{"index", "set", "nope", "--desc", "x"}, 1, `error updating index "nope"`},
		{"remove without name", []string{"index", "remove"}, 1, "usage: gleann remove"},
		{"remove only flags", []string{"index", "remove", "--index-dir", e.IndexDir}, 1, "no index name or pattern provided"},
		{"rebuild without name", []string{"index", "rebuild"}, 1, "usage: gleann index rebuild"},
		{"rebuild flag as name", []string{"index", "rebuild", "--graph"}, 1, "looks like a flag"},
		{"rebuild without docs", []string{"index", "rebuild", "nope"}, 1, "records no source directory"},
		{"sync without name", []string{"index", "sync"}, 1, "usage: gleann index sync"},
		{"sync flag as name", []string{"index", "sync", "--graph"}, 1, "looks like a flag"},
		{"watch without name", []string{"index", "watch"}, 1, "usage: gleann index watch"},
		{"watch flag as name", []string{"index", "watch", "--graph"}, 1, "looks like a flag"},
		{"watch without docs", []string{"index", "watch", "x"}, 1, "--docs flag required"},
		{"storage set without name", []string{"index", "storage", "--set", "store"}, 1, "usage: gleann index storage"},
		{"storage bad mode", []string{"index", "storage", "nope", "--set", "fast"}, 1, "--set must be"},
		{"storage missing index", []string{"index", "storage", "nope", "--set", "store"}, 1, "error"},
		{"search too few args", []string{"search", "demo"}, 1, "usage: gleann search"},
		{"search missing index", []string{"search", "nope", "query"}, 1, "error loading index"},
		{"unknown flag", []string{"index", "list", "--bogus-flag"}, 2, "--bogus-flag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.run(c.args...)
			wantExit(t, r, c.code)
			mustContain(t, strings.Join(c.args, " "), r.combined(), c.want)
		})
	}
}

func TestCLIIndexHelpTexts(t *testing.T) {
	e := newCLIEnv(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"index", "help"}, "gleann index"},
		{[]string{"index", "list", "--help"}, "Usage: gleann index list"},
		{[]string{"index", "info", "--help"}, "Usage: gleann index info"},
		{[]string{"index", "remove", "--help"}, "Usage: gleann index remove"},
		{[]string{"index", "rebuild", "--help"}, "Usage: gleann index rebuild"},
		{[]string{"index", "watch", "--help"}, "Usage: gleann index watch"},
		{[]string{"index", "sync", "--help"}, "usage: gleann index sync"},
		{[]string{"index", "storage", "--help"}, "Usage: gleann index storage"},
		{[]string{"index", "tag", "--help"}, "usage: gleann index tag"},
		{[]string{"index", "set", "--help"}, "usage: gleann index set"},
		{[]string{"search", "--help"}, "Usage: gleann search"},
	} {
		r := e.run(c.args...)
		wantExit(t, r, 0)
		mustContain(t, strings.Join(c.args, " "), r.combined(), c.want)
	}
}

func TestCLIIndexListEmptyAndFilters(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("index", "list")
	wantExit(t, r, 0)
	mustContain(t, "empty list", r.Out, "No indexes found.")

	e.sampleDocs()
	e.build("alpha")
	e.build("beta")
	wantExit(t, e.run("index", "tag", "alpha", "--add", "work,@backend"), 0)
	wantExit(t, e.run("index", "set", "beta", "--private", "--desc", "Beta docs"), 0)

	r = e.run("index", "list", "--tag", "work")
	wantExit(t, r, 0)
	mustContain(t, "tag filter", r.Out, "Indexes (1)", "alpha", "tags: [@backend, @work]")
	mustNotContain(t, "tag filter", r.Out, "beta")

	r = e.run("index", "list", "--private")
	wantExit(t, r, 0)
	mustContain(t, "private filter", r.Out, "beta", "Private", "↳ Beta docs")
	mustNotContain(t, "private filter", r.Out, "alpha")

	r = e.run("index", "list", "--mcp")
	wantExit(t, r, 0)
	mustContain(t, "mcp filter", r.Out, "alpha", "Public (MCP)")
	mustNotContain(t, "mcp filter", r.Out, "beta")

	// A model different from the one the index was built with is flagged.
	r = e.run("index", "list", "--model", "other-embedder")
	wantExit(t, r, 0)
	mustContain(t, "model mismatch", r.Out, "needs rebuild for other-embedder")
}

func TestCLIIndexTagAndSet(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	r := e.run("index", "tag", "demo")
	wantExit(t, r, 0)
	mustContain(t, "no tags", r.Out, `Index "demo" has no tags.`)

	r = e.run("index", "tag", "demo", "--add", "Work, @Infra")
	wantExit(t, r, 0)
	mustContain(t, "add", r.Out, "@infra, @work")

	r = e.run("index", "tag", "demo", "--remove", "@infra")
	wantExit(t, r, 0)
	mustContain(t, "remove", r.Out, `Updated tags for "demo": @work`)

	r = e.run("index", "tag", "demo", "--set", "b,a,A,@b")
	wantExit(t, r, 0)
	mustContain(t, "set", r.Out, "@a, @b")

	r = e.run("index", "tag", "demo")
	wantExit(t, r, 0)
	mustContain(t, "show", r.Out, `Tags for "demo": @a, @b`)

	r = e.run("index", "tag", "demo", "--clear")
	wantExit(t, r, 0)
	mustContain(t, "clear", r.Out, "(none)")

	r = e.run("index", "set", "demo", "--mcp", "false")
	wantExit(t, r, 0)
	mustContain(t, "mcp=false", r.Out, "Private (Hidden from MCP)", "Description:   (none)")
	r = e.run("index", "set", "demo", "--public", "--desc", "Demo project")
	wantExit(t, r, 0)
	mustContain(t, "public", r.Out, "Public (MCP Exposed)", "Demo project")

	meta, err := gleann.GetIndexMeta(e.IndexDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !meta.IsMCPExposed() || meta.Description != "Demo project" || len(meta.Tags) != 0 {
		t.Errorf("meta after tag/set = %+v", meta)
	}

	r = e.run("index", "info", "demo")
	wantExit(t, r, 0)
	mustContain(t, "info", r.Out, "Description: Demo project", "Exposed to MCP")
}

func TestCLIIndexRemovePatterns(t *testing.T) {
	e := newCLIEnv(t)
	e.write("a.md", "# A\n\nalpha text\n")
	for _, n := range []string{"proj-a", "proj-b", "other"} {
		e.build(n)
	}

	r := e.run("index", "remove", "zzz*")
	wantExit(t, r, 0)
	mustContain(t, "no match", r.Out, "No matching indexes found.")

	r = e.run("index", "remove", "proj-*", "--index-dir", e.IndexDir)
	wantExit(t, r, 0)
	mustContain(t, "pattern", r.Out, `Index "proj-a" removed.`, `Index "proj-b" removed.`, "Successfully removed 2 indexes.")
	metas, _ := gleann.ListIndexes(e.IndexDir)
	if len(metas) != 1 || metas[0].Name != "other" {
		t.Fatalf("after removing proj-*: %+v", metas)
	}

	// "*" asks for confirmation; anything but y aborts.
	r = e.runIn("n\n", "index", "remove", "*")
	wantExit(t, r, 0)
	mustContain(t, "abort", r.Out, "Are you sure", "Aborted.")
	if metas, _ := gleann.ListIndexes(e.IndexDir); len(metas) != 1 {
		t.Fatalf("aborted remove deleted indexes: %+v", metas)
	}
	r = e.runIn("y\n", "index", "remove", "*")
	wantExit(t, r, 0)
	mustContain(t, "confirm", r.Out, `Index "other" removed.`)
	if metas, _ := gleann.ListIndexes(e.IndexDir); len(metas) != 0 {
		t.Fatalf("confirmed remove left indexes: %+v", metas)
	}
}

func TestCLIIndexStorageSwitch(t *testing.T) {
	e := newCLIEnv(t)
	// With large vectors the stored ones dominate the index file, which is
	// how InspectVectorStorage tells the modes apart.
	e.Ollama.setDim(512)
	e.sampleDocs()
	e.build("demo", "--backend", "hnsw")

	r := e.run("index", "storage")
	wantExit(t, r, 0)
	mustContain(t, "table", r.Out, "INDEX", "MODE", "demo", "hnsw")

	r = e.run("index", "storage", "demo")
	wantExit(t, r, 0)
	mustContain(t, "one row", r.Out, "demo")

	r = e.run("index", "storage", "demo", "--set", "store")
	wantExit(t, r, 0)
	mustContain(t, "already", r.Out, `index "demo" already uses store storage`)

	r = e.run("index", "storage", "demo", "--set", "recompute", "--progress", "none")
	wantExit(t, r, 0)
	mustContain(t, "to recompute", r.Out, "demo: store -> recompute")
	vs := inspectStorage(t, e.IndexDir, "demo")
	if vs.Mode != gleann.StorageRecompute {
		t.Fatalf("mode after --set recompute = %q", vs.Mode)
	}

	r = e.run("index", "storage", "demo", "--set", "store", "--progress", "none")
	wantExit(t, r, 0)
	mustContain(t, "to store", r.Out, "demo: recompute -> store")

	// Searching still works after the round trip.
	r = e.run("search", "demo", "ValidateToken", "--json")
	wantExit(t, r, 0)
	if !strings.Contains(r.Out, "ValidateToken") {
		t.Errorf("search after storage round trip: %s", r.Out)
	}
}

func inspectStorage(t *testing.T, dir, name string) gleann.VectorStorage {
	t.Helper()
	meta, err := gleann.GetIndexMeta(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	return gleann.InspectVectorStorage(dir, *meta)
}

func TestCLIIndexRebuildKeepsSettings(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--tags", "work", "--desc", "Demo")
	wantExit(t, e.run("index", "set", "demo", "--private"), 0)
	e.write("extra.md", "# Extra\n\nA freshly added note about zebras.\n")

	r := e.run("index", "rebuild", "demo", "--no-report", "--no-agents")
	wantExit(t, r, 0)
	mustContain(t, "rebuild", r.Out, `Removing existing index "demo"`, "Removed.", `Rebuilding index "demo" from `+e.Docs)

	meta, err := gleann.GetIndexMeta(e.IndexDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != "Demo" || !meta.HasTag("work") || meta.IsMCPExposed() || meta.SourceDir != e.Docs {
		t.Errorf("rebuild lost settings: %+v", meta)
	}
	r = e.run("search", "demo", "zebras", "--json", "--top-k", "1")
	wantExit(t, r, 0)
	mustContain(t, "new file indexed", r.Out, "zebras")

	// Rebuild of an index that does not exist yet just builds it.
	r = e.run("index", "rebuild", "fresh", "--docs", e.Docs, "--no-report", "--no-agents")
	wantExit(t, r, 0)
	if _, err := gleann.GetIndexMeta(e.IndexDir, "fresh"); err != nil {
		t.Errorf("rebuild of a new index did not build it: %v", err)
	}
}

func TestCLIIndexSync(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	r := e.run("index", "sync", "demo")
	wantExit(t, r, 0)
	mustContain(t, "unchanged", r.combined(), "up to date")

	e.write("notes/new.md", "# New\n\nThe quokka is a small marsupial.\n")
	r = e.run("index", "sync", "demo", "--progress", "none")
	wantExit(t, r, 0)
	r = e.run("search", "demo", "quokka marsupial", "--json", "--top-k", "1")
	wantExit(t, r, 0)
	mustContain(t, "synced file searchable", r.Out, "quokka")

	// A sync of an index without a recorded source directory needs --docs.
	r = e.run("index", "sync", "missing")
	wantExit(t, r, 1)
	mustContain(t, "missing index", r.Err, "error")
}

func TestCLIIndexWatchInitialBuildAndSync(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	orig := watchNotify
	t.Cleanup(func() { watchNotify = orig })
	// Stop as soon as the watcher enters its loop.
	watchNotify = func(stop chan<- os.Signal) { stop <- os.Interrupt }

	r := e.run("index", "watch", "w", "--docs", e.Docs, "--interval", "1", "--graph")
	wantExit(t, r, 0)
	mustContain(t, "first watch", r.Out, "Watching "+e.Docs, "Index: w", "Stopping watcher...")
	if _, err := gleann.GetIndexMeta(e.IndexDir, "w"); err != nil {
		t.Fatalf("watch did not build the index: %v", err)
	}
	if !gleann.HasCodeGraph(e.IndexDir, "w") {
		t.Error("watch --graph did not build the code graph")
	}

	// A second watch syncs the existing index instead of rebuilding it.
	r = e.run("index", "watch", "w", "--docs", e.Docs)
	wantExit(t, r, 0)
	mustContain(t, "second watch", r.Out, `Index "w" exists, syncing instead of rebuilding`)

	// A docs directory that does not exist cannot be watched.
	r = e.run("index", "watch", "w2", "--docs", filepath.Join(e.Docs, "missing"))
	wantExit(t, r, 1)
}

func TestCLIOldFormatIndexIsRefused(t *testing.T) {
	e := newCLIEnv(t)
	writeOldFormatIndex(t, e.IndexDir, "legacy")

	for _, sub := range []string{"info", "sync", "tag", "set", "storage", "repair"} {
		args := []string{"index", sub, "legacy"}
		if sub == "set" {
			args = append(args, "--desc", "x")
		}
		r := e.run(args...)
		wantExit(t, r, 1)
		mustContain(t, sub, r.Err, "gleann index migrate")
	}
	r := e.run("index", "list")
	wantExit(t, r, 0)
	mustContain(t, "list marks it", r.Out, "legacy", "old store format: run gleann index migrate legacy")
}

// writeOldFormatIndex writes a minimal index in the v1.9 store layout.
func writeOldFormatIndex(t *testing.T, dir, name string) {
	t.Helper()
	if err := format1.Write(format1.Index{
		Dir: dir, Name: name,
		Passages: []format1.Passage{{ID: 0, Text: "old text", Metadata: map[string]any{"source": "a.md"}}},
		Meta:     map[string]any{"backend": "hnsw", "embedding_model": "m", "dimensions": 8},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCLISearchVariants(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	// --explain prints segment importance and BM25 terms.
	r := e.run("search", "demo", "token bucket rate limiter", "--explain", "--top-k", "2")
	wantExit(t, r, 0)
	mustContain(t, "explain", r.Out, "similarity", "BM25 terms:")

	r = e.run("search", "demo", "token bucket", "--explain", "--json", "--top-k", "1")
	wantExit(t, r, 0)
	var explained []map[string]any
	if err := json.Unmarshal([]byte(r.Out), &explained); err != nil || len(explained) != 1 || explained[0]["explanation"] == nil {
		t.Fatalf("--explain --json = %s (%v)", r.Out, err)
	}

	// Reranking through a /v1/rerank server (the fake Ollama serves it).
	r = e.run("search", "demo", "deploy script", "--rerank", "--rerank-url", e.Ollama.URL, "--rerank-pool", "5", "--json")
	wantExit(t, r, 0)
	if e.Ollama.count("/v1/rerank") == 0 {
		t.Error("--rerank-url did not call the rerank server")
	}
	var rr []gleann.SearchResult
	if err := json.Unmarshal([]byte(r.Out), &rr); err != nil || len(rr) == 0 || !strings.Contains(rr[0].Text, "deploy script") {
		t.Errorf("reranked top result = %+v (%v)", rr, err)
	}

	// Pure vector search and the other result shaping flags.
	r = e.run("search", "demo", "authentication", "--no-hybrid", "--one-per-file", "--include-tests", "--kind", "doc", "--graph")
	wantExit(t, r, 0)
	mustContain(t, "flags", r.Out, `Results for "authentication"`)
	r = e.run("search", "demo", "authentication", "--hybrid-alpha", "0.3", "--no-rerank")
	wantExit(t, r, 0)

	// Multi-index: comma separated, --all and the <name>-code/-docs pair.
	e.build("demo-code", "--mode", "code")
	e.build("demo-docs", "--mode", "docs")
	r = e.run("search", "demo-code,demo-docs", "ValidateToken", "--explain")
	wantExit(t, r, 0)
	mustContain(t, "multi", r.Out, "Multi-index results", "Index: demo-")
	mustContain(t, "multi explain note", r.Err, "--explain works for a single index only")

	r = e.run("search", "--all", "deploy", "--json")
	wantExit(t, r, 0)
	var multi []map[string]any
	if err := json.Unmarshal([]byte(r.Out), &multi); err != nil || len(multi) == 0 {
		t.Fatalf("search --all --json = %s (%v)", r.Out, err)
	}

	wantExit(t, e.run("index", "remove", "demo"), 0)
	r = e.run("search", "demo", "ValidateToken")
	wantExit(t, r, 0)
	mustContain(t, "auto pair", r.Out, "Multi-index results")
}
