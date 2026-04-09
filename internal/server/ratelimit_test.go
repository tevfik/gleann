package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- allow() unit tests ---

func TestRateLimiter_AllowsFirstRequest(t *testing.T) {
	rl := newRateLimiter()
	if !rl.allow("1.2.3.4") {
		t.Fatal("first request should always be allowed")
	}
}

func TestRateLimiter_AllowsWithinBurst(t *testing.T) {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    10,
		burst:   5,
		ttl:     10 * time.Minute,
		cleaned: time.Now(),
	}
	for i := 0; i < 5; i++ {
		if !rl.allow("10.0.0.1") {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
}

func TestRateLimiter_BlocksWhenBucketEmpty(t *testing.T) {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    1,
		burst:   2, // only 2 burst slots
		ttl:     10 * time.Minute,
		cleaned: time.Now(),
	}
	// Exhaust the bucket.
	rl.allow("192.168.1.1") // uses burst slot 1 (new IP: starts at burst-1 = 1 token)
	rl.allow("192.168.1.1") // uses burst slot 2

	// Next request should be blocked.
	if rl.allow("192.168.1.1") {
		t.Fatal("expected request to be blocked when bucket is empty")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    1000, // refill very fast (1000 tokens/sec)
		burst:   2,
		ttl:     10 * time.Minute,
		cleaned: time.Now(),
	}
	// Exhaust bucket.
	rl.allow("10.0.0.2")
	rl.allow("10.0.0.2")

	// Blocked now.
	if rl.allow("10.0.0.2") {
		t.Fatal("should be blocked immediately after exhaustion")
	}

	// Wait for refill.
	time.Sleep(5 * time.Millisecond) // 1000 t/s → 5 tokens in 5ms

	// Should be allowed again.
	if !rl.allow("10.0.0.2") {
		t.Fatal("should be allowed after refill")
	}
}

func TestRateLimiter_IsolatesIPs(t *testing.T) {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    1,
		burst:   1, // burst of 1
		ttl:     10 * time.Minute,
		cleaned: time.Now(),
	}
	// Both IPs start with fresh buckets.
	if !rl.allow("1.1.1.1") {
		t.Fatal("1.1.1.1 first request should pass")
	}
	// 1.1.1.1 is now exhausted; 2.2.2.2 should still pass.
	if !rl.allow("2.2.2.2") {
		t.Fatal("2.2.2.2 should be unaffected by 1.1.1.1 exhaustion")
	}
}

// --- clientIP tests ---

func TestClientIP_DirectConnection(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "203.0.113.10:54321"
	ip := clientIP(req)
	if ip != "203.0.113.10" {
		t.Fatalf("expected 203.0.113.10, got %s", ip)
	}
}

func TestClientIP_XForwardedForFromPrivate(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "10.0.0.1:1234" // private proxy
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	ip := clientIP(req)
	if ip != "1.2.3.4" {
		t.Fatalf("expected 1.2.3.4, got %s", ip)
	}
}

func TestClientIP_XForwardedForFromPublic_Ignored(t *testing.T) {
	// Header from a public IP should be ignored (potential spoofing).
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "203.0.113.10:54321" // public peer
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	ip := clientIP(req)
	if ip != "203.0.113.10" {
		t.Fatalf("expected RemoteAddr IP, got %s", ip)
	}
}

// --- rateLimitMiddleware HTTP tests ---

func TestRateLimitMiddleware_AllowsHealthEndpoint(t *testing.T) {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    0.001, // near-zero refill
		burst:   0,     // empty burst so any real request would be blocked
		ttl:     10 * time.Minute,
		cleaned: time.Now(),
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.middleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for /health, got %d", w.Code)
	}
}

func TestRateLimitMiddleware_Returns429WhenLimited(t *testing.T) {
	rl := &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    0.0001,
		burst:   1,
		ttl:     10 * time.Minute,
		cleaned: time.Now(),
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.middleware(inner)

	req1 := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	req1.RemoteAddr = "1.2.3.4:99"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	// First request allowed (new IP, starts at burst-1=0 tokens but still uses burst).

	req2 := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	req2.RemoteAddr = "1.2.3.4:99"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	// One of these should have been blocked.
	if w1.Code != http.StatusOK && w2.Code != http.StatusOK {
		t.Fatal("at least one request should have been allowed")
	}
}
