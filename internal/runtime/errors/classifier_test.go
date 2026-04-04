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
		"server 500": {
			err:        errors.New("internal server error"),
			statusCode: 500,
			want:       CategoryRetryable,
			wantRetry:  true,
		},
		"client 400": {
			err:        errors.New("bad request"),
			statusCode: 400,
			want:       CategoryInvalidRequest,
			wantRetry:  false,
		},
		"deadline exceeded": {
			err:        context.DeadlineExceeded,
			statusCode: 0,
			want:       CategoryRetryable,
			wantRetry:  true,
		},
		"nil error": {
			err:        nil,
			statusCode: 0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)

			classified := Classify(tc.err, tc.statusCode)
			if tc.err == nil {
				r.Nil(classified)
				return
			}

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
