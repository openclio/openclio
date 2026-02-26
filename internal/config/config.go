// Package config handles loading and validating the agent configuration.
// Configuration is loaded from a YAML file with environment variable overlays.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ProviderPreset stores saved model and API settings per provider so switching providers restores each one.
type ProviderPreset struct {
	Model     string `yaml:"model,omitempty"`
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
	BaseURL   string `yaml:"base_url,omitempty"`
	Name      string `yaml:"name,omitempty"`
}

// Config is the top-level configuration for the agent.
type Config struct {
	Gateway         GatewayConfig         `yaml:"gateway"`
	Model           ModelConfig           `yaml:"model"`
	ProviderPresets map[string]ProviderPreset `yaml:"provider_presets,omitempty"` // per-provider saved model/api; key = provider name
	ModelRouter     ModelRouterConfig     `yaml:"model_router"`
	Embeddings  EmbeddingsConfig  `yaml:"embeddings"`
	Context     ContextConfig     `yaml:"context"`
	MCPServers  []MCPServerConfig `yaml:"mcp_servers,omitempty"`
	Channels    ChannelsConfig    `yaml:"channels"`
	Agent       AgentConfig       `yaml:"agent"`
	Tools       ToolsConfig       `yaml:"tools"`
	CLI         CLIConfig         `yaml:"cli"`
	Logging     LoggingConfig     `yaml:"logging"`
	Retention   RetentionConfig   `yaml:"retention"`
	Cron        []CronJob         `yaml:"cron"`

	// DataDir is runtime-only (not serialized) and points at ~/.openclio.
	DataDir string `yaml:"-"`
}

// ModelRouterConfig configures heuristic model routing.
type ModelRouterConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Strategy       string `yaml:"strategy"` // cost_optimized | quality_first | speed_first | privacy_first
	CheapModel     string `yaml:"cheap_model,omitempty"`
	MidModel       string `yaml:"mid_model,omitempty"`
	ExpensiveModel string `yaml:"expensive_model,omitempty"`
	PrivacyModel   string `yaml:"privacy_model,omitempty"`
}

// RetentionConfig controls automatic data pruning.
type RetentionConfig struct {
	// SessionsDays deletes sessions older than this many days.
	// Set to 0 (default) to keep sessions forever.
	SessionsDays int `yaml:"sessions_days"`
	// MessagesPerSession trims the oldest messages when a session exceeds this
	// count. Set to 0 (default) to disable trimming.
	MessagesPerSession int `yaml:"messages_per_session"`
}

// AgentConfig configures the core agent behavior.
type AgentConfig struct {
	MaxToolIterations   int                   `yaml:"max_tool_iterations"`
	MaxTokensPerSession int                   `yaml:"max_tokens_per_session"`
	MaxTokensPerDay     int                   `yaml:"max_tokens_per_day"`
	Delegation          AgentDelegationConfig `yaml:"delegation"`
}

// AgentDelegationConfig controls optional parallel sub-agent delegation.
type AgentDelegationConfig struct {
	Enabled              bool          `yaml:"enabled"`
	MaxParallelSubAgents int           `yaml:"max_parallel_sub_agents"`
	SubAgentModel        string        `yaml:"sub_agent_model"`
	SynthesisModel       string        `yaml:"synthesis_model"`
	Timeout              time.Duration `yaml:"timeout"`
}

// ToolsConfig configures the tool system.
type ToolsConfig struct {
	MaxOutputSize      int               `yaml:"max_output_size"`
	ScrubOutput        bool              `yaml:"scrub_output"`        // redact passwords/secrets from tool results (default: true)
	AllowSystemAccess  bool              `yaml:"allow_system_access"` // when true, file/exec can access user home (user must enable explicitly)
	Exec               ExecToolConfig    `yaml:"exec"`
	Browser            BrowserToolConfig `yaml:"browser"`
	WebSearch          *WebSearchConfig  `yaml:"web_search,omitempty"`
}

// CLIConfig configures the interactive terminal.
type CLIConfig struct {
	ScannerBuffer int `yaml:"scanner_buffer"`
}

// GatewayConfig configures the HTTP/WebSocket server.
type GatewayConfig struct {
	Port        int    `yaml:"port"`
	Bind        string `yaml:"bind"`
	TLSCertFile string `yaml:"tls_cert_file"` // PEM cert file; enables TLS when set together with tls_key_file
	TLSKeyFile  string `yaml:"tls_key_file"`  // PEM key file
	GRPCPort    int    `yaml:"grpc_port"`     // gRPC port for out-of-process adapters (0 = disabled)
}

