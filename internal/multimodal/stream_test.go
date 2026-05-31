// Package multimodal — streaming tests (Bug #8).
package multimodal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// stubStreamingOllama returns an Ollama stub that emits a deterministic
// sequence of NDJSON chunks so we can verify per-token delivery.
func stubStreamingOllama(t *testing.T, tokens []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		for i, tok := range tokens {
			done := "false"
			if i == len(tokens)-1 {
				done = "true"
			}
			_, _ = w.Write([]byte(`{"message":{"content":"` + tok + `"},"done":` + done + `}` + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

// TestProcessFileStream_DeliversTokens verifies all stream chunks reach
// the callback and the concatenated description matches.
func TestProcessFileStream_DeliversTokens(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	tokens := []string{"Hello ", "from ", "stream"}
	srv := stubStreamingOllama(t, tokens)
	defer srv.Close()

	dir := t.TempDir()
	p := filepath.Join(dir, "x.png")
	_ = os.WriteFile(p, []byte("png-bytes"), 0o644)

	proc := NewProcessor(srv.URL, "stub-model")
	var seen []string
	var calls atomic.Int32
	result := proc.ProcessFileStream(p, func(tok string) error {
		seen = append(seen, tok)
		calls.Add(1)
		return nil
	})
	if result.Error != nil {
		t.Fatalf("err: %v", result.Error)
	}
	if calls.Load() != int32(len(tokens)) {
		t.Errorf("callback fired %d times, want %d", calls.Load(), len(tokens))
	}
	if got := strings.Join(seen, ""); got != "Hello from stream" {
		t.Errorf("description=%q, want %q", got, "Hello from stream")
	}
	if result.Description != "Hello from stream" {
		t.Errorf("result.Description=%q", result.Description)
	}
}

// TestProcessFileStream_CacheHit replays a previously cached description
// as a single token so callers can treat the path uniformly.
func TestProcessFileStream_CacheHit(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", cacheDir)
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "1")

	srv := stubStreamingOllama(t, []string{"X"})
	defer srv.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "img.png")
	_ = os.WriteFile(p, []byte("img-bytes"), 0o644)

	proc := NewProcessor(srv.URL, "stub-model")
	// First call — populates cache via streaming.
	_ = proc.ProcessFileStream(p, func(string) error { return nil })

	// Second call — must hit cache and not contact the server. We
	// detect that by closing the server before the second call.
	srv.Close()
	var hits int
	r := proc.ProcessFileStream(p, func(tok string) error { hits++; return nil })
	if r.Error != nil {
		t.Fatalf("second call err: %v", r.Error)
	}
	if hits != 1 {
		t.Errorf("cache hit should emit 1 token, got %d", hits)
	}
}
