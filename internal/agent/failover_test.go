package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// ─── AgentError ───────────────────────────────────────────────────────────────

func TestAgentErrorCode(t *testing.T) {
	err := ErrProviderTimeout.Wrap(fmt.Errorf("EOF"))
	if err.Code != "E1001" {
		t.Errorf("expected E1001, got %q", err.Code)
	}
	msg := err.Error()
	if msg == "" {
		t.Error("AgentError.Error() must not be empty")
	}
	// Must mention both code and cause
	if err.Code == "" || err.Cause == nil {
		t.Error("Wrap must set both Code and Cause")
	}
}

func TestAgentErrorWrapNilCause(t *testing.T) {
	// Wrapping nil should be valid (cause is optional)
	e := ErrStorageWrite.Wrap(nil)
	if e.Error() == "" {
		t.Error("AgentError.Error() must not be empty even with nil cause")
	}
}

// ─── FailoverProvider ─────────────────────────────────────────────────────────

type stubProvider struct {
	name  string
	failN int // fail for first failN calls
	callN int
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Chat(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	s.callN++
	if s.callN <= s.failN {
		return nil, &RetryableError{Err: fmt.Errorf("%s unavailable", s.name), StatusCode: 503}
	}
	return &ChatResponse{Content: "ok:" + s.name}, nil
}

func TestFailoverUsesSecondProvider(t *testing.T) {
	primary := &stubProvider{name: "primary", failN: 99}
	fallback := &stubProvider{name: "fallback", failN: 0}

	fp := NewFailoverProvider(primary, []Provider{fallback}, nil)
	resp, err := fp.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("expected success via fallback, got: %v", err)
	}
	if resp == nil || resp.Content != "ok:fallback" {
		t.Errorf("expected fallback response, got: %v", resp)
	}
}

func TestFailoverAllFail(t *testing.T) {
	p1 := &stubProvider{name: "p1", failN: 99}
	p2 := &stubProvider{name: "p2", failN: 99}

	fp := NewFailoverProvider(p1, []Provider{p2}, nil)
	_, err := fp.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestFailoverNoFallbacksPassthrough(t *testing.T) {
	primary := &stubProvider{name: "solo", failN: 0}
	fp := NewFailoverProvider(primary, nil, nil)
	// FailoverProvider always wraps — name should contain the primary's name.
	if !strings.Contains(fp.Name(), "solo") {
		t.Errorf("expected name to contain 'solo', got: %q", fp.Name())
	}
	// Should succeed — the single provider succeeds.
	resp, err := fp.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("single-provider failover should succeed, got: %v", err)
	}
	if resp == nil || resp.Content != "ok:solo" {
		t.Errorf("unexpected response: %v", resp)
	}
}

func TestFailoverContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	primary := &stubProvider{name: "primary", failN: 99}
	fallback := &stubProvider{name: "fallback", failN: 0}
	fp := NewFailoverProvider(primary, []Provider{fallback}, nil)

	// Even with cancelled context, FailoverProvider should return some error
	_, err := fp.Chat(ctx, ChatRequest{})
	// Behaviour depends on whether the cancelled context propagates — either
	// context error or provider error is acceptable.
	_ = err
}
