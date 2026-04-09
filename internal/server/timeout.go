// Package server — request-level timeout middleware.
//
// Injects a context deadline per request based on the path.
// This ensures the server stops work and returns 504 when a slow LLM
// or embedding provider hangs indefinitely.
//
// Defaults (overridable via env vars):
//
//	GLEANN_TIMEOUT_ASK_S    — seconds for /*/ask endpoints    (default 300 = 5 min)
//	GLEANN_TIMEOUT_SEARCH_S — seconds for /*/search endpoints (default 30)
//	GLEANN_TIMEOUT_BUILD_S  — seconds for /*/build endpoints  (default 600 = 10 min)
//	GLEANN_TIMEOUT_DEFAULT_S — seconds for all other paths    (default 60)
package server

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// timeoutConfig holds the resolved timeout durations.
type timeoutConfig struct {
	ask    time.Duration
	search time.Duration
	build  time.Duration
	dflt   time.Duration
}

// globalTimeouts is read once at startup.
var globalTimeouts = newTimeoutConfig()

func newTimeoutConfig() timeoutConfig {
	tc := timeoutConfig{
		ask:    5 * time.Minute,
		search: 30 * time.Second,
		build:  10 * time.Minute,
		dflt:   60 * time.Second,
	}
	if v := envSeconds("GLEANN_TIMEOUT_ASK_S"); v > 0 {
		tc.ask = time.Duration(v) * time.Second
	}
	if v := envSeconds("GLEANN_TIMEOUT_SEARCH_S"); v > 0 {
		tc.search = time.Duration(v) * time.Second
	}
	if v := envSeconds("GLEANN_TIMEOUT_BUILD_S"); v > 0 {
		tc.build = time.Duration(v) * time.Second
	}
	if v := envSeconds("GLEANN_TIMEOUT_DEFAULT_S"); v > 0 {
		tc.dflt = time.Duration(v) * time.Second
	}
	return tc
}

func envSeconds(key string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// pickTimeout returns the appropriate timeout for the request path.
func pickTimeout(path string) time.Duration {
	switch {
	case strings.HasSuffix(path, "/ask") || strings.Contains(path, "/chat/completions"):
		return globalTimeouts.ask
	case strings.HasSuffix(path, "/search") || path == "/api/search":
		return globalTimeouts.search
	case strings.HasSuffix(path, "/build"):
		return globalTimeouts.build
	default:
		return globalTimeouts.dflt
	}
}

// timeoutMiddleware wraps next with a per-request context deadline.
// SSE streaming endpoints (/ask?stream=true) are excluded — their read
// loop is controlled by the client disconnect signal instead.
func timeoutMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip timeout for SSE streams; the client disconnect context handles those.
		if r.URL.Query().Get("stream") == "true" ||
			r.Header.Get("Accept") == "text/event-stream" {
			next.ServeHTTP(w, r)
			return
		}

		d := pickTimeout(r.URL.Path)
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()

		// Use a custom ResponseWriter that detects whether the handler already
		// wrote a status.  If the context deadline fires after headers are sent
		// there is nothing useful we can do; if it fires before, we return 504.
		tw := &timeoutWriter{ResponseWriter: w}
		done := make(chan struct{})

		go func() {
			next.ServeHTTP(tw, r.WithContext(ctx))
			close(done)
		}()

		select {
		case <-done:
			// Normal completion; nothing to do.
		case <-ctx.Done():
			tw.mu.Lock()
			wrote := tw.wroteHeader
			tw.mu.Unlock()

			if !wrote {
				writeError(w, http.StatusGatewayTimeout,
					"request timed out — try a shorter query or increase GLEANN_TIMEOUT_*_S")
			}
			<-done // wait for handler goroutine to finish
		}
	})
}

// timeoutWriter tracks whether WriteHeader has been called so the outer
// goroutine knows whether it is safe to write a 504 response.
type timeoutWriter struct {
	http.ResponseWriter
	mu          sync.Mutex
	wroteHeader bool
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	tw.wroteHeader = true
	tw.mu.Unlock()
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	tw.wroteHeader = true
	tw.mu.Unlock()
	return tw.ResponseWriter.Write(b)
}

// Flush propagates the Flusher interface for SSE (though streaming skips the timeout).
func (tw *timeoutWriter) Flush() {
	if f, ok := tw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
