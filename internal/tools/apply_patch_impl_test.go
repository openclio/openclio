package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclio/openclio/internal/storage"
)

func TestApplyPatchToolLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// set CWD to root for convenience
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	_ = os.Chdir(root)

	// prepare initial file
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f1 := filepath.Join("sub", "a.txt")
	if err := os.WriteFile(filepath.Join(root, f1), []byte("old"), 0644); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// ensure DB present (migrations)
	dbPath := filepath.Join(root, ".openclio", "data.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db.Close()

	// apply patch: modify a.txt and create b.txt
	changes := []any{
		map[string]any{"path": f1, "content": "new"},
		map[string]any{"path": "sub/b.txt", "content": "hello"},
	}
	out, err := CallTool(ctx, "apply_patch", map[string]any{"changes": changes})
	if err != nil {
		t.Fatalf("apply_patch failed: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output: %#v", out)
	}
	bid, _ := m["backup_id"].(string)
	if bid == "" {
		t.Fatalf("expected backup_id")
	}

	// verify files updated
	got1, _ := os.ReadFile(filepath.Join(root, f1))
	if string(got1) != "new" {
		t.Fatalf("expected new content, got %s", string(got1))
	}
	got2, _ := os.ReadFile(filepath.Join(root, "sub", "b.txt"))
	if string(got2) != "hello" {
		t.Fatalf("expected hello, got %s", string(got2))
	}

	// revert
	_, err = CallTool(ctx, "apply_patch", map[string]any{"revert": bid})
	if err != nil {
		t.Fatalf("revert failed: %v", err)
	}

	// verify restored
	got1b, _ := os.ReadFile(filepath.Join(root, f1))
	if string(got1b) != "old" {
		t.Fatalf("expected old after revert, got %s", string(got1b))
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected b.txt removed after revert")
	}
}
