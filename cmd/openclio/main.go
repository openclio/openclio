// openclio — A local-first personal AI agent built in Go.
//
// Usage:
//
//	openclio                    Start interactive chat (default)
//	openclio init               Interactive first-time setup wizard
//	openclio chat               Start interactive chat
//	openclio serve              Start HTTP/WebSocket server + channel adapters
//	openclio cost               Show token usage and cost summary
//	openclio privacy            Show privacy settings and aggregate usage summary
//	openclio memory list        Show known knowledge graph entities
//	openclio memory search ...  Search knowledge graph entities
//	openclio memory edit        Edit knowledge graph entities in $EDITOR
//	openclio history            Show recent tool actions (write_file/exec)
//	openclio undo <id>          Undo one write_file action by history ID
//	openclio cron list          List scheduled cron jobs
//	openclio cron run <name>    Trigger a cron job immediately
//	openclio cron history       Show recent cron job runs
//	openclio status             Show agent status and config
//	openclio auth rotate        Rotate the auth token
//	openclio wipe               Delete all data (with confirmation)
//	openclio export             Export all data to JSON
//	openclio migrate openclaw <path>  Import OpenClaw history/identity files
//	openclio version            Print version
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/cli"
	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/cost"
	agentcron "github.com/openclio/openclio/internal/cron"
	"github.com/openclio/openclio/internal/gateway"
	internlog "github.com/openclio/openclio/internal/logger"
	"github.com/openclio/openclio/internal/mcp"
	"github.com/openclio/openclio/internal/plugin"
	discordadapter "github.com/openclio/openclio/internal/plugin/adapters/discord"
	slackadapter "github.com/openclio/openclio/internal/plugin/adapters/slack"
	telegramadapter "github.com/openclio/openclio/internal/plugin/adapters/telegram"
	webchatadapter "github.com/openclio/openclio/internal/plugin/adapters/webchat"
	whatsappadapter "github.com/openclio/openclio/internal/plugin/adapters/whatsapp"
	privacyreport "github.com/openclio/openclio/internal/privacy"
	"github.com/openclio/openclio/internal/rpc"
	"github.com/openclio/openclio/internal/storage"
	"github.com/openclio/openclio/internal/tools"
	"github.com/openclio/openclio/internal/workspace"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

type runtimeProviderSwitcher struct {
	mu      sync.RWMutex
	handler func(providerName, modelName string) error
}

func (r *runtimeProviderSwitcher) SetHandler(handler func(providerName, modelName string) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handler = handler
}

func (r *runtimeProviderSwitcher) SwitchProvider(providerName, modelName string) error {
	r.mu.RLock()
	handler := r.handler
	r.mu.RUnlock()
	if handler == nil {
		return fmt.Errorf("provider switch is unavailable in this mode")
	}
	return handler(providerName, modelName)
}

type runtimeChannelConnector struct {
	mu                sync.RWMutex
	handler           func(channelType string, credentials map[string]string) error
	disconnectHandler func(channelType string) error
}

func (r *runtimeChannelConnector) SetHandler(handler func(channelType string, credentials map[string]string) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handler = handler
}

func (r *runtimeChannelConnector) SetDisconnectHandler(handler func(channelType string) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disconnectHandler = handler
}

func (r *runtimeChannelConnector) ConnectChannel(channelType string, credentials map[string]string) error {
	r.mu.RLock()
	handler := r.handler
	r.mu.RUnlock()
	if handler == nil {
		return fmt.Errorf("channel connection is unavailable in this mode")
	}
	return handler(channelType, credentials)
}

func (r *runtimeChannelConnector) DisconnectChannel(channelType string) error {
	r.mu.RLock()
	handler := r.disconnectHandler
	r.mu.RUnlock()
	if handler == nil {
		return fmt.Errorf("channel disconnect is unavailable in this mode")
	}
	return handler(channelType)
}

type runtimeChannelStatusReader struct {
	mu          sync.RWMutex
	getHandler  func(channelType string) (tools.ChannelStatus, error)
	listHandler func() ([]tools.ChannelStatus, error)
}

func (r *runtimeChannelStatusReader) SetHandlers(
	getHandler func(channelType string) (tools.ChannelStatus, error),
	listHandler func() ([]tools.ChannelStatus, error),
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getHandler = getHandler
	r.listHandler = listHandler
}

func (r *runtimeChannelStatusReader) ChannelStatus(channelType string) (tools.ChannelStatus, error) {
	r.mu.RLock()
	handler := r.getHandler
	r.mu.RUnlock()
	if handler == nil {
		return tools.ChannelStatus{}, fmt.Errorf("channel status is unavailable in this mode")
	}
	return handler(channelType)
}

func (r *runtimeChannelStatusReader) ListChannelStatuses() ([]tools.ChannelStatus, error) {
	r.mu.RLock()
	handler := r.listHandler
	r.mu.RUnlock()
	if handler == nil {
		return nil, fmt.Errorf("channel status is unavailable in this mode")
	}
	return handler()
}

type runtimeDelegationExecutor struct {
	mu      sync.RWMutex
	handler func(ctx context.Context, objective string, tasks []string) (string, error)
}

