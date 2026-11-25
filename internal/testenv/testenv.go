// Package testenv holds the process-wide environment pins that every test
// binary applies from its TestMain, so no test can reach a real service by
// accident.
package testenv

import (
	"os"
	"strings"
)

// DeadOllamaHost is an address nothing listens on (port 1). Tests that need an
// Ollama use an httptest fake and set OLLAMA_HOST themselves.
const DeadOllamaHost = "http://127.0.0.1:1"

// PinOllamaHost sets OLLAMA_HOST to DeadOllamaHost unless it is already set to
// something other than the default Ollama port. Without it a test that
// resolves the Ollama host falls through to localhost:11434, which on a
// developer machine is a real Ollama that may start downloading models.
func PinOllamaHost() {
	if h := os.Getenv("OLLAMA_HOST"); h != "" && !UsesDefaultOllamaPort(h) {
		return
	}
	_ = os.Setenv("OLLAMA_HOST", DeadOllamaHost)
}

// UsesDefaultOllamaPort reports whether host names Ollama's default port 11434
// or no port at all (which Ollama's client resolves to 11434).
func UsesDefaultOllamaPort(host string) bool {
	h := strings.TrimRight(strings.TrimSpace(host), "/")
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if strings.HasSuffix(h, ":11434") {
		return true
	}
	// No port: a bare name, or a bracketed or bare IPv6 address.
	if strings.HasPrefix(h, "[") {
		return strings.HasSuffix(h, "]")
	}
	return !strings.Contains(h, ":") || strings.Count(h, ":") > 1
}

// TB is the part of testing.TB the guard needs.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
}

// AssertPinned fails when a test starts with OLLAMA_HOST unset or pointing at
// the default Ollama port: such a test would reach a real Ollama on a
// developer machine. Each package calls it from one guard test.
func AssertPinned(t TB) {
	t.Helper()
	if h := os.Getenv("OLLAMA_HOST"); h == "" || UsesDefaultOllamaPort(h) {
		t.Errorf("OLLAMA_HOST is %q: TestMain must pin it with testenv.PinOllamaHost so tests never default to localhost:11434", h)
	}
}
