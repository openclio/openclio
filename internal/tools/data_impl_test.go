package tools

import (
	"context"
	"testing"
)

func TestJSONQueryCSVAndTemplate(t *testing.T) {
	ctx := context.Background()
	jsonStr := `{"items":[{"name":"alice"},{"name":"bob"}],"meta":{"count":2}}`
	out, err := CallTool(ctx, "json_query", map[string]any{"json": jsonStr, "query": "items.0.name"})
	if err != nil {
		t.Fatalf("json_query failed: %v", err)
	}
	if out != "alice" {
		t.Fatalf("expected alice, got %#v", out)
	}

	csvContent := "id,name\n1,A\n2,B\n"
	csvOut, err := CallTool(ctx, "csv_read", map[string]any{"content": csvContent, "header": true})
	if err != nil {
		t.Fatalf("csv_read failed: %v", err)
	}
	if csvOut == nil {
		t.Fatalf("csv_read returned nil")
	}

	tmplOut, err := CallTool(ctx, "template_render", map[string]any{"template": "Hello {{.name}}", "vars": map[string]any{"name": "Joe"}})
	if err != nil {
		t.Fatalf("template_render failed: %v", err)
	}
	m, ok := tmplOut.(map[string]any)
	if !ok || m["rendered"] != "Hello Joe" {
		t.Fatalf("unexpected template render output: %#v", tmplOut)
	}
}
