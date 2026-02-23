package tools

import (
	"context"
	"testing"
)

func TestMemoryToolLifecycle(t *testing.T) {
	ctx := context.Background()

	// write
	out, err := CallTool(ctx, "memory_write", map[string]any{"content": "hello world", "metadata": map[string]any{"tag": "greeting"}})
	if err != nil {
		t.Fatalf("memory_write failed: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected write output: %#v", out)
	}
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("empty id from write")
	}

	// read
	rout, err := CallTool(ctx, "memory_read", map[string]any{"id": id})
	if err != nil {
		t.Fatalf("memory_read failed: %v", err)
	}
	if rout == nil {
		t.Fatalf("memory_read returned nil")
	}

	// search
	sout, err := CallTool(ctx, "memory_search", map[string]any{"query": "hello", "k": 5})
	if err != nil {
		t.Fatalf("memory_search failed: %v", err)
	}
	arr, ok := sout.([]map[string]any)
	if !ok {
		// may be []interface{}
		if _, ok2 := sout.([]any); !ok2 {
			t.Fatalf("unexpected search output type: %#v", sout)
		}
	}
	_ = arr

	// list
	_, err = CallTool(ctx, "memory_list", map[string]any{"limit": 10, "offset": 0})
	if err != nil {
		t.Fatalf("memory_list failed: %v", err)
	}

	// delete
	_, err = CallTool(ctx, "memory_delete", map[string]any{"id": id})
	if err != nil {
		t.Fatalf("memory_delete failed: %v", err)
	}
}
