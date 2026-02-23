package agent

import (
	"fmt"
	"os"
)

// compatEndpoint describes a known OpenAI-compatible service.
type compatEndpoint struct {
	baseURL       string // base URL without trailing slash
	defaultModel  string // used when the user omits model: in config
	defaultKeyEnv string // env var name to read the API key from; empty = no key required
}

// knownCompatEndpoints maps provider config names to their endpoint metadata.
// All of these speak the OpenAI Chat Completions API format.
var knownCompatEndpoints = map[string]compatEndpoint{
	"groq": {
		baseURL:       "https://api.groq.com/openai/v1",
		defaultModel:  "llama-3.3-70b-versatile",
		defaultKeyEnv: "GROQ_API_KEY",
	},
	"deepseek": {
		baseURL:       "https://api.deepseek.com/v1",
		defaultModel:  "deepseek-chat",
		defaultKeyEnv: "DEEPSEEK_API_KEY",
	},
	"mistral": {
		baseURL:       "https://api.mistral.ai/v1",
		defaultModel:  "mistral-large-latest",
		defaultKeyEnv: "MISTRAL_API_KEY",
	},
	"xai": {
		baseURL:       "https://api.x.ai/v1",
		defaultModel:  "grok-2-latest",
		defaultKeyEnv: "XAI_API_KEY",
	},
	"cerebras": {
		baseURL:       "https://api.cerebras.ai/v1",
		defaultModel:  "llama3.1-70b",
		defaultKeyEnv: "CEREBRAS_API_KEY",
	},
	"together": {
		baseURL:       "https://api.together.xyz/v1",
		defaultModel:  "meta-llama/Llama-3.3-70B-Instruct-Turbo",
		defaultKeyEnv: "TOGETHER_API_KEY",
	},
	"fireworks": {
		baseURL:       "https://api.fireworks.ai/inference/v1",
		defaultModel:  "accounts/fireworks/models/llama-v3p3-70b-instruct",
		defaultKeyEnv: "FIREWORKS_API_KEY",
	},
	"perplexity": {
		baseURL:       "https://api.perplexity.ai",
		defaultModel:  "sonar-pro",
		defaultKeyEnv: "PERPLEXITY_API_KEY",
	},
	"openrouter": {
		baseURL:       "https://openrouter.ai/api/v1",
		defaultModel:  "anthropic/claude-sonnet-4-6",
		defaultKeyEnv: "OPENROUTER_API_KEY",
	},
	"kimi": {
		baseURL:       "https://api.moonshot.cn/v1",
		defaultModel:  "moonshot-v1-8k",
		defaultKeyEnv: "MOONSHOT_API_KEY",
	},
	"sambanova": {
		baseURL:       "https://api.sambanova.ai/v1",
		defaultModel:  "Meta-Llama-3.1-70B-Instruct",
		defaultKeyEnv: "SAMBANOVA_API_KEY",
	},
	"lambda": {
		baseURL:       "https://api.lambdalabs.com/v1",
		defaultModel:  "llama3.1-70b-instruct-fp8",
		defaultKeyEnv: "LAMBDA_API_KEY",
	},
	// lmstudio runs locally; no API key is required.
	"lmstudio": {
		baseURL:       "http://localhost:1234",
		defaultModel:  "",
		defaultKeyEnv: "",
	},
}

// openAICompatProvider wraps OpenAIProvider with a custom name.
// It reuses all Chat and Stream logic from OpenAIProvider unchanged.
type openAICompatProvider struct {
	*OpenAIProvider
	providerName string
}

// Name returns the service-specific name (e.g. "groq", "deepseek", "kimi").
func (p *openAICompatProvider) Name() string { return p.providerName }

// newNamedCompatProvider creates a provider for one of the entries in
// knownCompatEndpoints.  If apiKeyEnv is empty the table's default is used.
func newNamedCompatProvider(providerName, apiKeyEnv, model string) (*openAICompatProvider, error) {
	ep, ok := knownCompatEndpoints[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	keyEnv := apiKeyEnv
	if keyEnv == "" {
		keyEnv = ep.defaultKeyEnv
	}

	key := ""
	if keyEnv != "" {
		key = os.Getenv(keyEnv)
		if key == "" {
			return nil, fmt.Errorf("environment variable %s is not set (set api_key_env in config to override)", keyEnv)
		}
	}

	if model == "" {
		model = ep.defaultModel
	}

	return &openAICompatProvider{
		OpenAIProvider: &OpenAIProvider{
			apiKey:  key,
			model:   model,
			baseURL: ep.baseURL,
		},
		providerName: providerName,
	}, nil
}

// newOpenAICompatProvider creates a fully-custom OpenAI-compatible provider
// from explicit config values (used for provider: openai-compat).
// baseURL and model must be provided; apiKeyEnv is optional for local endpoints.
func newOpenAICompatProvider(name, baseURL, apiKeyEnv, model string) (*openAICompatProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("openai-compat provider requires base_url in config")
	}

	key := ""
	if apiKeyEnv != "" {
		key = os.Getenv(apiKeyEnv)
		if key == "" {
			return nil, fmt.Errorf("environment variable %s is not set", apiKeyEnv)
		}
	}

	if name == "" {
		name = "openai-compat"
	}

	return &openAICompatProvider{
		OpenAIProvider: &OpenAIProvider{
			apiKey:  key,
			model:   model,
			baseURL: baseURL,
		},
		providerName: name,
	}, nil
}
