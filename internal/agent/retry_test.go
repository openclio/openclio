package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("connection refused"), true},
		{errors.New("timeout"), true},
		{errors.New("EOF"), true},
		{&RetryableError{Err: errors.New("x"), StatusCode: 429}, true},
		{&RetryableError{Err: errors.New("x"), StatusCode: 500}, true},
		{&RetryableError{Err: errors.New("x"), StatusCode: 400}, false}, // bad request — permanent
		{errors.New("invalid API key"), false},
	}
	for _, tt := range tests {
		got := IsRetryable(tt.err)
		if got != tt.want {
			t.Errorf("IsRetryable(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestRetryWithBackoffSuccess(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(context.Background(), RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}, func() error {
		calls++
		if calls < 2 {
			return &RetryableError{Err: errors.New("transient"), StatusCode: 503}
		}
		return nil
	})
	if err != nil {
		t.Errorf("expected success after retry, got: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestRetryWithBackoffPermanent(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(context.Background(), RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}, func() error {
		calls++
		return &RetryableError{Err: errors.New("bad request"), StatusCode: 400}
	})
	if err == nil {
		t.Error("expected error for permanent failure")
	}
	if calls != 1 {
		t.Errorf("expected 1 call for permanent error, got %d", calls)
	}
}

func TestRetryWithBackoffExhausted(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(context.Background(), RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}, func() error {
		calls++
		return &RetryableError{Err: errors.New("server error"), StatusCode: 503}
	})
	if err == nil {
		t.Error("expected error after 3 exhausted attempts")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}
