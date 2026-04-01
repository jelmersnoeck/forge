// Package retry wraps LLM provider calls with exponential backoff.
//
// Classifies errors by HTTP status (or error string) and retries only
// transient failures. Fatal errors (auth, bad request) bail immediately.
package retry

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
)

// Policy configures retry behavior.
type Policy struct {
	MaxRetries int           // max attempts (0 = no retries)
	BaseDelay  time.Duration // initial backoff
	MaxDelay   time.Duration // backoff cap
}

// DefaultPolicy returns a policy tuned for Anthropic rate limits.
func DefaultPolicy() Policy {
	return Policy{
		MaxRetries: 5,
		BaseDelay:  1 * time.Second,
		MaxDelay:   60 * time.Second,
	}
}

// Classification of an error for retry decisions.
type Classification int

const (
	Fatal     Classification = iota // don't retry (400, 401, 403, 404)
	Retryable                       // retry with backoff (429, 529, 5xx, connection)
)

// Classify inspects an error and decides whether to retry.
//
// We can't type-assert Anthropic SDK errors directly (they're internal),
// so we parse the error string for HTTP status codes. Ugly but effective.
func Classify(err error) Classification {
	if err == nil {
		return Fatal // shouldn't be called with nil, but don't retry "nothing"
	}

	msg := err.Error()

	// Context cancellation — user interrupted, don't retry.
	if strings.Contains(msg, "context canceled") || strings.Contains(msg, "context deadline exceeded") {
		return Fatal
	}

	// Rate limit (429) or overloaded (529) — always retry.
	switch {
	case strings.Contains(msg, "429") && strings.Contains(msg, "rate"):
		return Retryable
	case strings.Contains(msg, "529"):
		return Retryable
	case strings.Contains(msg, "overloaded"):
		return Retryable
	}

	// Server errors (5xx) — retry.
	for _, code := range []string{"500", "502", "503", "504"} {
		if strings.Contains(msg, code) {
			return Retryable
		}
	}

	// Connection errors — retry.
	switch {
	case strings.Contains(msg, "connection refused"):
		return Retryable
	case strings.Contains(msg, "connection reset"):
		return Retryable
	case strings.Contains(msg, "EOF"):
		return Retryable
	case strings.Contains(msg, "timeout"):
		return Retryable
	case strings.Contains(msg, "TLS handshake"):
		return Retryable
	}

	// Client errors (400, 401, 403) — fatal.
	return Fatal
}

// Attempt holds info about a single retry attempt, emitted to callers.
type Attempt struct {
	Number   int           // 1-based attempt number
	MaxRetry int           // max retries configured
	Delay    time.Duration // how long we'll wait before next attempt
	Err      error         // the error that triggered the retry
}

// Do executes fn with retries according to policy. Calls onRetry before each
// backoff sleep so the caller can emit events ("retrying in 4s...").
func Do[T any](ctx context.Context, policy Policy, onRetry func(Attempt), fn func() (T, error)) (T, error) {
	var zero T

	for attempt := 0; ; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		if Classify(err) == Fatal || attempt >= policy.MaxRetries {
			return zero, err
		}

		delay := Backoff(attempt, policy.BaseDelay, policy.MaxDelay)

		if onRetry != nil {
			onRetry(Attempt{
				Number:   attempt + 1,
				MaxRetry: policy.MaxRetries,
				Delay:    delay,
				Err:      err,
			})
		}

		select {
		case <-ctx.Done():
			return zero, fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
}

// Backoff calculates delay with exponential growth + jitter.
//
//	delay = min(base * 2^attempt + jitter, max)
func Backoff(attempt int, base, max time.Duration) time.Duration {
	exp := math.Pow(2, float64(attempt))
	delay := time.Duration(float64(base) * exp)

	// Add jitter: ±25% of delay.
	jitter := time.Duration(rand.Float64()*float64(delay)/2 - float64(delay)/4)
	delay += jitter

	if delay > max {
		delay = max
	}
	if delay < 0 {
		delay = base
	}
	return delay
}
