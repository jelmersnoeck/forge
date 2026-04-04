package errors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassify(t *testing.T) {
	tests := map[string]struct {
		err        error
		statusCode int
		want       Category
		wantRetry  bool
		wantTokens bool
	}{
		"prompt too long": {
			err:        errors.New("prompt is too long: 137500 tokens > 135000 maximum"),
			statusCode: 400,
			want:       CategoryPromptTooLong,
			wantRetry:  true,
			wantTokens: true,
		},
		"rate limit": {
			err:        errors.New("rate limit exceeded, retry after 60 seconds"),
			statusCode: 429,
			want:       CategoryRateLimit,
			wantRetry:  true,
		},
		"invalid API key": {
			err:        errors.New("invalid api key"),
			statusCode: 401,
			want:       CategoryAuth,
			wantRetry:  false,
		},
		"overloaded 529": {
			err:        errors.New("529 overloaded"),
			statusCode: 529,
			want:       CategoryRetryable,
			wantRetry:  true,
		},
		"context canceled": {
			err:        context.Canceled,
			statusCode: 0,
			want:       CategoryFatal,
			wantRetry:  false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			classified := Classify(tc.err, tc.statusCode)
			r.NotNil(classified)
			r.Equal(tc.want, classified.Category)
			r.Equal(tc.wantRetry, classified.IsRetryable)

			if tc.wantTokens {
				r.Greater(classified.TokensActual, 0)
				r.Greater(classified.TokensLimit, 0)
				r.True(classified.ShouldCompact)
			}
		})
	}
}

func TestRetry_SuccessOnFirstAttempt(t *testing.T) {
	r := require.New(t)

	attempts := 0
	config := DefaultRetryConfig()

	err := Retry(context.Background(), config, func() error {
		attempts++
		return nil // Success
	})

	r.NoError(err)
	r.Equal(1, attempts)
}

func TestRetry_SuccessAfterRetries(t *testing.T) {
	r := require.New(t)

	attempts := 0
	config := DefaultRetryConfig()
	config.InitialBackoff = 10 * time.Millisecond

	err := Retry(context.Background(), config, func() error {
		attempts++
		if attempts < 3 {
			return &ClassifiedError{
				Original:    errors.New("retry me"),
				Category:    CategoryRetryable,
				IsRetryable: true,
			}
		}
		return nil
	})

	r.NoError(err)
	r.Equal(3, attempts)
}

func TestRetry_NonRetryableError(t *testing.T) {
	r := require.New(t)

	attempts := 0
	config := DefaultRetryConfig()

	err := Retry(context.Background(), config, func() error {
		attempts++
		return errors.New("invalid api key")
	})

	r.Error(err)
	r.Equal(1, attempts) // No retries
}

func TestRetry_ExponentialBackoff(t *testing.T) {
	r := require.New(t)

	config := DefaultRetryConfig()
	config.InitialBackoff = 10 * time.Millisecond
	config.MaxBackoff = 50 * time.Millisecond
	config.BackoffMultiplier = 2.0
	config.MaxAttempts = 2

	attempts := 0
	start := time.Now()

	err := Retry(context.Background(), config, func() error {
		attempts++
		if attempts < 3 {
			return &ClassifiedError{
				Original:    errors.New("mock"),
				Category:    CategoryRetryable,
				IsRetryable: true,
			}
		}
		return nil
	})

	elapsed := time.Since(start)

	r.NoError(err)
	r.Equal(3, attempts)
	r.Greater(elapsed, 25*time.Millisecond)
	r.Less(elapsed, 60*time.Millisecond)
}
