//go:build live

package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/openclio/openclio/internal/config"
)

func TestLiveAnthropicConnection(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping live test: ANTHROPIC_API_KEY is not set")
	}

	provider, err := NewProvider(config.ModelConfig{
		Provider:  "anthropic",
		Model:     "claude-3-haiku-20240307",
		APIKeyEnv: "ANTHROPIC_API_KEY",
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	req := ChatRequest{
		SystemPrompt: "You are a helpful assistant. Keep your answer to one word.",
		Messages: []Message{
			{Role: "user", Content: "What is 2+2?"},
		},
		MaxTokens: 10,
		Model:     "claude-3-haiku-20240307",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		t.Fatalf("live LLM call failed: %v", err)
	}

	if resp.Content == "" {
		t.Errorf("expected non-empty response content")
	}
	if resp.Usage.InputTokens == 0 || resp.Usage.OutputTokens == 0 {
		t.Errorf("expected token usage tracking, got Input: %d, Output: %d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
	// Log cache metrics — cache_creation_tokens > 0 on first call, cache_read_tokens > 0 on repeat calls
	t.Logf("cache_creation_tokens=%d cache_read_tokens=%d",
		resp.Usage.CacheCreationTokens, resp.Usage.CacheReadTokens)
}
