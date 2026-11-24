package main

import (
	"os"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/tui"
)

// setup --bootstrap honours --model, --provider and --llm-model, and refuses
// to overwrite a completed configuration without --force.
func TestCLIBootstrapHonoursFlagsAndRefusesOverwrite(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("setup", "--bootstrap", "--host", e.Ollama.URL,
		"--model", "my-embed", "--provider", "openai", "--llm-model", "my-chat")
	wantExit(t, r, 0)
	cfg := d2ReadConfig(t, e)
	if cfg["embedding_model"] != "my-embed" || cfg["embedding_provider"] != "openai" || cfg["llm_model"] != "my-chat" {
		t.Fatalf("flags ignored: %v", cfg)
	}

	before, err := os.ReadFile(d2ConfigPath(e))
	if err != nil {
		t.Fatal(err)
	}
	r = e.run("setup", "--bootstrap", "--host", e.Ollama.URL)
	wantExit(t, r, 1)
	mustContain(t, "refused", r.Err, "already exists", "--force")
	after, _ := os.ReadFile(d2ConfigPath(e))
	if string(before) != string(after) {
		t.Fatalf("config changed by a refused bootstrap:\n%s\n---\n%s", before, after)
	}

	r = e.run("setup", "--bootstrap", "--force", "--host", e.Ollama.URL, "--model", "other-embed")
	wantExit(t, r, 0)
	if cfg := d2ReadConfig(t, e); cfg["embedding_model"] != "other-embed" {
		t.Fatalf("--force did not replace the config: %v", cfg)
	}
	bak, err := os.ReadFile(d2ConfigPath(e) + ".bak")
	if err != nil || !strings.Contains(string(bak), "my-embed") {
		t.Fatalf("backup missing or wrong: %v %s", err, bak)
	}
}

// doctor warns about an enabled reranker that cannot rerank (Ollama has no
// rerank API) instead of calling it healthy.
func TestRerankerUnusable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     *tui.OnboardResult
		warning bool
	}{
		{"nil", nil, false},
		{"off", &tui.OnboardResult{EmbeddingProvider: "ollama", RerankModel: "bge-reranker"}, false},
		{"ollama explicit", &tui.OnboardResult{RerankEnabled: true, RerankProvider: "ollama", RerankModel: "bge-reranker"}, true},
		{"auto over ollama", &tui.OnboardResult{RerankEnabled: true, EmbeddingProvider: "ollama", RerankModel: "bge-reranker"}, true},
		{"llamacpp", &tui.OnboardResult{RerankEnabled: true, RerankProvider: "llamacpp", RerankModel: "bge-reranker"}, false},
	} {
		if got := rerankerUnusable(tc.cfg) != ""; got != tc.warning {
			t.Errorf("%s: warning=%v, want %v", tc.name, got, tc.warning)
		}
	}
}

// A search without a query is an error (exit 2), as on REST and MCP, and a
// numeric flag with a value that is not a number is an error instead of 0.
func TestCLISearchEmptyQueryAndBadNumber(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("code")

	for _, args := range [][]string{
		{"search", "code", "--json"},
		{"search", "code", "--kind", "-h"},
		{"search", "code", "  "},
	} {
		r := e.run(args...)
		wantExit(t, r, 2)
		mustContain(t, strings.Join(args, " "), r.Err, "no query")
	}

	r := e.run("search", "code", "token", "--hybrid", "abc")
	wantExit(t, r, 2)
	mustContain(t, "hybrid", r.Err, "--hybrid needs a number")
	for _, a := range [][]string{{"--top-k", "x"}, {"--top-k", "10x"}, {"--top-k", "1.5"}, {"--hybrid", "0.5abc"}} {
		wantExit(t, e.run(append([]string{"search", "code", "token"}, a...)...), 2)
	}

	wantExit(t, e.run("search", "code", "token", "--hybrid", "0.5"), 0)
}
