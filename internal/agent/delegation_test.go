package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openclio/openclio/internal/config"
)

type delegationMockProvider struct {
	mu            sync.Mutex
	calls         []ChatRequest
	subErr        error
	synthesisResp string
}

func (m *delegationMockProvider) Name() string { return "delegation-mock" }

func (m *delegationMockProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	_ = ctx
	m.mu.Lock()
	m.calls = append(m.calls, req)
	m.mu.Unlock()

	if strings.Contains(req.SystemPrompt, "synthesis agent") {
		return &ChatResponse{Content: m.synthesisResp}, nil
	}
	if m.subErr != nil {
		return nil, m.subErr
	}
	return &ChatResponse{Content: "sub-result"}, nil
}

func TestDelegateRunsSubTasksAndSynthesizes(t *testing.T) {
	provider := &delegationMockProvider{synthesisResp: "final merged answer"}
	a := &Agent{provider: provider, model: "active-model"}

	out, err := a.Delegate(context.Background(), "compare databases", []string{"latency", "cost"}, config.AgentDelegationConfig{
		Enabled:              true,
		MaxParallelSubAgents: 2,
		SubAgentModel:        "sub-model",
		SynthesisModel:       "synth-model",
		Timeout:              2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Delegate returned error: %v", err)
	}
	if out != "final merged answer" {
		t.Fatalf("unexpected synthesis output: %q", out)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.calls) != 3 {
		t.Fatalf("expected 3 provider calls (2 sub + 1 synthesis), got %d", len(provider.calls))
	}
	var subModelCalls, synthModelCalls int
	for _, call := range provider.calls {
		if call.Model == "sub-model" {
			subModelCalls++
		}
		if call.Model == "synth-model" {
			synthModelCalls++
		}
	}
	if subModelCalls != 2 {
		t.Fatalf("expected 2 sub-model calls, got %d", subModelCalls)
	}
	if synthModelCalls != 1 {
		t.Fatalf("expected 1 synthesis-model call, got %d", synthModelCalls)
	}
}

func TestDelegateFailsWhenAllSubTasksFail(t *testing.T) {
	provider := &delegationMockProvider{
		subErr:        errors.New("provider down"),
		synthesisResp: "should not be used",
	}
	a := &Agent{provider: provider, model: "active-model"}

	_, err := a.Delegate(context.Background(), "do work", []string{"a", "b"}, config.AgentDelegationConfig{
		Enabled:              true,
		MaxParallelSubAgents: 2,
		Timeout:              time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "all delegated sub-tasks failed") {
		t.Fatalf("expected all-subtasks-failed error, got %v", err)
	}
}
