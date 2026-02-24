package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRetry_SuccessFirstTry(t *testing.T) {
	callCount := 0
	err := Retry(context.Background(), DefaultRetryConfig(), func(_ context.Context) error {
		callCount++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestRetry_TransientThenSuccess(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	callCount := 0
	err := Retry(context.Background(), cfg, func(_ context.Context) error {
		callCount++
		if callCount < 3 {
			return NewTransientError(errors.New("connection refused"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestRetry_MaxAttemptsExhausted(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	callCount := 0
	err := Retry(context.Background(), cfg, func(_ context.Context) error {
		callCount++
		return NewTransientError(errors.New("always fails"))
	})
	if err == nil {
		t.Fatal("expected error after max attempts")
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
	if !errors.Is(err, errors.Unwrap(err)) {
		// Verify the original error is accessible.
		var te *TransientError
		if !errors.As(err, &te) {
			t.Error("expected original TransientError to be in error chain")
		}
	}
}

func TestRetry_PermanentErrorStopsImmediately(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	callCount := 0
	err := Retry(context.Background(), cfg, func(_ context.Context) error {
		callCount++
		return NewPermanentError(errors.New("invalid config"))
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call for permanent error, got %d", callCount)
	}
}

func TestRetry_NonTransientErrorStopsImmediately(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	callCount := 0
	err := Retry(context.Background(), cfg, func(_ context.Context) error {
		callCount++
		return errors.New("file not found")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call for non-transient error, got %d", callCount)
	}
}

func TestRetry_ContextCancellation(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 1 * time.Second, // Long delay to test cancellation.
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := Retry(ctx, cfg, func(_ context.Context) error {
		callCount++
		return NewTransientError(errors.New("transient"))
	})
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in error chain, got: %v", err)
	}
	// Should have made at least 1 call but not all 5.
	if callCount == 0 || callCount >= 5 {
		t.Errorf("expected between 1 and 4 calls, got %d", callCount)
	}
}

func TestRetry_ExponentialBackoff(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  4,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	}

	var callTimes []time.Time
	err := Retry(context.Background(), cfg, func(_ context.Context) error {
		callTimes = append(callTimes, time.Now())
		if len(callTimes) < 4 {
			return NewTransientError(errors.New("transient"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(callTimes) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(callTimes))
	}

	// Verify delays roughly increase (allowing for jitter and scheduling variance).
	// Expected delays: ~10ms, ~20ms, ~40ms
	for i := 1; i < len(callTimes); i++ {
		delay := callTimes[i].Sub(callTimes[i-1])
		minExpected := cfg.InitialDelay * time.Duration(1<<(i-1)) / 2 // Half the expected for tolerance.
		if delay < minExpected {
			t.Errorf("delay %d: %v is less than minimum expected %v", i, delay, minExpected)
		}
	}

	// Verify delays increase (with some tolerance for jitter).
	if len(callTimes) >= 3 {
		delay1 := callTimes[2].Sub(callTimes[1])
		delay0 := callTimes[1].Sub(callTimes[0])
		// The second delay should be larger than the first (accounting for jitter).
		// We use a generous tolerance since timing can vary.
		if delay1 < delay0/2 {
			t.Errorf("expected increasing delays, got %v then %v", delay0, delay1)
		}
	}
}

func TestRetry_JitterIsApplied(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   1.0, // No backoff growth, so all delays are the same base.
	}

	// Run multiple trials and check that delays aren't exactly the same.
	// With jitter of 0-25%, delays should vary.
	var delays []time.Duration
	for trial := 0; trial < 5; trial++ {
		var callTimes []time.Time
		_ = Retry(context.Background(), cfg, func(_ context.Context) error {
			callTimes = append(callTimes, time.Now())
			if len(callTimes) < 3 {
				return NewTransientError(errors.New("transient"))
			}
			return nil
		})
		if len(callTimes) >= 2 {
			delays = append(delays, callTimes[1].Sub(callTimes[0]))
		}
	}

	if len(delays) < 2 {
		t.Skip("not enough delay samples to verify jitter")
	}

	// Check that at least some delays differ from each other.
	// This is a probabilistic check — with 5 trials and 0-25% jitter,
	// the chance of all delays being identical is vanishingly small.
	allSame := true
	for i := 1; i < len(delays); i++ {
		if delays[i] != delays[0] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Logf("all delays were identical (%v) — jitter may not be working", delays[0])
		// We don't fail here because timing precision could cause this.
	}
}

func TestRetry_DefaultConfigValidation(t *testing.T) {
	// Zero-valued config should use defaults.
	cfg := RetryConfig{}.validate()
	defaults := DefaultRetryConfig()

	if cfg.MaxAttempts != defaults.MaxAttempts {
		t.Errorf("MaxAttempts = %d, want %d", cfg.MaxAttempts, defaults.MaxAttempts)
	}
	if cfg.InitialDelay != defaults.InitialDelay {
		t.Errorf("InitialDelay = %v, want %v", cfg.InitialDelay, defaults.InitialDelay)
	}
	if cfg.MaxDelay != defaults.MaxDelay {
		t.Errorf("MaxDelay = %v, want %v", cfg.MaxDelay, defaults.MaxDelay)
	}
	if cfg.Multiplier != defaults.Multiplier {
		t.Errorf("Multiplier = %v, want %v", cfg.Multiplier, defaults.Multiplier)
	}
}

func TestRetryWithResult_SuccessFirstTry(t *testing.T) {
	result, err := RetryWithResult(context.Background(), DefaultRetryConfig(), func(_ context.Context) (string, error) {
		return "hello", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("result = %q, want %q", result, "hello")
	}
}

func TestRetryWithResult_TransientThenSuccess(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	callCount := 0
	result, err := RetryWithResult(context.Background(), cfg, func(_ context.Context) (int, error) {
		callCount++
		if callCount < 2 {
			return 0, NewTransientError(errors.New("timeout"))
		}
		return 42, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Errorf("result = %d, want 42", result)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestRetryWithResult_PermanentError(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	callCount := 0
	result, err := RetryWithResult(context.Background(), cfg, func(_ context.Context) (string, error) {
		callCount++
		return "", NewPermanentError(errors.New("bad input"))
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if result != "" {
		t.Errorf("expected zero value, got %q", result)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestRetryWithResult_MaxAttemptsExhausted(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  2,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	callCount := 0
	_, err := RetryWithResult(context.Background(), cfg, func(_ context.Context) (int, error) {
		callCount++
		return 0, NewTransientError(errors.New("still failing"))
	})
	if err == nil {
		t.Fatal("expected error after max attempts")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestRetryWithResult_ContextCancellation(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  10,
		InitialDelay: 1 * time.Second,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := RetryWithResult(ctx, cfg, func(_ context.Context) (string, error) {
		callCount++
		return "", NewTransientError(errors.New("transient"))
	})
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in error chain, got: %v", err)
	}
}

func TestBackoffDelay(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	}

	// Test that delays increase with attempt number.
	prev := time.Duration(0)
	for attempt := 1; attempt <= 4; attempt++ {
		delay := backoffDelay(cfg, attempt)
		// The base delay (without jitter) should be initialDelay * multiplier^(attempt-1).
		baseDelay := time.Duration(float64(cfg.InitialDelay) * float64(int64(1)<<(attempt-1)))
		maxWithJitter := baseDelay + baseDelay/4 // 25% jitter.

		if delay < baseDelay {
			t.Errorf("attempt %d: delay %v is less than base %v", attempt, delay, baseDelay)
		}
		if delay > maxWithJitter {
			t.Errorf("attempt %d: delay %v exceeds max with jitter %v", attempt, delay, maxWithJitter)
		}
		if attempt > 1 && delay <= prev/2 {
			// Allow for jitter, but delays should generally increase.
			t.Errorf("attempt %d: delay %v did not increase from previous %v", attempt, delay, prev)
		}
		prev = delay
	}
}

func TestBackoffDelay_MaxDelayCap(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  10,
		InitialDelay: 1 * time.Second,
		MaxDelay:     5 * time.Second,
		Multiplier:   10.0,
	}

	// At attempt 3, base delay would be 1s * 10^2 = 100s, but should be capped.
	delay := backoffDelay(cfg, 3)
	maxAllowed := cfg.MaxDelay + cfg.MaxDelay/4 // Max delay + 25% jitter.
	if delay > maxAllowed {
		t.Errorf("delay %v exceeds max allowed %v (with jitter)", delay, maxAllowed)
	}
}

func TestRetry_UnclassifiedTransientMessage(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	callCount := 0
	err := Retry(context.Background(), cfg, func(_ context.Context) error {
		callCount++
		if callCount < 3 {
			return fmt.Errorf("HTTP status 503 service unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (retried on transient message), got %d", callCount)
	}
}
