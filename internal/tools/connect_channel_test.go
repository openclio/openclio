package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type captureConnector struct {
	channel string
	creds   map[string]string
	err     error
	calls   int
}

func (c *captureConnector) ConnectChannel(channelType string, credentials map[string]string) error {
	c.calls++
	c.channel = channelType
	c.creds = credentials
	return c.err
}

type captureStatusReader struct {
	status ChannelStatus
	err    error
}

func (c *captureStatusReader) ChannelStatus(channelType string) (ChannelStatus, error) {
	if c.err != nil {
		return ChannelStatus{}, c.err
	}
	return c.status, nil
}

func (c *captureStatusReader) ListChannelStatuses() ([]ChannelStatus, error) {
	if c.err != nil {
		return nil, c.err
	}
	return []ChannelStatus{c.status}, nil
}

type captureLifecycle struct {
	lastChannel string
	calls       int
	err         error
}

func (c *captureLifecycle) DisconnectChannel(channelType string) error {
	c.calls++
	c.lastChannel = channelType
	return c.err
}

func TestConnectChannelToolExecute(t *testing.T) {
	connector := &captureConnector{}
	tool := NewConnectChannelTool(connector)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{
		"channel_type": "slack",
		"token": "xoxb-test",
		"extra": {"app_id": "123"}
	}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connector.calls != 1 {
		t.Fatalf("expected one connect call, got %d", connector.calls)
	}
	if connector.channel != "slack" {
		t.Fatalf("expected slack channel, got %q", connector.channel)
	}
	if connector.creds["token"] != "xoxb-test" {
		t.Fatalf("expected token to be forwarded")
	}
	if connector.creds["app_id"] != "123" {
		t.Fatalf("expected extra credentials to be forwarded")
	}
	if !strings.Contains(out, "Connected slack channel") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestConnectChannelToolRequiresTokenForNonWhatsApp(t *testing.T) {
	tool := NewConnectChannelTool(&captureConnector{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"channel_type":"telegram"}`))
	if err == nil {
		t.Fatal("expected token validation error")
	}
}

func TestConnectChannelToolAllowsWhatsAppWithoutToken(t *testing.T) {
	connector := &captureConnector{}
	tool := NewConnectChannelTool(connector)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"channel_type":"whatsapp"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connector.channel != "whatsapp" {
		t.Fatalf("expected whatsapp channel, got %q", connector.channel)
	}
	if !strings.Contains(strings.ToLower(out), "scan the qr") {
		t.Fatalf("expected QR guidance in output, got: %q", out)
	}
}

func TestConnectChannelToolWhatsAppAlreadyConnectedWithoutForceReconnect(t *testing.T) {
	connector := &captureConnector{}
	status := &captureStatusReader{
		status: ChannelStatus{Name: "whatsapp", Connected: true},
	}
	tool := NewConnectChannelTool(connector)
	tool.SetStatusReader(status)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"channel_type":"whatsapp"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connector.calls != 0 {
		t.Fatalf("expected no connect call when already connected, got %d", connector.calls)
	}
	if !strings.Contains(strings.ToLower(out), "already connected") {
		t.Fatalf("expected already-connected message, got %q", out)
	}
}

func TestConnectChannelToolWhatsAppForceReconnect(t *testing.T) {
	connector := &captureConnector{}
	status := &captureStatusReader{
		status: ChannelStatus{Name: "whatsapp", Connected: true},
	}
	lifecycle := &captureLifecycle{}
	tool := NewConnectChannelTool(connector)
	tool.SetStatusReader(status)
	tool.SetLifecycleController(lifecycle)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"channel_type":"whatsapp","force_reconnect":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lifecycle.calls != 1 || lifecycle.lastChannel != "whatsapp" {
		t.Fatalf("expected disconnect before reconnect, got calls=%d channel=%q", lifecycle.calls, lifecycle.lastChannel)
	}
	if connector.calls != 1 {
		t.Fatalf("expected reconnect call, got %d", connector.calls)
	}
	if connector.creds["force_reconnect"] != "true" {
		t.Fatalf("expected force_reconnect credential, got %q", connector.creds["force_reconnect"])
	}
}

func TestConnectChannelToolWhatsAppForceReconnectRequiresLifecycle(t *testing.T) {
	connector := &captureConnector{}
	tool := NewConnectChannelTool(connector)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"channel_type":"whatsapp","force_reconnect":true}`))
	if err == nil {
		t.Fatal("expected lifecycle error")
	}
	if !strings.Contains(err.Error(), "lifecycle control") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConnectChannelToolBubblesConnectorError(t *testing.T) {
	connector := &captureConnector{err: context.Canceled}
	tool := NewConnectChannelTool(connector)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"channel_type":"slack","token":"xoxb"}`))
	if err == nil {
		t.Fatal("expected connector error")
	}
	if !strings.Contains(err.Error(), "connecting channel failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
