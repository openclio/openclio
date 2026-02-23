package tools

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclio/openclio/internal/storage"
)

func TestProcessManagerLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
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

	// spawn a short-lived process that prints twice
	cmd := "printf 'hello\n'; sleep 0.1; printf 'world\n'"
	out, err := CallTool(ctx, "process_spawn", map[string]any{"command": cmd})
	if err != nil {
		t.Fatalf("process_spawn failed: %v", err)
	}
	m := out.(map[string]any)
	id := m["id"].(string)

	// wait a bit
	time.Sleep(200 * time.Millisecond)

	// list
	_, err = CallTool(ctx, "process_list", map[string]any{})
	if err != nil {
		t.Fatalf("process_list failed: %v", err)
	}

	// read stdout
	rout, err := CallTool(ctx, "process_read", map[string]any{"id": id, "lines": 10})
	if err != nil {
		t.Fatalf("process_read failed: %v", err)
	}
	_ = rout

	// spawn long running and kill
	out2, err := CallTool(ctx, "process_spawn", map[string]any{"command": "sleep 5"})
	if err != nil {
		t.Fatalf("process_spawn #2 failed: %v", err)
	}
	id2 := out2.(map[string]any)["id"].(string)
	_, err = CallTool(ctx, "process_kill", map[string]any{"id": id2})
	if err != nil {
		t.Fatalf("process_kill failed: %v", err)
	}
}
