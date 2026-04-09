// Package server — per-IP token-bucket rate limiter middleware.
//
// Algorithm: token bucket.
//   - Each IP gets a bucket of capacity `burst` tokens.
//   - Tokens refill at `rate` tokens/second.
//   - A request consumes 1 token; if the bucket is empty → 429.
//
// Configuration (env vars, read once at startup):
//
//	GLEANN_RATE_LIMIT   — tokens/second sustained (default 60)
//	GLEANN_RATE_BURST   — burst capacity         (default 120)
//
// Skipped paths: /health, /metrics (monitoring always allowed).
package server

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// rateLimiter is a process-global per-IP rate limiter.
var globalRateLimiter = newRateLimiter()

// bucket is a single IP's token bucket state.
type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// rateLimiter holds per-IP buckets and global configuration.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // refill rate (tokens/second)
	burst   float64 // maximum bucket capacity

	// cleanup: drop buckets inactive for this long.
	ttl     time.Duration
	cleaned time.Time
}

func newRateLimiter() *rateLimiter {
	rate := 60.0
	burst := 120.0

	if v := os.Getenv("GLEANN_RATE_LIMIT"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			rate = n
		}
	}
	if v := os.Getenv("GLEANN_RATE_BURST"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			burst = n
		}
	}

	return &rateLimiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		burst:   burst,
		ttl:     10 * time.Minute,
		cleaned: time.Now(),
	}
}

// allow returns true if the request from ip is within the rate limit.
func (rl *rateLimiter) allow(ip string) bool {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Periodic GC: remove stale entries every 5 minutes.
	if now.Sub(rl.cleaned) > 5*time.Minute {
		for k, b := range rl.buckets {
			if now.Sub(b.lastSeen) > rl.ttl {
				delete(rl.buckets, k)
			}
		}
		rl.cleaned = now
	}

	b, ok := rl.buckets[ip]
	if !ok {
		// New IP — start with a full bucket.
		rl.buckets[ip] = &bucket{tokens: rl.burst - 1, lastSeen: now}
		return true
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false // bucket empty
	}
	b.tokens--
	return true
}

// rateLimitMiddleware wraps next with per-IP rate limiting.
// Monitoring paths (/health, /metrics) bypass the limiter.
func rateLimitMiddleware(next http.Handler) http.Handler {
	return globalRateLimiter.middleware(next)
}

// middleware returns an http.Handler that rate-limits requests using this limiter.
// Useful for testing with custom limiter instances.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for health/metrics endpoints.
		p := r.URL.Path
		if p == "/health" || p == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)
		if !rl.allow(ip) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded — slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the real client IP, respecting X-Forwarded-For from trusted
// proxies. We only trust the header if it is present; otherwise fall back to
// RemoteAddr.  We do NOT blindly trust all X-Forwarded-For values to avoid
// header spoofing attacks.
func clientIP(r *http.Request) string {
	// Only trust X-Real-IP / X-Forwarded-For if the immediate peer is a
	// loopback/private address (i.e. a local proxy/load-balancer).
	if isPrivate(r.RemoteAddr) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the leftmost (original client) IP.
			i := 0
			for i < len(xff) && xff[i] != ',' {
				i++
			}
			if ip := sanitizeIP(xff[:i]); ip != "" {
				return ip
			}
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			if ip := sanitizeIP(xri); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sanitizeIP parses and returns a trimmed IP string, or "" on failure.
func sanitizeIP(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			s = s[i+1:]
			i = -1
		}
	}
	if net.ParseIP(s) == nil {
		return ""
	}
	return s
}

// isPrivate returns true if addr (host:port or host) belongs to a
// loopback or RFC-1918/RFC-4193 private address range.
func isPrivate(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}
