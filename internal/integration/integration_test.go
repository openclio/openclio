//go:build integration

// Package integration contains integration tests that exercise multiple
// modules together with a real SQLite database.
//
// Run with:
//
//	go test -race -tags=integration ./internal/integration/...
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/cost"
	"github.com/openclio/openclio/internal/gateway"
	"github.com/openclio/openclio/internal/storage"
)

// ── Mock LLM Provider ────────────────────────────────────────────────────────

type mockProvider struct {
	response string
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Chat(_ context.Context, _ agent.ChatRequest) (*agent.ChatResponse, error) {
	return &agent.ChatResponse{
		Content: m.response,
		Usage:   agent.Usage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

// slowProvider simulates a provider that always times out
type slowProvider struct{}

func (s *slowProvider) Name() string { return "slow" }
func (s *slowProvider) Chat(ctx context.Context, _ agent.ChatRequest) (*agent.ChatResponse, error) {
	select {
	case <-time.After(10 * time.Second):
		return &agent.ChatResponse{Content: "done"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ── Test helpers ──────────────────────────────────────────────────────────────

func newTestDB(t *testing.T) (*storage.DB, func()) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, func() { db.Close() }
}

func newTestEngine() *agentctx.Engine {
	return agentctx.NewEngine(agentctx.NewNoOpEmbedder(), 4000, 5)
}

func newTestGateway(t *testing.T, db *storage.DB, response, token string) (*httptest.Server, func()) {
	t.Helper()
	engine := newTestEngine()
	prov := &mockProvider{response: response}
	a := agent.NewAgent(prov, engine, nil, config.AgentConfig{}, "mock")
	cfg := config.GatewayConfig{Port: 0, Bind: "127.0.0.1"}
	fullCfg := config.DefaultConfig()
	tracker := cost.NewTracker(db)
	srv := gateway.NewServer(cfg, fullCfg, a, db, engine, tracker, token)
	ts := httptest.NewServer(srv.Handler())
	return ts, ts.Close
}

// testMsgProvider adapts MessageStore to agentctx.MessageProvider
type testMsgProvider struct {
	messages  *storage.MessageStore
	sessionID string
}

func (p *testMsgProvider) GetRecentMessages(sessionID string, limit int) ([]agentctx.ContextMessage, error) {
	msgs, err := p.messages.GetRecent(sessionID, limit)
	if err != nil {
		return nil, err
	}
	var result []agentctx.ContextMessage
	for _, m := range msgs {
		result = append(result, agentctx.ContextMessage{Role: m.Role, Content: m.Content})
	}
	return result, nil
}

func (p *testMsgProvider) GetStoredEmbeddings(sessionID string) ([]agentctx.StoredEmbedding, error) {
	return nil, nil
}

// ── Agent integration tests ───────────────────────────────────────────────────

func TestAgentRun_BasicResponse(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	engine := newTestEngine()
	provider := &mockProvider{response: "Hello from the mock LLM!"}
	a := agent.NewAgent(provider, engine, nil, config.AgentConfig{}, "mock-model")

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)
	session, err := sessions.Create("test", "user1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	msgProvider := &testMsgProvider{messages: messages, sessionID: session.ID}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := a.Run(ctx, session.ID, "Hello!", msgProvider, nil)
	if err != nil {
		t.Fatalf("agent.Run: %v", err)
	}
	if resp.Text != "Hello from the mock LLM!" {
		t.Errorf("unexpected response: %q", resp.Text)
	}
	if resp.Usage.InputTokens == 0 {
		t.Error("expected non-zero input token count")
	}
}

func TestAgentRun_MessagesPersisted(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	engine := newTestEngine()
	provider := &mockProvider{response: "Stored response"}
	a := agent.NewAgent(provider, engine, nil, config.AgentConfig{}, "mock-model")

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)
	session, _ := sessions.Create("test", "user1")
	msgProvider := &testMsgProvider{messages: messages, sessionID: session.ID}

	// Pre-store user message (as the gateway/CLI does)
	messages.Insert(session.ID, "user", "What is Go?", 5)

	resp, err := a.Run(context.Background(), session.ID, "What is Go?", msgProvider, nil)
	if err != nil {
		t.Fatalf("agent.Run: %v", err)
	}

	// Post-store assistant response
	messages.Insert(session.ID, "assistant", resp.Text, 5)

	stored, err := messages.GetBySession(session.ID)
	if err != nil {
		t.Fatalf("GetBySession: %v", err)
	}
	if len(stored) < 2 {
		t.Errorf("expected ≥2 stored messages, got %d", len(stored))
	}
}

func TestAgentRun_ContextCancellation(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	engine := newTestEngine()
	a := agent.NewAgent(&slowProvider{}, engine, nil, config.AgentConfig{}, "mock-model")

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)
	session, _ := sessions.Create("test", "user1")
	msgProvider := &testMsgProvider{messages: messages, sessionID: session.ID}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := a.Run(ctx, session.ID, "Hello", msgProvider, nil)
	if err == nil {
		t.Error("expected error from context cancellation, got nil")
	}
}

// ── Gateway HTTP integration tests ────────────────────────────────────────────

func TestGateway_HealthEndpoint(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	ts, stop := newTestGateway(t, db, "ok", "")
	defer stop()

	resp, err := http.Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("health GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGateway_ChatAuth(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	const token = "secret-token"
	ts, stop := newTestGateway(t, db, "auth test", token)
	defer stop()

	// Without auth → 401
	resp, _ := http.Post(ts.URL+"/api/v1/chat", "application/json",
		bytes.NewBufferString(`{"message":"hello"}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.StatusCode)
	}

	// With wrong auth → 401
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/chat",
		bytes.NewBufferString(`{"message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// With correct auth → 200
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/chat",
		bytes.NewBufferString(`{"message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with valid token, got %d", resp.StatusCode)
	}
}

func TestGateway_ChatBodyLimit(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	ts, stop := newTestGateway(t, db, "ok", "tok")
	defer stop()

	// 11MB body — should be rejected (body limit is 10MB)
	big := bytes.Repeat([]byte("x"), 11*1024*1024)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/chat", bytes.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 400 or 413 for oversized body, got %d", resp.StatusCode)
	}
}

func TestGateway_SessionCRUD(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	const token = "tok"
	ts, stop := newTestGateway(t, db, "session test", token)
	defer stop()

	authHdr := "Bearer " + token
	client := &http.Client{}

	// Create session implicitly via chat
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/chat",
		bytes.NewBufferString(`{"message":"start session"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHdr)
	resp, _ := client.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat returned %d", resp.StatusCode)
	}

	var chatResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&chatResp)
	resp.Body.Close()
	sessionID, _ := chatResp["session_id"].(string)
	if sessionID == "" {
		t.Fatal("no session_id in response")
	}

	// List sessions
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/sessions", nil)
	req.Header.Set("Authorization", authHdr)
	resp, _ = client.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("list sessions returned %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Get session
	req, _ = http.NewRequest("GET", fmt.Sprintf("%s/api/v1/sessions/%s", ts.URL, sessionID), nil)
	req.Header.Set("Authorization", authHdr)
	resp, _ = client.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("get session returned %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Delete session
	req, _ = http.NewRequest("DELETE", fmt.Sprintf("%s/api/v1/sessions/%s", ts.URL, sessionID), nil)
	req.Header.Set("Authorization", authHdr)
	resp, _ = client.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("delete session returned %d", resp.StatusCode)
	}
	resp.Body.Close()
}
