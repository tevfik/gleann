package testenv

import "testing"

func TestUsesDefaultOllamaPort(t *testing.T) {
	for h, want := range map[string]bool{
		"http://localhost:11434": true, "localhost:11434": true, "localhost": true,
		"http://127.0.0.1:1": false, "http://127.0.0.1:33761": false, "[::1]": true, "[::1]:5": false,
	} {
		if got := UsesDefaultOllamaPort(h); got != want {
			t.Errorf("UsesDefaultOllamaPort(%q) = %v, want %v", h, got, want)
		}
	}
}

type recorder struct{ failed bool }

func (r *recorder) Helper()               {}
func (r *recorder) Errorf(string, ...any) { r.failed = true }

func TestAssertPinnedDetectsDefaults(t *testing.T) {
	for host, wantFail := range map[string]bool{"": true, "http://localhost:11434": true, "localhost": true, DeadOllamaHost: false} {
		t.Setenv("OLLAMA_HOST", host)
		r := &recorder{}
		AssertPinned(r)
		if r.failed != wantFail {
			t.Errorf("AssertPinned with OLLAMA_HOST=%q failed=%v, want %v", host, r.failed, wantFail)
		}
	}
	t.Setenv("OLLAMA_HOST", "")
	PinOllamaHost()
	AssertPinned(t)
}
