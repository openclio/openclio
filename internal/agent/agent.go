// Package agent implements the core agent loop — receiving messages,
// assembling context, calling LLM providers, executing tools, and
// streaming responses back.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/cost"
	"github.com/openclio/openclio/internal/workspace"
)

// ToolExecutor executes tool calls and returns results.
type ToolExecutor interface {
	Execute(ctx context.Context, name string, args json.RawMessage) (string, error)
	ListNames() []string
	HasTool(name string) bool
}

// ToolDefinitionProvider is an optional extension that supplies full tool
// metadata (description + JSON schema) for native provider function-calling.
type ToolDefinitionProvider interface {
	ToolDefinitions() []ToolDef
}

// Agent is the core agent that orchestrates LLM calls and tool execution.
type Agent struct {
	provider           Provider
	contextEngine      *agentctx.Engine
	toolExecutor       ToolExecutor
	cfg                config.AgentConfig
	contextCfg         config.ContextConfig
	model              string
	workspace            *workspace.Workspace // optional personalization
	costTracker          *cost.Tracker        // optional cost recording
	gitContext           string
	maxIterations        int
	dryRun               bool
	allowSystemAccess    bool // when true, file/exec tools can access user home (user must enable in config)
	providerMu         sync.RWMutex
	compactionInFlight sync.Map // sessionID -> struct{}
}

// SetDryRun enables or disables dry-run mode.
func (a *Agent) SetDryRun(dryRun bool) {
	a.dryRun = dryRun
}

// SetGitContext configures optional repository context to include in prompts.
func (a *Agent) SetGitContext(gitContext string) {
	a.gitContext = strings.TrimSpace(gitContext)
}

// SetProvider hot-swaps the active provider and model at runtime.
// It acquires an exclusive lock; concurrent Run/RunStream calls only hold read locks.
func (a *Agent) SetProvider(provider Provider, model string) {
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	a.provider = provider
	if strings.TrimSpace(model) != "" {
		a.model = strings.TrimSpace(model)
	}
}

func (a *Agent) providerSnapshot() (Provider, string) {
	a.providerMu.RLock()
	defer a.providerMu.RUnlock()
	return a.provider, a.model
}

// NewAgent creates a new agent with the given provider and context engine.
func NewAgent(provider Provider, contextEngine *agentctx.Engine, toolExecutor ToolExecutor, cfg config.AgentConfig, model string) *Agent {
	iters := cfg.MaxToolIterations
	if iters == 0 {
		iters = 10
	}
	return &Agent{
		provider:      provider,
		contextEngine: contextEngine,
		toolExecutor:  toolExecutor,
		cfg:           cfg,
		contextCfg:    config.DefaultConfig().Context,
		model:         model,
		maxIterations: iters,
	}
}

// NewAgentWithWorkspace creates an agent with workspace personalization and cost tracking.
// allowSystemAccess: when true, the agent's file/exec tools may access the user's home directory (user must enable in config).
func NewAgentWithWorkspace(
	provider Provider,
	contextEngine *agentctx.Engine,
	toolExecutor ToolExecutor,
	cfg config.AgentConfig,
	model string,
	ws *workspace.Workspace,
	tracker *cost.Tracker,
	allowSystemAccess bool,
) *Agent {
	iters := cfg.MaxToolIterations
	if iters == 0 {
		iters = 10
	}
	return &Agent{
		provider:           provider,
		contextEngine:     contextEngine,
		toolExecutor:      toolExecutor,
		cfg:                cfg,
		contextCfg:         config.DefaultConfig().Context,
		model:              model,
		workspace:          ws,
		costTracker:        tracker,
		maxIterations:      iters,
		allowSystemAccess:  allowSystemAccess,
	}
}

// ConfigureContext applies context-engine-related runtime settings.
func (a *Agent) ConfigureContext(cfg config.ContextConfig) {
	a.contextCfg = cfg
}

// Response is the final output of an agent run.
type Response struct {
	Text             string                     `json:"text"`
	Usage            TotalUsage                 `json:"usage"`
	ToolsUsed        []ToolRecord               `json:"tools_used,omitempty"`
	Iterations       int                        `json:"iterations"`
	Duration         time.Duration              `json:"duration"`
	AssembledContext *agentctx.AssembledContext `json:"assembled_context,omitempty"`
}

// TotalUsage tracks cumulative token usage across all LLM calls in one agent run.
type TotalUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	LLMCalls            int `json:"llm_calls"`
	CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// ToolRecord is a record of a tool call made during the agent run.
