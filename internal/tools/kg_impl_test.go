package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclio/openclio/internal/storage"
)

func TestKGToolsLifecycle(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("setenv HOME: %v", err)
	}
	dbPath := filepath.Join(tmp, ".openclio", "data.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	defer db.Close()

	// add node
	out, err := CallTool(ctx, "kg_add_node", map[string]any{"name": "Alice", "type": "person", "confidence": 0.9})
	if err != nil {
		t.Fatalf("kg_add_node failed: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("kg_add_node unexpected output: %#v", out)
	}
	idf, ok := m["id"].(float64)
	if !ok {
		t.Fatalf("expected numeric id, got %#v", m["id"])
	}
	id := int64(idf)

	// search
	sout, err := CallTool(ctx, "kg_search", map[string]any{"query": "alice", "limit": 10})
	if err != nil {
		t.Fatalf("kg_search failed: %v", err)
	}
	if sout == nil {
		t.Fatalf("kg_search returned nil")
	}

	// get node
	gout, err := CallTool(ctx, "kg_get_node", map[string]any{"id": id})
	if err != nil {
		t.Fatalf("kg_get_node failed: %v", err)
	}
	if gout == nil {
		t.Fatalf("kg_get_node returned nil")
	}

	// add another node and edge
	out2, err := CallTool(ctx, "kg_add_node", map[string]any{"name": "Acme Corp", "type": "org", "confidence": 0.8})
	if err != nil {
		t.Fatalf("kg_add_node #2 failed: %v", err)
	}
	m2 := out2.(map[string]any)
	toidf := m2["id"].(float64)
	toid := int64(toidf)

	_, err = CallTool(ctx, "kg_add_edge", map[string]any{"from_id": id, "to_id": toid, "relation": "works_at"})
	if err != nil {
		t.Fatalf("kg_add_edge failed: %v", err)
	}

	// delete node
	_, err = CallTool(ctx, "kg_delete_node", map[string]any{"id": id})
	if err != nil {
		t.Fatalf("kg_delete_node failed: %v", err)
	}
}
