package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openclio/openclio/internal/plugin"
)

// MessageSendTool allows the agent to send outbound messages to connected channels.
type MessageSendTool struct {
	manager   *plugin.Manager
	connector ChannelConnector
}

// NewMessageSendTool creates a message_send tool backed by the plugin manager.
func NewMessageSendTool(manager *plugin.Manager) *MessageSendTool {
	return &MessageSendTool{manager: manager}
}

// SetChannelConnector wires an optional runtime connector so message_send can
// auto-connect adapters (notably WhatsApp) before sending.
func (t *MessageSendTool) SetChannelConnector(connector ChannelConnector) {
	t.connector = connector
}

func (t *MessageSendTool) Name() string { return "message_send" }

func (t *MessageSendTool) Description() string {
	return "Send a message to a connected channel. Params: {channel_type, chat_id, text, dry_run=false}. For WhatsApp, use E.164 with country code (for example 15551234567) or full JID (for example 15551234567@s.whatsapp.net)."
}

func (t *MessageSendTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"channel_type":{"type":"string"},
			"chat_id":{"type":"string","description":"Destination chat identifier. WhatsApp supports E.164 numbers with country code or full JID (@s.whatsapp.net or @g.us)."},
			"text":{"type":"string"},
			"dry_run":{"type":"boolean"}
		},
		"required":["channel_type","chat_id","text"]
	}`)
}

type messageSendParams struct {
	ChannelType string `json:"channel_type"`
	ChatID      string `json:"chat_id"`
	Text        string `json:"text"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

func (t *MessageSendTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	if t.manager == nil {
		return "", fmt.Errorf("message_send is unavailable")
	}
	var p messageSendParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	channel := strings.ToLower(strings.TrimSpace(p.ChannelType))
	if channel == "" {
		return "", fmt.Errorf("channel_type is required")
	}
	if p.ChatID == "" {
		return "", fmt.Errorf("chat_id is required")
	}
	if p.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if p.DryRun {
		return fmt.Sprintf("dry_run=ok; channel=%s; chat_id=%s; text=%s", channel, p.ChatID, p.Text), nil
	}

	// WhatsApp sends are executed synchronously when supported so users get real
	// delivery-acceptance errors (for example invalid chat IDs or missing country code).
	if channel == "whatsapp" {
		adapter := t.manager.AdapterByName("whatsapp")
		autoConnected := false
		if adapter == nil && t.connector != nil {
			if err := t.connector.ConnectChannel("whatsapp", map[string]string{}); err != nil {
				return "", fmt.Errorf("send failed: whatsapp channel is not connected and auto-connect failed: %w", err)
			}
			autoConnected = true
			adapter = t.manager.AdapterByName("whatsapp")
		}
		if adapter == nil {
			return "", fmt.Errorf("send failed: whatsapp channel is not connected; connect whatsapp first")
		}
		if sender, ok := adapter.(interface {
			SendDirect(context.Context, string, string) error
		}); ok {
			for attempt := 0; ; attempt++ {
				err := sender.SendDirect(ctx, p.ChatID, p.Text)
				if err == nil {
					return "sent", nil
				}
				if autoConnected && attempt < 20 && shouldRetryWhatsAppSend(err) {
					select {
					case <-ctx.Done():
						return "", fmt.Errorf("send failed: %w", ctx.Err())
					case <-time.After(250 * time.Millisecond):
						continue
					}
				}
				return "", fmt.Errorf("send failed: %w", err)
			}
		}
	}

	err := t.manager.Send(channel, plugin.OutboundMessage{
		ChatID: p.ChatID,
		Text:   p.Text,
	})
	if err != nil {
		return "", fmt.Errorf("send failed: %w", err)
	}
	return "sent", nil
}

func shouldRetryWhatsAppSend(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not connected") || strings.Contains(msg, "not initialized")
}