type ToolRecord struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Result    string          `json:"result"`
	Error     string          `json:"error,omitempty"`
	Duration  time.Duration   `json:"duration,omitempty"`
}

// CompactionMessage is a persisted message candidate for history compaction.
type CompactionMessage struct {
	ID      int64
	Role    string
	Content string
}

// CompactionProvider is an optional extension for providers that can
// retrieve and archive old messages.
type CompactionProvider interface {
	GetOldMessages(sessionID string, keepRecentTurns int) ([]CompactionMessage, error)
	ArchiveMessages(sessionID string, olderThanID int64) (int64, error)
	InsertCompactionSummary(sessionID, content string, tokens int) error
}

// MemoryWriter is an optional extension for semantic memory providers.
type MemoryWriter interface {
	AddMemory(content string) error
}

func (a *Agent) checkBudget(sessionID, systemPrompt, userMessage string) error {
	// Skip check if no limits configured
	if a.cfg.MaxTokensPerSession <= 0 && a.cfg.MaxTokensPerDay <= 0 {
		return nil
	}

	// Estimate immediate hit
	est := agentctx.EstimateTokens(systemPrompt) + agentctx.EstimateTokens(userMessage)

	if a.costTracker != nil {
		if a.cfg.MaxTokensPerSession > 0 {
			sessionSummary, err := a.costTracker.GetSummaryBySession(sessionID)
			if err == nil {
				if sessionSummary.InputTokens+sessionSummary.OutputTokens+est >= a.cfg.MaxTokensPerSession {
					return fmt.Errorf("budget exceeded: maximum tokens per session (%d) reached", a.cfg.MaxTokensPerSession)
				}
			}
		}

		if a.cfg.MaxTokensPerDay > 0 {
			daySummary, err := a.costTracker.GetSummary("today")
			if err == nil {
				if daySummary.InputTokens+daySummary.OutputTokens+est >= a.cfg.MaxTokensPerDay {
					return fmt.Errorf("budget exceeded: maximum tokens per day (%d) reached based on today's usage", a.cfg.MaxTokensPerDay)
				}
			}
		}
	}
	return nil
}

