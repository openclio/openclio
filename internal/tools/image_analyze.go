package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// ImageAnalyzeTool provides a pluggable vision tool. The default implementation
// is lightweight and returns placeholder results. A real backend can be wired
// later (OpenAI/Google/Local).
type ImageAnalyzeTool struct{}

func NewImageAnalyzeTool() *ImageAnalyzeTool { return &ImageAnalyzeTool{} }

func (t *ImageAnalyzeTool) Name() string { return "image_analyze" }

func (t *ImageAnalyzeTool) Description() string {
	return "Analyze an image for caption, OCR text, and basic metadata. Supports image_url or image_path. (Default: placeholder)"
}

func (t *ImageAnalyzeTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"image_url":{"type":"string"},
			"image_path":{"type":"string"},
			"simulate_caption":{"type":"string","description":"(test-only) return this caption"},
			"with_ocr":{"type":"boolean"}
		}
	}`)
}

type imageAnalyzeParams struct {
	ImageURL        string `json:"image_url,omitempty"`
	ImagePath       string `json:"image_path,omitempty"`
	SimulateCaption string `json:"simulate_caption,omitempty"`
	WithOCR         bool   `json:"with_ocr,omitempty"`
}

type imageAnalyzeResult struct {
	Caption string                 `json:"caption,omitempty"`
	OCRText string                 `json:"ocr_text,omitempty"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
}

func (t *ImageAnalyzeTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p imageAnalyzeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	if p.ImageURL == "" && p.ImagePath == "" {
		return "", fmt.Errorf("image_url or image_path is required")
	}

	// Placeholder behaviour:
	res := imageAnalyzeResult{
		Meta: map[string]interface{}{
			"source": func() string {
				if p.ImageURL != "" {
					return "url"
				}
				return "path"
			}(),
		},
	}

	if p.SimulateCaption != "" {
		res.Caption = p.SimulateCaption
	} else {
		res.Caption = "No caption available (vision backend not configured)"
	}

	if p.WithOCR {
		// OCR not implemented in default; return explanatory text
		res.OCRText = "[ocr not available: backend not configured]"
	}

	out, _ := json.Marshal(res)
	return string(out), nil
}