func (r *runtimeDelegationExecutor) SetHandler(handler func(ctx context.Context, objective string, tasks []string) (string, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handler = handler
}

func (r *runtimeDelegationExecutor) Delegate(ctx context.Context, objective string, tasks []string) (string, error) {
	r.mu.RLock()
	handler := r.handler
	r.mu.RUnlock()
	if handler == nil {
		return "", fmt.Errorf("delegation is unavailable in this mode")
	}
	return handler(ctx, objective, tasks)
}

func main() {
	var verbose, dryRun bool
	var filteredArgs []string
	for _, arg := range os.Args[1:] {
		if arg == "-v" || arg == "--verbose" {
			verbose = true
		} else if arg == "--dry-run" {
			dryRun = true
		} else {
			filteredArgs = append(filteredArgs, arg)
		}
	}

	args := filteredArgs
	subcmd := "chat"
	if len(args) > 0 {
		subcmd = args[0]
	}

	if subcmd == "version" {
		fmt.Printf("agent %s (built %s)\n", version, buildTime)
		os.Exit(0)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	dataDir := filepath.Join(homeDir, ".openclio")

	// Keep a baseline set of built-in skills available for every install.
	// Existing user-managed skill files are preserved.
	if err := workspace.SeedDefaultSkills(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to seed default skills: %v\n", err)
	}

	// 'init' must run before config load (config may not exist yet)
	if subcmd == "init" {
		runInit(dataDir)
		return
	}

	cfg, err := config.Load(filepath.Join(dataDir, "config.yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if verbose {
		cfg.Logging.Level = "debug"
	}

	log := internlog.New(cfg.Logging.Level, cfg.Logging.Output)
	internlog.Global = log

	dbPath := filepath.Join(dataDir, "data.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: migration failed: %v\n", err)
		os.Exit(1)
	}

	// Apply startup data retention policy.
	retentionResult, err := db.EnforceRetention(cfg.Retention.SessionsDays, cfg.Retention.MessagesPerSession)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: retention cleanup failed: %v\n", err)
		os.Exit(1)
	}
	if cfg.Retention.SessionsDays > 0 || cfg.Retention.MessagesPerSession > 0 {
		log.Info("startup retention cleanup complete",
			"sessions_deleted", retentionResult.DeletedSessions,
			"messages_deleted", retentionResult.DeletedMessages,
			"sessions_days", cfg.Retention.SessionsDays,
			"messages_per_session", cfg.Retention.MessagesPerSession,
		)
	}

	// Reclaim free pages at startup when incremental auto-vacuum is enabled.
	if err := db.IncrementalVacuum(); err != nil {
		log.Warn("initial incremental vacuum failed", "error", err)
	}
	privacyStore := storage.NewPrivacyStore(db)
	actionLogStore := storage.NewActionLogStore(db)
	embeddingErrorStore := storage.NewEmbeddingErrorStore(db)
	knowledgeGraphStore := storage.NewKnowledgeGraphStore(db)

	// Quick commands that only need DB
	switch subcmd {
	case "cost":
		runCost(db)
		return
	case "privacy":
		runPrivacy(db, cfg, privacyStore)
		return
	case "history":
		runHistory(db, args[1:])
		return
	case "memory":
		runMemory(db, args[1:])
		return
	case "undo":
		runUndo(db, args[1:])
		return
	case "cron":
		if len(args) < 2 || args[1] != "run" {
			runCronCmd(args[1:], cfg, db, log, nil, nil, nil, nil, nil)
			return
		}
	case "allow":
		runAllowCmd(args[1:], dataDir, cfg, true)
		return
	case "deny":
		runAllowCmd(args[1:], dataDir, cfg, false)
		return
	case "allowlist":
		runAllowList(dataDir, cfg)
		return
	case "wipe":
		runWipe(dataDir, dbPath)
		return
	case "export":
		runExport(db, dataDir)
		return
	case "status":
		runStatus(cfg, dataDir)
		return
	case "skills":
		if len(args) < 2 || args[1] != "list" {
			fmt.Fprintln(os.Stderr, "usage: agent skills list")
			os.Exit(1)
		}
		runSkillsList(dataDir)
		return
	case "auth":
		if len(args) < 2 || args[1] != "rotate" {
			fmt.Fprintln(os.Stderr, "usage: agent auth rotate")
			os.Exit(1)
		}
		runAuthRotate(dataDir)
		return
	case "migrate":
		runMigrateCmd(args[1:], dataDir, db)
		return
	}

	// Full agent initialization
	embedder := selectEmbedder(cfg, log)
	contextEngine := agentctx.NewEngine(embedder, cfg.Context.MaxTokensPerCall, cfg.Context.HistoryRetrievalK)
	contextEngine.SetEmbeddingErrorReporter(embeddingErrorStore, "context_assemble")

	ws, err := workspace.Load(dataDir)
	if err != nil {
		// Workspace is optional — continue with empty personalization.
		// NEVER fall back to os.TempDir() as it is world-readable (#5).
		log.Warn("workspace unavailable, continuing without personalization", "error", err)
		ws = workspace.Empty()
	}

	setupMode := false
	var provider agent.Provider
	if strings.TrimSpace(cfg.Model.Provider) == "" {
		setupMode = true
		log.Warn("no model provider configured, starting in setup mode", "hint", "run `openclio init` and choose a provider")
	} else {
		provider, err = buildProviderStack(cfg, log)
		if err != nil {
			if isMissingAPIKeyError(err) {
				setupMode = true
				log.Warn("no provider API key found, starting in setup mode", "provider", cfg.Model.Provider, "api_key_env", cfg.Model.APIKeyEnv)
				provider = nil
			} else {
				if strings.TrimSpace(cfg.Model.APIKeyEnv) != "" {
					fmt.Fprintf(os.Stderr, "error: %v\nhint: set the %s environment variable\n", err, cfg.Model.APIKeyEnv)
				} else {
					fmt.Fprintf(os.Stderr, "error: %v\nhint: run `openclio init` and choose a provider/model\n", err)
				}
				os.Exit(1)
			}
		}
	}

	workDir, _ := os.Getwd()
	providerSwitcher := &runtimeProviderSwitcher{}
	channelConnector := &runtimeChannelConnector{}
	channelStatusReader := &runtimeChannelStatusReader{}
	delegationExecutor := &runtimeDelegationExecutor{}
	var delegationStore tools.DelegationExecutor
	if cfg.Agent.Delegation.Enabled {
		delegationStore = delegationExecutor
	}
	toolRegistry := tools.NewRegistry(cfg.Tools, workDir, dataDir, tools.Stores{
		Privacy:          privacyStore,
		ActionLog:        actionLogStore,
		ProviderSwitcher: providerSwitcher,
		ChannelConnector: channelConnector,
		ChannelLifecycle: channelConnector,
		ChannelStatus:    channelStatusReader,
		Delegation:       delegationStore,
	})
	mcpServers, err := registerMCPTools(toolRegistry, cfg.MCPServers, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, srv := range mcpServers {
			_ = srv.Stop(stopCtx)
		}
	}()
	costTracker := cost.NewTracker(db)
	agentInstance := agent.NewAgentWithWorkspace(
		provider,
		contextEngine,
		toolRegistry,
		cfg.Agent,
		cfg.Model.Model,
		ws,
		costTracker,
	)
	agentInstance.ConfigureContext(cfg.Context)
	agentInstance.SetGitContext(tools.GetGitContext(workDir))
	var providerSwitchMu sync.Mutex
	providerSwitcher.SetHandler(func(providerName, modelName string) error {
		providerSwitchMu.Lock()
		defer providerSwitchMu.Unlock()

		providerName = strings.ToLower(strings.TrimSpace(providerName))
		modelName = strings.TrimSpace(modelName)
		if providerName == "" {
			return fmt.Errorf("provider is required")
		}
		if modelName == "" {
			return fmt.Errorf("model is required")
		}
		switch providerName {
		case "anthropic", "openai", "gemini", "ollama", "groq", "deepseek":
		default:
			return fmt.Errorf("unsupported provider %q", providerName)
		}

		prevProvider := cfg.Model.Provider
		prevModel := cfg.Model.Model
		prevAPIKeyEnv := cfg.Model.APIKeyEnv

		cfg.Model.Provider = providerName
		cfg.Model.Model = modelName
		cfg.Model.APIKeyEnv = defaultAPIKeyEnvForProvider(providerName)
		if cfg.Model.APIKeyEnv == "" {
			cfg.Model.APIKeyEnv = prevAPIKeyEnv
		}

		switchedProvider, err := buildProviderStack(cfg, log)
		if err != nil {
			cfg.Model.Provider = prevProvider
			cfg.Model.Model = prevModel
			cfg.Model.APIKeyEnv = prevAPIKeyEnv
			return err
		}
		agentInstance.SetProvider(switchedProvider, modelName)
		log.Info("runtime model switch complete", "provider", providerName, "model", modelName)
		return nil
	})
	if cfg.Agent.Delegation.Enabled {
		delegationExecutor.SetHandler(func(ctx context.Context, objective string, tasks []string) (string, error) {
			return agentInstance.Delegate(ctx, objective, tasks, cfg.Agent.Delegation)
		})
	}

	log.Info("agent ready", "version", version, "provider", cfg.Model.Provider, "model", cfg.Model.Model, "setup_mode", setupMode)

	switch subcmd {
	case "chat":
		runChat(agentInstance, db, contextEngine, costTracker, cfg, embedder, embeddingErrorStore, knowledgeGraphStore, dryRun)
	case "serve":
		runServe(agentInstance, db, contextEngine, cfg, embedder, embeddingErrorStore, knowledgeGraphStore, toolRegistry, dataDir, dbPath, mcpServers, channelConnector, channelStatusReader, log)
	case "cron":
		cronMessages := storage.NewMessageStore(db, embedder)
		cronMessages.SetEmbeddingErrorStore(embeddingErrorStore)
		cronMessages.SetKnowledgeGraphStore(knowledgeGraphStore)
		runCronCmd(
			args[1:],
			cfg,
			db,
			log,
			agentInstance,
			storage.NewSessionStore(db),
			cronMessages,
			contextEngine,
			nil,
		)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\nusage: agent [chat|serve|cost|privacy|memory|history|undo|cron|migrate|version]\n", subcmd)
		os.Exit(1)
	}
}

func buildProviderStack(cfg *config.Config, log *internlog.Logger) (agent.Provider, error) {
	primaryModel := strings.TrimSpace(cfg.Model.Model)
	if primaryModel == "" {
		primaryModel = defaultModelForProvider(cfg.Model.Provider)
	}

	var router *agent.ModelRouter
	if cfg.ModelRouter.Enabled {
		router = agent.NewModelRouter(agent.ModelRouterConfig{
			Strategy:       cfg.ModelRouter.Strategy,
			CheapModel:     cfg.ModelRouter.CheapModel,
			MidModel:       cfg.ModelRouter.MidModel,
			ExpensiveModel: cfg.ModelRouter.ExpensiveModel,
			PrivacyModel:   cfg.ModelRouter.PrivacyModel,
		}, internlog.AsLogger(log))
		log.Info("model router enabled",
			"strategy", cfg.ModelRouter.Strategy,
			"cheap_model", cfg.ModelRouter.CheapModel,
			"mid_model", cfg.ModelRouter.MidModel,
			"expensive_model", cfg.ModelRouter.ExpensiveModel,
			"privacy_model", cfg.ModelRouter.PrivacyModel,
		)
	}

	wrapProvider := func(p agent.Provider, fallbackModel string) agent.Provider {
		if router != nil {
			// Let the router select a model per request; use fallback as default when
			// router tier models are unset.
			p = agent.WithModelRouter(p, router)
			return agent.WithDefaultModel(p, fallbackModel)
		}
		return agent.WithModel(p, fallbackModel)
	}

	primaryCfg := cfg.Model
	primaryCfg.Model = primaryModel
	primaryRaw, err := agent.NewProvider(primaryCfg)
	if err != nil {
		return nil, err
	}
	primary := wrapProvider(primaryRaw, primaryModel)

	var fallbacks []agent.Provider
	for _, raw := range cfg.Model.FallbackProviders {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" || name == cfg.Model.Provider {
			continue
		}

		fallbackModel := primaryModel
		if v := strings.TrimSpace(cfg.Model.FallbackModels[name]); v != "" {
			fallbackModel = v
		}
		if fallbackModel == "" {
			fallbackModel = defaultModelForProvider(name)
		}

		apiKeyEnv := defaultAPIKeyEnvForProvider(name)
		if v := strings.TrimSpace(cfg.Model.FallbackAPIKeyEnv[name]); v != "" {
			apiKeyEnv = v
		}

		fallbackCfg := config.ModelConfig{
			Provider:  name,
			Model:     fallbackModel,
			APIKeyEnv: apiKeyEnv,
		}
		fallbackRaw, err := agent.NewProvider(fallbackCfg)
		if err != nil {
			log.Warn("failed to initialize fallback provider", "provider", name, "error", err)
			continue
		}
		fallbacks = append(fallbacks, wrapProvider(fallbackRaw, fallbackModel))
	}

	if len(fallbacks) > 0 {
		log.Info("failover providers enabled", "primary", cfg.Model.Provider, "fallback_count", len(fallbacks))
		return agent.NewFailoverProvider(primary, fallbacks, internlog.AsLogger(log)), nil
	}
	return primary, nil
}

func isMissingAPIKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "environment variable") && strings.Contains(msg, "is not set")
}

func selectEmbedder(cfg *config.Config, log *internlog.Logger) agentctx.Embedder {
	provider := strings.ToLower(strings.TrimSpace(cfg.Embeddings.Provider))
	switch provider {
	case "openai":
		apiKeyEnv := cfg.Embeddings.APIKeyEnv
		if strings.TrimSpace(apiKeyEnv) == "" {
			apiKeyEnv = "OPENAI_API_KEY"
		}
		oe, err := agentctx.NewOpenAIEmbedder(apiKeyEnv)
		if err != nil {
			log.Warn("openai embeddings requested but unavailable, disabling semantic memory", "error", err)
			return agentctx.NewNoOpEmbedder()
		}
		log.Info("embeddings enabled", "provider", "openai")
		return oe

	case "ollama":
		log.Info("embeddings enabled", "provider", "ollama")
		return agentctx.NewOllamaEmbedder(cfg.Embeddings.BaseURL, cfg.Embeddings.Model)
	}

	// Auto mode:
	// 1) if the model provider is ollama, use ollama embeddings.
	// 2) else if local ollama is reachable, auto-enable ollama embeddings.
	baseURL := cfg.Embeddings.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://localhost:11434"
	}
	if cfg.Model.Provider == "ollama" {
		log.Info("embeddings enabled", "provider", "ollama", "reason", "model.provider=ollama")
		return agentctx.NewOllamaEmbedder(baseURL, cfg.Embeddings.Model)
	}
	if ollamaAvailable(baseURL) {
		log.Info("embeddings enabled", "provider", "ollama", "reason", "auto-detected running ollama")
		return agentctx.NewOllamaEmbedder(baseURL, cfg.Embeddings.Model)
	}

	log.Warn("semantic memory disabled. Run Ollama or set embeddings.provider to enable full memory.")
	return agentctx.NewNoOpEmbedder()
}

func ollamaAvailable(baseURL string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return true
}

func defaultAPIKeyEnvForProvider(provider string) string {
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
		return "MOONSHOT_API_KEY"
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

func defaultModelForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-sonnet-4-20250514"
	case "openai":
		return "gpt-4o-mini"
	case "gemini":
		return "gemini-1.5-flash"
	case "ollama":
		return "llama3.1"
	default:
		return ""
	}
}

// ── Subcommand handlers ──────────────────────────────────────────────────────

func runCost(db *storage.DB) {
	tracker := cost.NewTracker(db)
	summaries := make(map[string]*cost.Summary)
	for _, period := range []string{"today", "week", "month", "all"} {
		if s, err := tracker.GetSummary(period); err == nil {
			summaries[period] = s
		}
	}
	byProvider, _ := tracker.ProviderBreakdown("all")
	fmt.Println(cost.FormatSummary(summaries, byProvider, nil))
}

func runPrivacy(db *storage.DB, cfg *config.Config, privacyStore *storage.PrivacyStore) {
	tracker := cost.NewTracker(db)
	scrubOutput := cfg != nil && cfg.Tools.ScrubOutput
	report, err := privacyreport.BuildReport(tracker, privacyStore, scrubOutput, "all")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading privacy report: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Privacy Report")
	fmt.Println("────────────────────────────────")
	fmt.Printf("  Tool Output Scrubbing: %t\n", report.Privacy.ScrubOutput)
	fmt.Printf("  Secrets Redacted:      %d\n", report.Privacy.SecretsRedacted)
	fmt.Printf("  Calls:                 %d\n", report.Totals.Calls)
	fmt.Printf("  Input Tokens:          %d\n", report.Totals.InputTokens)
	fmt.Printf("  Output Tokens:         %d\n", report.Totals.OutputTokens)
	fmt.Printf("  Estimated Cost (USD):  %.6f\n", report.Totals.EstimatedCost)
	fmt.Println()

	if len(report.Providers) > 0 {
		fmt.Println("By Provider")
		fmt.Println("────────────────────────────────")
		for _, p := range report.Providers {
			fmt.Printf("  %-14s %6d calls  %8d in / %8d out  ~$%.6f  (%s)\n",
				p.Provider, p.Calls, p.InputTokens, p.OutputTokens, p.EstimatedCost, p.Privacy)
		}
		fmt.Println()
	}
}

type editableMemoryNode struct {
	ID         int64   `json:"id"`
	Type       string  `json:"type"`
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
}

func runMemory(db *storage.DB, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agent memory [list|search <query>|edit]")
		os.Exit(1)
	}

	store := storage.NewKnowledgeGraphStore(db)

	switch args[0] {
	case "list":
		limit := 100
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--limit", "-n":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "usage: agent memory list [--limit <n>]")
					os.Exit(1)
				}
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "error: invalid limit %q\n", args[i+1])
					os.Exit(1)
				}
				limit = n
				i++
			default:
				fmt.Fprintln(os.Stderr, "usage: agent memory list [--limit <n>]")
				os.Exit(1)
			}
		}
		nodes, err := store.ListNodes(limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		printMemoryNodes(nodes)

	case "search":
		limit := 100
		nodeType := ""
		var queryParts []string

		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--limit", "-n":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "usage: agent memory search <query> [--type <type>] [--limit <n>]")
					os.Exit(1)
				}
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "error: invalid limit %q\n", args[i+1])
					os.Exit(1)
				}
				limit = n
				i++
			case "--type":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "usage: agent memory search <query> [--type <type>] [--limit <n>]")
					os.Exit(1)
				}
				nodeType = strings.TrimSpace(args[i+1])
				i++
			default:
				queryParts = append(queryParts, args[i])
			}
		}

		query := strings.TrimSpace(strings.Join(queryParts, " "))
		if query == "" {
			fmt.Fprintln(os.Stderr, "usage: agent memory search <query> [--type <type>] [--limit <n>]")
			os.Exit(1)
		}

		nodes, err := store.SearchNodes(query, nodeType, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		printMemoryNodes(nodes)

	case "edit":
		runMemoryEdit(store)

	default:
		fmt.Fprintf(os.Stderr, "unknown memory subcommand: %s\nusage: agent memory [list|search <query>|edit]\n", args[0])
		os.Exit(1)
	}
}

func runMemoryEdit(store *storage.KnowledgeGraphStore) {
	nodes, err := store.ListNodes(500)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading entities: %v\n", err)
		os.Exit(1)
	}
	if len(nodes) == 0 {
		fmt.Println("No known entities yet.")
		return
	}

	editable := make([]editableMemoryNode, 0, len(nodes))
	existing := make(map[int64]storage.KGNode, len(nodes))
	for _, node := range nodes {
		editable = append(editable, editableMemoryNode{
			ID:         node.ID,
			Type:       node.Type,
			Name:       node.Name,
			Confidence: node.Confidence,
		})
		existing[node.ID] = node
	}

	data, err := json.MarshalIndent(editable, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error preparing editable memory file: %v\n", err)
		os.Exit(1)
	}

	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("openclio-memory-edit-%d.json", time.Now().UnixNano()))
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "error writing temporary edit file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmpPath)

	fmt.Printf("Opening %s\n", tmpPath)
	fmt.Println("Edit the JSON rows to correct type/name/confidence.")
	fmt.Println("Remove a row to delete that entity.")
	if err := openInEditor(tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "error opening editor: %v\n", err)
		os.Exit(1)
	}

	editedData, err := os.ReadFile(tmpPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading edited memory file: %v\n", err)
		os.Exit(1)
	}
	var edited []editableMemoryNode
	if err := json.Unmarshal(editedData, &edited); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing edited JSON: %v\n", err)
		os.Exit(1)
	}
	if len(edited) == 0 {
		fmt.Fprintln(os.Stderr, "error: refusing to apply an empty memory file")
		os.Exit(1)
	}

	seen := make(map[int64]struct{}, len(edited))
	updated := 0
	for _, row := range edited {
		if row.ID <= 0 {
			fmt.Fprintf(os.Stderr, "error: invalid row id %d\n", row.ID)
			os.Exit(1)
		}
		if _, ok := existing[row.ID]; !ok {
			fmt.Fprintf(os.Stderr, "error: unknown entity id %d\n", row.ID)
			os.Exit(1)
		}
		if _, dup := seen[row.ID]; dup {
			fmt.Fprintf(os.Stderr, "error: duplicate entity id %d\n", row.ID)
			os.Exit(1)
		}
		seen[row.ID] = struct{}{}
		if err := store.UpdateNode(row.ID, row.Type, row.Name, row.Confidence); err != nil {
			fmt.Fprintf(os.Stderr, "error updating entity %d: %v\n", row.ID, err)
			os.Exit(1)
		}
		updated++
	}

	deleted := 0
	for id := range existing {
		if _, ok := seen[id]; ok {
			continue
		}
		if err := store.DeleteNode(id); err != nil && !errors.Is(err, storage.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "error deleting entity %d: %v\n", id, err)
			os.Exit(1)
		}
		deleted++
	}

	fmt.Printf("✓ Updated %d entities, removed %d entities\n", updated, deleted)
}