// ModelConfig configures the LLM provider.
type ModelConfig struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	APIKeyEnv string `yaml:"api_key_env"`
	// BaseURL overrides the default API endpoint. Required for openai-compat;
	// optional for openai (overrides OPENAI_BASE_URL env var).
	BaseURL string `yaml:"base_url,omitempty"`
	// Name sets the display name reported by the provider (used by openai-compat).
	Name              string            `yaml:"name,omitempty"`
	FallbackProviders []string          `yaml:"fallback_providers,omitempty"`
	FallbackModels    map[string]string `yaml:"fallback_models,omitempty"`
	FallbackAPIKeyEnv map[string]string `yaml:"fallback_api_key_env,omitempty"`
}

// APIKey reads the actual API key from the environment variable.
// The key is never stored in the config file.
func (m *ModelConfig) APIKey() string {
	if m.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(m.APIKeyEnv)
}

// SaveCurrentToPreset writes the current Model (provider, model, api_key_env, base_url, name) into ProviderPresets[provider] so it can be restored when switching back.
func (c *Config) SaveCurrentToPreset() {
	if c == nil || strings.TrimSpace(c.Model.Provider) == "" {
		return
	}
	if c.ProviderPresets == nil {
		c.ProviderPresets = make(map[string]ProviderPreset)
	}
	c.ProviderPresets[c.Model.Provider] = ProviderPreset{
		Model:     strings.TrimSpace(c.Model.Model),
		APIKeyEnv: strings.TrimSpace(c.Model.APIKeyEnv),
		BaseURL:   strings.TrimSpace(c.Model.BaseURL),
		Name:      strings.TrimSpace(c.Model.Name),
	}
}

// GetPreset returns the saved preset for a provider, or a zero value if none.
func (c *Config) GetPreset(provider string) ProviderPreset {
	if c == nil || c.ProviderPresets == nil {
		return ProviderPreset{}
	}
	return c.ProviderPresets[provider]
}

// ContextConfig configures the context engine.
type ContextConfig struct {
	MaxTokensPerCall     int     `yaml:"max_tokens_per_call"`
	HistoryRetrievalK    int     `yaml:"history_retrieval_k"`
	ProactiveCompaction  float64 `yaml:"proactive_compaction"`
	CompactionKeepRecent int     `yaml:"compaction_keep_recent"`
	CompactionModel      string  `yaml:"compaction_model"`
	ToolResultSummary    bool    `yaml:"tool_result_summary"`
}

// EmbeddingsConfig configures semantic embedding generation.
type EmbeddingsConfig struct {
	Provider  string `yaml:"provider"` // auto | openai | ollama
	Model     string `yaml:"model"`
	APIKeyEnv string `yaml:"api_key_env"`
	BaseURL   string `yaml:"base_url"`
}

// ChannelsConfig configures messaging channel adapters.
type ChannelsConfig struct {
	AllowAll bool            `yaml:"allow_all"` // default true; false = only approved senders
	Telegram *TelegramConfig `yaml:"telegram,omitempty"`
	WhatsApp *WhatsAppConfig `yaml:"whatsapp,omitempty"`
	Discord  *DiscordConfig  `yaml:"discord,omitempty"`
	Slack    *SlackConfig    `yaml:"slack,omitempty"`
}

// SlackConfig configures the Slack adapter.
type SlackConfig struct {
	TokenEnv string `yaml:"token_env"` // env var holding the Slack bot token (xoxb-...)
}

// TelegramConfig configures the Telegram adapter.
type TelegramConfig struct {
	TokenEnv string `yaml:"token_env"`
}

// WhatsAppConfig configures the WhatsApp adapter.
type WhatsAppConfig struct {
	Enabled bool `yaml:"enabled"`
	// TokenEnv and WebhookURL are reserved for a future Cloud API mode.
	// Currently the adapter uses whatsmeow QR-code login (no API key required).
	// Setting these fields has no effect — they are ignored by the current implementation.
	TokenEnv   string `yaml:"token_env,omitempty"`
	WebhookURL string `yaml:"webhook_url,omitempty"`
	DataDir    string `yaml:"data_dir"` // directory for whatsmeow session SQLite (defaults to ~/.openclio)
}

// DiscordConfig configures the Discord adapter.
type DiscordConfig struct {
	TokenEnv string `yaml:"token_env"`
	AppIDEnv string `yaml:"app_id_env"` // optional, for slash commands
}

// ExecToolConfig configures shell command execution.
type ExecToolConfig struct {
	Sandbox             string        `yaml:"sandbox"`
	Timeout             time.Duration `yaml:"timeout"`
	DockerImage         string        `yaml:"docker_image"`
	NetworkAccess       bool          `yaml:"network_access"`
	RequireConfirmation bool          `yaml:"require_confirmation"`
}

