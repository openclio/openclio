package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openclio/openclio/internal/plugin"
)

// MessageSendTool allows the agent to send outbound messages to connected channels.
type MessageSendTool struct {
	manager *plugin.Manager
}

// NewMessageSendTool creates a message_send tool backed by the plugin manager.
func NewMessageSendTool(manager *plugin.Manager) *MessageSendTool {
	return &MessageSendTool{manager: manager}
}

func (t *MessageSendTool) Name() string { return "message_send" }

func (t *MessageSendTool) Description() string {
	return "Send a message to a connected channel. Params: {channel_type, chat_id, text, dry_run=false}."
}

func (t *MessageSendTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"channel_type":{"type":"string"},
			"chat_id":{"type":"string"},
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
	channel := p.ChannelType
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

	err := t.manager.Send(channel, plugin.OutboundMessage{
		ChatID: p.ChatID,
		Text:   p.Text,
	})
	if err != nil {
		return "", fmt.Errorf("send failed: %w", err)
	}
	return "sent", nil
}
