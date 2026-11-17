package main

import (
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// CLI tests of `gleann search` and `gleann def`: option handling, long
// results, the graph context and the failures of the embedding backend.

// eLongDocs writes a corpus with a passage longer than the 200 and 300
// characters the printers cut at, plus the sample project (with a graph).
func eLongDocs(e *cliEnv) {
	e.sampleDocs()
	e.write("long.md", "# Long note\n\n"+strings.Repeat("zebra stripes alternate between black and white bands. ", 12)+"\n")
}

func TestCLIESearchLongResultsAndExpand(t *testing.T) {
	e := newCLIEnv(t)
	eLongDocs(e)
	e.build("long")
	e.build("other")
	e.Ollama.setReply("zebra stripes black white bands")

	// Long passages are cut in the listing, single and multi index.
	r := e.run("search", "long", "zebra stripes bands")
	wantExit(t, r, 0)
	mustContain(t, "single", r.Out, "Results for", "zebra stripes alternate", "...")
	r = e.run("search", "long,other", "zebra stripes bands")
	wantExit(t, r, 0)
	mustContain(t, "multi", r.Out, "Multi-index results for", "Index: long", "...")

	// --expand asks the LLM for a sketch and searches with it too.
	before := e.Ollama.count("/api/chat") + e.Ollama.count("/api/generate")
	r = e.run("search", "long", "how do zebras look", "--expand")
	wantExit(t, r, 0)
	if after := e.Ollama.count("/api/chat") + e.Ollama.count("/api/generate"); after <= before {
		t.Errorf("--expand made no LLM call (%d -> %d)", before, after)
	}
	mustContain(t, "expanded search", r.Out, "Results for")

	// The explanation cuts long segments as well.
	r = e.run("search", "long", "zebra stripes bands", "--explain", "--top-k", "1")
	wantExit(t, r, 0)
	mustContain(t, "explain", r.Out, "similarity", "▸ Δ")
}

func TestCLIESearchGraphContext(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("code", "--graph")

	r := e.run("search", "code", "ValidateToken checkSignature", "--graph")
	wantExit(t, r, 0)
	mustContain(t, "graph context", r.Out, "Graph Context:", "ValidateToken")
}

func TestCLIESearchFailures(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	r := e.run("search", "demo", "q", "--provider", "llamacpp", "--model", "bge-m3")
	wantExit(t, r, 1)
	mustContain(t, "llamacpp", r.Err, "error initializing llamacpp")

	r = e.run("search", "demo,missing", "q")
	wantExit(t, r, 1)
	mustContain(t, "multi-search", r.Err, "error in multi-search")

	// The embedding backend going down is an error, not an empty result.
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = true
	e.Ollama.mu.Unlock()
	r = e.run("search", "demo", "rate limiter", "--no-hybrid")
	wantExit(t, r, 1)
	mustContain(t, "search failure", r.Err, "error searching")

	// With keyword-only scoring the search still works; the explanation,
	// which needs embeddings, stops with a message and the results stay.
	r = e.run("search", "demo", "rate limiter", "--hybrid-alpha", "0", "--explain")
	wantExit(t, r, 0)
	mustContain(t, "results stay", r.Out, "Results for", "token bucket rate limiter")
	mustContain(t, "explain failure", r.Err, "explain: explain: embed")
}

func TestCLIERerankerConfigFromFlags(t *testing.T) {
	cfg := gleann.DefaultConfig()
	got := rerankerConfigFromFlags(cfg, []string{"--rerank-provider", "jina", "--rerank-model", "m"})
	if got.Provider != gleann.RerankerJina || got.Model != "m" {
		t.Errorf("--rerank-provider/--rerank-model: %+v", got)
	}
	got = rerankerConfigFromFlags(cfg, []string{"--rerank-url", "http://127.0.0.1:1"})
	if got.Provider != gleann.RerankerJina || got.BaseURL != "http://127.0.0.1:1" || got.Model != gleann.DefaultServerRerankModel {
		t.Errorf("--rerank-url: %+v", got)
	}
}

func TestCLIEDef(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("def")
	wantExit(t, r, 1)
	mustContain(t, "usage", r.combined(), "Usage: gleann def")
	r = e.run("def", "--help")
	wantExit(t, r, 0)
	r = e.run("def", "--json")
	wantExit(t, r, 1)
	mustContain(t, "no symbol", r.Err, "symbol argument is required")
	r = e.run("def", "ValidateToken")
	wantExit(t, r, 1)
	mustContain(t, "no index", r.Err, "no index found")
	r = e.run("def", "ValidateToken", "--index", "missing")
	wantExit(t, r, 1)
	mustContain(t, "missing index", r.Err, `error loading index "missing"`)

	e.sampleDocs()
	e.build("demo", "--graph")
	// -k limits the definitions; the first index is used when none is named.
	r = e.run("def", "ValidateToken", "-k", "1")
	wantExit(t, r, 0)
	mustContain(t, "definition", r.Out, "ValidateToken", "auth/token.go")
	r = e.run("def", "ValidateToken", "-k", "x", "--kind", "function", "--json")
	wantExit(t, r, 0)
	mustContain(t, "json", r.Out, "ValidateToken")
	r = e.run("def", "NoSuchSymbolAnywhere")
	wantExit(t, r, 0)
	mustContain(t, "none", r.Out, `No definition found for symbol "NoSuchSymbolAnywhere" in index "demo".`)
}