// Run executes the agent loop for a single user message.
//
// Flow:
//  1. Build system prompt
//  2. Assemble context (via context engine)
//  3. Call LLM provider
//  4. If tool calls → execute → add results → loop (max 10 iterations)
//  5. Return final text response with usage stats
func (a *Agent) Run(ctx context.Context, sessionID, userMessage string,
	msgProvider agentctx.MessageProvider, memProvider agentctx.MemoryProvider,
) (*Response, error) {
	start := time.Now()

	response := &Response{}
	provider, model := a.providerSnapshot()
	if provider == nil {
		response.Text = "Setup required: configure a model provider and API key first."
		response.Duration = time.Since(start)
		return response, nil
	}

	// Build tool definitions
	var toolDefs []ToolDef
	var toolNames []string
	var ctxToolDefs []agentctx.ToolDef
	if a.toolExecutor != nil {
		toolNames = a.toolExecutor.ListNames()
		if p, ok := a.toolExecutor.(ToolDefinitionProvider); ok {
			toolDefs = p.ToolDefinitions()
		}
		// For now, tools provide their names; full schemas will come with Dept 6
		for _, name := range toolNames {
			ctxToolDefs = append(ctxToolDefs, agentctx.ToolDef{
				Name: name,
			})
		}
	}

	// Build system prompt — inject workspace context if available
	identity := ""
	userCtx := ""
	assistantName := ""
	assistantIcon := ""
	if a.workspace != nil {
		a.workspace.RefreshIfChanged()
		identity = a.workspace.Identity
		userCtx = a.workspace.UserCtx
		assistantName = a.workspace.AssistantName
		assistantIcon = a.workspace.AssistantIcon
	}
	systemPrompt := BuildSystemPrompt(identity, userCtx, a.gitContext, toolNames, assistantName, assistantIcon, a.allowSystemAccess)

	// Pre-flight check: token budget
	if err := a.checkBudget(sessionID, systemPrompt, userMessage); err != nil {
		response.Text = err.Error()
		response.Duration = time.Since(start)
		return response, nil
	}

	// Assemble context
	assembled, err := a.contextEngine.Assemble(
		sessionID, userMessage, systemPrompt,
		ctxToolDefs, msgProvider, memProvider,
	)
	if err != nil {
		return nil, fmt.Errorf("assembling context: %w", err)
	}
	response.AssembledContext = assembled
	a.maybeTriggerCompaction(sessionID, assembled, msgProvider, memProvider)

	if a.dryRun {
		response.Text = "[Dry Run] Context assembled. API call skipped."
		response.Duration = time.Since(start)
		return response, nil
	}

	// Convert assembled context messages to agent messages
	var messages []Message
	for _, cm := range assembled.Messages {
		messages = append(messages, Message{
			Role:    cm.Role,
			Content: cm.Content,
		})
	}

	// Agent loop — iterate until we get a text response or hit max iterations
	for iteration := 0; iteration < a.maxIterations; iteration++ {
		response.Iterations = iteration + 1

		chatReq := ChatRequest{
			SystemPrompt: assembled.SystemPrompt,
			Messages:     messages,
			Tools:        toolDefs,
			MaxTokens:    4096,
			Model:        model,
		}

		// Call LLM with automatic retry on transient errors (timeouts, 429, 5xx).
		var chatResp *ChatResponse
		err = RetryWithBackoff(ctx, DefaultRetryConfig, func() error {
			var callErr error
			chatResp, callErr = provider.Chat(ctx, chatReq)
			return callErr
		})
		if err != nil {
			if IsRateLimit(err) {
				return nil, ErrProviderRateLimit.Wrap(fmt.Errorf("iteration %d: %w", iteration, err))
			}
			return nil, ErrProviderUnavailable.Wrap(fmt.Errorf("iteration %d: %w", iteration, err))
		}

		// Track usage
		response.Usage.InputTokens += chatResp.Usage.InputTokens
		response.Usage.OutputTokens += chatResp.Usage.OutputTokens
		response.Usage.CacheReadTokens += chatResp.Usage.CacheReadTokens
		response.Usage.CacheCreationTokens += chatResp.Usage.CacheCreationTokens
		response.Usage.LLMCalls++

		// Record cost
		if a.costTracker != nil {
			a.costTracker.Record(sessionID, provider.Name(), model,
				chatResp.Usage.InputTokens, chatResp.Usage.OutputTokens)
		}

		// If no tool calls, we're done
		if len(chatResp.ToolCalls) == 0 {
			response.Text = chatResp.Content
			response.Duration = time.Since(start)
			return response, nil
		}

		// Add assistant message with tool calls
		messages = append(messages, Message{
			Role:      "assistant",
			Content:   chatResp.Content,
			ToolCalls: chatResp.ToolCalls,
		})

		// Execute tool calls
		if a.toolExecutor == nil {
			response.Text = chatResp.Content
			response.Duration = time.Since(start)
			return response, nil
		}

		for _, tc := range chatResp.ToolCalls {
			record := ToolRecord{
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}

			// Validate that the tool exists before trying to execute it
			if !a.toolExecutor.HasTool(tc.Name) {
				record.Error = fmt.Sprintf("Tool %s not found or registered", tc.Name)
				record.Result = fmt.Sprintf("Error: the tool '%s' does not exist. Please use only available tools.", tc.Name)
			} else {
				// Execute the tool, checking for context cancellation
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}

				toolStart := time.Now()
				result, err := a.toolExecutor.Execute(ctx, tc.Name, tc.Arguments)
				record.Duration = time.Since(toolStart)

				if err != nil {
					record.Error = err.Error()
					record.Result = fmt.Sprintf("Error: %v", err)
				} else {
					record.Result = result
				}
			}

			response.ToolsUsed = append(response.ToolsUsed, record)

			// Wrap result in prompt injection isolation delimiters before
			// re-injecting into the conversation context
			wrappedResult := WrapToolResult(tc.Name, record.Result)

			// Add tool result to messages for next iteration
			messages = append(messages, Message{
				Role:       "tool",
				Content:    wrappedResult,
				ToolCallID: tc.ID,
			})
		}
	}

	// Hit max iterations — return whatever we have
	response.Text = "[Agent reached maximum tool iterations. Last response may be incomplete.]"
	response.Duration = time.Since(start)
	return response, nil
}

