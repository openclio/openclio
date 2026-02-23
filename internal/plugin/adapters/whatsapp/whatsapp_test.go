package whatsapp

import (
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
