package engine

import (
	"errors"
	"strings"
)

// TransientError wraps an error that is transient and can be retried.
// Transient errors include network timeouts, rate limits, connection resets,
// and agent process crashes. Wrapping an error as TransientError signals to
// the retry logic that the operation may succeed on a subsequent attempt.
type TransientError struct {
	Err error
}

func (e *TransientError) Error() string { return e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// NewTransientError wraps err as a TransientError.
func NewTransientError(err error) *TransientError {
	return &TransientError{Err: err}
}

// PermanentError wraps an error that should not be retried.
// Permanent errors include invalid configuration, authentication failures,
// and logical errors in agent output. The retry logic returns these immediately
// without additional attempts.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// NewPermanentError wraps err as a PermanentError.
func NewPermanentError(err error) *PermanentError {
	return &PermanentError{Err: err}
}

// IsTransient reports whether err (or any error in its chain) is transient.
// An error is considered transient if:
//   - It wraps a *TransientError
//   - Its message matches common transient patterns (and it is not a *PermanentError)
//
// Errors that are explicitly marked as *PermanentError are never transient.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}

	// Explicitly permanent errors are never transient.
	var permanent *PermanentError
	if errors.As(err, &permanent) {
		return false
	}

	// Explicitly marked transient errors are always transient.
	var transient *TransientError
	if errors.As(err, &transient) {
		return true
	}

	// Fall back to message-based heuristics for unclassified errors.
	return IsTransientMessage(err.Error())
}

// transientPatterns contains substrings commonly found in transient error messages.
var transientPatterns = []string{
	"connection refused",
	"connection reset",
	"connection closed",
	"broken pipe",
	"timeout",
	"deadline exceeded",
	"rate limit",
	"too many requests",
	"service unavailable",
	"bad gateway",
	"gateway timeout",
	"temporary failure",
	"try again",
	"server error",
	"status 429",
	"status 502",
	"status 503",
	"status 504",
	"signal: killed",
	"signal: segmentation fault",
}

// IsTransientMessage checks an error message string for common transient patterns.
// This is a heuristic fallback for errors that are not explicitly wrapped as
// TransientError or PermanentError.
func IsTransientMessage(msg string) bool {
	lower := strings.ToLower(msg)
	for _, pattern := range transientPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