// BrowserToolConfig configures browser automation.
type BrowserToolConfig struct {
	Enabled      bool          `yaml:"enabled"`
	ChromePath   string        `yaml:"chrome_path"`
	ChromiumPath string        `yaml:"chromium_path,omitempty"` // alias supported for compatibility
	Headless     bool          `yaml:"headless"`
	Timeout      time.Duration `yaml:"timeout"`
}

// MCPServerConfig configures one MCP stdio server.
type MCPServerConfig struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"` // values may be literal or env var names
}

// WebSearchConfig configures web search.
type WebSearchConfig struct {
	Provider  string `yaml:"provider"`
	APIKeyEnv string `yaml:"api_key_env"`
}

// LoggingConfig configures structured logging.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Output string `yaml:"output"` // "stderr", "stdout", or a file path
}

// CronJob defines a scheduled agent task.
type CronJob struct {
	Name        string `yaml:"name"`
	Schedule    string `yaml:"schedule"`          // standard cron expression (for time-based jobs)
	Trigger     string `yaml:"trigger,omitempty"` // event-driven watch trigger, e.g. "every 6 hours"
	Prompt      string `yaml:"prompt"`
	Channel     string `yaml:"channel,omitempty"`      // adapter to send result to
	SessionMode string `yaml:"session_mode,omitempty"` // isolated | shared (default: isolated)
	TimeoutSec  int    `yaml:"timeout_seconds,omitempty"`
}

// Load reads a YAML config file and returns a Config.
// Environment variables can override specific fields.
func Load(path string) (*Config, error) {
	// Load ~/.openclio/.env (or sibling .env) as a fallback source for API keys.
	// Existing process environment always takes precedence.
	_ = LoadDotEnv(filepath.Join(filepath.Dir(path), ".env"))

	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file — use defaults + env overrides.
			applyEnvOverrides(cfg)
			normalizeBrowserPath(cfg)
			if err := cfg.Validate(); err != nil {
				return nil, fmt.Errorf("invalid configuration: %w", err)
			}
			cfg.DataDir = filepath.Dir(path)
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// Apply environment variable overrides
	applyEnvOverrides(cfg)
	normalizeBrowserPath(cfg)

	// Validate configuration before returning
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Runtime-only field: the config data directory.
	cfg.DataDir = filepath.Dir(path)

	return cfg, nil
}

// Save writes the config to disk atomically.
// API keys are not persisted here (only env var names), matching Config design.
func Save(path string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory %s: %w", dir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("writing config temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename %s -> %s failed: %w", tmpPath, path, err)
	}
	return nil
}

// applyEnvOverrides lets environment variables override config values.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("AGENT_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Gateway.Port)
	}
	if v := os.Getenv("AGENT_BIND"); v != "" {
		cfg.Gateway.Bind = v
	}
	if v := os.Getenv("AGENT_MODEL_PROVIDER"); v != "" {
		cfg.Model.Provider = v
	}
	if v := os.Getenv("AGENT_MODEL"); v != "" {
		cfg.Model.Model = v
	}
	if v := os.Getenv("AGENT_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
}

func normalizeBrowserPath(cfg *Config) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(cfg.Tools.Browser.ChromePath) == "" && strings.TrimSpace(cfg.Tools.Browser.ChromiumPath) != "" {
		cfg.Tools.Browser.ChromePath = strings.TrimSpace(cfg.Tools.Browser.ChromiumPath)
	}
}

