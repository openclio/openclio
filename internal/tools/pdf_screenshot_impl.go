package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func init() {
	_ = ReplaceTool("pdf_read", pdfReadTool)
	_ = ReplaceTool("screenshot", screenshotTool)
}

func pdfReadTool(ctx context.Context, payload map[string]any) (any, error) {
	pathI, ok := payload["path"]
	if !ok {
		return nil, fmt.Errorf("path is required")
	}
	path, _ := pathI.(string)
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("path is required")
	}
	abs, _ := filepath.Abs(path)
	// Use pdftotext if available
	if p, err := exec.LookPath("pdftotext"); err == nil && p != "" {
		cmd := exec.Command(p, "-layout", "-q", abs, "-")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("pdftotext failed: %w", err)
		}
		return map[string]any{"text": out.String()}, nil
	}
	return nil, fmt.Errorf("pdftotext not found; install poppler-utils (pdftotext) to enable pdf_read")
}

func screenshotTool(ctx context.Context, payload map[string]any) (any, error) {
	// Try wkhtmltoimage if available and url provided
	urlI, _ := payload["url"]
	url, _ := urlI.(string)
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("url is required")
	}
	if p, err := exec.LookPath("wkhtmltoimage"); err == nil && p != "" {
		cmd := exec.Command(p, "--quality", "90", url, "-")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("wkhtmltoimage failed: %w", err)
		}
		return map[string]any{"image_bytes": out.Bytes()}, nil
	}
	return nil, fmt.Errorf("no supported screenshotter found (wkhtmltoimage)")
}