func printMemoryNodes(nodes []storage.KGNode) {
	if len(nodes) == 0 {
		fmt.Println("No known entities yet.")
		return
	}

	fmt.Printf("\n%-6s %-14s %-40s %-10s %-20s\n", "ID", "TYPE", "NAME", "CONF", "UPDATED")
	fmt.Println("────── ────────────── ──────────────────────────────────────── ────────── ────────────────────")
	for _, n := range nodes {
		name := strings.ReplaceAll(n.Name, "\n", " ")
		if len(name) > 40 {
			name = name[:40] + "..."
		}
		fmt.Printf("%-6d %-14s %-40s %-10.2f %-20s\n",
			n.ID,
			n.Type,
			name,
			n.Confidence,
			n.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		)
	}
	fmt.Println()
}

func openInEditor(path string) error {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "vi"
		}
	}

	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return fmt.Errorf("EDITOR is empty")
	}
	args := append(parts[1:], path)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runHistory(db *storage.DB, args []string) {
	limit := 20
	if len(args) > 0 {
		switch args[0] {
		case "--last", "-n":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: agent history [--last <n>]")
				os.Exit(1)
			}
			n, err := strconv.Atoi(args[1])
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "error: invalid history limit %q\n", args[1])
				os.Exit(1)
			}
			limit = n
		default:
			n, err := strconv.Atoi(args[0])
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, "usage: agent history [--last <n>]")
				os.Exit(1)
			}
			limit = n
		}
	}

	store := storage.NewActionLogStore(db)
	entries, err := store.List(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("No tool actions recorded yet.")
		return
	}

	fmt.Printf("\n%-6s %-20s %-10s %-8s %-45s %-8s\n", "ID", "TIME", "TOOL", "STATUS", "DETAIL", "UNDO")
	fmt.Println("────── ──────────────────── ────────── ──────── ───────────────────────────────────────────── ────────")
	for _, e := range entries {
		status := "OK"
		if !e.Success {
			status = "ERR"
		}
		detail := ""
		switch e.ToolName {
		case "write_file":
			detail = e.TargetPath
			if e.BeforeExists {
				detail += " (overwrite)"
			} else {
				detail += " (create)"
			}
		case "exec":
			detail = strings.TrimSpace(e.Command)
		default:
			detail = e.ToolName
		}
		detail = strings.ReplaceAll(detail, "\n", " ")
		if len(detail) > 45 {
			detail = detail[:45] + "..."
		}
		undoable := "NO"
		if e.ToolName == "write_file" {
			undoable = "YES"
		}
		fmt.Printf("%-6d %-20s %-10s %-8s %-45s %-8s\n",
			e.ID,
			e.CreatedAt.Local().Format("2006-01-02 15:04:05"),
			e.ToolName,
			status,
			detail,
			undoable,
		)
	}
	fmt.Println()
}

