package tools

import (
	"context"
	"encoding/json"
	"testing"
)

type mockChannelStatusReader struct {
	one  ChannelStatus
	all  []ChannelStatus
	err  error
	err2 error
}

func (m *mockChannelStatusReader) ChannelStatus(_ string) (ChannelStatus, error) {
	if m.err != nil {
		return ChannelStatus{}, m.err
	}
	return m.one, nil
}

func (m *mockChannelStatusReader) ListChannelStatuses() ([]ChannelStatus, error) {
	if m.err2 != nil {
		return nil, m.err2
	}
	return m.all, nil
}

func TestChannelStatusToolSingle(t *testing.T) {
	tool := NewChannelStatusTool(&mockChannelStatusReader{
		one: ChannelStatus{
			Name:        "whatsapp",
			Running:     true,
			Healthy:     true,
			Connected:   true,
			QRAvailable: false,
			QREvent:     "connected",
			Message:     "WhatsApp is connected to openclio.",
		},
	})

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"channel_type":"whatsapp"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty status output")
	}
}

func TestChannelStatusToolList(t *testing.T) {
	tool := NewChannelStatusTool(&mockChannelStatusReader{
		all: []ChannelStatus{
			{Name: "webchat", Running: true, Healthy: true},
			{Name: "whatsapp", Running: true, Healthy: true, Connected: false, QRAvailable: true},
		},
	})

	out, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected list output")
	}
}
