package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclio/openclio/internal/storage"
)

func TestFSToolsLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// prepare files
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// ensure DB present
	dbPath := filepath.Join(root, ".openclio", "data.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db.Close()

	// search substring
	out, err := CallTool(ctx, "search_files", map[string]any{"work_dir": root, "pattern": "dir/a.txt"})
	if err != nil {
		t.Fatalf("search_files failed: %v", err)
	}
	if out == nil {
		t.Fatalf("search_files returned nil")
	}

	// move file
	_, err = CallTool(ctx, "move_file", map[string]any{"work_dir": root, "src": "dir/a.txt", "dst": "dir/b.txt", "overwrite": true})
	if err != nil {
		t.Fatalf("move_file failed: %v", err)
	}

	// delete file (requires force)
	_, err = CallTool(ctx, "delete_file", map[string]any{"work_dir": root, "path": "dir/b.txt", "force": true})
	if err != nil {
		t.Fatalf("delete_file failed: %v", err)
	}
}
