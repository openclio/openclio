package telegram

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramAdapter_Name(t *testing.T) {
	a := &Adapter{}
	if a.Name() != "telegram" {
		t.Errorf("expected 'telegram', got %q", a.Name())
	}
}

func TestTelegramAdapter_New_EmptyToken(t *testing.T) {
	_, err := New("", nil)
	if err == nil {
		t.Error("expected error for empty token")
	}
}

func TestTelegramAdapter_MessageSplit_Short(t *testing.T) {
	text := "hello telegram"
	chunks := splitMessage(text, 4096)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("expected %q, got %q", text, chunks[0])
	}
}

func TestTelegramAdapter_MessageSplit_Long(t *testing.T) {
	// Build a string longer than 4096 chars
	text := strings.Repeat("x", 5000)
	chunks := splitMessage(text, 4096)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 4096 {
			t.Errorf("chunk %d exceeds 4096 chars: len=%d", i, len(c))
		}
	}
}

func TestTelegramAdapter_MessageSplit_WithNewlines(t *testing.T) {
	// Build text with newline near the 4096-char boundary
	part1 := strings.Repeat("a", 4000)
	part2 := strings.Repeat("b", 200)
	text := part1 + "\n" + part2
	chunks := splitMessage(text, 4096)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 4096 {
			t.Errorf("chunk %d exceeds 4096 chars", i)
		}
	}
}

func TestTelegramAdapter_Health_BadToken(t *testing.T) {
	// Mock a Telegram-like server that returns 401 for bad token
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate Telegram returning non-200 for invalid token
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	// We can't easily redirect the Health() call to the mock server without
	// changing the adapter. Instead, test with a clearly invalid token that
	// will return non-OK from the real Telegram API (when available)
	// or skip this sub-check if offline.
	a := &Adapter{token: "badtoken:that_is_not_valid"}
	if testing.Short() {
		t.Skip("skipping HTTP-dependent test in short mode")
	}
	err := a.Health()
	// Telegram API returns a non-200 for invalid tokens
	if err == nil {
		t.Error("expected error for bad Telegram token")
	}
}

func TestTelegramAdapter_Stop_Idempotent(t *testing.T) {
	a := &Adapter{done: make(chan struct{})}
	// Stop twice should not panic
	a.Stop()
	a.Stop()
}

func TestParseChatID_Valid(t *testing.T) {
	id, err := parseChatID("12345678")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 12345678 {
		t.Errorf("expected 12345678, got %d", id)
	}
}

func TestParseChatID_Invalid(t *testing.T) {
	_, err := parseChatID("not-a-number")
	if err == nil {
		t.Error("expected error for invalid chat ID")
	}
}
