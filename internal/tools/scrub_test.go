package tools

import (
	"strings"
	"testing"
)

func TestScrubToolOutput(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantHide string // substring that must NOT appear in output
		wantKeep string // substring that MUST still appear in output
	}{
		{
			name:     "password env line",
			input:    "DB_PASSWORD=supersecret123\nexport DB_HOST=localhost",
			wantHide: "supersecret123",
			wantKeep: "DB_HOST",
		},
		{
			name:     "secret colon style",
			input:    "secret: mysecretvalue\nother: fine",
			wantHide: "mysecretvalue",
			wantKeep: "other",
		},
		{
			name:     "OpenAI API key in output",
			input:    "Using key sk-abc123DEF456ghi789JKL012mno345PQR678stu",
			wantHide: "sk-abc123",
			wantKeep: "Using key",
		},
		{
			name:     "AWS access key",
			input:    "Found key: AKIAIOSFODNN7EXAMPLE",
			wantHide: "AKIAIOSFODNN7EXAMPLE",
			wantKeep: "Found",
		},
		{
			name:     "Authorization Bearer header",
			input:    "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig",
			wantHide: "eyJhbGciOiJIUzI1NiJ9",
			wantKeep: "Authorization",
		},
		{
			name:     "clean output unchanged",
			input:    "total files: 42\nstatus: ok",
			wantHide: "", // nothing to redact
			wantKeep: "total files: 42",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScrubToolOutput(tc.input)
			if tc.wantHide != "" && strings.Contains(got, tc.wantHide) {
				t.Errorf("secret %q still visible in output: %q", tc.wantHide, got)
			}
			if tc.wantKeep != "" && !strings.Contains(got, tc.wantKeep) {
				t.Errorf("expected %q to remain in output, got: %q", tc.wantKeep, got)
			}
		})
	}
}
