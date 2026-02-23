package slack

import (
	"testing"
)

func TestSlackAdapter_Name(t *testing.T) {
	a := &Adapter{}
	if a.Name() != "slack" {
		t.Errorf("expected 'slack', got %q", a.Name())
	}
}

func TestSlackAdapter_New_EmptyToken(t *testing.T) {
	_, err := New("", nil)
	if err == nil {
		t.Error("expected error for empty token")
	}
}

func TestSlackAdapter_New_ValidToken(t *testing.T) {
	a, err := New("xoxb-fake-token-for-testing", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
	if a.Name() != "slack" {
		t.Errorf("expected Name() == 'slack', got %q", a.Name())
	}
}

func TestSlackAdapter_Health_BadToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HTTP-dependent test in short mode")
	}
	// AuthTest() with a bad token should return a non-nil error from Slack API.
	a, err := New("xoxb-invalid-token-that-will-fail", nil)
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}
	err = a.Health()
	if err == nil {
		t.Error("expected error for bad Slack token")
	}
}

func TestSlackAdapter_Stop_Idempotent(t *testing.T) {
	a := &Adapter{done: make(chan struct{})}
	// Stop twice should not panic
	a.Stop()
	a.Stop()
}
