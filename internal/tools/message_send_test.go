package tools

import (
	"context"
	"encoding/json"
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
