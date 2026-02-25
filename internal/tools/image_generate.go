package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ImageGenerateTool calls an image-generation API (OpenAI Images) and writes files.
type ImageGenerateTool struct{}

func NewImageGenerateTool() *ImageGenerateTool { return &ImageGenerateTool{} }

func (t *ImageGenerateTool) Name() string { return "image_generate" }

func (t *ImageGenerateTool) Description() string {
	return "Generate images from a prompt. Uses OPENAI_API_KEY and writes PNGs to ~/.openclio/output/imagegen/"
}

func (t *ImageGenerateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"prompt":{"type":"string"},
			"model":{"type":"string","description":"image model (default: gpt-image-1.5)"},
			"size":{"type":"string","description":"WxH (e.g. 1024x1024)","default":"1024x1024"},
			"n":{"type":"integer","default":1},
			"out_dir":{"type":"string","description":"output directory; defaults to ~/.openclio/output/imagegen"},
			"out_name":{"type":"string","description":"optional base name for output files (timestamp and index appended)"}
		},
		"required":["prompt"]
	}`)
}

type imageGenerateParams struct {
	Prompt  string `json:"prompt"`
	Model   string `json:"model"`
	Size    string `json:"size"`
	N       int    `json:"n"`
	OutDir  string `json:"out_dir"`
	OutName string `json:"out_name"`
}

type openAIImagesResponse struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	Error any `json:"error,omitempty"`
}

func (t *ImageGenerateTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p imageGenerateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	prompt := strings.TrimSpace(p.Prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if p.Model == "" {
		p.Model = "gpt-image-1.5"
	}
	if p.Size == "" {
		p.Size = "1024x1024"
	}
	if p.N <= 0 {
		p.N = 1
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set — create a key at https://platform.openai.com/api-keys and set it in your environment")
	}

	// Build request payload
	payload := map[string]any{
		"model":  p.Model,
		"prompt": prompt,
		"size":   p.Size,
		"n":      p.N,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/images/generations", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("image API error: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var apiResp openAIImagesResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("invalid api response: %w", err)
	}
	if len(apiResp.Data) == 0 {
		return "", fmt.Errorf("no image returned")
	}

	// Determine output dir
	outDir := strings.TrimSpace(p.OutDir)
	if outDir == "" {
		home, _ := os.UserHomeDir()
		outDir = filepath.Join(home, ".openclio", "output", "imagegen")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("creating output dir: %w", err)
	}

	base := strings.TrimSpace(p.OutName)
	if base == "" {
		base = "image"
	}
	ts := time.Now().UTC().Format("20060102-150405")

	var written []string
	for i, d := range apiResp.Data {
		b, err := base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil {
			return "", fmt.Errorf("decoding image %d: %w", i, err)
		}
		ext := ".png"
		filename := fmt.Sprintf("%s-%s-%d%s", base, ts, i+1, ext)
		path := filepath.Join(outDir, filename)
		if err := os.WriteFile(path, b, 0644); err != nil {
			return "", fmt.Errorf("writing file %s: %w", path, err)
		}
		written = append(written, path)
	}

	out, _ := json.Marshal(map[string]any{
		"files": written,
		"count": len(written),
		"dir":   outDir,
	})
	return string(out), nil
}

