package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SwitchModelTool lets users change the active LLM provider and model
// mid-conversation via an injected runtime ProviderSwitcher.
type SwitchModelTool struct {
	switcher  ProviderSwitcher
	providers map[string]struct{}
}

// NewSwitchModelTool creates a switch_model tool.
func NewSwitchModelTool(switcher ProviderSwitcher) *SwitchModelTool {
	return &SwitchModelTool{
		switcher: switcher,
		providers: map[string]struct{}{
			"anthropic": {},
			"openai":    {},
			"gemini":    {},
			"ollama":    {},
			"groq":      {},
			"deepseek":  {},
		},
	}
}

func (t *SwitchModelTool) Name() string { return "switch_model" }

func (t *SwitchModelTool) Description() string {
	return "Switch the active AI provider and model. Call this when the user asks to change the model, use a different AI, switch to GPT, Claude, Gemini, or a local model."
}

func (t *SwitchModelTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"provider": {
				"type": "string",
				"enum": ["anthropic", "openai", "gemini", "ollama", "groq", "deepseek"],
				"description": "Provider name"
			},
			"model": {
				"type": "string",
				"description": "Exact model name. Anthropic: claude-opus-4-5, claude-sonnet-4-6, claude-haiku-4-5-20251001, claude-3-5-sonnet-20241022, claude-3-5-haiku-20241022. OpenAI: gpt-4o, gpt-4o-mini, o3-mini. Gemini: gemini-2.0-flash, gemini-1.5-pro. Ollama: llama3.2, mistral. Groq: llama-3.3-70b-versatile. DeepSeek: deepseek-chat, deepseek-reasoner."
			}
		},
		"required": ["provider", "model"]
	}`)
}

type switchModelParams struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (t *SwitchModelTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	_ = ctx
	if t.switcher == nil {
		return "", fmt.Errorf("switch_model is unavailable")
	}

	var p switchModelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}

	provider := strings.ToLower(strings.TrimSpace(p.Provider))
	model := strings.TrimSpace(p.Model)
	if provider == "" {
		return "", fmt.Errorf("provider is required")
	}
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	if _, ok := t.providers[provider]; !ok {
		return "", fmt.Errorf("unsupported provider %q (supported: anthropic, openai, gemini, ollama, groq, deepseek)", provider)
	}

	if err := t.switcher.SwitchProvider(provider, model); err != nil {
		return "", fmt.Errorf("switching model failed: %w", err)
	}

	return fmt.Sprintf("Switched active model to %s/%s", provider, model), nil
}
