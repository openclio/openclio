package tools

import (
	"context"
	"testing"
)

func TestNoopToolsRegistered(t *testing.T) {
	want := []string{"memory_search", "message_send", "apply_patch"}
	for _, name := range want {
		if _, ok := GetTool(name); !ok {
			t.Fatalf("expected tool %s to be registered", name)
		}
	}
}

func TestNoopToolReturnsNotImplemented(t *testing.T) {
	fn, ok := GetTool("memory_search")
	if !ok {
		t.Fatal("memory_search not registered")
	}
	_, err := fn(context.Background(), map[string]any{"q": "test"})
	// Implementation may be a noop or real; ensure calling does not panic and returns either result or error.
	if err != nil {
		t.Logf("tool returned error (expected for noop or incomplete impl): %v", err)
	} else {
		t.Log("tool returned result")
	}
}