func runUndo(db *storage.DB, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: agent undo <history-id>")
		os.Exit(1)
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		fmt.Fprintf(os.Stderr, "error: invalid history id %q\n", args[0])
		os.Exit(1)
	}

	store := storage.NewActionLogStore(db)
	entry, err := store.Get(id)
	if err != nil {
		if err == storage.ErrNotFound {
			fmt.Fprintf(os.Stderr, "error: action %d not found\n", id)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if entry.ToolName != "write_file" {
		fmt.Fprintf(os.Stderr, "error: action %d is not undoable (tool=%s)\n", id, entry.ToolName)
		os.Exit(1)
	}
	if strings.TrimSpace(entry.TargetPath) == "" {
		fmt.Fprintf(os.Stderr, "error: action %d has no target path\n", id)
		os.Exit(1)
	}

	if entry.BeforeExists {
		if err := os.MkdirAll(filepath.Dir(entry.TargetPath), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error: creating parent directory: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(entry.TargetPath, []byte(entry.BeforeContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error: restoring file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Restored %s\n", entry.TargetPath)
	} else {
		if err := os.Remove(entry.TargetPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "error: removing file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Removed %s\n", entry.TargetPath)
	}
	fmt.Printf("✓ Undid action #%d\n", id)
}

func runCronCmd(
	args []string,
	cfg *config.Config,
	db *storage.DB,
	log *internlog.Logger,
	agentInstance *agent.Agent,
	sessions *storage.SessionStore,
	messages *storage.MessageStore,
	contextEngine *agentctx.Engine,
	manager *plugin.Manager,
) {
	if len(args) == 0 {
		fmt.Println("usage: agent cron [list|run <name>|history]")
		os.Exit(1)
	}

	scheduler := agentcron.NewScheduler(agentInstance, sessions, messages, contextEngine, manager, db, internlog.AsLogger(log))
	for _, job := range cfg.Cron {
		if err := scheduler.Add(cronJobFromConfig(job)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping cron job %q: %v\n", job.Name, err)
		}
	}
	if loaded, skipped, err := scheduler.LoadPersistedJobs(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load persisted cron jobs: %v\n", err)
	} else if loaded > 0 || skipped > 0 {
		fmt.Fprintf(os.Stderr, "info: loaded %d persisted cron jobs (%d skipped)\n", loaded, skipped)
	}

	switch args[0] {
	case "list":
		jobs := scheduler.ListJobs()
		if len(jobs) == 0 {
			fmt.Println("No cron jobs configured. Add them to ~/.openclio/config.yaml under 'cron:' or create persisted jobs via the dashboard API.")
			return
		}
		fmt.Printf("\n%-20s  %-24s  %-10s  %-8s  %-8s  %-20s  %-20s\n", "NAME", "SCHEDULE/TRIGGER", "MODE", "SOURCE", "ENABLED", "LAST RUN", "NEXT RUN")
		fmt.Println("────────────────────  ────────────────────────  ──────────  ────────  ────────  ────────────────────  ────────────────────")
		for _, j := range jobs {
			lastRun := "—"
			if !j.LastRun.IsZero() {
				lastRun = j.LastRun.Local().Format("2006-01-02 15:04:05")
			}
			nextRun := "—"
			if !j.NextRun.IsZero() {
				nextRun = j.NextRun.Local().Format("2006-01-02 15:04:05")
			}
			enabled := "no"
			if j.Enabled {
				enabled = "yes"
			}
			triggerOrSchedule := j.Schedule
			if strings.TrimSpace(j.Trigger) != "" {
				triggerOrSchedule = "trigger: " + j.Trigger
			}
			fmt.Printf("%-20s  %-20s  %-10s  %-8s  %-8s  %-20s  %-20s\n",
				j.Name, triggerOrSchedule, j.SessionMode, j.Source, enabled, lastRun, nextRun)
		}
		fmt.Println()

	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: agent cron run <name>")
			os.Exit(1)
		}
		if agentInstance == nil || sessions == nil || messages == nil || contextEngine == nil {
			fmt.Fprintln(os.Stderr, "error: cron run requires full runtime initialization")
			os.Exit(1)
		}
		fmt.Printf("Triggering cron job %q...\n", args[1])
		if err := scheduler.RunNow(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Job completed.")

	case "history":
		entries, err := scheduler.History(20)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(entries) == 0 {
			fmt.Println("No cron history yet.")
			return
		}
		fmt.Printf("\n%-20s  %-20s  %-8s  %-7s  %s\n", "JOB", "RAN AT", "DURATION", "STATUS", "OUTPUT")
		fmt.Println("────────────────────  ────────────────────  ────────  ───────  ─────────────────────────────")
		for _, e := range entries {
			status := "✓ OK"
			if !e.Success {
				status = "✗ ERR"
			}
			preview := e.Output
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			fmt.Printf("%-20s  %-20s  %-8s  %-7s  %s\n",
				e.JobName,
				e.RanAt.Local().Format("2006-01-02 15:04:05"),
				fmt.Sprintf("%dms", e.DurationMs),
				status,
				preview,
			)
		}
		fmt.Println()

	default:
		fmt.Fprintf(os.Stderr, "unknown cron subcommand: %s\nusage: agent cron [list|run <name>|history]\n", args[0])
		os.Exit(1)
	}
}

func runChat(
	agentInstance *agent.Agent,
	db *storage.DB,
	contextEngine *agentctx.Engine,
	costTracker *cost.Tracker,
	cfg *config.Config,
	embedder agentctx.Embedder,
	embeddingErrors *storage.EmbeddingErrorStore,
	knowledgeGraph *storage.KnowledgeGraphStore,
	dryRun bool,
) {
	agentInstance.SetDryRun(dryRun)
	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db, embedder)
	messages.SetEmbeddingErrorStore(embeddingErrors)
	messages.SetKnowledgeGraphStore(knowledgeGraph)

	// Collect cron job names for /skill display
	cronNames := make([]string, len(cfg.Cron))
	for i, j := range cfg.Cron {
		cronNames[i] = j.Name + " (" + j.Schedule + ")"
	}

	chatCLI := cli.NewCLI(
		agentInstance, sessions, messages, contextEngine, costTracker,
		cfg.CLI, cfg.Model.Provider, cfg.Model.Model,
		"", cronNames,
	)
	if err := chatCLI.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runServe(
	agentInstance *agent.Agent,
	db *storage.DB,
	contextEngine *agentctx.Engine,
	cfg *config.Config,
	embedder agentctx.Embedder,
	embeddingErrors *storage.EmbeddingErrorStore,
	knowledgeGraph *storage.KnowledgeGraphStore,
	toolRegistry *tools.Registry,
	dataDir, dbPath string,
	mcpServers []*mcp.Server,
	channelConnector *runtimeChannelConnector,
	channelStatusReader *runtimeChannelStatusReader,
	log *internlog.Logger,
) {
	ctx, cancel := context.WithCancel(context.Background())

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db, embedder)
	messages.SetEmbeddingErrorStore(embeddingErrors)
	messages.SetKnowledgeGraphStore(knowledgeGraph)
	startIncrementalVacuumLoop(ctx, db, log)
	mcpHealth := startMCPHealthLoop(ctx, mcpServers, log)

	// Plugin manager + router
	manager := plugin.NewManager(internlog.AsLogger(log))
	// Layer 2: Unknown sender allowlist (allow_all=true by default — permits everyone;
	// set allow_all: false in config to block unknown senders)
	allowAll := true
	if !cfg.Channels.AllowAll {
		allowAll = false
	}
	allowlist := plugin.NewAllowlist(dataDir, allowAll)
	router := plugin.NewRouter(agentInstance, sessions, messages, contextEngine, manager, internlog.AsLogger(log)).
		WithAllowlist(allowlist)
	if channelStatusReader != nil {
		channelStatusReader.SetHandlers(
			func(channelType string) (tools.ChannelStatus, error) {
				channelType = strings.ToLower(strings.TrimSpace(channelType))
				if channelType == "" {
					return tools.ChannelStatus{}, fmt.Errorf("channel_type is required")
				}
				statuses := manager.Statuses()
				for _, st := range statuses {
					if st.Name != channelType {
						continue
					}
					return resolveChannelStatus(manager, st), nil
				}
				return tools.ChannelStatus{}, fmt.Errorf("channel %q is not registered", channelType)
			},
			func() ([]tools.ChannelStatus, error) {
				statuses := manager.Statuses()
				out := make([]tools.ChannelStatus, 0, len(statuses))
				for _, st := range statuses {
					out = append(out, resolveChannelStatus(manager, st))
				}
				return out, nil
			},
		)
		defer channelStatusReader.SetHandlers(nil, nil)
	}
	if channelConnector != nil {
		disconnectRuntimeChannel := func(channelType string, clearSession bool) error {
			channelType = strings.ToLower(strings.TrimSpace(channelType))
			if channelType == "" {
				return fmt.Errorf("channel type is required")
			}
			adapter := manager.AdapterByName(channelType)
			if adapter == nil {
				return fmt.Errorf("%s channel is not connected", channelType)
			}

			if clearSession && channelType == "whatsapp" {
				resetter, ok := adapter.(interface {
					ResetSession(context.Context) error
				})
				if !ok {
					return fmt.Errorf("whatsapp adapter does not support session reset")
				}
				resetCtx, cancelReset := context.WithTimeout(ctx, 20*time.Second)
				err := resetter.ResetSession(resetCtx)
				cancelReset()
				if err != nil {
					return fmt.Errorf("resetting whatsapp session failed: %w", err)
				}
			}

			adapter.Stop()
			manager.Unregister(channelType)

			if closer, ok := adapter.(interface{ Close() error }); ok {
				if err := closer.Close(); err != nil {
					log.Warn("runtime channel close failed", "channel", channelType, "error", err)
				}
			}

			log.Info("runtime channel disconnected", "channel", channelType)
			return nil
		}

		channelConnector.SetHandler(func(channelType string, credentials map[string]string) error {
			channelType = strings.ToLower(strings.TrimSpace(channelType))
			forceReconnect, _ := strconv.ParseBool(strings.TrimSpace(credentials["force_reconnect"]))
			existing := manager.AdapterByName(channelType)
			if existing != nil {
				statusRunning := false
				for _, st := range manager.Statuses() {
					if st.Name == channelType {
						statusRunning = st.Running
						break
					}
				}

				if channelType == "whatsapp" {
					if forceReconnect {
						if err := disconnectRuntimeChannel(channelType, true); err != nil {
							return err
						}
					} else {
						qrProvider, ok := existing.(interface {
							QRCodeState() plugin.QRCodeState
						})
						if ok {
							qrState := qrProvider.QRCodeState()
							event := strings.ToLower(strings.TrimSpace(qrState.Event))
							if event == "connected" || event == "success" {
								return fmt.Errorf("%s channel is already connected", channelType)
							}
							if event == "waiting_for_qr" || event == "code" {
								return fmt.Errorf("%s pairing is already in progress", channelType)
							}
						}
						if statusRunning {
							return fmt.Errorf("%s channel is already connected", channelType)
						}
						if err := disconnectRuntimeChannel(channelType, false); err != nil {
							return err
						}
					}
				} else {
					if statusRunning {
						return fmt.Errorf("%s channel is already connected", channelType)
					}
					if err := disconnectRuntimeChannel(channelType, false); err != nil {
						return err
					}
				}
			}

			var adapter plugin.Adapter
			switch channelType {
			case "slack":
				token := strings.TrimSpace(credentials["token"])
				if token == "" && cfg.Channels.Slack != nil && strings.TrimSpace(cfg.Channels.Slack.TokenEnv) != "" {
					token = strings.TrimSpace(os.Getenv(cfg.Channels.Slack.TokenEnv))
				}
				if token == "" {
					return fmt.Errorf("slack token is required")
				}
				sl, err := slackadapter.New(token, internlog.AsLogger(log))
				if err != nil {
					return err
				}
				adapter = sl
			case "telegram":
				token := strings.TrimSpace(credentials["token"])
				if token == "" {
					token = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
				}
				if token == "" {
					return fmt.Errorf("telegram token is required")
				}
				tg, err := telegramadapter.New(token, internlog.AsLogger(log))
				if err != nil {
					return err
				}
				adapter = tg
			case "discord":
				token := strings.TrimSpace(credentials["token"])
				if token == "" {
					token = strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
				}
				if token == "" {
					return fmt.Errorf("discord token is required")
				}
				appID := strings.TrimSpace(credentials["app_id"])
				if appID == "" && cfg.Channels.Discord != nil && cfg.Channels.Discord.AppIDEnv != "" {
					appID = os.Getenv(cfg.Channels.Discord.AppIDEnv)
				}
				dc, err := discordadapter.New(token, appID, internlog.AsLogger(log))
				if err != nil {
					return err
				}
				adapter = dc
			case "whatsapp":
				waDataDir := strings.TrimSpace(credentials["data_dir"])
				if waDataDir == "" {
					waDataDir = dataDir
					if cfg.Channels.WhatsApp != nil && strings.TrimSpace(cfg.Channels.WhatsApp.DataDir) != "" {
						waDataDir = cfg.Channels.WhatsApp.DataDir
					}
				}
				waDataDir = resolveLocalPath(waDataDir)
				if forceReconnect {
					if err := whatsappadapter.ResetStoredSession(waDataDir); err != nil {
						return fmt.Errorf("resetting whatsapp stored session failed: %w", err)
					}
				}
				wa, err := whatsappadapter.New(waDataDir, internlog.AsLogger(log))
				if err != nil {
					return err
				}
				adapter = wa
			default:
				return fmt.Errorf("unsupported channel type %q", channelType)
			}

			manager.Register(adapter)
			manager.RunOne(ctx, adapter)
			log.Info("runtime channel connected", "channel", channelType)
			return nil
		})
		channelConnector.SetDisconnectHandler(func(channelType string) error {
			channelType = strings.ToLower(strings.TrimSpace(channelType))
			return disconnectRuntimeChannel(channelType, channelType == "whatsapp")
		})
		defer channelConnector.SetHandler(nil)
		defer channelConnector.SetDisconnectHandler(nil)
	}

	// Telegram adapter
	if cfg.Channels.Telegram != nil {
		if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
			if tg, err := telegramadapter.New(token, internlog.AsLogger(log)); err == nil {
				manager.Register(tg)
				log.Info("telegram adapter registered")
			}
		}
	}

	// Discord adapter
	if cfg.Channels.Discord != nil {
		if token := os.Getenv("DISCORD_BOT_TOKEN"); token != "" {
			appID := ""
			if cfg.Channels.Discord.AppIDEnv != "" {
				appID = os.Getenv(cfg.Channels.Discord.AppIDEnv)
			}
			if dc, err := discordadapter.New(token, appID, internlog.AsLogger(log)); err == nil {
				manager.Register(dc)
				log.Info("discord adapter registered")
			}
		}
	}

	// WhatsApp adapter (whatsmeow QR login; no API token required)
	if cfg.Channels.WhatsApp != nil && cfg.Channels.WhatsApp.Enabled {
		waDataDir := dataDir
		if cfg.Channels.WhatsApp.DataDir != "" {
			waDataDir = cfg.Channels.WhatsApp.DataDir
		}
		waDataDir = resolveLocalPath(waDataDir)
		wa, err := whatsappadapter.New(waDataDir, internlog.AsLogger(log))
		if err != nil {
			log.Warn("whatsapp adapter failed to initialise", "error", err)
		} else {
			manager.Register(wa)
			log.Info("whatsapp adapter registered")
		}
	}

	// Slack adapter
	if cfg.Channels.Slack != nil {
		if token := os.Getenv(cfg.Channels.Slack.TokenEnv); token != "" {
			if sl, err := slackadapter.New(token, internlog.AsLogger(log)); err == nil {
				manager.Register(sl)
				log.Info("slack adapter registered")
			} else {
				log.Warn("slack adapter failed to initialise", "error", err)
			}
		}
	}

	// Webchat adapter — bridges the embedded web UI to the agent message bus
	wcAdapter := webchatadapter.NewAdapter()
	manager.Register(wcAdapter)
	log.Info("webchat adapter registered")

	// Start adapters + router
	manager.Start(ctx)
	go router.Run(ctx)

	// Cron scheduler
	scheduler := agentcron.NewScheduler(agentInstance, sessions, messages, contextEngine, manager, db, internlog.AsLogger(log))
	for _, job := range cfg.Cron {
		if err := scheduler.Add(cronJobFromConfig(job)); err != nil {
			log.Warn("invalid config cron job", "name", job.Name, "error", err)
		}
	}
	if loaded, skipped, err := scheduler.LoadPersistedJobs(); err != nil {
		log.Warn("failed loading persisted cron jobs", "error", err)
	} else if loaded > 0 || skipped > 0 {
		log.Info("persisted cron jobs loaded", "loaded", loaded, "skipped", skipped)
	}
	scheduler.Start()

	// Auth token + gateway
	authToken, err := gateway.LoadOrCreateToken(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		cancel()
		os.Exit(1)
	}

	// Cost tracker
	costTracker := cost.NewTracker(db)
	server := gateway.NewServer(cfg.Gateway, cfg, agentInstance, db, contextEngine, costTracker, authToken, embedder)
	server.AttachToolRegistry(toolRegistry)
	server.AttachMCPStatusSource(mcpHealth)
	server.AttachRuntimeSources(manager, scheduler, allowlist, cfg.MCPServers)
	server.AttachChannelRuntime(channelConnector, channelConnector)

	// gRPC out-of-process adapter server (opt-in via gateway.grpc_port)
	var grpcCore *rpc.CoreServer
	if cfg.Gateway.GRPCPort > 0 {
		grpcCore = rpc.NewCoreServer(agentInstance, sessions, messages)
		grpcAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Gateway.GRPCPort)
		go func() {
			if err := grpcCore.ListenAndServe(grpcAddr); err != nil {
				log.Warn("gRPC server stopped", "error", err)
			}
		}()
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
		manager.Stop()
		scheduler.Stop()
		if grpcCore != nil {
			grpcCore.Stop()
		}
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		server.Shutdown(shutCtx)
	}()

	uiURL := fmt.Sprintf("http://%s:%d/?token=%s", cfg.Gateway.Bind, cfg.Gateway.Port, authToken)
	fmt.Printf("openclio %s ready\n\n", version)
	fmt.Printf("  Open this URL in your browser:\n")
	fmt.Printf("  %s\n\n", uiURL)
	fmt.Printf("  API:  http://%s:%d/api/v1/\n", cfg.Gateway.Bind, cfg.Gateway.Port)
	fmt.Printf("  WS:   ws://%s:%d/ws\n\n", cfg.Gateway.Bind, cfg.Gateway.Port)
	openBrowser(uiURL)

	if err := server.Start(); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "address already in use"):
			fmt.Fprintf(os.Stderr, "error: port %d is already in use\n", cfg.Gateway.Port)
			fmt.Fprintf(os.Stderr, "  hint: find the owner with: lsof -i :%d\n", cfg.Gateway.Port)
			fmt.Fprintf(os.Stderr, "  or change gateway.port in ~/.openclio/config.yaml\n")
		case strings.Contains(msg, "security:"):
			fmt.Fprintf(os.Stderr, "error: %s\n", msg)
		default:
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		}
		os.Exit(1)
	}
}

