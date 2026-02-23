package logger

import (
	"strings"
	"testing"
)

func TestScrubSecrets(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOut string
	}{
		{
			name:    "OpenAI key",
			input:   "calling with key sk-proj-ABCDEFGHIJKLMNOPQRSTabcdef",
			wantOut: "[REDACTED]",
		},
		{
			name:    "Anthropic key in header",
			input:   "x-api-key: sk-ant-api03-supersecret12345678",
			wantOut: "x-api-key: [REDACTED]",
		},
		{
			name:    "Safe message unchanged",
			input:   "user said hello world",
			wantOut: "user said hello world",
		},
		{
			name:    "Bearer token",
			input:   "Authorization: Bearer abcdefghijklmnopqrstuvwxyz1234567890",
			wantOut: "[REDACTED]",
		},
		{
			name:    "ENV style key",
			input:   "ANTHROPIC_API_KEY=sk-ant-supersecretkey123456",
			wantOut: "[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScrubSecrets(tt.input)
			if strings.Contains(got, "supersecret") || strings.Contains(got, "ABCDEFGHIJKLMNOPQRST") {
				t.Errorf("ScrubSecrets(%q) = %q — secret still present", tt.input, got)
			}
			if tt.name == "Safe message unchanged" && got != tt.wantOut {
				t.Errorf("ScrubSecrets(%q) = %q, want %q", tt.input, got, tt.wantOut)
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	// Should not panic for any valid config
	l := New("debug", "stderr")
	if l == nil {
		t.Fatal("expected non-nil logger")
	}

	l2 := New("info", "")
	if l2 == nil {
		t.Fatal("expected non-nil logger")
	}

	l3 := New("invalid-level", "stderr")
	if l3 == nil {
		t.Fatal("expected non-nil logger for invalid level")
	}
}
