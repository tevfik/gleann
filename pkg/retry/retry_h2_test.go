package retry_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/retry"
)

func fastConfig() retry.ResilienceConfig {
	return retry.ResilienceConfig{
		MaxRetries:      2,
		RetryDelay:      time.Millisecond,
		RetryMaxDelay:   2 * time.Millisecond,
		CBFailThreshold: 5,
		CBSuccessThresh: 1,
		CBDelay:         time.Minute,
		CallTimeout:     5 * time.Second,
	}
}

func TestNewExecutor_RetriesTransientError(t *testing.T) {
	exec := retry.NewExecutor[string](fastConfig())
	calls := 0
	got, err := exec.Get(func() (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("503 service unavailable")
		}
		return "ok", nil
	})
	if err != nil || got != "ok" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (one retry)", calls)
	}
}

func TestNewExecutor_DoesNotRetryPermanentError(t *testing.T) {
	exec := retry.NewExecutor[string](fastConfig())
	calls := 0
	_, err := exec.Get(func() (string, error) {
		calls++
		return "", errors.New("404 not found")
	})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d, want an error after a single call", err, calls)
	}
}

func TestNewExecutor_CircuitOpens(t *testing.T) {
	cfg := fastConfig()
	cfg.CBFailThreshold = 1
	exec := retry.NewExecutor[string](cfg)
	calls := 0
	_, err := exec.Get(func() (string, error) {
		calls++
		return "", errors.New("502 bad gateway")
	})
	if err == nil {
		t.Fatal("expected an error with the circuit open")
	}
	// The first failure opens the circuit, so the retries are rejected
	// without calling fn again.
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDo_ContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := retry.Do(ctx, retry.Policy{MaxAttempts: 3}, func() error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("fn must not run with a cancelled context")
	}
}

func TestIsRetryable_URLErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"url_reset", &url.Error{Op: "Post", URL: "http://x", Err: errors.New("read: connection reset by peer")}, true},
		{"url_eof", &url.Error{Op: "Post", URL: "http://x", Err: errors.New("unexpected EOF")}, true},
		{"url_other", &url.Error{Op: "Post", URL: "http://x", Err: errors.New("unsupported protocol scheme")}, false},
		{"unknown", errors.New("disk full"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := retry.IsRetryable(tc.err); got != tc.want {
				t.Fatalf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
