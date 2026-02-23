package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryWriteTool(t *testing.T) {
	// Create temporary data dir
	tmpDir, err := os.MkdirTemp("", "agent-memory-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tool := NewMemoryWriteTool(tmpDir)
	if tool.Name() != "memory_write" {
		t.Errorf("expected name memory_write, got %s", tool.Name())
	}

	memPath := filepath.Join(tmpDir, "memory.md")

	// 1. Write the first fact
	params := []byte(`{"fact": "My favorite color is blue."}`)
	res, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("failed to execute tool: %v", err)
	}
	if !strings.Contains(res, "successfully saved") {
		t.Errorf("unexpected success message: %s", res)
	}

	// Verify file content
	data, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("failed to read memory file: %v", err)
	}
	content := string(data)
	if content != "- My favorite color is blue.\n" {
		t.Errorf("unexpected memory content: %q", content)
	}

	// 2. Write a second fact (should append)
	params2 := []byte(`{"fact": "I prefer Go over Python."}`)
	_, err = tool.Execute(context.Background(), params2)
	if err != nil {
		t.Fatalf("failed to execute tool second time: %v", err)
	}

	// Verify appended content
	data, err = os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("failed to read memory file: %v", err)
	}
	content = string(data)
	expected := "- My favorite color is blue.\n- I prefer Go over Python.\n"
	if content != expected {
		t.Errorf("unexpected memory content after append: %q", content)
	}

	// 3. Test empty missing fact
	paramsEmpty := []byte(`{"fact": ""}`)
	_, err = tool.Execute(context.Background(), paramsEmpty)
	if err == nil {
		t.Error("expected error for empty fact")
	}

	// 4. Test invalid JSON
	paramsInvalid := []byte(`{"fact": `) // malformed
	_, err = tool.Execute(context.Background(), paramsInvalid)
	if err == nil {
		t.Error("expected error for invalid json")
	}
}
