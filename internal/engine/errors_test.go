package engine

import (
	"errors"
	"fmt"
	"testing"
)

func TestTransientError(t *testing.T) {
	inner := errors.New("connection reset by peer")
	te := NewTransientError(inner)

	if te.Error() != inner.Error() {
		t.Errorf("Error() = %q, want %q", te.Error(), inner.Error())
	}

	if te.Unwrap() != inner {
		t.Error("Unwrap() did not return the inner error")
	}

	// errors.As should find the TransientError.
	var found *TransientError
	if !errors.As(te, &found) {
		t.Error("errors.As failed to find TransientError")
	}

	// Wrapping should still be discoverable.
	wrapped := fmt.Errorf("outer: %w", te)
	if !errors.As(wrapped, &found) {
		t.Error("errors.As failed to find wrapped TransientError")
	}
}

func TestPermanentError(t *testing.T) {
	inner := errors.New("invalid credentials")
	pe := NewPermanentError(inner)

	if pe.Error() != inner.Error() {
		t.Errorf("Error() = %q, want %q", pe.Error(), inner.Error())
	}

	if pe.Unwrap() != inner {
		t.Error("Unwrap() did not return the inner error")
	}

	// errors.As should find the PermanentError.
	var found *PermanentError
	if !errors.As(pe, &found) {
		t.Error("errors.As failed to find PermanentError")
	}
}

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "explicit transient error",
			err:  NewTransientError(errors.New("timeout")),
			want: true,
		},
		{
			name: "wrapped transient error",
			err:  fmt.Errorf("operation failed: %w", NewTransientError(errors.New("timeout"))),
			want: true,
		},
		{
			name: "explicit permanent error",
			err:  NewPermanentError(errors.New("invalid config")),
			want: false,
		},
		{
			name: "permanent wrapping transient message",
			err:  NewPermanentError(errors.New("connection refused")),
			want: false,
		},
		{
			name: "unclassified connection refused",
			err:  errors.New("dial tcp: connection refused"),
			want: true,
		},
		{
			name: "unclassified connection reset",
			err:  errors.New("read: connection reset by peer"),
			want: true,
		},
		{
			name: "unclassified timeout",
			err:  errors.New("request timeout after 30s"),
			want: true,
		},
		{
			name: "unclassified deadline exceeded",
			err:  errors.New("context deadline exceeded"),
			want: true,
		},
		{
			name: "unclassified rate limit",
			err:  errors.New("API rate limit exceeded"),
			want: true,
		},
		{
			name: "unclassified 429",
			err:  errors.New("HTTP status 429"),
			want: true,
		},
		{
			name: "unclassified 502",
			err:  errors.New("status 502 bad gateway"),
			want: true,
		},
		{
			name: "unclassified 503",
			err:  errors.New("status 503 service unavailable"),
			want: true,
		},
		{
			name: "unclassified 504",
			err:  errors.New("status 504 gateway timeout"),
			want: true,
		},
		{
			name: "unclassified signal killed",
			err:  errors.New("process exited: signal: killed"),
			want: true,
		},
		{
			name: "unclassified generic error",
			err:  errors.New("file not found"),
			want: false,
		},
		{
			name: "unclassified auth error",
			err:  errors.New("authentication failed"),
			want: false,
		},
		{
			name: "wrapped unclassified transient message",
			err:  fmt.Errorf("agent: %w", errors.New("connection refused")),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTransient(tt.err)
			if got != tt.want {
				t.Errorf("IsTransient() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsTransientMessage(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"connection refused", true},
		{"Connection Refused", true},
		{"connection reset by peer", true},
		{"broken pipe", true},
		{"request timeout", true},
		{"context deadline exceeded", true},
		{"rate limit exceeded", true},
		{"too many requests", true},
		{"service unavailable", true},
		{"bad gateway", true},
		{"gateway timeout", true},
		{"temporary failure in name resolution", true},
		{"please try again later", true},
		{"internal server error", true},
		{"HTTP status 429", true},
		{"status 502", true},
		{"status 503", true},
		{"status 504", true},
		{"signal: killed", true},
		{"signal: segmentation fault", true},
		{"connection closed unexpectedly", true},
		{"file not found", false},
		{"invalid argument", false},
		{"permission denied", false},
		{"authentication failed", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got := IsTransientMessage(tt.msg)
			if got != tt.want {
				t.Errorf("IsTransientMessage(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}
