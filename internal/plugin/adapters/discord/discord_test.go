package discord

import (
	"strings"
	"testing"
)

func TestDiscordAdapter_Name(t *testing.T) {
	a := &Adapter{}
	if a.Name() != "discord" {
		t.Errorf("expected 'discord', got %q", a.Name())
	}
}

func TestDiscordAdapter_Health_NoSession(t *testing.T) {
	a := &Adapter{}
	err := a.Health()
	if err == nil {
		t.Error("expected error when session is nil")
	}
}

func TestDiscordAdapter_MessageSplit_Short(t *testing.T) {
	text := "hello world"
	chunks := splitMessage(text, 2000)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("expected %q, got %q", text, chunks[0])
	}
}

func TestDiscordAdapter_MessageSplit_Long(t *testing.T) {
	// Build a string longer than 2000 chars
	text := strings.Repeat("a", 2500)
	chunks := splitMessage(text, 2000)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 2000 {
			t.Errorf("chunk %d exceeds 2000 chars: len=%d", i, len(c))
		}
	}
}

func TestDiscordAdapter_MessageSplit_WithNewlines(t *testing.T) {
	// Build a string with a newline near the 2000-char boundary
	part1 := strings.Repeat("a", 1900)
	part2 := strings.Repeat("b", 600)
	text := part1 + "\n" + part2
	chunks := splitMessage(text, 2000)
	// Should split at the newline
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 2000 {
			t.Errorf("chunk %d exceeds 2000 chars", i)
		}
	}
}

func TestDiscordAdapter_MessageSplit_Empty(t *testing.T) {
	chunks := splitMessage("", 2000)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("expected 1 empty chunk, got %v", chunks)
	}
}

func TestDiscordAdapter_Stop_Idempotent(t *testing.T) {
	a := &Adapter{done: make(chan struct{})}
	// Stop twice should not panic
	a.Stop()
	a.Stop()
}
