package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- pickTimeout unit tests ---

func TestPickTimeout_AskEndpoint(t *testing.T) {
	d := pickTimeout("/index1/ask")
	if d != globalTimeouts.ask {
		t.Fatalf("expected ask timeout %v, got %v", globalTimeouts.ask, d)
	}
}

func TestPickTimeout_SearchEndpoint(t *testing.T) {
	d := pickTimeout("/index1/search")
	if d != globalTimeouts.search {
		t.Fatalf("expected search timeout %v, got %v", globalTimeouts.search, d)
	}
}

func TestPickTimeout_BuildEndpoint(t *testing.T) {
	d := pickTimeout("/index1/build")
	if d != globalTimeouts.build {
		t.Fatalf("expected build timeout %v, got %v", globalTimeouts.build, d)
	}
}

func TestPickTimeout_DefaultEndpoint(t *testing.T) {
	d := pickTimeout("/api/unknown")
	if d != globalTimeouts.dflt {
		t.Fatalf("expected default timeout %v, got %v", globalTimeouts.dflt, d)
	}
}

func TestPickTimeout_OpenAPICompletions(t *testing.T) {
	d := pickTimeout("/v1/chat/completions")
	if d != globalTimeouts.ask {
		t.Fatalf("expected ask timeout for /v1/chat/completions, got %v", d)
	}
}

// --- timeoutMiddleware HTTP tests ---

func TestTimeoutMiddleware_NormalCompletion(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := timeoutMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestTimeoutMiddleware_Returns504OnTimeout(t *testing.T) {
	// Handler that sleeps longer than the override timeout below.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	})

	// Temporarily swap out the global timeout for this path to be very short.
	origDflt := globalTimeouts.dflt
	globalTimeouts.dflt = 5 * time.Millisecond
	defer func() { globalTimeouts.dflt = origDflt }()

	handler := timeoutMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d", w.Code)
	}
}

func TestTimeoutMiddleware_SkipsSSEStream(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no deadline has been applied via context.
		_, hasDeadline := r.Context().Deadline()
		if hasDeadline {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := timeoutMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/index1/ask?stream=true", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for SSE stream (deadline should be skipped), got %d", w.Code)
	}
}

func TestTimeoutMiddleware_SkipsEventStreamAccept(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := timeoutMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/index1/ask", nil)
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for text/event-stream, got %d", w.Code)
	}
}

// --- envSeconds tests ---

func TestEnvSeconds_Empty(t *testing.T) {
	t.Setenv("TEST_TIMEOUT", "")
	// Test indirectly via newTimeoutConfig with overrides.
	v := envSeconds("TEST_TIMEOUT")
	if v != 0 {
		t.Fatalf("expected 0 for empty env, got %d", v)
	}
}

func TestEnvSeconds_ValidNumber(t *testing.T) {
	t.Setenv("TEST_TIMEOUT2", "42")
	v := envSeconds("TEST_TIMEOUT2")
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
}

func TestEnvSeconds_InvalidNumber(t *testing.T) {
	t.Setenv("TEST_TIMEOUT3", "abc")
	v := envSeconds("TEST_TIMEOUT3")
	if v != 0 {
		t.Fatalf("expected 0 for invalid, got %d", v)
	}
}

func TestEnvSeconds_NegativeNumber(t *testing.T) {
	t.Setenv("TEST_TIMEOUT4", "-5")
	v := envSeconds("TEST_TIMEOUT4")
	if v != 0 {
		t.Fatalf("expected 0 for negative, got %d", v)
	}
}

// --- newTimeoutConfig env var overrides ---

func TestNewTimeoutConfig_EnvOverrides(t *testing.T) {
	t.Setenv("GLEANN_TIMEOUT_ASK_S", "120")
	t.Setenv("GLEANN_TIMEOUT_SEARCH_S", "15")
	t.Setenv("GLEANN_TIMEOUT_BUILD_S", "300")
	t.Setenv("GLEANN_TIMEOUT_DEFAULT_S", "45")
	cfg := newTimeoutConfig()
	if cfg.ask != 120*time.Second {
		t.Fatalf("expected ask=120s, got %v", cfg.ask)
	}
	if cfg.search != 15*time.Second {
		t.Fatalf("expected search=15s, got %v", cfg.search)
	}
	if cfg.build != 300*time.Second {
		t.Fatalf("expected build=300s, got %v", cfg.build)
	}
	if cfg.dflt != 45*time.Second {
		t.Fatalf("expected default=45s, got %v", cfg.dflt)
	}
}

// --- timeoutWriter Write/Flush tests ---

func TestTimeoutWriter_Write(t *testing.T) {
	w := httptest.NewRecorder()
	tw := &timeoutWriter{ResponseWriter: w}
	n, err := tw.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("expected 5 bytes, got %d, err=%v", n, err)
	}
	if !tw.wroteHeader {
		t.Fatal("Write should set wroteHeader=true")
	}
}

func TestTimeoutWriter_Flush(t *testing.T) {
	w := httptest.NewRecorder()
	tw := &timeoutWriter{ResponseWriter: w}
	// Flush should not panic even without prior write.
	tw.Flush()
}
