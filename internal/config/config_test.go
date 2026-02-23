package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Gateway.Port != 18789 {
		t.Errorf("expected default port 18789, got %d", cfg.Gateway.Port)
	}
	if cfg.Model.Provider != "anthropic" {
		t.Errorf("expected default provider anthropic, got %s", cfg.Model.Provider)
	}
	if cfg.Logging.Output != "~/.openclio/openclio.log" {
		t.Errorf("expected default logging output ~/.openclio/openclio.log, got %s", cfg.Logging.Output)
	}
	if cfg.Embeddings.Provider != "auto" {
		t.Errorf("expected default embeddings provider auto, got %s", cfg.Embeddings.Provider)
	}
	if cfg.Tools.Browser.Timeout == 0 {
		t.Errorf("expected default browser timeout to be set")
	}
	if cfg.Agent.Delegation.MaxParallelSubAgents != 5 {
		t.Errorf("expected default delegation parallelism 5, got %d", cfg.Agent.Delegation.MaxParallelSubAgents)
	}
	if cfg.Agent.Delegation.Timeout == 0 {
		t.Errorf("expected default delegation timeout to be set")
	}
}

func TestLoad_NoFile(t *testing.T) {
	// Should return defaults if file doesn't exist
	cfg, err := Load("does_not_exist.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if cfg.Gateway.Port != 18789 {
		t.Errorf("expected default port, got %d", cfg.Gateway.Port)
	}
}

func TestLoad_WithFile(t *testing.T) {
	content := []byte(`
gateway:
  port: 9090
model:
  provider: openai
`)
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.Gateway.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Gateway.Port)
	}
	if cfg.Model.Provider != "openai" {
		t.Errorf("expected provider openai, got %s", cfg.Model.Provider)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Model.Provider = "openai"
	cfg.Model.Model = "gpt-4o-mini"
	cfg.Model.APIKeyEnv = "OPENAI_API_KEY"
	cfg.DataDir = t.TempDir() // runtime-only; should not be serialized

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded.Model.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", loaded.Model.Provider)
	}
	if loaded.Model.Model != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini, got %q", loaded.Model.Model)
	}
	if loaded.Model.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("expected api_key_env OPENAI_API_KEY, got %q", loaded.Model.APIKeyEnv)
	}
}

func TestLoad_BrowserChromiumPathAlias(t *testing.T) {
	content := []byte(`
tools:
  browser:
    enabled: true
    chromium_path: /usr/bin/chromium
`)
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmpFile)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Tools.Browser.ChromePath != "/usr/bin/chromium" {
		t.Fatalf("expected chromium_path alias to populate chrome_path, got %q", cfg.Tools.Browser.ChromePath)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	os.Setenv("AGENT_PORT", "8080")
	os.Setenv("AGENT_MODEL_PROVIDER", "ollama")
	defer os.Unsetenv("AGENT_PORT")
	defer os.Unsetenv("AGENT_MODEL_PROVIDER")

	cfg := DefaultConfig()
	applyEnvOverrides(cfg)

	if cfg.Gateway.Port != 8080 {
		t.Errorf("expected port 8080 from env, got %d", cfg.Gateway.Port)
	}
	if cfg.Model.Provider != "ollama" {
		t.Errorf("expected provider ollama from env, got %s", cfg.Model.Provider)
	}
}

func TestValidate(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid, got %v", err)
	}

	cfg.Gateway.Port = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid port 0")
	}
	cfg.Gateway.Port = 18789

	cfg.Model.Provider = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty provider")
	}
	cfg.Model.Provider = "anthropic"

	cfg.Context.ProactiveCompaction = 1.5
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for proactive_compaction > 1")
	}
	cfg.Context.ProactiveCompaction = 0.5

	cfg.Tools.Exec.Sandbox = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid sandbox mode")
	}
	cfg.Tools.Exec.Sandbox = "none"

	cfg.Embeddings.Provider = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid embeddings provider")
	}
	cfg.Embeddings.Provider = "auto"

	cfg.Model.FallbackProviders = []string{"invalid-provider"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid fallback provider")
	}

	cfg.Model.FallbackProviders = nil
	cfg.Cron = []CronJob{{Name: "daily", Schedule: "* * * * *", Prompt: "hi", SessionMode: "broken"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid cron session mode")
	}
	cfg.Cron = nil

	cfg.Cron = []CronJob{{Name: "missing-trigger-and-schedule", Prompt: "hi"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when cron job has neither schedule nor trigger")
	}
	cfg.Cron = []CronJob{{Name: "trigger-only", Trigger: "every 6 hours", Prompt: "hi"}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("trigger-only cron job should be valid, got %v", err)
	}
	cfg.Cron = []CronJob{{Name: "both", Schedule: "* * * * *", Trigger: "every 1 hour", Prompt: "hi"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when cron job defines both schedule and trigger")
	}
	cfg.Cron = nil

	cfg.MCPServers = []MCPServerConfig{{Name: "", Command: "npx"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty MCP server name")
	}
	cfg.MCPServers = nil

	cfg.Agent.Delegation.Enabled = true
	cfg.Agent.Delegation.MaxParallelSubAgents = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for enabled delegation with zero max_parallel_sub_agents")
	}
	cfg.Agent.Delegation.MaxParallelSubAgents = 2
	cfg.Agent.Delegation.Timeout = -time.Second
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative delegation timeout")
	}
	cfg.Agent.Delegation.Timeout = time.Second
	cfg.Agent.Delegation.Enabled = false
}

func TestLoadDotEnv_PrefersProcessEnv(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	if err := os.WriteFile(envPath, []byte("ANTHROPIC_API_KEY=from-dotenv\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "from-process")
	if err := LoadDotEnv(envPath); err != nil {
		t.Fatalf("LoadDotEnv returned error: %v", err)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "from-process" {
		t.Fatalf("expected process env to win, got %q", got)
	}
}

func TestLoadDotEnv_LoadsMissingKey(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	envPath := filepath.Join(tmp, ".env")
	if err := os.WriteFile(envPath, []byte("ANTHROPIC_API_KEY=from-dotenv\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("model:\n  provider: anthropic\n"), 0600); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("ANTHROPIC_API_KEY")
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DataDir != tmp {
		t.Fatalf("expected DataDir=%s, got %s", tmp, cfg.DataDir)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "from-dotenv" {
		t.Fatalf("expected dotenv value, got %q", got)
	}
}

func TestLoad_MissingConfigUsesDefaultsWithDataDirAndEnvOverrides(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")

	t.Setenv("AGENT_MODEL_PROVIDER", "openai")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DataDir != tmp {
		t.Fatalf("expected DataDir=%s, got %s", tmp, cfg.DataDir)
	}
	if cfg.Model.Provider != "openai" {
		t.Fatalf("expected provider override openai, got %s", cfg.Model.Provider)
	}
}

func TestUpsertDotEnvKey_AtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	if err := os.WriteFile(envPath, []byte("# comment\nOPENAI_API_KEY=old\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := UpsertDotEnvKey(envPath, "OPENAI_API_KEY", "new value"); err != nil {
		t.Fatalf("UpsertDotEnvKey returned error: %v", err)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `OPENAI_API_KEY="new value"`) {
		t.Fatalf("expected updated key in dotenv file, got: %s", content)
	}
	if _, err := os.Stat(envPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected temp file to be absent after atomic rename, err=%v", err)
	}
}
