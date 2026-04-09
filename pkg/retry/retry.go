// Package retry provides exponential-backoff retry for transient errors.
//
//	err := retry.Do(ctx, retry.DefaultPolicy(), func() error {
//	    return callFlakeyService()
//	})
package retry

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"time"
)

// Policy defines retry behaviour.
type Policy struct {
	// MaxAttempts is the total number of attempts (1 = no retry).
	MaxAttempts int
	// Base is the initial delay before the second attempt.
	Base time.Duration
	// MaxDelay caps each individual sleep.
	MaxDelay time.Duration
	// JitterFrac adds random jitter ∈ [0, JitterFrac*delay].
	JitterFrac float64
}

// DefaultPolicy returns a sensible policy for LLM / embedding API calls:
// 3 attempts with exponential back-off starting at 1 s, capping at 30 s.
func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts: 3,
		Base:        1 * time.Second,
		MaxDelay:    30 * time.Second,
		JitterFrac:  0.25,
	}
}

// AggressivePolicy returns a policy for batch embedding where failures are
// more likely due to GPU memory pressure (5 attempts, longer back-off).
func AggressivePolicy() Policy {
	return Policy{
		MaxAttempts: 5,
		Base:        2 * time.Second,
		MaxDelay:    60 * time.Second,
		JitterFrac:  0.3,
	}
}

// Do calls fn up to p.MaxAttempts times, sleeping between attempts.
// It stops immediately if:
//   - fn returns nil (success)
//   - fn returns a non-retryable error (see IsRetryable)
//   - ctx is cancelled
func Do(ctx context.Context, p Policy, fn func() error) error {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < p.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsRetryable(lastErr) {
			return lastErr
		}
		if attempt == p.MaxAttempts-1 {
			break // don't sleep after final attempt
		}

		delay := p.Base * (1 << uint(attempt)) // 1s, 2s, 4s, …
		if delay > p.MaxDelay {
			delay = p.MaxDelay
		}
		if p.JitterFrac > 0 {
			jitter := time.Duration(float64(delay) * p.JitterFrac * rand.Float64())
			delay += jitter
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

// IsRetryable reports whether err is worth retrying.
// Returns false for context cancellation, bad-request style errors, etc.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Context cancellation/deadline is not retryable — the caller gave up.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true // allow retry if deadline is from the server, not from the request
	}

	msg := strings.ToLower(err.Error())

	// Network-level transient errors.
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return true
		}
		// connection refused / reset — provider restarting.
		inner := strings.ToLower(urlErr.Error())
		if strings.Contains(inner, "connection refused") ||
			strings.Contains(inner, "connection reset") ||
			strings.Contains(inner, "eof") ||
			strings.Contains(inner, "broken pipe") {
			return true
		}
	}

	// HTTP status codes embedded in error messages by our providers.
	for _, token := range []string{
		"503", "service unavailable",
		"502", "bad gateway",
		"429", "too many requests", "rate limit",
		"500", "internal server error",
		"connection refused", "connection reset", "eof",
		"broken pipe", "timeout", "deadline exceeded",
	} {
		if strings.Contains(msg, token) {
			return true
		}
	}

	// 400 Bad Request, 401 Unauthorized, 404 Not Found → not retryable.
	for _, token := range []string{"400", "401", "403", "404", "bad request", "not found", "unauthorized"} {
		if strings.Contains(msg, token) {
			return false
		}
	}

	return false
}
