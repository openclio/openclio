package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type mockDelegationExecutor struct {
	objective string
	tasks     []string
	result    string
	err       error
}

func (m *mockDelegationExecutor) Delegate(ctx context.Context, objective string, tasks []string) (string, error) {
	_ = ctx
	m.objective = objective
	m.tasks = append([]string(nil), tasks...)
	if m.err != nil {
		return "", m.err
	}
	return m.result, nil
}

func TestDelegateToolExecuteSuccess(t *testing.T) {
	mock := &mockDelegationExecutor{result: "combined answer"}
	tool := NewDelegateTool(mock)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{
		"objective":"compare cloud providers",
		"tasks":[" pricing ", " latency", " "]
	}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if out != "combined answer" {
		t.Fatalf("expected result passthrough, got %q", out)
	}
	if mock.objective != "compare cloud providers" {
		t.Fatalf("unexpected objective: %q", mock.objective)
	}
	if len(mock.tasks) != 2 {
		t.Fatalf("expected 2 cleaned tasks, got %d", len(mock.tasks))
	}
}

func TestDelegateToolValidation(t *testing.T) {
	tool := NewDelegateTool(&mockDelegationExecutor{})

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"objective":"","tasks":["a"]}`))
	if err == nil || !strings.Contains(err.Error(), "objective is required") {
		t.Fatalf("expected objective validation error, got %v", err)
	}

	_, err = tool.Execute(context.Background(), json.RawMessage(`{"objective":"x","tasks":[" ",""]}`))
	if err == nil || !strings.Contains(err.Error(), "tasks must include at least one non-empty item") {
		t.Fatalf("expected tasks validation error, got %v", err)
	}
}

func TestRegistryRegistersDelegateToolWhenExecutorProvided(t *testing.T) {
	cfg := defaultToolsConfig()
	reg := NewRegistry(cfg, t.TempDir(), "", Stores{
		Delegation: &mockDelegationExecutor{result: "ok"},
	})
	if !reg.HasTool("delegate") {
		t.Fatal("expected delegate tool to be registered")
	}
}
