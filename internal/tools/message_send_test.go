package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openclio/openclio/internal/plugin"
)

type captureAdapter struct {
	name    string
	inbound chan<- plugin.InboundMessage
	outCh   chan plugin.OutboundMessage
	done    chan struct{}
}

type directWhatsAppAdapter struct {
	sendFn func(ctx context.Context, chatID, text string) error
}

type mockChannelConnector struct {
	connectFn func(channelType string, credentials map[string]string) error
}

func (m *mockChannelConnector) ConnectChannel(channelType string, credentials map[string]string) error {
	if m.connectFn != nil {
		return m.connectFn(channelType, credentials)
	}
	return nil
}

func (d *directWhatsAppAdapter) Name() string { return "whatsapp" }
func (d *directWhatsAppAdapter) Start(context.Context, chan<- plugin.InboundMessage, <-chan plugin.OutboundMessage) error {
	return nil
}
func (d *directWhatsAppAdapter) Stop()         {}
func (d *directWhatsAppAdapter) Health() error { return nil }
func (d *directWhatsAppAdapter) SendDirect(ctx context.Context, chatID, text string) error {
	if d.sendFn != nil {
		return d.sendFn(ctx, chatID, text)
	}
	return nil
}

func (c *captureAdapter) Name() string { return c.name }
func (c *captureAdapter) Start(ctx context.Context, inbound chan<- plugin.InboundMessage, outbound <-chan plugin.OutboundMessage) error {
	c.inbound = inbound
	c.outCh = make(chan plugin.OutboundMessage, 4)
	c.done = make(chan struct{})
	// Forward outbound messages to a buffer we control for test inspection.
	go func() {
		for {
			select {
			case msg, ok := <-outbound:
				if !ok {
					close(c.done)
					return
				}
				// copy into our channel
				c.outCh <- msg
			case <-ctx.Done():
				close(c.done)
				return
			}
		}
	}()
	return nil
}
func (c *captureAdapter) Stop()         { /* noop */ }
func (c *captureAdapter) Health() error { return nil }

func TestMessageSendTool(t *testing.T) {
	mgr := plugin.NewManager(nil)
	adapter := &captureAdapter{name: "testchan"}
	mgr.Register(adapter)

	// Run adapter supervised loop so outbound channel is active.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.RunOne(ctx, adapter)
	// allow a short moment for run loop to start
	time.Sleep(50 * time.Millisecond)

	tool := NewMessageSendTool(mgr)

	// Execute send
	params := map[string]any{
		"channel_type": "testchan",
		"chat_id":      "user123",
		"text":         "hello",
	}
	raw, _ := json.Marshal(params)
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res != "sent" {
		t.Fatalf("unexpected result: %s", res)
	}

	// Wait for adapter to receive message
	select {
	case m := <-adapter.outCh:
		if m.ChatID != "user123" || m.Text != "hello" {
			t.Fatalf("unexpected outbound message: %#v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for outbound message")
	}
}

func TestMessageSendTool_WhatsAppDirectSend(t *testing.T) {
	mgr := plugin.NewManager(nil)
	called := false
	adapter := &directWhatsAppAdapter{
		sendFn: func(ctx context.Context, chatID, text string) error {
			called = true
			if chatID != "919500080653@s.whatsapp.net" {
				t.Fatalf("unexpected chat id: %s", chatID)
			}
			if text != "hello wa" {
				t.Fatalf("unexpected text: %s", text)
			}
			return nil
		},
	}
	mgr.Register(adapter)

	tool := NewMessageSendTool(mgr)
	raw, _ := json.Marshal(map[string]any{
		"channel_type": "whatsapp",
		"chat_id":      "919500080653@s.whatsapp.net",
		"text":         "hello wa",
	})
	out, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out != "sent" {
		t.Fatalf("unexpected output: %s", out)
	}
	if !called {
		t.Fatal("expected direct whatsapp sender to be called")
	}
}

func TestMessageSendTool_WhatsAppDirectSendError(t *testing.T) {
	mgr := plugin.NewManager(nil)
	adapter := &directWhatsAppAdapter{
		sendFn: func(context.Context, string, string) error {
			return fmt.Errorf("missing country code")
		},
	}
	mgr.Register(adapter)

	tool := NewMessageSendTool(mgr)
	raw, _ := json.Marshal(map[string]any{
		"channel_type": "whatsapp",
		"chat_id":      "9500080653",
		"text":         "hello wa",
	})
	_, err := tool.Execute(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got == "" || !containsFold(got, "missing country code") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMessageSendTool_WhatsAppAutoConnectAndSend(t *testing.T) {
	mgr := plugin.NewManager(nil)
	tool := NewMessageSendTool(mgr)

	called := false
	connector := &mockChannelConnector{
		connectFn: func(channelType string, credentials map[string]string) error {
			if channelType != "whatsapp" {
				t.Fatalf("expected whatsapp channel, got %q", channelType)
			}
			mgr.Register(&directWhatsAppAdapter{
				sendFn: func(ctx context.Context, chatID, text string) error {
					called = true
					if chatID != "919500080653@s.whatsapp.net" {
						t.Fatalf("unexpected chat id: %s", chatID)
					}
					if text != "hello auto" {
						t.Fatalf("unexpected text: %s", text)
					}
					return nil
				},
			})
			return nil
		},
	}
	tool.SetChannelConnector(connector)

	raw, _ := json.Marshal(map[string]any{
		"channel_type": "whatsapp",
		"chat_id":      "919500080653@s.whatsapp.net",
		"text":         "hello auto",
	})
	out, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out != "sent" {
		t.Fatalf("unexpected output: %s", out)
	}
	if !called {
		t.Fatal("expected whatsapp send to be called after auto-connect")
	}
}

func TestMessageSendTool_WhatsAppAutoConnectFailure(t *testing.T) {
	mgr := plugin.NewManager(nil)
	tool := NewMessageSendTool(mgr)
	tool.SetChannelConnector(&mockChannelConnector{
		connectFn: func(channelType string, credentials map[string]string) error {
			return fmt.Errorf("connect failed")
		},
	})

	raw, _ := json.Marshal(map[string]any{
		"channel_type": "whatsapp",
		"chat_id":      "919500080653@s.whatsapp.net",
		"text":         "hello auto",
	})
	_, err := tool.Execute(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error")
	}
	if !containsFold(err.Error(), "auto-connect failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMessageSendTool_WhatsAppAutoConnectRetriesUntilConnected(t *testing.T) {
	mgr := plugin.NewManager(nil)
	tool := NewMessageSendTool(mgr)

	attempts := 0
	tool.SetChannelConnector(&mockChannelConnector{
		connectFn: func(channelType string, credentials map[string]string) error {
			mgr.Register(&directWhatsAppAdapter{
				sendFn: func(ctx context.Context, chatID, text string) error {
					attempts++
					if attempts < 3 {
						return fmt.Errorf("whatsapp is not connected")
					}
					return nil
				},
			})
			return nil
		},
	})

	raw, _ := json.Marshal(map[string]any{
		"channel_type": "whatsapp",
		"chat_id":      "919500080653@s.whatsapp.net",
		"text":         "hello retry",
	})
	out, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out != "sent" {
		t.Fatalf("unexpected output: %s", out)
	}
	if attempts < 3 {
		t.Fatalf("expected retries before success, got attempts=%d", attempts)
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