// Validate checks the configuration for obvious errors.
func (c *Config) Validate() error {
	if c.Gateway.Port < 1 || c.Gateway.Port > 65535 {
		return fmt.Errorf("gateway port must be between 1 and 65535, got %d", c.Gateway.Port)
	}
	if strings.TrimSpace(c.Model.Provider) == "" {
		// Allowed in setup mode; user picks provider during init or dashboard setup.
	}
	for _, p := range c.Model.FallbackProviders {
		switch p {
		case "anthropic", "openai", "gemini", "ollama", "cohere",
			"groq", "deepseek", "mistral", "xai", "cerebras",
			"together", "fireworks", "perplexity", "openrouter",
			"kimi", "sambanova", "lambda", "lmstudio", "openai-compat":
		default:
			return fmt.Errorf("model fallback provider %q is not a recognised provider", p)
		}
	}
	switch c.Embeddings.Provider {
	case "", "auto", "openai", "ollama":
	default:
		return fmt.Errorf("embeddings provider must be one of: auto, openai, ollama")
	}
	if c.Context.MaxTokensPerCall <= 0 {
		return fmt.Errorf("context max tokens per call must be positive")
	}
	if c.Context.HistoryRetrievalK <= 0 {
		return fmt.Errorf("context history retrieval K must be positive")
	}
	if c.Context.ProactiveCompaction < 0 || c.Context.ProactiveCompaction > 1 {
		return fmt.Errorf("context proactive_compaction must be between 0 and 1")
	}
	if c.Context.CompactionKeepRecent < 0 {
		return fmt.Errorf("context compaction_keep_recent cannot be negative")
	}
	for _, s := range c.MCPServers {
		if s.Name == "" {
			return fmt.Errorf("mcp_servers entry has empty name")
		}
		if s.Command == "" {
			return fmt.Errorf("mcp_servers[%s] has empty command", s.Name)
		}
	}
	if c.Agent.MaxToolIterations <= 0 {
		return fmt.Errorf("agent max tool iterations must be positive, got %d", c.Agent.MaxToolIterations)
	}
	if c.Agent.MaxTokensPerSession < 0 {
		return fmt.Errorf("agent max tokens per session cannot be negative")
	}
	if c.Agent.MaxTokensPerDay < 0 {
		return fmt.Errorf("agent max tokens per day cannot be negative")
	}
	if c.Agent.Delegation.MaxParallelSubAgents < 0 {
		return fmt.Errorf("agent delegation max_parallel_sub_agents cannot be negative")
	}
	if c.Agent.Delegation.Enabled && c.Agent.Delegation.MaxParallelSubAgents == 0 {
		return fmt.Errorf("agent delegation max_parallel_sub_agents must be positive when enabled")
	}
	if c.Agent.Delegation.Timeout < 0 {
		return fmt.Errorf("agent delegation timeout cannot be negative")
	}
	switch c.Tools.Exec.Sandbox {
	case "", "none", "namespace", "docker":
	default:
		return fmt.Errorf("tools.exec.sandbox must be one of: none, namespace, docker")
	}
	if c.Tools.Browser.Timeout < 0 {
		return fmt.Errorf("tools.browser.timeout cannot be negative")
	}
	for _, j := range c.Cron {
		if strings.TrimSpace(j.Schedule) == "" && strings.TrimSpace(j.Trigger) == "" {
			return fmt.Errorf("cron job %q must define either schedule or trigger", j.Name)
		}
		if strings.TrimSpace(j.Schedule) != "" && strings.TrimSpace(j.Trigger) != "" {
			return fmt.Errorf("cron job %q cannot define both schedule and trigger", j.Name)
		}
		switch j.SessionMode {
		case "", "isolated", "shared":
		default:
			return fmt.Errorf("cron job %q has invalid session_mode %q (valid: isolated, shared)", j.Name, j.SessionMode)
		}
		if j.TimeoutSec < 0 {
			return fmt.Errorf("cron job %q timeout_seconds cannot be negative", j.Name)
		}
	}
	switch c.ModelRouter.Strategy {
	case "", "cost_optimized", "quality_first", "speed_first", "privacy_first":
	default:
		return fmt.Errorf("model_router.strategy must be one of: cost_optimized, quality_first, speed_first, privacy_first")
	}
	return nil
}

// DefaultModelForProvider returns the default model ID for a given provider.
func DefaultModelForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-sonnet-4-20250514"
	case "openai":
		return "gpt-4o-mini"
	case "gemini":
		return "gemini-2.0-flash"
	case "ollama":
		return "llama3.1"
	case "cohere":
		return "command-r-plus-08-2024"
	case "groq":
		return "llama-3.3-70b-versatile"
	case "deepseek":
		return "deepseek-chat"
	case "mistral":
		return "mistral-large-latest"
	case "xai":
		return "grok-2-latest"
	case "cerebras":
		return "llama3.1-70b"
	case "together":
		return "meta-llama/Llama-3.3-70B-Instruct-Turbo"
	case "fireworks":
		return "accounts/fireworks/models/llama-v3p3-70b-instruct"
	case "perplexity":
		return "sonar-pro"
	case "openrouter":
		return "anthropic/claude-sonnet-4-6"
	case "kimi":
		return "moonshot-v1-8k"
	case "sambanova":
		return "Meta-Llama-3.1-70B-Instruct"
	case "lambda":
		return "llama3.1-70b-instruct-fp8"
	case "lmstudio":
		return ""
	default:
		return ""
	}
}

// DefaultAPIKeyEnvForProvider returns the default env var name for a provider's API key.
func DefaultAPIKeyEnvForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	case "cohere":
		return "COHERE_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "xai":
		return "XAI_API_KEY"
	case "cerebras":
		return "CEREBRAS_API_KEY"
	case "together":
		return "TOGETHER_API_KEY"
	case "fireworks":
		return "FIREWORKS_API_KEY"
	case "perplexity":
		return "PERPLEXITY_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "kimi":
		return "KIMI_API_KEY"
	case "sambanova":
		return "SAMBANOVA_API_KEY"
	case "lambda":
		return "LAMBDA_API_KEY"
	case "openai-compat":
		return "OPENAI_API_KEY"
	case "ollama", "lmstudio":
		return ""
	default:
		return ""
	}
}
