package errors

import (
	"context"
	"errors"
	"testing"

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
