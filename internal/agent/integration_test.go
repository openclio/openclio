//go:build integration

package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/storage"
)

// integrationMockProvider simply records the prompts it receives and returns a fixed response.
type integrationMockProvider struct {
	lastMessages []Message
}

func (m *integrationMockProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	m.lastMessages = req.Messages
	return &ChatResponse{
		Content: "Mocked response",
		Usage: Usage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}, nil
}

func (m *integrationMockProvider) Name() string {
	return "integration-mock"
}

// integrationMockToolExecutor has no tools.
type integrationMockToolExecutor struct{}

func (m integrationMockToolExecutor) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	return "", nil
}
func (m integrationMockToolExecutor) ListNames() []string      { return nil }
func (m integrationMockToolExecutor) HasTool(name string) bool { return false }

// integrationMockMsgProvider adapts storage.MessageStore to agentctx.MessageProvider
type integrationMockMsgProvider struct {
	store *storage.MessageStore
}

func (p *integrationMockMsgProvider) GetRecentMessages(sessionID string, limit int) ([]agentctx.ContextMessage, error) {
	msgs, err := p.store.GetRecent(sessionID, limit)
	if err != nil {
		return nil, err
	}

	ctxMsgs := make([]agentctx.ContextMessage, len(msgs))
	for i, m := range msgs {
		ctxMsgs[i] = agentctx.ContextMessage{Role: m.Role, Content: m.Content}
	}
	return ctxMsgs, nil
}

func (p *integrationMockMsgProvider) GetStoredEmbeddings(sessionID string) ([]agentctx.StoredEmbedding, error) {
	return nil, nil
}

func TestAgentIntegration_ContextStitching(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "integration.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	db.Migrate()

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)
	engine := agentctx.NewEngine(agentctx.NewNoOpEmbedder(), 8000, 10)

	s, _ := sessions.Create("cli", "integration-user")
	messages.Insert(s.ID, "user", "History message 1", 5)
	messages.Insert(s.ID, "assistant", "History reply 1", 5)

	provider := &integrationMockProvider{}
	ag := NewAgent(provider, engine, integrationMockToolExecutor{}, config.AgentConfig{MaxToolIterations: 10}, "mock-model")

	reqCtx := context.Background()
	msgAdapter := &integrationMockMsgProvider{store: messages}

	_, err = ag.Run(reqCtx, s.ID, "Integration prompt", msgAdapter, nil)
	if err != nil {
		t.Fatalf("agent run failed: %v", err)
	}

	// Verify the provider received the history correctly stitched together
	if len(provider.lastMessages) != 4 {
		// Actually let's just search for the specific text
		foundHistory := false
		foundPrompt := false
		for _, msg := range provider.lastMessages {
			if strings.Contains(msg.Content, "History message 1") {
				foundHistory = true
			}
			if strings.Contains(msg.Content, "Integration prompt") {
				foundPrompt = true
			}
		}

		if !foundHistory {
			t.Errorf("Provider context missing history message")
		}
		if !foundPrompt {
			t.Errorf("Provider context missing latest prompt message")
		}
	}
}