// RunStream executes one agent call and emits token chunks incrementally.
// If the provider does not support streaming, it transparently falls back to Run.
func (a *Agent) RunStream(
	ctx context.Context,
	sessionID, userMessage string,
	msgProvider agentctx.MessageProvider,
	memProvider agentctx.MemoryProvider,
	onToken func(string),
	onToolCall func(string, string),
) (*Response, error) {
	provider, model := a.providerSnapshot()
	if provider == nil {
		return &Response{
			Text:     "Setup required: configure a model provider and API key first.",
			Duration: 0,
		}, nil
	}
	streamer, canStream := provider.(Streamer)
	if !canStream {
		resp, err := a.Run(ctx, sessionID, userMessage, msgProvider, memProvider)
		if err != nil {
			return nil, err
		}
		if onToken != nil && resp.Text != "" {
			onToken(resp.Text)
		}
		if onToolCall != nil {
			for _, tr := range resp.ToolsUsed {
				onToolCall(tr.Name, tr.Result)
			}
		}
		return resp, nil
	}

	start := time.Now()
	response := &Response{Iterations: 1}

	var toolNames []string
	var ctxToolDefs []agentctx.ToolDef
	var toolDefs []ToolDef
	if a.toolExecutor != nil {
		toolNames = a.toolExecutor.ListNames()
		if p, ok := a.toolExecutor.(ToolDefinitionProvider); ok {
			toolDefs = p.ToolDefinitions()
		}
		for _, name := range toolNames {
			ctxToolDefs = append(ctxToolDefs, agentctx.ToolDef{Name: name})
		}
	}

	identity := ""
	userCtx := ""
	assistantName := ""
	assistantIcon := ""
	if a.workspace != nil {
		a.workspace.RefreshIfChanged()
		identity = a.workspace.Identity
		userCtx = a.workspace.UserCtx
		assistantName = a.workspace.AssistantName
		assistantIcon = a.workspace.AssistantIcon
	}
	systemPrompt := BuildSystemPrompt(identity, userCtx, a.gitContext, toolNames, assistantName, assistantIcon, a.allowSystemAccess)

	if err := a.checkBudget(sessionID, systemPrompt, userMessage); err != nil {
		response.Text = err.Error()
		response.Duration = time.Since(start)
		return response, nil
	}

	assembled, err := a.contextEngine.Assemble(
		sessionID, userMessage, systemPrompt,
		ctxToolDefs, msgProvider, memProvider,
	)
	if err != nil {
		return nil, fmt.Errorf("assembling context for stream: %w", err)
	}
	response.AssembledContext = assembled
	a.maybeTriggerCompaction(sessionID, assembled, msgProvider, memProvider)

	if a.dryRun {
		response.Text = "[Dry Run] Context assembled. API call skipped."
		response.Duration = time.Since(start)
		return response, nil
	}

	var messages []Message
	for _, cm := range assembled.Messages {
		messages = append(messages, Message{Role: cm.Role, Content: cm.Content})
	}

	// Agentic streaming loop — mirrors Run() but streams text tokens in real-time.
	var full strings.Builder
	for iteration := 0; iteration < a.maxIterations; iteration++ {
		response.Iterations = iteration + 1
		full.Reset()

		chatReq := ChatRequest{
			SystemPrompt: assembled.SystemPrompt,
			Messages:     messages,
			Tools:        toolDefs,
			MaxTokens:    4096,
			Model:        model,
		}

		ch, err := streamer.Stream(ctx, chatReq)
		if err != nil {
			return nil, ErrProviderUnavailable.Wrap(fmt.Errorf("streaming call failed: %w", err))
		}

		var toolCalls []ToolCall
		for chunk := range ch {
			if chunk.Error != nil {
				if IsRateLimit(chunk.Error) {
					return nil, ErrProviderRateLimit.Wrap(chunk.Error)
				}
				return nil, ErrProviderUnavailable.Wrap(chunk.Error)
			}
			if chunk.Text != "" {
				full.WriteString(chunk.Text)
				if onToken != nil {
					onToken(chunk.Text)
				}
			}
			if len(chunk.ToolCalls) > 0 {
				toolCalls = append(toolCalls, chunk.ToolCalls...)
			}
			if chunk.Done {
				break
			}
		}

		response.Usage.LLMCalls++
		// Streaming APIs may omit usage counters; use deterministic estimates.
		response.Usage.InputTokens += assembled.Stats.TotalTokens
		response.Usage.OutputTokens += agentctx.EstimateTokens(full.String())

		if a.costTracker != nil {
			a.costTracker.Record(sessionID, provider.Name(), model,
				assembled.Stats.TotalTokens, agentctx.EstimateTokens(full.String()))
		}

		// No tool calls — streaming is done.
		if len(toolCalls) == 0 || a.toolExecutor == nil {
			break
		}

		// Add the assistant turn (with tool calls) to the conversation.
		messages = append(messages, Message{
			Role:      "assistant",
			Content:   full.String(),
			ToolCalls: toolCalls,
		})

		// Execute each tool call and append the result for the next LLM turn.
		for _, tc := range toolCalls {
			record := ToolRecord{
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}

			if !a.toolExecutor.HasTool(tc.Name) {
				record.Error = fmt.Sprintf("Tool %s not found or registered", tc.Name)
				record.Result = fmt.Sprintf("Error: the tool '%s' does not exist. Please use only available tools.", tc.Name)
			} else {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
				toolStart := time.Now()
				result, err := a.toolExecutor.Execute(ctx, tc.Name, tc.Arguments)
				record.Duration = time.Since(toolStart)
				if err != nil {
					record.Error = err.Error()
					record.Result = fmt.Sprintf("Error: %v", err)
				} else {
					record.Result = result
				}
			}

			response.ToolsUsed = append(response.ToolsUsed, record)

			wrappedResult := WrapToolResult(tc.Name, record.Result)
			messages = append(messages, Message{
				Role:       "tool",
				Content:    wrappedResult,
				ToolCallID: tc.ID,
			})

			if onToolCall != nil {
				onToolCall(record.Name, record.Result)
			}
		}
	}

	response.Text = full.String()
	response.Duration = time.Since(start)
	return response, nil
}

