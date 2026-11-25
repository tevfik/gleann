package main

import (
	"fmt"
	"strings"
	"testing"
)

// First-run `gleann serve` without --host resolves the Ollama host from
// OLLAMA_HOST (the harness points it at a fake), and when that Ollama is
// reachable but lacks the default models it informs and guides: it never
// requests /api/pull.
func TestServeFirstRunHonoursOllamaHostAndNeverPulls(t *testing.T) {
	e := newCLIEnv(t)
	e.Ollama.mu.Lock()
	e.Ollama.models = []string{"llama3.2:latest"} // none of the default models
	e.Ollama.mu.Unlock()
	addr := fmt.Sprintf("127.0.0.1:%d", d2FreePort(t))

	r, ok := d2Serve(t, e, func(string) {}, "--addr", addr)
	wantExit(t, r, 0)
	if !ok {
		t.Fatalf("server never became healthy:\n%s\n%s", r.Out, r.Err)
	}
	if e.Ollama.count("/api/tags") == 0 {
		t.Errorf("OLLAMA_HOST fake was never probed (auto-setup probed another host?)")
	}
	if n := e.Ollama.count("/api/pull"); n != 0 {
		t.Errorf("serve pulled models in the background: %d /api/pull requests", n)
	}
	mustContain(t, "serve output", r.Out, "Nothing was downloaded", "ollama pull bge-m3")
	if got := d2ReadConfig(t, e)["ollama_host"]; got != e.Ollama.URL {
		t.Errorf("auto-setup saved ollama_host=%v, want %s", got, e.Ollama.URL)
	}
	if strings.Contains(r.Out+r.Err, "11434") {
		t.Errorf("output mentions the default port:\n%s%s", r.Out, r.Err)
	}
}
