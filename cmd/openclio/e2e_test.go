//go:build e2e

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/cost"
	"github.com/openclio/openclio/internal/gateway"
	"github.com/openclio/openclio/internal/storage"
	"github.com/openclio/openclio/internal/tools"
)

// e2eMockProvider simply returns a fixed response.
type e2eMockProvider struct{}

func (m *e2eMockProvider) Chat(ctx context.Context, req agent.ChatRequest) (*agent.ChatResponse, error) {
	return &agent.ChatResponse{
		Content: "E2E OK",
		Usage:   agent.Usage{InputTokens: 5, OutputTokens: 5},
	}, nil
}
func (m *e2eMockProvider) Name() string { return "e2e-mock" }

func TestE2E_API(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	db.Migrate()

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)
	engine := agentctx.NewEngine(agentctx.NewNoOpEmbedder(), 8000, 10)
	cfg := config.DefaultConfig()

	registry := tools.NewRegistry(cfg.Tools, "", "")

	ag := agent.NewAgent(&e2eMockProvider{}, engine, registry, cfg.Agent, "e2e-model")
	tracker := cost.NewTracker(db)

	handlers := gateway.NewHandlers(ag, sessions, messages, engine, tracker, cfg)

	// Create token
	tokenFile := filepath.Join(t.TempDir(), "auth_token")
	os.WriteFile(tokenFile, []byte("e2e-secret"), 0600)

	authMiddleware := gateway.AuthMiddleware("e2e-secret")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", handlers.Health)

	// Protected endpoints
	mux.Handle("/api/v1/metrics", authMiddleware(http.HandlerFunc(handlers.Metrics)))

	server := httptest.NewServer(mux)
	defer server.Close()

	// 1. Health check (No auth needed)
	resp, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 Health, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Metrics without auth (should 401)
	resp, err = http.Get(server.URL + "/api/v1/metrics")
	if err != nil {
		t.Fatalf("metrics check failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Metrics without auth, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. Metrics with auth
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer e2e-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("metrics auth check failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 Metrics, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