func resolveChannelStatus(manager *plugin.Manager, st plugin.AdapterStatus) tools.ChannelStatus {
	status := tools.ChannelStatus{
		Name:            st.Name,
		Running:         st.Running,
		Healthy:         st.Healthy,
		LastHealthError: st.LastHealthError,
	}

	if st.Name != "whatsapp" {
		return status
	}

	adapter := manager.AdapterByName("whatsapp")
	if adapter == nil {
		status.Message = "WhatsApp adapter is not registered."
		return status
	}

	qrProvider, ok := adapter.(interface {
		QRCodeState() plugin.QRCodeState
	})
	if !ok {
		status.Message = "WhatsApp adapter does not expose QR status."
		return status
	}

	qrState := qrProvider.QRCodeState()
	status.QREvent = strings.TrimSpace(qrState.Event)
	status.QRAvailable = strings.EqualFold(status.QREvent, "code") && strings.TrimSpace(qrState.Code) != ""
	status.Connected = strings.EqualFold(status.QREvent, "connected") || strings.EqualFold(status.QREvent, "success")

	switch {
	case status.Connected:
		status.Message = "WhatsApp is connected to openclio."
	case status.QRAvailable:
		status.Message = "WhatsApp QR code is available in openclio webchat."
	case status.QREvent != "":
		status.Message = "WhatsApp pairing state: " + status.QREvent
	default:
		status.Message = "WhatsApp is initializing."
	}

	return status
}

func resolveLocalPath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}

func startIncrementalVacuumLoop(ctx context.Context, db *storage.DB, log *internlog.Logger) {
	if db == nil || log == nil {
		return
	}
	ticker := time.NewTicker(6 * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := db.IncrementalVacuum(); err != nil {
					log.Warn("incremental vacuum failed", "error", err)
				}
			}
		}
	}()
}

type mcpRuntimeState struct {
	Name                string
	Status              string
	Healthy             bool
	LastHealthCheck     time.Time
	LastHealthError     string
	RestartCount        int
	ConsecutiveFailures int
	NextRetryAt         time.Time
	RetryBackoff        time.Duration
	Disabled            bool
	RestartInFlight     bool
}

