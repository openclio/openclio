package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type captureSwitcher struct {
	provider string
	model    string
	err      error
	calls    int
}

func (s *captureSwitcher) SwitchProvider(providerName, modelName string) error {
	s.calls++
	s.provider = providerName
	s.model = modelName
	return s.err
}

func TestSwitchModelToolExecute(t *testing.T) {
	switcher := &captureSwitcher{}
	tool := NewSwitchModelTool(switcher)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"provider":"openai","model":"gpt-4o-mini"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if switcher.calls != 1 {
		t.Fatalf("expected one switch call, got %d", switcher.calls)
	}
	if switcher.provider != "openai" || switcher.model != "gpt-4o-mini" {
		t.Fatalf("unexpected switch request: provider=%q model=%q", switcher.provider, switcher.model)
	}
	if !strings.Contains(out, "Switched active model") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestSwitchModelToolRejectsUnsupportedProvider(t *testing.T) {
	tool := NewSwitchModelTool(&captureSwitcher{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"provider":"cohere","model":"command-r"}`))
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestSwitchModelToolBubblesSwitcherError(t *testing.T) {
	switcher := &captureSwitcher{err: context.Canceled}
	tool := NewSwitchModelTool(switcher)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"provider":"openai","model":"gpt-4o-mini"}`))
	if err == nil {
		t.Fatal("expected switch error")
	}
	if !strings.Contains(err.Error(), "switching model failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
