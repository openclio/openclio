package tools

import (
	"encoding/json"
	"testing"
)

func TestImageAnalyzeToolPlaceholder(t *testing.T) {
	tool := NewImageAnalyzeTool()
	params := map[string]any{
		"image_url":        "https://example.com/image.jpg",
		"simulate_caption": "A test caption",
		"with_ocr":         true,
	}
	raw, _ := json.Marshal(params)
	out, err := tool.Execute(nil, raw)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
	if res["caption"] != "A test caption" {
		t.Fatalf("unexpected caption: %#v", res["caption"])
	}
	if _, ok := res["ocr_text"]; !ok {
		t.Fatalf("ocr_text missing")
	}
}
