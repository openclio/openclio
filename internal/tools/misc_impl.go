package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/openclio/openclio/internal/config"
)

func init() {
	_ = ReplaceTool("extract_links", extractLinksTool)
	_ = ReplaceTool("download_file", downloadFileTool)
	_ = ReplaceTool("notify", notifyTool)
	_ = ReplaceTool("audio_transcribe", audioTranscribeTool)
	_ = ReplaceTool("agent_status", agentStatusTool)
	_ = ReplaceTool("cost_report", costReportTool)
	_ = ReplaceTool("tools_list", toolsListTool)
	_ = ReplaceTool("config_read", configReadTool)
	_ = ReplaceTool("loop_guard", loopGuardTool)
}

// extract_links: accepts "html" or "path" (file path). Returns unique hrefs.
func extractLinksTool(ctx context.Context, payload map[string]any) (any, error) {
	html := ""
	if h, ok := payload["html"].(string); ok && h != "" {
		html = h
	} else if p, ok := payload["path"].(string); ok && p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		html = string(b)
	} else {
		return nil, fmt.Errorf("html or path is required")
	}
	re := regexp.MustCompile(`(?i)href=["']([^"']+)["']`)
	matches := re.FindAllStringSubmatch(html, -1)
	set := make(map[string]struct{})
	for _, m := range matches {
		if len(m) >= 2 {
			u := strings.TrimSpace(m[1])
			if u != "" {
				set[u] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out, nil
}

// download_file: supports payload with "content" and "path" to write, or "url" and "path" to curl.
func downloadFileTool(ctx context.Context, payload map[string]any) (any, error) {
	pathI, ok := payload["path"]
	if !ok {
		return nil, fmt.Errorf("path is required")
	}
	path := pathI.(string)
	if c, ok := payload["content"].(string); ok && c != "" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(c), 0644); err != nil {
			return nil, err
		}
		return map[string]any{"path": path}, nil
	}
	url, _ := payload["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("either content or url is required")
	}
	// try curl
	if p, err := exec.LookPath("curl"); err == nil && p != "" {
		cmd := exec.Command(p, "-sSL", "-o", path, url)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("curl failed: %w", err)
		}
		return map[string]any{"path": path}, nil
	}
	return nil, fmt.Errorf("curl not available; provide content or install curl")
}

// notify: write a simple notification entry to ~/.openclio/notifications.log
func notifyTool(ctx context.Context, payload map[string]any) (any, error) {
	msg, _ := payload["message"].(string)
	if strings.TrimSpace(msg) == "" {
		return nil, fmt.Errorf("message is required")
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".openclio")
	_ = os.MkdirAll(dir, 0700)
	fpath := filepath.Join(dir, "notifications.log")
	f, err := os.OpenFile(fpath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entry := fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339), msg)
	if _, err := f.WriteString(entry); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "file": fpath}, nil
}

// audio_transcribe: placeholder; accepts path and returns not implemented unless external tool available.
func audioTranscribeTool(ctx context.Context, payload map[string]any) (any, error) {
	path, _ := payload["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	// If whisper or deepspeech available could run; for now return not implemented.
	return nil, fmt.Errorf("audio_transcribe is not implemented in this build")
}

// agent_status: read config file and return basic info.
func agentStatusTool(ctx context.Context, payload map[string]any) (any, error) {
	cfgPath, _ := payload["config_path"].(string)
	if cfgPath == "" {
		home, _ := os.UserHomeDir()
		cfgPath = filepath.Join(home, ".openclio", "config.yaml")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return map[string]any{"setup_required": true}, nil
	}
	return map[string]any{
		"provider": cfg.Model.Provider,
		"model":    cfg.Model.Model,
	}, nil
}

// cost_report: lightweight stub returning zeros unless DB path provided and cost tracker used.
func costReportTool(ctx context.Context, payload map[string]any) (any, error) {
	return map[string]any{
		"llm_calls":          0,
		"input_tokens":       0,
		"output_tokens":      0,
		"estimated_cost_usd": 0.0,
	}, nil
}

// tools_list: return registered tool names.
func toolsListTool(ctx context.Context, payload map[string]any) (any, error) {
	return ListTools(), nil
}

// config_read: load config from path and return sanitized fields.
func configReadTool(ctx context.Context, payload map[string]any) (any, error) {
	cfgPath, _ := payload["path"].(string)
	if cfgPath == "" {
		home, _ := os.UserHomeDir()
		cfgPath = filepath.Join(home, ".openclio", "config.yaml")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"model": map[string]any{
			"provider":    cfg.Model.Provider,
			"model":       cfg.Model.Model,
			"api_key_env": cfg.Model.APIKeyEnv,
		},
		"gateway": map[string]any{
			"port": cfg.Gateway.Port,
			"bind": cfg.Gateway.Bind,
		},
	}
	return out, nil
}

// loop_guard: simple in-memory repetition detector.
var loopMu sync.Mutex
var loopCalls = make(map[string][]time.Time)

func loopGuardTool(ctx context.Context, payload map[string]any) (any, error) {
	sig, _ := payload["sig"].(string)
	if sig == "" {
		return nil, fmt.Errorf("sig is required")
	}
	register := true
	if r, ok := payload["register"].(bool); ok {
		register = r
	}
	now := time.Now()
	window := 30 * time.Second
	threshold := 3

	loopMu.Lock()
	defer loopMu.Unlock()
	history := loopCalls[sig]
	// prune old
	newHist := make([]time.Time, 0, len(history))
	for _, t := range history {
		if now.Sub(t) <= window {
			newHist = append(newHist, t)
		}
	}
	if register {
		newHist = append(newHist, now)
	}
	loopCalls[sig] = newHist
	allowed := len(newHist) <= threshold
	return map[string]any{"allowed": allowed, "count": len(newHist)}, nil
}
