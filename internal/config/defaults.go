package config

import "time"

// DefaultConfig returns a Config with sensible defaults for all settings.
func DefaultConfig() *Config {
	return &Config{
		Gateway: GatewayConfig{
			Port: 18789,
			Bind: "127.0.0.1", // loopback only — safe default
		},
		Model: ModelConfig{
			// Intentionally empty by default.
			// Users choose provider/model during `openclio init` or setup UI.
			Provider:  "",
			Model:     "",
			APIKeyEnv: "",
		},
		ModelRouter: ModelRouterConfig{
			Enabled:  false,
			Strategy: "cost_optimized",
		},
		Embeddings: EmbeddingsConfig{
			Provider:  "auto",
			Model:     "nomic-embed-text",
			APIKeyEnv: "OPENAI_API_KEY",
			BaseURL:   "http://127.0.0.1:11434",
		},
		Context: ContextConfig{
			MaxTokensPerCall:     8000,
			HistoryRetrievalK:    10,
			ProactiveCompaction:  0.5,
			CompactionKeepRecent: 5,
			ToolResultSummary:    true,
		},
		Agent: AgentConfig{
			MaxToolIterations: 10,
			Delegation: AgentDelegationConfig{
				Enabled:              false,
				MaxParallelSubAgents: 5,
				Timeout:              90 * time.Second,
			},
		},
		Tools: ToolsConfig{
			MaxOutputSize: 100 * 1024, // 100KB
			ScrubOutput:   true,       // redact passwords/secrets from tool output by default
			Exec: ExecToolConfig{
				Sandbox:             "none",
				Timeout:             30 * time.Second,
				DockerImage:         "alpine:latest",
				NetworkAccess:       false,
				RequireConfirmation: false,
			},
			Browser: BrowserToolConfig{
				Enabled:  true,
				Headless: true,
				Timeout:  30 * time.Second,
			},
		},
		CLI: CLIConfig{
			ScannerBuffer: 64 * 1024, // 64KB
		},
		Logging: LoggingConfig{
			Level:  "info",
			Output: "~/.openclio/openclio.log",
		},
		Retention: RetentionConfig{
			SessionsDays:       0, // keep forever by default
			MessagesPerSession: 0, // no trim by default
		},
	}
}
