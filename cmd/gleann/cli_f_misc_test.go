package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CLI tests of smaller commands and their failure paths: query expansion
// flags, `gleann multimodal analyze`, `gleann tokens` and `gleann config`.

// --expand asks the LLM for a hypothetical answer before searching; the
// model comes from --expand-model, then the saved expand_model. --no-expand
// overrides a saved "expand": true.
func TestCLIFSearchQueryExpansion(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")
	e.Ollama.setReply("Tokens are validated before each request by the token bucket.")

	before := e.Ollama.count("/api/chat") + e.Ollama.count("/api/generate")
	r := e.run("search", "demo", "how are tokens checked", "--expand", "--expand-model", "gemma3:4b")
	wantExit(t, r, 0)
	after := e.Ollama.count("/api/chat") + e.Ollama.count("/api/generate")
	if after == before {
		t.Fatal("--expand made no LLM call")
	}
	body := e.Ollama.lastBody("/api/chat") + e.Ollama.lastBody("/api/generate")
	mustContain(t, "expand model", body, "gemma3:4b")

	fSaveConfig(t, e.Home, `{"expand": true, "expand_model": "llama3.2:latest", "ollama_host": "`+e.Ollama.URL+`"}`)
	r = e.run("search", "demo", "rate limiter")
	wantExit(t, r, 0)
	mustContain(t, "saved expand model", e.Ollama.lastBody("/api/chat")+e.Ollama.lastBody("/api/generate"), "llama3.2:latest")

	before = e.Ollama.count("/api/chat") + e.Ollama.count("/api/generate")
	r = e.run("search", "demo", "rate limiter", "--no-expand")
	wantExit(t, r, 0)
	if got := e.Ollama.count("/api/chat") + e.Ollama.count("/api/generate"); got != before {
		t.Errorf("--no-expand still called the LLM (%d -> %d)", before, got)
	}
}

func TestCLIFMultimodalLongDescriptionsAndPDF(t *testing.T) {
	e := newCLIEnv(t)
	long := strings.Repeat("A detailed description of the picture. ", 20) // > 300 chars
	e.Ollama.setReply(long)
	e.write("pics/one.png", "\x89PNG\r\n\x1a\nfirst image")

	// Directory results are cut at 200 characters.
	r := e.run("multimodal", "analyze", filepath.Join(e.Docs, "pics"), "--model", "gemma3:4b", "--ollama-api-key", "k-123")
	wantExit(t, r, 0)
	mustContain(t, "dir", r.Out, "Processed 1 multimodal files", "one.png", "...")
	for _, line := range strings.Split(r.Out, "\n") {
		if _, desc, ok := strings.Cut(line, "one.png: "); ok && (len(desc) != 203 || !strings.HasSuffix(desc, "...")) {
			t.Errorf("description not cut at 200 characters: %d bytes %q", len(desc), desc)
		}
	}

	// A file that is not a PDF fails to render.
	pdf := e.write("bad.pdf", "this is not a pdf")
	r = e.run("multimodal", "analyze", pdf, "--model", "gemma3:4b")
	wantExit(t, r, 1)
	mustContain(t, "bad pdf", r.Err, "error:")
}

func TestCLIFTokensEdgeCases(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("tokens")
	wantExit(t, r, 1)
	mustContain(t, "usage", r.Out, "gleann tokens <file-or-dir>")

	r = e.run("tokens", filepath.Join(e.Docs, "missing"))
	wantExit(t, r, 1)
	mustContain(t, "missing", r.Err, "Error:")

	// Only binaries and invalid UTF-8: nothing readable.
	e.write("bin/logo.png", "\x89PNG")
	e.write("bin/blob.txt", "\xff\xfe\xfd")
	r = e.run("tokens", filepath.Join(e.Docs, "bin"))
	wantExit(t, r, 0)
	mustContain(t, "no text", r.Out, "No readable text files found.")

	// An empty text file counts as a file with zero tokens: no division by
	// zero, compression reported as 1.0x.
	e.write("empty/blank.md", "")
	r = e.run("tokens", filepath.Join(e.Docs, "empty"))
	wantExit(t, r, 0)
	mustContain(t, "empty", r.Out, "Scanned 1 file(s)", "0.0%", "1.0x")
	mustNotContain(t, "empty", r.Out, "Inf", "NaN")
}

func TestCLIFConfigFailures(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.Home, ".gleann", "config.json")

	// config.json is a directory: validate cannot read it, set cannot save.
	if err := os.MkdirAll(cfgPath, 0o755); err != nil {
		t.Fatal(err)
	}
	r := e.run("config", "validate")
	wantExit(t, r, 1)
	mustContain(t, "validate read error", r.Err, "error reading config")
	// Other commands refuse to run on an unreadable config.
	r = e.run("config", "set", "llm_model", "x")
	wantExit(t, r, 1)
	mustContain(t, "set read error", r.Err, "error: config: reading")
	if err := os.RemoveAll(cfgPath); err != nil {
		t.Fatal(err)
	}

	// An index directory below a regular file cannot be inspected.
	blocked := fBlockedPath(t)
	fSaveConfig(t, e.Home, `{"embedding_provider": "openai", "llm_provider": "openai", "embedding_model": "m",
		"index_dir": "`+filepath.ToSlash(blocked)+`"}`)
	r = e.run("config", "validate")
	wantExit(t, r, 0)
	if !strings.Contains(r.Out, "Index Directory error") && !strings.Contains(r.Out, "Index Directory does not exist") {
		t.Errorf("no index directory warning:\n%s", r.Out)
	}
}