type mcpHealthSupervisor struct {
	mu             sync.RWMutex
	log            *internlog.Logger
	servers        map[string]*mcp.Server
	states         map[string]*mcpRuntimeState
	checkTimeout   time.Duration
	restartTimeout time.Duration
	maxFailures    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

func startMCPHealthLoop(ctx context.Context, servers []*mcp.Server, log *internlog.Logger) *mcpHealthSupervisor {
	return startMCPHealthLoopWithInterval(ctx, servers, log, 30*time.Second)
}

func startMCPHealthLoopWithInterval(ctx context.Context, servers []*mcp.Server, log *internlog.Logger, interval time.Duration) *mcpHealthSupervisor {
	if len(servers) == 0 || log == nil {
		return nil
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}

	supervisor := newMCPHealthSupervisor(servers, log)
	supervisor.Start(ctx, interval)
	return supervisor
}

func newMCPHealthSupervisor(servers []*mcp.Server, log *internlog.Logger) *mcpHealthSupervisor {
	s := &mcpHealthSupervisor{
		log:            log,
		servers:        make(map[string]*mcp.Server, len(servers)),
		states:         make(map[string]*mcpRuntimeState, len(servers)),
		checkTimeout:   5 * time.Second,
		restartTimeout: 15 * time.Second,
		maxFailures:    8,
		initialBackoff: 1 * time.Second,
		maxBackoff:     2 * time.Minute,
	}

	for _, srv := range servers {
		if srv == nil || strings.TrimSpace(srv.Name) == "" {
			continue
		}
		s.servers[srv.Name] = srv
		s.states[srv.Name] = &mcpRuntimeState{
			Name:            srv.Name,
			Status:          "healthy",
			Healthy:         true,
			LastHealthCheck: time.Now().UTC(),
		}
	}
	return s
}

func (s *mcpHealthSupervisor) Start(ctx context.Context, interval time.Duration) {
	if s == nil {
		return
	}
	// Immediate baseline check.
	s.runHealthChecks(ctx)

	// Passive crash detection from stdio process exits.
	for _, srv := range s.servers {
		go s.watchExit(ctx, srv)
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runHealthChecks(ctx)
			}
		}
	}()
}

func (s *mcpHealthSupervisor) runHealthChecks(ctx context.Context) {
	if s == nil {
		return
	}
	for _, srv := range s.servers {
		if srv == nil {
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, s.checkTimeout)
		_, err := srv.ListTools(checkCtx)
		cancel()
		if err != nil {
			s.handleFailure(ctx, srv, err)
			continue
		}
		s.markHealthy(srv.Name)
	}
}

func (s *mcpHealthSupervisor) watchExit(ctx context.Context, srv *mcp.Server) {
	if s == nil || srv == nil {
		return
	}

	var observed <-chan error
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ch := srv.ExitCh()
		if ch == nil || ch == observed {
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}
		observed = ch

		select {
		case <-ctx.Done():
			return
		case err, ok := <-ch:
			if ctx.Err() != nil {
				return
			}
			if !ok {
				err = nil
			}
			exitAt, exitErr := srv.LastExit()
			msg := "mcp process exited unexpectedly"
			if exitErr != "" {
				msg = exitErr
			} else if err != nil {
				msg = err.Error()
			}
			if !exitAt.IsZero() {
				msg = fmt.Sprintf("%s at %s", msg, exitAt.Format(time.RFC3339))
			}
			s.handleFailure(ctx, srv, errors.New(msg))
		}
	}
}

func (s *mcpHealthSupervisor) handleFailure(ctx context.Context, srv *mcp.Server, cause error) {
	if s == nil || srv == nil {
		return
	}
	name := srv.Name
	now := time.Now().UTC()
	causeText := "unknown mcp health failure"
	if cause != nil {
		causeText = cause.Error()
	}

	s.mu.Lock()
	st, ok := s.states[name]
	if !ok {
		st = &mcpRuntimeState{Name: name}
		s.states[name] = st
	}
	if st.Disabled {
		st.Healthy = false
		st.Status = "offline"
		st.LastHealthCheck = now
		st.LastHealthError = causeText
		s.mu.Unlock()
		return
	}
	st.Healthy = false
	st.Status = "degraded"
	st.LastHealthCheck = now
	st.LastHealthError = causeText
	if st.RestartInFlight {
		s.mu.Unlock()
		return
	}
	if !st.NextRetryAt.IsZero() && now.Before(st.NextRetryAt) {
		st.Status = "retrying"
		s.mu.Unlock()
		return
	}
	st.RestartInFlight = true
	st.Status = "restarting"
	s.mu.Unlock()

	if err := s.restartServer(ctx, srv); err != nil {
		s.mu.Lock()
		st = s.states[name]
		st.RestartInFlight = false
		st.Healthy = false
		st.LastHealthCheck = time.Now().UTC()
		st.LastHealthError = err.Error()
		st.ConsecutiveFailures++
		if st.RetryBackoff <= 0 {
			st.RetryBackoff = s.initialBackoff
		} else {
			st.RetryBackoff *= 2
			if st.RetryBackoff > s.maxBackoff {
				st.RetryBackoff = s.maxBackoff
			}
		}
		st.NextRetryAt = time.Now().UTC().Add(st.RetryBackoff)
		if st.ConsecutiveFailures >= s.maxFailures {
			st.Disabled = true
			st.Status = "offline"
		} else {
			st.Status = "retrying"
		}
		disabled := st.Disabled
		attempt := st.ConsecutiveFailures
		retryIn := st.RetryBackoff
		lastErr := st.LastHealthError
		s.mu.Unlock()

		if disabled {
			s.log.Error("mcp server marked offline after max restart attempts",
				"server", name,
				"attempts", attempt,
				"error", lastErr,
			)
		} else {
			s.log.Error("mcp restart failed; will retry with backoff",
				"server", name,
				"attempt", attempt,
				"retry_in", retryIn,
				"error", lastErr,
			)
		}
		return
	}

	s.mu.Lock()
	st = s.states[name]
	st.RestartInFlight = false
	st.Healthy = true
	st.Status = "healthy"
	st.LastHealthCheck = time.Now().UTC()
	st.LastHealthError = ""
	st.RestartCount++
	st.ConsecutiveFailures = 0
	st.Disabled = false
	st.NextRetryAt = time.Time{}
	st.RetryBackoff = s.initialBackoff
	restarts := st.RestartCount
	s.mu.Unlock()

	s.log.Info("mcp server restarted", "server", name, "restart_count", restarts)
}

func (s *mcpHealthSupervisor) restartServer(ctx context.Context, srv *mcp.Server) error {
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = srv.Stop(stopCtx)
	stopCancel()

	startCtx, startCancel := context.WithTimeout(ctx, s.restartTimeout)
	err := srv.Start(startCtx)
	startCancel()
	if err != nil {
		return err
	}
	return nil
}

func (s *mcpHealthSupervisor) markHealthy(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[name]
	if !ok {
		return
	}
	if st.RestartInFlight {
		return
	}
	st.Healthy = true
	st.Status = "healthy"
	st.LastHealthCheck = time.Now().UTC()
	st.LastHealthError = ""
	st.ConsecutiveFailures = 0
	st.Disabled = false
	st.NextRetryAt = time.Time{}
	if st.RetryBackoff <= 0 {
		st.RetryBackoff = s.initialBackoff
	}
}

func (s *mcpHealthSupervisor) SnapshotMCPStatus() []gateway.MCPRuntimeStatus {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]gateway.MCPRuntimeStatus, 0, len(s.states))
	for _, st := range s.states {
		if st == nil {
			continue
		}
		row := gateway.MCPRuntimeStatus{
			Name:                st.Name,
			Status:              st.Status,
			Healthy:             st.Healthy,
			LastHealthError:     st.LastHealthError,
			RestartCount:        st.RestartCount,
			ConsecutiveFailures: st.ConsecutiveFailures,
			RetryBackoffMs:      st.RetryBackoff.Milliseconds(),
			Disabled:            st.Disabled,
		}
		if !st.LastHealthCheck.IsZero() {
			row.LastHealthCheck = st.LastHealthCheck.Format(time.RFC3339)
		}
		if !st.NextRetryAt.IsZero() {
			row.NextRetryAt = st.NextRetryAt.Format(time.RFC3339)
		}
		out = append(out, row)
	}
	return out
}

func (s *mcpHealthSupervisor) RestartMCPServer(name string) error {
	name = strings.TrimSpace(name)
	if s == nil || name == "" {
		return fmt.Errorf("mcp server name is required")
	}

	s.mu.RLock()
	srv := s.servers[name]
	s.mu.RUnlock()
	if srv == nil {
		return fmt.Errorf("mcp server %q not found", name)
	}

	s.mu.Lock()
	st, ok := s.states[name]
	if !ok {
		st = &mcpRuntimeState{Name: name}
		s.states[name] = st
	}
	if st.RestartInFlight {
		s.mu.Unlock()
		return fmt.Errorf("mcp server %q restart already in progress", name)
	}
	st.RestartInFlight = true
	st.Status = "restarting"
	st.Disabled = false
	st.NextRetryAt = time.Time{}
	s.mu.Unlock()

	if err := s.restartServer(context.Background(), srv); err != nil {
		s.mu.Lock()
		st = s.states[name]
		st.RestartInFlight = false
		st.Healthy = false
		st.Status = "degraded"
		st.LastHealthCheck = time.Now().UTC()
		st.LastHealthError = err.Error()
		s.mu.Unlock()
		return err
	}

	s.mu.Lock()
	st = s.states[name]
	st.RestartInFlight = false
	st.Healthy = true
	st.Status = "healthy"
	st.LastHealthCheck = time.Now().UTC()
	st.LastHealthError = ""
	st.RestartCount++
	st.ConsecutiveFailures = 0
	st.RetryBackoff = s.initialBackoff
	st.NextRetryAt = time.Time{}
	s.mu.Unlock()

	s.log.Info("mcp server restarted by operator", "server", name)
	return nil
}

