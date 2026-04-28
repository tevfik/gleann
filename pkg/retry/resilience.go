package retry

import (
	"log/slog"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// ResilienceConfig holds tuning parameters for the failsafe-go executor.
type ResilienceConfig struct {
	// Retry
	MaxRetries    int
	RetryDelay    time.Duration
	RetryMaxDelay time.Duration
	// Circuit Breaker
	CBFailThreshold uint
	CBSuccessThresh uint
	CBDelay         time.Duration
	// Timeout
	CallTimeout time.Duration
}

// DefaultResilienceConfig returns sensible defaults for embedding/LLM calls.
func DefaultResilienceConfig() ResilienceConfig {
	return ResilienceConfig{
		MaxRetries:      3,
		RetryDelay:      1 * time.Second,
		RetryMaxDelay:   30 * time.Second,
		CBFailThreshold: 5,
		CBSuccessThresh: 2,
		CBDelay:         60 * time.Second,
		CallTimeout:     5 * time.Minute,
	}
}

// EmbeddingResilienceConfig returns config tuned for batch embedding
// (longer timeout, more retries for GPU memory pressure).
func EmbeddingResilienceConfig() ResilienceConfig {
	return ResilienceConfig{
		MaxRetries:      5,
		RetryDelay:      2 * time.Second,
		RetryMaxDelay:   60 * time.Second,
		CBFailThreshold: 10,
		CBSuccessThresh: 3,
		CBDelay:         90 * time.Second,
		CallTimeout:     10 * time.Minute,
	}
}

// NewExecutor creates a failsafe-go executor with retry + circuit breaker + timeout.
// The executor wraps functions that return (T, error).
func NewExecutor[T any](cfg ResilienceConfig) failsafe.Executor[T] {
	logger := slog.Default()

	rp := retrypolicy.NewBuilder[T]().
		WithBackoff(cfg.RetryDelay, cfg.RetryMaxDelay).
		WithMaxRetries(cfg.MaxRetries).
		HandleIf(func(_ T, err error) bool {
			return IsRetryable(err)
		}).
		OnRetry(func(e failsafe.ExecutionEvent[T]) {
			logger.Warn("gleann: retrying after transient error",
				"attempt", e.Attempts(),
				"error", e.LastError(),
			)
		}).
		Build()

	cb := circuitbreaker.NewBuilder[T]().
		WithFailureThreshold(cfg.CBFailThreshold).
		WithSuccessThreshold(cfg.CBSuccessThresh).
		WithDelay(cfg.CBDelay).
		OnStateChanged(func(e circuitbreaker.StateChangedEvent) {
			logger.Warn("gleann: circuit breaker state changed",
				"from", e.OldState,
				"to", e.NewState,
			)
		}).
		Build()

	tp := timeout.New[T](cfg.CallTimeout)

	return failsafe.With[T](rp, cb, tp)
}

// NewVoidExecutor creates a failsafe-go executor for functions that return only error.
func NewVoidExecutor(cfg ResilienceConfig) failsafe.Executor[any] {
	return NewExecutor[any](cfg)
}
