package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryConfig provides sensible defaults.
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   1 * time.Second,
	MaxDelay:    30 * time.Second,
}

// RetryableError wraps an error with retry metadata.
type RetryableError struct {
	Err        error
	StatusCode int
	RetryAfter time.Duration
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// IsRateLimit returns true if the error is an HTTP 429 (rate limit).
func IsRateLimit(err error) bool {
	var re *RetryableError
	if errors.As(err, &re) {
		return re.StatusCode == http.StatusTooManyRequests
	}
	return strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit")
}

// IsRetryable returns true if the error is transient and worth retrying.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if IsRateLimit(err) {
		return true
	}
	var re *RetryableError
	if errors.As(err, &re) {
		// 5xx errors are retryable; 4xx (except 429) are not
		return re.StatusCode >= 500 || re.StatusCode == 0
	}
	msg := err.Error()
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "EOF")
}

// RetryWithBackoff calls fn up to maxAttempts times with exponential backoff.
func RetryWithBackoff(ctx context.Context, cfg RetryConfig, fn func() error) error {
	delay := cfg.BaseDelay
	var lastErr error

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if !IsRetryable(lastErr) {
			return lastErr // permanent error — don't retry
		}

		if attempt == cfg.MaxAttempts {
			break
		}

		// Check for rate limit with Retry-After
		retryAfter := delay
		var re *RetryableError
		if errors.As(lastErr, &re) && re.RetryAfter > 0 {
			retryAfter = re.RetryAfter
		}

		// Cap at MaxDelay
		if retryAfter > cfg.MaxDelay {
			retryAfter = cfg.MaxDelay
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(retryAfter):
		}

		// Exponential backoff
		delay *= 2
	}

	return fmt.Errorf("all %d attempts failed: %w", cfg.MaxAttempts, lastErr)
}
