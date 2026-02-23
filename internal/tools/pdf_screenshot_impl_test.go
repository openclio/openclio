package tools

import (
	"context"
	"testing"
)

func TestPDFAndScreenshotParameterValidation(t *testing.T) {
	ctx := context.Background()
	// pdf_read requires path
	if _, err := CallTool(ctx, "pdf_read", map[string]any{}); err == nil {
		t.Fatalf("expected error for missing path")
	}
	// screenshot requires url
	if _, err := CallTool(ctx, "screenshot", map[string]any{}); err == nil {
		t.Fatalf("expected error for missing url")
	}
}
