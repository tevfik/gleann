package retry_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/retry"
)

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), retry.DefaultPolicy(), func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestDo_RetriesOnRetryableError(t *testing.T) {
	// Use a fast policy to avoid slow tests.
	p := retry.Policy{MaxAttempts: 3, Base: time.Millisecond, MaxDelay: time.Millisecond}

	calls := 0
	netErr := &net.OpError{Op: "connect", Err: errors.New("connection refused")}
	err := retry.Do(context.Background(), p, func() error {
		calls++
		if calls < 3 {
			return netErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil on final success, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestDo_StopsOnNonRetryableError(t *testing.T) {
	// A plain "bad request" error should not be retried.
	p := retry.Policy{MaxAttempts: 5, Base: time.Millisecond, MaxDelay: time.Millisecond}
	calls := 0
	fixed := errors.New("400 bad request")
	err := retry.Do(context.Background(), p, func() error {
		calls++
		return fixed
	})
	if !errors.Is(err, fixed) {
		t.Fatalf("expected fixed error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry), got %d", calls)
	}
}

func TestDo_HonoursMaxAttempts(t *testing.T) {
	p := retry.Policy{MaxAttempts: 3, Base: time.Millisecond, MaxDelay: time.Millisecond}
	calls := 0
	netErr := &net.OpError{Op: "connect", Err: errors.New("connection refused")}
	err := retry.Do(context.Background(), p, func() error {
		calls++
		return netErr
	})
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestDo_RespectsContextCancellation(t *testing.T) {
	p := retry.Policy{MaxAttempts: 10, Base: 50 * time.Millisecond, MaxDelay: 50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	netErr := &net.OpError{Op: "connect", Err: errors.New("connection refused")}
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	err := retry.Do(ctx, p, func() error {
		calls++
		return netErr
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// Should not have tried 10 times.
	if calls >= 10 {
		t.Fatalf("too many calls (%d) before ctx cancel", calls)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"connection_refused", &net.OpError{Op: "connect", Err: errors.New("connection refused")}, true},
		{"net_timeout", &net.OpError{Op: "read", Net: "tcp", Err: &timeoutErr{}}, true},
		{"503", errors.New("server returned 503 Service Unavailable"), true},
		{"502", errors.New("upstream 502 Bad Gateway"), true},
		{"429", errors.New("rate limited: 429 Too Many Requests"), true},
		{"500", errors.New("internal server error: 500"), true},
		{"400", errors.New("400 bad request"), false},
		{"401", errors.New("401 Unauthorized"), false},
		{"403", errors.New("403 Forbidden"), false},
		{"404", errors.New("404 Not Found"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := retry.IsRetryable(tc.err); got != tc.want {
				t.Fatalf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// timeoutErr is a net.Error that reports Timeout() == true.
type timeoutErr struct{}

func (e *timeoutErr) Error() string   { return "i/o timeout" }
func (e *timeoutErr) Timeout() bool   { return true }
func (e *timeoutErr) Temporary() bool { return true }
