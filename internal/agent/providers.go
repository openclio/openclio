package agent

import (
	"fmt"

	"github.com/openclio/openclio/internal/config"
)

// NewProvider creates an LLM provider based on the config.
//
// Supported provider values:
//
//	anthropic     — Anthropic Claude (claude-sonnet-4-6, etc.)
//	openai        — OpenAI GPT (gpt-4o, o3, etc.)
//	gemini        — Google Gemini (gemini-2.0-flash, etc.)
//	ollama        — Local Ollama server (any model)
//	cohere        — Cohere Command R / R+
//
//	-- OpenAI-compatible named shortcuts --
//	groq          — Groq (llama-3.3-70b-versatile, mixtral-8x7b, etc.)
//	deepseek      — DeepSeek (deepseek-chat, deepseek-reasoner)
//	mistral       — Mistral AI (mistral-large-latest, codestral-latest)
//	xai           — xAI Grok (grok-2-latest, grok-3-latest)
//	cerebras      — Cerebras (llama3.1-70b — very fast)
//	together      — Together AI (100+ open-source models)
//	fireworks     — Fireworks AI (fast open-source inference)
//	perplexity    — Perplexity (sonar-pro — search-augmented)
//	openrouter    — OpenRouter (200+ models via one key)
//	kimi          — Moonshot AI Kimi (moonshot-v1-8k, moonshot-v1-32k)
//	sambanova     — SambaNova (fast Llama inference)
//	lambda        — Lambda Labs GPU cloud
//	lmstudio      — LM Studio local server (no key required)
//
//	openai-compat — Generic OpenAI-compatible endpoint; requires base_url in config.
func NewProvider(cfg config.ModelConfig) (Provider, error) {
	switch cfg.Provider {
	// ── First-class providers with custom implementations ────────────────────
	case "anthropic":
		return NewAnthropicProvider(cfg.APIKeyEnv, cfg.Model)
	case "openai":
		return NewOpenAIProvider(cfg.APIKeyEnv, cfg.Model)
	case "gemini":
		return NewGeminiProvider(cfg.APIKeyEnv, cfg.Model)
	case "ollama":
		return NewOllamaProvider(cfg.Model), nil
	case "cohere":
		return NewCohereProvider(cfg.APIKeyEnv, cfg.Model)

	// ── OpenAI-compatible named shortcuts ────────────────────────────────────
	case "groq", "deepseek", "mistral", "xai", "cerebras",
		"together", "fireworks", "perplexity", "openrouter",
		"kimi", "sambanova", "lambda", "lmstudio":
		return newNamedCompatProvider(cfg.Provider, cfg.APIKeyEnv, cfg.Model)

	// ── Generic OpenAI-compatible endpoint ───────────────────────────────────
	case "openai-compat":
		return newOpenAICompatProvider(cfg.Name, cfg.BaseURL, cfg.APIKeyEnv, cfg.Model)

	default:
		return nil, fmt.Errorf(
			"unknown provider: %q\n\nSupported providers: anthropic, openai, gemini, ollama, cohere, "+
				"groq, deepseek, mistral, xai, cerebras, together, fireworks, perplexity, "+
				"openrouter, kimi, sambanova, lambda, lmstudio, openai-compat",
			cfg.Provider,
		)
	}
}
