package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"
)

// RetryConfig configures retry behavior for transient error recovery.
type RetryConfig struct {
	MaxAttempts  int           // Maximum number of attempts (default: 3).
	InitialDelay time.Duration // Initial delay between retries (default: 1s).
	MaxDelay     time.Duration // Maximum delay cap (default: 30s).
	Multiplier   float64       // Backoff multiplier (default: 2.0).
}

// DefaultRetryConfig returns sensible defaults for retry behavior.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
	}
}

// validate returns a copy of cfg with any zero-valued fields replaced by defaults.
func (cfg RetryConfig) validate() RetryConfig {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultRetryConfig().MaxAttempts
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = DefaultRetryConfig().InitialDelay
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = DefaultRetryConfig().MaxDelay
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = DefaultRetryConfig().Multiplier
	}
	return cfg
}

// Retry executes fn with exponential backoff and jitter.
// It retries on transient errors (identified by IsTransient).
// Permanent errors and non-transient errors are returned immediately.
// Context cancellation between retries is respected.
func Retry(ctx context.Context, cfg RetryConfig, fn func(ctx context.Context) error) error {
	cfg = cfg.validate()

	var lastErr error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}

		// Do not retry permanent or non-transient errors.
		if !IsTransient(lastErr) {
			slog.WarnContext(ctx, "non-transient error, not retrying",
				"attempt", attempt,
				"error", lastErr,
			)
			return lastErr
		}

		// Do not retry on the last attempt.
		if attempt == cfg.MaxAttempts {
			break
		}

		delay := backoffDelay(cfg, attempt)

		slog.WarnContext(ctx, "transient error, retrying",
			"attempt", attempt,
			"max_attempts", cfg.MaxAttempts,
			"next_delay", delay,
			"error", lastErr,
		)

		// Wait for the backoff delay, respecting context cancellation.
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry aborted: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	return fmt.Errorf("max retries (%d) exhausted: %w", cfg.MaxAttempts, lastErr)
}

// RetryWithResult executes fn and returns the result on success.
// It follows the same retry semantics as Retry.
func RetryWithResult[T any](ctx context.Context, cfg RetryConfig, fn func(ctx context.Context) (T, error)) (T, error) {
	cfg = cfg.validate()

	var zero T
	var lastErr error
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// Do not retry permanent or non-transient errors.
		if !IsTransient(lastErr) {
			slog.WarnContext(ctx, "non-transient error, not retrying",
				"attempt", attempt,
				"error", lastErr,
			)
			return zero, lastErr
		}

		// Do not retry on the last attempt.
		if attempt == cfg.MaxAttempts {
			break
		}

		delay := backoffDelay(cfg, attempt)

		slog.WarnContext(ctx, "transient error, retrying",
			"attempt", attempt,
			"max_attempts", cfg.MaxAttempts,
			"next_delay", delay,
			"error", lastErr,
		)

		// Wait for the backoff delay, respecting context cancellation.
		select {
		case <-ctx.Done():
			return zero, fmt.Errorf("retry aborted: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	return zero, fmt.Errorf("max retries (%d) exhausted: %w", cfg.MaxAttempts, lastErr)
}

// backoffDelay calculates the delay for a given attempt using exponential
// backoff with jitter. The delay is: min(maxDelay, initialDelay * multiplier^(attempt-1))
// with a random jitter of 0-25% added to avoid thundering herd.
func backoffDelay(cfg RetryConfig, attempt int) time.Duration {
	delay := float64(cfg.InitialDelay) * math.Pow(cfg.Multiplier, float64(attempt-1))
	if delay > float64(cfg.MaxDelay) {
		delay = float64(cfg.MaxDelay)
	}

	// Add random jitter: 0-25% of the computed delay.
	jitter := delay * 0.25 * rand.Float64()
	delay += jitter

	return time.Duration(delay)
}
