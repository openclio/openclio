package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMiscTools(t *testing.T) {
	ctx := context.Background()

	// extract_links from html
	html := `<a href="https://a">A</a><a href="/b">B</a>`
	out, err := CallTool(ctx, "extract_links", map[string]any{"html": html})
	if err != nil {
		t.Fatalf("extract_links failed: %v", err)
	}
	if out == nil {
		t.Fatalf("extract_links returned nil")
	}

	// download_file with content
	tmp := t.TempDir()
	p := filepath.Join(tmp, "f.txt")
	_, err = CallTool(ctx, "download_file", map[string]any{"path": p, "content": "x"})
	if err != nil {
		t.Fatalf("download_file failed: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected file created")
	}

	// notify
	home := t.TempDir()
	_ = os.Setenv("HOME", home)
	_, err = CallTool(ctx, "notify", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("notify failed: %v", err)
	}

	// audio_transcribe param validation
	if _, err := CallTool(ctx, "audio_transcribe", map[string]any{}); err == nil {
		t.Fatalf("audio_transcribe should error on missing path")
	}

	// agent_status reading config
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfgContent := "model:\n  provider: testprov\n  model: testmodel\ngateway:\n  port: 18789\n  bind: 127.0.0.1\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err = CallTool(ctx, "agent_status", map[string]any{"config_path": cfgPath})
	if err != nil {
		t.Fatalf("agent_status failed: %v", err)
	}

	// tools_list
	_, err = CallTool(ctx, "tools_list", map[string]any{})
	if err != nil {
		t.Fatalf("tools_list failed: %v", err)
	}

	// config_read
	_, err = CallTool(ctx, "config_read", map[string]any{"path": cfgPath})
	if err != nil {
		t.Fatalf("config_read failed: %v", err)
	}

	// loop_guard: repeated calls should block after threshold
	sig := "tloop"
	for i := 0; i < 4; i++ {
		res, err := CallTool(ctx, "loop_guard", map[string]any{"sig": sig, "register": true})
		if err != nil {
			t.Fatalf("loop_guard failed: %v", err)
		}
		m := res.(map[string]any)
		if i < 3 {
			if !m["allowed"].(bool) {
				t.Fatalf("expected allowed at iteration %d", i)
			}
		} else {
			if m["allowed"].(bool) {
				t.Fatalf("expected blocked at iteration %d", i)
			}
		}
	}
}
