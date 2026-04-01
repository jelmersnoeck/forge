package retry

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassify(t *testing.T) {
	tests := map[string]struct {
		err  error
		want Classification
	}{
		"nil error":          {err: nil, want: Fatal},
		"rate limit 429":     {err: fmt.Errorf("status 429: rate limit exceeded"), want: Retryable},
		"overloaded 529":     {err: fmt.Errorf("status 529: overloaded"), want: Retryable},
		"generic overloaded": {err: fmt.Errorf("API overloaded, try again later"), want: Retryable},
		"server error 500":   {err: fmt.Errorf("status 500: internal server error"), want: Retryable},
		"bad gateway 502":    {err: fmt.Errorf("status 502: bad gateway"), want: Retryable},
		"timeout":            {err: fmt.Errorf("request timeout after 30s"), want: Retryable},
		"connection refused": {err: fmt.Errorf("dial: connection refused"), want: Retryable},
		"connection reset":   {err: fmt.Errorf("read: connection reset by peer"), want: Retryable},
		"EOF":                {err: fmt.Errorf("unexpected EOF"), want: Retryable},
		"TLS handshake":      {err: fmt.Errorf("TLS handshake failure"), want: Retryable},
		"context canceled":   {err: fmt.Errorf("context canceled"), want: Fatal},
		"context deadline":   {err: fmt.Errorf("context deadline exceeded"), want: Fatal},
		"bad request 400":    {err: fmt.Errorf("status 400: bad request"), want: Fatal},
		"auth error 401":     {err: fmt.Errorf("status 401: unauthorized"), want: Fatal},
		"unknown error":      {err: fmt.Errorf("something unexpected happened"), want: Fatal},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			r.Equal(tc.want, Classify(tc.err))
		})
	}
}

func TestDo_Success(t *testing.T) {
	r := require.New(t)

	result, err := Do(context.Background(), DefaultPolicy(), nil, func() (string, error) {
		return "Troy and Abed in the morning!", nil
	})

	r.NoError(err)
	r.Equal("Troy and Abed in the morning!", result)
}

func TestDo_RetryThenSucceed(t *testing.T) {
	r := require.New(t)

	callCount := 0
	var attempts []Attempt

	result, err := Do(context.Background(), Policy{MaxRetries: 3, BaseDelay: 1 * time.Millisecond, MaxDelay: 10 * time.Millisecond},
		func(a Attempt) { attempts = append(attempts, a) },
		func() (string, error) {
			callCount++
			if callCount < 3 {
				return "", fmt.Errorf("status 529: overloaded")
			}
			return "Cool cool cool", nil
		},
	)

	r.NoError(err)
	r.Equal("Cool cool cool", result)
	r.Equal(3, callCount)
	r.Len(attempts, 2) // 2 retries before success
}

func TestDo_FatalError(t *testing.T) {
	r := require.New(t)

	callCount := 0
	_, err := Do(context.Background(), DefaultPolicy(), nil, func() (string, error) {
		callCount++
		return "", fmt.Errorf("status 401: unauthorized")
	})

	r.Error(err)
	r.Contains(err.Error(), "401")
	r.Equal(1, callCount) // no retries for fatal errors
}

func TestDo_MaxRetriesExhausted(t *testing.T) {
	r := require.New(t)

	callCount := 0
	_, err := Do(context.Background(), Policy{MaxRetries: 2, BaseDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond}, nil,
		func() (string, error) {
			callCount++
			return "", fmt.Errorf("status 529: overloaded")
		},
	)

	r.Error(err)
	r.Equal(3, callCount) // initial + 2 retries
}

func TestDo_ContextCancelled(t *testing.T) {
	r := require.New(t)

	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	_, err := Do(ctx, Policy{MaxRetries: 10, BaseDelay: 100 * time.Millisecond, MaxDelay: 1 * time.Second},
		func(a Attempt) {
			// Cancel during first retry wait.
			cancel()
		},
		func() (string, error) {
			callCount++
			return "", fmt.Errorf("status 529: overloaded")
		},
	)

	r.Error(err)
	r.Contains(err.Error(), "cancelled")
}

func TestBackoff(t *testing.T) {
	tests := map[string]struct {
		attempt int
		base    time.Duration
		max     time.Duration
	}{
		"first attempt":  {attempt: 0, base: 1 * time.Second, max: 60 * time.Second},
		"second attempt": {attempt: 1, base: 1 * time.Second, max: 60 * time.Second},
		"fifth attempt":  {attempt: 4, base: 1 * time.Second, max: 60 * time.Second},
		"capped at max":  {attempt: 10, base: 1 * time.Second, max: 5 * time.Second},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			delay := backoff(tc.attempt, tc.base, tc.max)
			r.LessOrEqual(delay, tc.max)
			r.Greater(delay, time.Duration(0))
		})
	}
}

func TestDo_NoRetries(t *testing.T) {
	r := require.New(t)

	callCount := 0
	_, err := Do(context.Background(), Policy{MaxRetries: 0, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}, nil,
		func() (string, error) {
			callCount++
			return "", fmt.Errorf("status 529: overloaded")
		},
	)

	r.Error(err)
	r.Equal(1, callCount) // no retries when MaxRetries=0
}
