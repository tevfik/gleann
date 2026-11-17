package main

import (
	"os"
	"path/filepath"
	"testing"
)

// CLI tests of the failure paths of `gleann setup --auto` and `--bootstrap`.

func TestCLIFSetupAutoFailures(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	host := e.Ollama.URL

	// An unknown vector backend stops the build before anything is indexed.
	r := e.run("setup", "--auto", "--yes", "--host", host, "--docs", e.Docs, "--name", "badbackend", "--backend", "nope")
	wantExit(t, r, 1)
	mustContain(t, "bad backend", r.Err, "error:", "nope")

	// A failing embedding backend is reported as a build error.
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = true
	e.Ollama.mu.Unlock()
	r = e.run("setup", "--auto", "--yes", "--host", host, "--docs", e.Docs, "--name", "embfail",
		"--code-embedding-model", "jina-code", "--doc-embedding-model", "bge-small")
	wantExit(t, r, 1)
	mustContain(t, "embed failure", r.Err, "error building index")
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = false
	e.Ollama.mu.Unlock()

	// --docs naming a missing directory cannot be read.
	r = e.run("setup", "--auto", "--yes", "--host", host, "--docs", filepath.Join(e.Docs, "missing"), "--name", "missing")
	wantExit(t, r, 1)
	mustContain(t, "missing docs", r.Err, "error")
}

// The vault (file-change tracker) is optional: when it cannot be opened the
// build goes on without it.
func TestCLIFSetupAutoWithoutVault(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	vault := filepath.Join(e.Home, ".gleann", "vault", "vault.db")
	// A non-empty directory where the DB file goes: it can neither be
	// opened nor removed and recreated.
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.run("setup", "--auto", "--yes", "--host", e.Ollama.URL, "--docs", e.Docs, "--name", "novault")
	wantExit(t, r, 0)
	mustContain(t, "vault warning", r.Err, "warning: vault tracker unavailable")
	mustContain(t, "built", r.Out, "passages indexed")
}

// The configuration cannot be saved when ~/.gleann is not a directory.
func TestCLIFSetupAutoConfigNotSaved(t *testing.T) {
	e := newCLIEnv(t)
	cfgDir := filepath.Join(e.Home, ".gleann")
	if err := os.WriteFile(cfgDir, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.run("setup", "--auto", "--yes", "--host", e.Ollama.URL, "--docs", e.Docs)
	wantExit(t, r, 1)
	mustContain(t, "save error", r.Err, "error saving config")
}

func TestCLIFSetupBootstrapAliasesAndFailure(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("setup", "--bootstrap", "--host", e.Ollama.URL,
		"--code-embedding-model", "jina-code", "--doc-embedding-model", "bge-small")
	wantExit(t, r, 0)
	cfg := d2ReadConfig(t, e)
	if cfg["code_embedding_model"] != "jina-code" || cfg["doc_embedding_model"] != "bge-small" {
		t.Errorf("bootstrap aliases not saved: %v", cfg)
	}

	// The bootstrap fails when the configuration cannot be saved.
	cfgDir := filepath.Join(e.Home, ".gleann")
	if err := os.RemoveAll(cfgDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgDir, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("setup", "--bootstrap", "--host", e.Ollama.URL)
	wantExit(t, r, 1)
	mustContain(t, "bootstrap failure", r.Err, "error:", "save config")
}