// Provider returns the underlying LLM provider.
// Used by the gateway to type-assert against the Streamer interface.
func (a *Agent) Provider() Provider {
	p, _ := a.providerSnapshot()
	return p
}

// ToolNames returns the currently registered tool names.
func (a *Agent) ToolNames() []string {
	if a.toolExecutor == nil {
		return nil
	}
	names := a.toolExecutor.ListNames()
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// BuildStreamRequest builds a ChatRequest suitable for streaming.
// It assembles context the same way Run does, but returns the request
// instead of executing the full agent loop.
func (a *Agent) BuildStreamRequest(
	ctx context.Context,
	sessionID, userMessage string,
	msgProvider agentctx.MessageProvider,
) (*ChatRequest, error) {
	_, model := a.providerSnapshot()

	var toolNames []string
	var ctxToolDefs []agentctx.ToolDef
	var toolDefs []ToolDef
	if a.toolExecutor != nil {
		toolNames = a.toolExecutor.ListNames()
		if p, ok := a.toolExecutor.(ToolDefinitionProvider); ok {
			toolDefs = p.ToolDefinitions()
		}
		for _, name := range toolNames {
			ctxToolDefs = append(ctxToolDefs, agentctx.ToolDef{Name: name})
		}
	}

	identity := ""
	userCtx := ""
	assistantName := ""
	assistantIcon := ""
	if a.workspace != nil {
		a.workspace.RefreshIfChanged()
		identity = a.workspace.Identity
		userCtx = a.workspace.UserCtx
		assistantName = a.workspace.AssistantName
		assistantIcon = a.workspace.AssistantIcon
	}
	systemPrompt := BuildSystemPrompt(identity, userCtx, a.gitContext, toolNames, assistantName, assistantIcon, a.allowSystemAccess)

	assembled, err := a.contextEngine.Assemble(
		sessionID, userMessage, systemPrompt,
		ctxToolDefs, msgProvider, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("assembling context for stream: %w", err)
	}

	var messages []Message
	for _, cm := range assembled.Messages {
		messages = append(messages, Message{Role: cm.Role, Content: cm.Content})
	}

	return &ChatRequest{
		SystemPrompt: assembled.SystemPrompt,
		Messages:     messages,
		Tools:        toolDefs,
		MaxTokens:    4096,
		Model:        model,
	}, nil
}

func (a *Agent) maybeTriggerCompaction(
	sessionID string,
	assembled *agentctx.AssembledContext,
	msgProvider agentctx.MessageProvider,
	memProvider agentctx.MemoryProvider,
) {
	if assembled == nil || assembled.Stats.BudgetTotal <= 0 {
		return
	}
	threshold := a.contextCfg.ProactiveCompaction
	if threshold <= 0 {
		return
	}
	usageRatio := float64(assembled.Stats.TotalTokens) / float64(assembled.Stats.BudgetTotal)
	if usageRatio < threshold {
		return
	}
	if _, loaded := a.compactionInFlight.LoadOrStore(sessionID, struct{}{}); loaded {
		return // compaction already running for this session
	}

	go func() {
		defer a.compactionInFlight.Delete(sessionID)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := a.runCompaction(ctx, sessionID, msgProvider, memProvider); err != nil {
			fmt.Printf("warning: proactive compaction failed for session %s: %v\n", sessionID, err)
		}
	}()
}

// ForceCompaction runs compaction immediately and returns the result synchronously.
func (a *Agent) ForceCompaction(
	ctx context.Context,
	sessionID string,
	msgProvider agentctx.MessageProvider,
	memProvider agentctx.MemoryProvider,
) error {
	return a.runCompaction(ctx, sessionID, msgProvider, memProvider)
}

func (a *Agent) runCompaction(
	ctx context.Context,
	sessionID string,
	msgProvider agentctx.MessageProvider,
	memProvider agentctx.MemoryProvider,
) error {
	compactable, ok := msgProvider.(CompactionProvider)
	if !ok {
		return nil
	}

	keepRecentTurns := a.contextCfg.CompactionKeepRecent
	if keepRecentTurns <= 0 {
		keepRecentTurns = 5
	}
	oldMessages, err := compactable.GetOldMessages(sessionID, keepRecentTurns)
	if err != nil {
		return fmt.Errorf("loading old messages: %w", err)
	}
	if len(oldMessages) == 0 {
		return nil
	}

	summaryReq := ChatRequest{
		SystemPrompt: "Summarize this conversation history in 5 concise bullet points. Preserve decisions, preferences, and important facts.",
		Messages: []Message{
			{Role: "user", Content: formatCompactionMessages(oldMessages)},
		},
		MaxTokens: 400,
		Model:     a.compactionModel(),
	}

	provider, _ := a.providerSnapshot()
	if provider == nil {
		return fmt.Errorf("setup required: model provider is not configured")
	}
	summaryResp, err := provider.Chat(ctx, summaryReq)
	if err != nil {
		return fmt.Errorf("summarizing history: %w", err)
	}
	summary := strings.TrimSpace(summaryResp.Content)
	if summary == "" {
		return nil
	}

	summaryBlock := "[Compacted history]\n" + summary
	if err := compactable.InsertCompactionSummary(sessionID, summaryBlock, agentctx.EstimateTokens(summaryBlock)); err != nil {
		return fmt.Errorf("saving compaction summary: %w", err)
	}

	// Optional semantic memory sink.
	if mw, ok := memProvider.(MemoryWriter); ok {
		_ = mw.AddMemory(summaryBlock)
	}

	_, err = compactable.ArchiveMessages(sessionID, oldMessages[len(oldMessages)-1].ID)
	if err != nil {
		return fmt.Errorf("archiving compacted messages: %w", err)
	}
	return nil
}

func (a *Agent) compactionModel() string {
	if strings.TrimSpace(a.contextCfg.CompactionModel) != "" {
		return strings.TrimSpace(a.contextCfg.CompactionModel)
	}
	provider, model := a.providerSnapshot()
	if preferred := compactionModelFromProvider(provider); preferred != "" {
		return preferred
	}
	return strings.TrimSpace(model)
}

// compactionModelFromProvider picks the safest low-cost model from wrapped
// provider stacks for history compaction.
func compactionModelFromProvider(p Provider) string {
	switch v := p.(type) {
	case *providerWithRouter:
		if v.router != nil {
			if m := strings.TrimSpace(v.router.cfg.PrivacyModel); m != "" {
				return m
			}
			if m := strings.TrimSpace(v.router.cfg.CheapModel); m != "" {
				return m
			}
			if m := strings.TrimSpace(v.router.cfg.MidModel); m != "" {
				return m
			}
			if m := strings.TrimSpace(v.router.cfg.ExpensiveModel); m != "" {
				return m
			}
		}
		return compactionModelFromProvider(v.provider)
	case *streamerWithRouter:
		if v.providerWithRouter != nil {
			return compactionModelFromProvider(v.providerWithRouter)
		}
		return ""
	case *providerWithModel:
		if m := compactionModelFromProvider(v.provider); m != "" {
			return m
		}
		return strings.TrimSpace(v.model)
	case *streamerWithModel:
		if v.providerWithModel != nil {
			return compactionModelFromProvider(v.providerWithModel)
		}
		return ""
	case *FailoverProvider:
		for _, candidate := range v.providers {
			if m := compactionModelFromProvider(candidate); m != "" {
				return m
			}
		}
		return ""
	default:
		return ""
	}
}

func formatCompactionMessages(msgs []CompactionMessage) string {
	var b strings.Builder
	for _, msg := range msgs {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		b.WriteString("[")
		b.WriteString(msg.Role)
		b.WriteString("] ")
		b.WriteString(content)
		b.WriteString("\n")
	}
	return b.String()
}