// runWipe deletes the database after user confirmation.
func runWipe(dataDir, dbPath string) {
	fmt.Println("⚠  This will permanently delete ALL agent data:")
	fmt.Printf("   Database: %s\n", dbPath)
	fmt.Println("\nYour config, auth token, and workspace files are NOT affected.")
	fmt.Print("\nType 'yes' to confirm: ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input != "yes" {
		fmt.Println("Aborted.")
		return
	}

	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ All data wiped. The database will be recreated on next run.")
}

// runExport exports all session and message data to a JSON file.
func runExport(db *storage.DB, dataDir string) {
	exportPath := filepath.Join(dataDir, fmt.Sprintf("export-%s.json", time.Now().Format("20060102-150405")))

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)

	allSessions, err := sessions.List(10000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing sessions: %v\n", err)
		os.Exit(1)
	}

	type exportEntry struct {
		Session  interface{} `json:"session"`
		Messages interface{} `json:"messages"`
	}

	var entries []exportEntry
	for _, s := range allSessions {
		msgs, _ := messages.GetBySession(s.ID)
		entries = append(entries, exportEntry{Session: s, Messages: msgs})
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshaling data: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(exportPath, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "error writing export: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Exported %d sessions to %s\n", len(allSessions), exportPath)
}

// runStatus shows the agent's current configuration and state.
func runStatus(cfg *config.Config, dataDir string) {
	fmt.Println()
	fmt.Println("Agent Status")
	fmt.Println("────────────────────────────────")

	// Token
	tokenPath := filepath.Join(dataDir, "auth.token")
	tokenStatus := "not found"
	if data, err := os.ReadFile(tokenPath); err == nil {
		t := strings.TrimSpace(string(data))
		if len(t) >= 8 {
			tokenStatus = t[:4] + "..." + t[len(t)-4:]
		}
	}

	// DB size
	dbPath := filepath.Join(dataDir, "data.db")
	dbSize := "not found"
	if info, err := os.Stat(dbPath); err == nil {
		dbSize = fmt.Sprintf("%.1f KB", float64(info.Size())/1024)
	}

	fmt.Printf("  Provider:   %s\n", cfg.Model.Provider)
	fmt.Printf("  Model:      %s\n", cfg.Model.Model)
	fmt.Printf("  Gateway:    %s:%d\n", cfg.Gateway.Bind, cfg.Gateway.Port)
	fmt.Printf("  Token:      %s\n", tokenStatus)
	fmt.Printf("  DB Size:    %s\n", dbSize)
	fmt.Printf("  Data Dir:   %s\n", dataDir)
	fmt.Printf("  Log Level:  %s\n", cfg.Logging.Level)
	fmt.Println()
}

// runAuthRotate generates a new auth token.
func runAuthRotate(dataDir string) {
	newToken, err := gateway.RotateToken(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	preview := newToken[:4] + "..." + newToken[len(newToken)-4:]
	fmt.Printf("✓ Auth token rotated: %s\n", preview)
	fmt.Println("  Restart the server for the new token to take effect.")
	fmt.Printf("  Full token: %s\n", newToken)
}

// runAllowCmd approves or revokes a channel sender.
// Usage: openclio allow <adapter> <userID>
//
//	openclio deny  <adapter> <userID>
func runAllowCmd(args []string, dataDir string, cfg *config.Config, approve bool) {
	if len(args) < 2 {
		action := "allow"
		if !approve {
			action = "deny"
		}
		fmt.Fprintf(os.Stderr, "usage: agent %s <adapter> <userID>\n", action)
		fmt.Fprintf(os.Stderr, "example: agent %s telegram 123456789\n", action)
		os.Exit(1)
	}
	adapter, userID := args[0], args[1]
	al := plugin.NewAllowlist(dataDir, cfg.Channels.AllowAll)

	if approve {
		if err := al.Approve(adapter, userID); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Approved: %s / %s\n", adapter, userID)
		fmt.Println("  They can now interact with the agent.")
	} else {
		if err := al.Revoke(adapter, userID); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Revoked: %s / %s\n", adapter, userID)
		fmt.Println("  They will be blocked until re-approved.")
	}
}

// runAllowList prints the current approved sender list.
func runAllowList(dataDir string, cfg *config.Config) {
	al := plugin.NewAllowlist(dataDir, cfg.Channels.AllowAll)

	if cfg.Channels.AllowAll {
		fmt.Println("Allowlist mode: OFF (all senders permitted)")
		fmt.Println("Set 'channels.allow_all: false' in config to enable strict mode.")
		return
	}

	senders := al.List()
	if len(senders) == 0 {
		fmt.Println("No approved senders. Use 'openclio allow <adapter> <userID>' to add one.")
		return
	}

	fmt.Printf("\n%-12s  %s\n", "ADAPTER", "USER ID")
	fmt.Println("────────────  ─────────────────────────────────")
	for _, s := range senders {
		parts := strings.SplitN(s, ":", 2)
		if len(parts) == 2 {
			fmt.Printf("%-12s  %s\n", parts[0], parts[1])
		} else {
			fmt.Println(s)
		}
	}
	fmt.Println()
}

// runSkillsList prints all available markdown skills in the skills directory.
func runSkillsList(dataDir string) {
	skillsDir := filepath.Join(dataDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No skills found. Add .md files to ~/.openclio/skills/")
			return
		}
		fmt.Fprintf(os.Stderr, "error reading skills: %v\n", err)
		os.Exit(1)
	}

	var skills []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			skills = append(skills, strings.TrimSuffix(e.Name(), ".md"))
		}
	}

	if len(skills) == 0 {
		fmt.Println("No skills found. Add .md files to ~/.openclio/skills/")
		return
	}

	fmt.Println("\nAvailable Skills")
	fmt.Println("────────────────")
	for _, s := range skills {
		fmt.Printf("  • %s\n", s)
	}
	fmt.Println("\nTo use a skill in chat, type: /skill <name>")
}

// openBrowser launches the system default browser pointing at url.
// It runs in a goroutine so it never blocks startup.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", url}
	default: // linux and others
		cmd, args = "xdg-open", []string{url}
	}
	go func() {
		_ = exec.Command(cmd, args...).Start()
	}()
}

func registerMCPTools(registry *tools.Registry, servers []config.MCPServerConfig, log *internlog.Logger) ([]*mcp.Server, error) {
	if len(servers) == 0 {
		return nil, nil
	}
	builtInNames := make(map[string]struct{})
	for _, name := range registry.ListNames() {
		builtInNames[name] = struct{}{}
	}
	started := make([]*mcp.Server, 0, len(servers))
	for _, cfg := range servers {
		srv := mcp.NewServer(cfg.Name, cfg.Command, cfg.Args, cfg.Env)
		startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := srv.Start(startCtx)
		cancel()
		if err != nil {
			for _, running := range started {
				_ = running.Stop(context.Background())
			}
			return nil, fmt.Errorf("starting mcp server %q: %w", cfg.Name, err)
		}

		toolsCtx, toolsCancel := context.WithTimeout(context.Background(), 10*time.Second)
		decls, err := srv.ListTools(toolsCtx)
		toolsCancel()
		if err != nil {
			_ = srv.Stop(context.Background())
			for _, running := range started {
				_ = running.Stop(context.Background())
			}
			return nil, fmt.Errorf("listing tools from mcp server %q: %w", cfg.Name, err)
		}

		for _, t := range decls {
			if _, exists := builtInNames[t.Name]; exists {
				log.Warn("skipping mcp tool due to built-in name collision", "server", cfg.Name, "tool", t.Name)
				continue
			}
			if registry.HasTool(t.Name) {
				log.Warn("skipping mcp tool due to duplicate name", "server", cfg.Name, "tool", t.Name)
				continue
			}
			registry.Register(tools.NewMCPTool(cfg.Name, srv, t))
		}
		log.Info("mcp server connected", "server", cfg.Name, "tools", len(decls))
		started = append(started, srv)
	}
	return started, nil
}

func cronJobFromConfig(job config.CronJob) agentcron.Job {
	return agentcron.Job{
		Name:        job.Name,
		Schedule:    job.Schedule,
		Trigger:     job.Trigger,
		Prompt:      job.Prompt,
		Channel:     job.Channel,
		SessionMode: job.SessionMode,
		TimeoutSec:  job.TimeoutSec,
	}
}
