// Package errors implements error classification and retry logic for the LLM API.
package errors

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NOTE: Retry logic lives in internal/runtime/retry/retry.go.
// This package only provides error classification.

// ── Error Categories ─────────────────────────────────────────

// Category represents the class of error encountered.
type Category string

const (
	// CategoryRetryable indicates a transient error that should be retried.
	// Examples: 529 overload, connection timeout, rate limit (with wait)
	CategoryRetryable Category = "retryable"

	// CategoryRateLimit indicates a rate limit was hit (needs backoff).
	CategoryRateLimit Category = "rate_limit"

	// CategoryPromptTooLong indicates the prompt exceeded token limits.
	// Should trigger context compaction.
	CategoryPromptTooLong Category = "prompt_too_long"

	// CategoryAuth indicates an authentication/authorization error.
	// Non-retryable, needs user intervention.
	CategoryAuth Category = "auth"

	// CategoryInvalidRequest indicates a malformed request.
	// Non-retryable.
	CategoryInvalidRequest Category = "invalid_request"

	// CategoryFatal indicates an unrecoverable error.
	// Examples: model not found, service unavailable
	CategoryFatal Category = "fatal"

	// CategoryUnknown indicates we couldn't classify the error.
	CategoryUnknown Category = "unknown"
)

// ── Classified Error ─────────────────────────────────────────

// ClassifiedError wraps an error with classification metadata.
type ClassifiedError struct {
	Original      error
	Category      Category
	Message       string // User-friendly message
	RetryAfter    time.Duration
	StatusCode    int
	TokensActual  int // For prompt-too-long errors
	TokensLimit   int // For prompt-too-long errors
	IsRetryable   bool
	ShouldCompact bool // True for prompt-too-long
}

func (e *ClassifiedError) Error() string {
	return e.Message
}

func (e *ClassifiedError) Unwrap() error {
	return e.Original
}

// ── Classification ───────────────────────────────────────────

var (
	// Prompt too long patterns
	promptTooLongRe = regexp.MustCompile(`prompt is too long[^0-9]*(\d+)\s*tokens?\s*>\s*(\d+)`)
	
	// Rate limit patterns
	rateLimitRe = regexp.MustCompile(`rate limit|quota|too many requests`)
	retryAfterRe = regexp.MustCompile(`retry.?after[:\s]+(\d+)`)
	
	// Auth patterns
	authErrorPatterns = []string{
		"invalid api key",
		"unauthorized",
		"forbidden",
		"authentication failed",
		"token expired",
		"token revoked",
	}
	
	// Overload patterns
	overloadPatterns = []string{
		"529",
		"overloaded",
		"server is overloaded",
		"service unavailable",
	}
)

// Classify analyzes an error and returns classification metadata.
func Classify(err error, statusCode int) *ClassifiedError {
	if err == nil {
		return nil
	}

	errMsg := strings.ToLower(err.Error())
	
	// Check for context cancellation (user interrupt)
	if errors.Is(err, context.Canceled) {
		return &ClassifiedError{
			Original:    err,
			Category:    CategoryFatal,
			Message:     "Request interrupted by user",
			StatusCode:  0,
			IsRetryable: false,
		}
	}
	
	// Check for deadline exceeded (timeout)
	if errors.Is(err, context.DeadlineExceeded) {
		return &ClassifiedError{
			Original:    err,
			Category:    CategoryRetryable,
			Message:     "Request timed out - retrying...",
			StatusCode:  0,
			IsRetryable: true,
			RetryAfter:  2 * time.Second,
		}
	}

	// Check for prompt too long
	if matches := promptTooLongRe.FindStringSubmatch(errMsg); len(matches) == 3 {
		actual, _ := strconv.Atoi(matches[1])
		limit, _ := strconv.Atoi(matches[2])
		return &ClassifiedError{
			Original:      err,
			Category:      CategoryPromptTooLong,
			Message:       fmt.Sprintf("Prompt too long (%d tokens > %d limit) - compacting conversation...", actual, limit),
			StatusCode:    statusCode,
			TokensActual:  actual,
			TokensLimit:   limit,
			IsRetryable:   true,
			ShouldCompact: true,
			RetryAfter:    1 * time.Second,
		}
	}

	// Check for rate limits
	if rateLimitRe.MatchString(errMsg) {
		retryAfter := 5 * time.Second // Default backoff
		if matches := retryAfterRe.FindStringSubmatch(errMsg); len(matches) == 2 {
			if seconds, err := strconv.Atoi(matches[1]); err == nil {
				retryAfter = time.Duration(seconds) * time.Second
			}
		}
		
		return &ClassifiedError{
			Original:    err,
			Category:    CategoryRateLimit,
			Message:     fmt.Sprintf("Rate limit exceeded - waiting %v before retry...", retryAfter),
			StatusCode:  statusCode,
			IsRetryable: true,
			RetryAfter:  retryAfter,
		}
	}

	// Check for auth errors
	for _, pattern := range authErrorPatterns {
		if strings.Contains(errMsg, pattern) {
			return &ClassifiedError{
				Original:    err,
				Category:    CategoryAuth,
				Message:     "API key invalid or expired. Please check your ANTHROPIC_API_KEY.",
				StatusCode:  statusCode,
				IsRetryable: false,
			}
		}
	}

	// Check for overload (529, service unavailable)
	for _, pattern := range overloadPatterns {
		if strings.Contains(errMsg, pattern) {
			return &ClassifiedError{
				Original:    err,
				Category:    CategoryRetryable,
				Message:     "API is overloaded - retrying with exponential backoff...",
				StatusCode:  statusCode,
				IsRetryable: true,
				RetryAfter:  5 * time.Second,
			}
		}
	}

	// Check for 4xx client errors (non-retryable)
	if statusCode >= 400 && statusCode < 500 {
		return &ClassifiedError{
			Original:    err,
			Category:    CategoryInvalidRequest,
			Message:     fmt.Sprintf("Invalid request: %s", err.Error()),
			StatusCode:  statusCode,
			IsRetryable: false,
		}
	}

	// Check for 5xx server errors (retryable)
	if statusCode >= 500 && statusCode < 600 {
		return &ClassifiedError{
			Original:    err,
			Category:    CategoryRetryable,
			Message:     "Server error - retrying...",
			StatusCode:  statusCode,
			IsRetryable: true,
			RetryAfter:  3 * time.Second,
		}
	}

	// Unknown error
	return &ClassifiedError{
		Original:    err,
		Category:    CategoryUnknown,
		Message:     fmt.Sprintf("API Error: %s", err.Error()),
		StatusCode:  statusCode,
		IsRetryable: false,
	}
}


