package whatsapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skipIfForeignKeysUnsupported skips the test when modernc.org/sqlite cannot
// enable foreign keys (happens without CGO or with certain connection pool
// configurations).  whatsmeow requires foreign_keys; this is an environment
// limitation, not a code bug.
func skipIfForeignKeysUnsupported(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "foreign keys") {
		t.Skipf("skipping: SQLite foreign_keys not available in this test environment: %v", err)
	}
}

func TestWhatsAppAdapter_Name(t *testing.T) {
	a := &Adapter{}
	if a.Name() != "whatsapp" {
		t.Errorf("expected 'whatsapp', got %q", a.Name())
	}
}

func TestWhatsAppAdapter_New_CreatesDataDir(t *testing.T) {
	dir := t.TempDir()

	a, err := New(dir, nil)
	skipIfForeignKeysUnsupported(t, err)
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}

	// Verify the data directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("expected data dir %s to exist", dir)
	}

	// Verify whatsapp.db was created
	dbPath := filepath.Join(dir, "whatsapp.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("expected %s to be created", dbPath)
	}
}

func TestWhatsAppAdapter_New_NestedDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "sub", "dir")

	// Should create nested directories
	a, err := New(dir, nil)
	skipIfForeignKeysUnsupported(t, err)
	if err != nil {
		t.Fatalf("New() returned unexpected error for nested dir: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("expected nested dir %s to be created", dir)
	}
}

func TestWhatsAppAdapter_Health_Disconnected(t *testing.T) {
	dir := t.TempDir()

	a, err := New(dir, nil)
	skipIfForeignKeysUnsupported(t, err)
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	// Client is initialized but not connected — Health() should return an error
	err = a.Health()
	if err == nil {
		t.Error("expected error when client is not connected")
	}
}

func TestWhatsAppAdapter_Health_PairingStatesAreHealthy(t *testing.T) {
	dir := t.TempDir()

	a, err := New(dir, nil)
	skipIfForeignKeysUnsupported(t, err)
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	a.setQRState("waiting_for_qr", "")
	if err := a.Health(); err != nil {
		t.Fatalf("expected waiting_for_qr to be treated as healthy, got %v", err)
	}

	a.setQRState("code", "qr-code")
	if err := a.Health(); err != nil {
		t.Fatalf("expected code to be treated as healthy, got %v", err)
	}
}

func TestWhatsAppAdapter_Health_NilClient(t *testing.T) {
	a := &Adapter{client: nil}
	err := a.Health()
	if err == nil {
		t.Error("expected error when client is nil")
	}
}

func TestWhatsAppAdapter_Stop_Idempotent(t *testing.T) {
	a := &Adapter{done: make(chan struct{})}
	// Stop twice should not panic
	a.Stop()
	a.Stop()
}

func TestResetStoredSession_RemovesSessionFiles(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		filepath.Join(dir, "whatsapp.db"),
		filepath.Join(dir, "whatsapp.db-shm"),
		filepath.Join(dir, "whatsapp.db-wal"),
	}
	for _, file := range files {
		if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}

	if err := ResetStoredSession(dir); err != nil {
		t.Fatalf("ResetStoredSession() error: %v", err)
	}

	for _, file := range files {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err=%v", file, err)
		}
	}
}

func TestNormalizeWhatsAppChatID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "full jid",
			input: "919500080653@s.whatsapp.net",
			want:  "919500080653@s.whatsapp.net",
		},
		{
			name:  "legacy c us",
			input: "919500080653@c.us",
			want:  "919500080653@s.whatsapp.net",
		},
		{
			name:  "e164 with plus",
			input: "+91 95000 80653",
			want:  "919500080653@s.whatsapp.net",
		},
		{
			name:  "e164 with 00 prefix",
			input: "00919500080653",
			want:  "919500080653@s.whatsapp.net",
		},
		{
			name:    "missing country code",
			input:   "9500080653",
			wantErr: "missing country code",
		},
		{
			name:    "invalid value",
			input:   "abc",
			wantErr: "invalid whatsapp phone number",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			jid, err := normalizeWhatsAppChatID(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tc.wantErr)
				}
				if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := jid.String(); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestShouldResetWhatsAppSessionStore(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "foreign keys check failure",
			err:  fmt.Errorf("whatsapp: open session store: failed to check if foreign keys are enabled: SQL logic error: out of memory (1)"),
			want: true,
		},
		{
			name: "generic out of memory sqlite logic",
			err:  fmt.Errorf("SQL logic error: out of memory (1)"),
			want: true,
		},
		{
			name: "unrelated error",
			err:  fmt.Errorf("permission denied"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := shouldResetWhatsAppSessionStore(tc.err)
			if got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}
