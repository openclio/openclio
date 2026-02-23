// Package cli provides the interactive terminal chat interface.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/cost"
	"github.com/openclio/openclio/internal/storage"
)

// totalUsage tracks cumulative token usage across the session.
type totalUsage struct {
	inputTokens  int
	outputTokens int
	llmCalls     int
}

// CLI is the interactive terminal chat interface.
type CLI struct {
	agent         *agent.Agent
	sessions      *storage.SessionStore
	messages      *storage.MessageStore
	contextEngine *agentctx.Engine
	costTracker   *cost.Tracker
	sessionID     string
	provider      string
	model         string
	totalUsage    totalUsage
	scannerBuf    int
	// For /skill display
	workspaceName string
	cronJobs      []string

	// For /debug display
	lastContext *agentctx.AssembledContext
}

// NewCLI creates a new CLI instance.
func NewCLI(
	agentInstance *agent.Agent,
	sessions *storage.SessionStore,
	messages *storage.MessageStore,
	contextEngine *agentctx.Engine,
	costTracker *cost.Tracker,
	cfg config.CLIConfig,
	provider, model string,
	workspaceName string,
	cronJobs []string,
) *CLI {
	buf := cfg.ScannerBuffer
	if buf == 0 {
		buf = 64 * 1024
	}
	return &CLI{
		agent:         agentInstance,
		sessions:      sessions,
		messages:      messages,
		contextEngine: contextEngine,
		costTracker:   costTracker,
		provider:      provider,
		model:         model,
		scannerBuf:    buf,
		workspaceName: workspaceName,
		cronJobs:      cronJobs,
	}
}

// Run starts the interactive REPL loop.
func (c *CLI) Run() error {
	// Create a new session
	session, err := c.sessions.Create("cli", "local")
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	c.sessionID = session.ID

	PrintWelcome(c.sessionID, c.provider, c.model)

	scanner := bufio.NewScanner(os.Stdin)
	// Increase scanner buffer for long inputs
	scanner.Buffer(make([]byte, 0, c.scannerBuf), c.scannerBuf)

	for {
		fmt.Printf("%s> %s", colorBold(), colorReset())
		if !scanner.Scan() {
			break // EOF or Ctrl+D
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Exit commands
		if input == "exit" || input == "quit" {
			PrintInfo("Goodbye! 👋")
			return nil
		}

		// Slash commands
		if c.HandleCommand(input) {
			continue
		}

		// Send message to agent
		c.chat(input)
	}

	// Check for scanner error (e.g. read error, not just EOF)
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input error: %w", err)
	}

	fmt.Println()
	PrintInfo("Goodbye! 👋")
	return nil
}

func (c *CLI) chat(userMessage string) {
	// Store user message
	userTokens := agentctx.EstimateTokens(userMessage)
	if _, err := c.messages.Insert(c.sessionID, "user", userMessage, userTokens); err != nil {
		PrintError("Failed to store message: " + err.Error())
		return
	}

	// Update session activity
	c.sessions.UpdateLastActive(c.sessionID)

	// Create message provider
	msgProvider := &cliMessageProvider{
		messages:  c.messages,
		sessionID: c.sessionID,
	}

	// Run agent with a timeout — prevents hanging forever on slow LLM responses (#11)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := c.agent.Run(ctx, c.sessionID, userMessage, msgProvider, nil)
	if err != nil {
		PrintError("Agent error: " + err.Error())
		return
	}

	// Store for /debug commands
	c.lastContext = resp.AssembledContext

	// Display tool calls
	for _, tc := range resp.ToolsUsed {
		PrintToolCall(tc.Name, string(tc.Arguments), tc.Result, tc.Error)
	}

	// Display response
	PrintAssistant(resp.Text)

	// Display usage
	PrintUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.LLMCalls, resp.Duration.Milliseconds())

	// Store assistant response
	assistantTokens := agentctx.EstimateTokens(resp.Text)
	c.messages.Insert(c.sessionID, "assistant", resp.Text, assistantTokens)

	// Track cumulative usage
	c.totalUsage.inputTokens += resp.Usage.InputTokens
	c.totalUsage.outputTokens += resp.Usage.OutputTokens
	c.totalUsage.llmCalls += resp.Usage.LLMCalls
}

// cliMessageProvider adapts storage to the context engine interface.
type cliMessageProvider struct {
	messages  *storage.MessageStore
	sessionID string
}

func (p *cliMessageProvider) GetRecentMessages(sessionID string, limit int) ([]agentctx.ContextMessage, error) {
	msgs, err := p.messages.GetRecent(sessionID, limit)
	if err != nil {
		return nil, err
	}
	var result []agentctx.ContextMessage
	for _, m := range msgs {
		result = append(result, agentctx.ContextMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return result, nil
}

func (p *cliMessageProvider) GetStoredEmbeddings(sessionID string) ([]agentctx.StoredEmbedding, error) {
	msgs, err := p.messages.GetEmbeddings(sessionID)
	if err != nil {
		return nil, err
	}
	var result []agentctx.StoredEmbedding
	for _, m := range msgs {
		result = append(result, agentctx.StoredEmbedding{
			MessageID: m.ID,
			SessionID: m.SessionID,
			Role:      m.Role,
			Content:   m.Content,
			Summary:   m.Summary,
			Tokens:    m.Tokens,
			Embedding: m.Embedding,
		})
	}
	return result, nil
}

func (p *cliMessageProvider) SearchKnowledge(query, nodeType string, limit int) ([]agentctx.KnowledgeNode, error) {
	nodes, err := p.messages.SearchKnowledge(query, nodeType, limit)
	if err != nil {
		return nil, err
	}
	out := make([]agentctx.KnowledgeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, agentctx.KnowledgeNode{
			ID:         n.ID,
			Type:       n.Type,
			Name:       n.Name,
			Confidence: n.Confidence,
		})
	}
	return out, nil
}

func (p *cliMessageProvider) GetOldMessages(sessionID string, keepRecentTurns int) ([]agent.CompactionMessage, error) {
	msgs, err := p.messages.GetOldMessages(sessionID, keepRecentTurns)
	if err != nil {
		return nil, err
	}
	result := make([]agent.CompactionMessage, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, agent.CompactionMessage{
			ID:      m.ID,
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return result, nil
}

func (p *cliMessageProvider) ArchiveMessages(sessionID string, olderThanID int64) (int64, error) {
	return p.messages.ArchiveMessages(sessionID, olderThanID)
}

func (p *cliMessageProvider) InsertCompactionSummary(sessionID, content string, tokens int) error {
	_, err := p.messages.Insert(sessionID, "system", content, tokens)
	return err
}
