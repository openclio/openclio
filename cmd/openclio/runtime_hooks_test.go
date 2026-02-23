package main

import "testing"

func TestRuntimeProviderSwitcher(t *testing.T) {
	s := &runtimeProviderSwitcher{}
	if err := s.SwitchProvider("openai", "gpt-4o-mini"); err == nil {
		t.Fatal("expected unavailable error before handler is set")
	}

	called := 0
	s.SetHandler(func(providerName, modelName string) error {
		called++
		if providerName != "openai" || modelName != "gpt-4o-mini" {
			t.Fatalf("unexpected args: %s/%s", providerName, modelName)
		}
		return nil
	})
	if err := s.SwitchProvider("openai", "gpt-4o-mini"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 1 {
		t.Fatalf("expected one call, got %d", called)
	}
}

func TestRuntimeChannelConnector(t *testing.T) {
	c := &runtimeChannelConnector{}
	if err := c.ConnectChannel("slack", map[string]string{"token": "x"}); err == nil {
		t.Fatal("expected unavailable error before handler is set")
	}

	called := 0
	c.SetHandler(func(channelType string, credentials map[string]string) error {
		called++
		if channelType != "slack" {
			t.Fatalf("unexpected channel: %s", channelType)
		}
		if credentials["token"] != "x" {
			t.Fatalf("unexpected credentials: %#v", credentials)
		}
		return nil
	})
	if err := c.ConnectChannel("slack", map[string]string{"token": "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 1 {
		t.Fatalf("expected one call, got %d", called)
	}
}
