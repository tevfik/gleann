package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSanitizeIP(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"valid ipv4", "192.168.1.1", "192.168.1.1"},
		{"valid ipv6", "::1", "::1"},
		{"valid ipv6 full", "2001:db8::1", "2001:db8::1"},
		{"with leading spaces", "  10.0.0.1", "10.0.0.1"},
		{"with tabs", "\t10.0.0.1", "10.0.0.1"},
		{"invalid", "not-an-ip", ""},
		{"empty", "", ""},
		{"with port (invalid for ParseIP)", "192.168.1.1:8080", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeIP(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeIP(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsPrivate(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"loopback", "127.0.0.1", true},
		{"loopback with port", "127.0.0.1:8080", true},
		{"private 10.x", "10.0.0.1", true},
		{"private 192.168", "192.168.1.1", true},
		{"private 172.16", "172.16.0.1", true},
		{"public", "8.8.8.8", false},
		{"public with port", "8.8.8.8:443", false},
		{"ipv6 loopback", "::1", true},
		{"invalid", "not-an-ip", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPrivate(tt.addr)
			if got != tt.want {
				t.Errorf("isPrivate(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		want       string
	}{
		{"direct connection", "8.8.8.8:1234", "", "", "8.8.8.8"},
		{"loopback with XFF", "127.0.0.1:1234", "203.0.113.50", "", "203.0.113.50"},
		{"private with X-Real-IP", "10.0.0.1:1234", "", "203.0.113.50", "203.0.113.50"},
		{"public ignores XFF", "8.8.8.8:1234", "203.0.113.50", "", "8.8.8.8"},
		{"XFF with multiple IPs", "127.0.0.1:1234", "203.0.113.50, 70.41.3.18", "", "203.0.113.50"},
		{"no port", "8.8.8.8", "", "", "8.8.8.8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/test", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				r.Header.Set("X-Real-IP", tt.xri)
			}

			got := clientIP(r)
			if got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPickTimeout(t *testing.T) {
	tests := []struct {
		name string
		path string
		want time.Duration
	}{
		{"ask endpoint", "/v1/ask", globalTimeouts.ask},
		{"chat completions", "/v1/chat/completions", globalTimeouts.ask},
		{"search endpoint", "/v1/search", globalTimeouts.search},
		{"api search", "/api/search", globalTimeouts.search},
		{"build endpoint", "/v1/build", globalTimeouts.build},
		{"default", "/health", globalTimeouts.dflt},
		{"root", "/", globalTimeouts.dflt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickTimeout(tt.path)
			if got != tt.want {
				t.Errorf("pickTimeout(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestNewTimeoutConfig(t *testing.T) {
	tc := newTimeoutConfig()
	if tc.ask != 5*time.Minute {
		t.Errorf("ask = %v, want 5m", tc.ask)
	}
	if tc.search != 30*time.Second {
		t.Errorf("search = %v, want 30s", tc.search)
	}
	if tc.build != 10*time.Minute {
		t.Errorf("build = %v, want 10m", tc.build)
	}
	if tc.dflt != 60*time.Second {
		t.Errorf("default = %v, want 60s", tc.dflt)
	}
}

func TestRateLimiterAllow(t *testing.T) {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    10,
		burst:   5,
		ttl:     time.Minute,
		cleaned: time.Now(),
	}

	// First 5 requests should be allowed (burst)
	for i := 0; i < 5; i++ {
		if !rl.allow("192.168.1.1") {
			t.Errorf("request %d should be allowed within burst", i+1)
		}
	}

	// 6th request should be denied (burst exhausted)
	if rl.allow("192.168.1.1") {
		t.Error("request 6 should be denied (burst exhausted)")
	}

	// Different IP should be allowed
	if !rl.allow("192.168.1.2") {
		t.Error("different IP should have its own bucket")
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    1,
		burst:   2,
		ttl:     time.Minute,
		cleaned: time.Now(),
	}

	mw := rl.middleware(handler)

	// Health endpoint bypasses rate limit
	req := httptest.NewRequest("GET", "/health", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/health should always be 200, got %d", rec.Code)
	}

	// Metrics endpoint bypasses rate limit
	req = httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics should always be 200, got %d", rec.Code)
	}

	// Regular requests are rate limited
	for i := 0; i < 2; i++ {
		req = httptest.NewRequest("GET", "/api/search", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec = httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
	}

	// Third should be 429
	req = httptest.NewRequest("GET", "/api/search", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

func TestTimeoutMiddlewareSkipsSSE(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify context has no deadline for SSE
		_, ok := r.Context().Deadline()
		if ok {
			t.Error("SSE request should not have deadline")
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := timeoutMiddleware(handler)

	// stream=true query parameter
	req := httptest.NewRequest("GET", "/v1/ask?stream=true", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	// Accept: text/event-stream header
	req = httptest.NewRequest("GET", "/v1/ask", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
}

func TestParamName(t *testing.T) {
	result := paramName()
	if result == nil {
		t.Fatal("paramName() returned nil")
	}
	if _, ok := result["name"]; !ok {
		t.Error("expected 'name' key in paramName result")
	}
}

func TestRefSchema(t *testing.T) {
	result := refSchema("SearchResult")
	if result == nil {
		t.Fatal("refSchema() returned nil")
	}
	ref, ok := result["$ref"]
	if !ok {
		t.Error("expected '$ref' key in refSchema result")
	}
	refStr, ok := ref.(string)
	if !ok || refStr != "#/components/schemas/SearchResult" {
		t.Errorf("$ref = %q, want '#/components/schemas/SearchResult'", refStr)
	}
}

func TestWriteJSONExtended(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]string{"status": "ok"})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestWriteErrorExtended(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "bad request")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestNewRateLimiter(t *testing.T) {
	rl := newRateLimiter()
	if rl == nil {
		t.Fatal("newRateLimiter returned nil")
	}
	if rl.rate <= 0 {
		t.Error("rate should be positive")
	}
	if rl.burst <= 0 {
		t.Error("burst should be positive")
	}
}
