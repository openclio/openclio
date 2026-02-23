package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ChannelStatusTool returns runtime channel connection status and health.
type ChannelStatusTool struct {
	reader ChannelStatusReader
}

// NewChannelStatusTool creates a channel_status tool.
func NewChannelStatusTool(reader ChannelStatusReader) *ChannelStatusTool {
	return &ChannelStatusTool{reader: reader}
}

func (t *ChannelStatusTool) Name() string { return "channel_status" }

func (t *ChannelStatusTool) Description() string {
	return "Check channel connection status and health (Slack, Telegram, Discord, WhatsApp). Use this when users ask if a channel is connected."
}

func (t *ChannelStatusTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_type": {
				"type": "string",
				"enum": ["slack", "telegram", "discord", "whatsapp", "webchat"],
				"description": "Optional specific channel to check. If omitted, returns all channel statuses."
			}
		}
	}`)
}

type channelStatusParams struct {
	ChannelType string `json:"channel_type"`
}

func (t *ChannelStatusTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	_ = ctx
	if t.reader == nil {
		return "", fmt.Errorf("channel_status is unavailable")
	}

	var p channelStatusParams
	if len(strings.TrimSpace(string(params))) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &p); err != nil {
			return "", fmt.Errorf("invalid params: %w", err)
		}
	}

	channelType := strings.ToLower(strings.TrimSpace(p.ChannelType))
	if channelType == "" {
		statuses, err := t.reader.ListChannelStatuses()
		if err != nil {
			return "", fmt.Errorf("listing channel statuses failed: %w", err)
		}
		if len(statuses) == 0 {
			return "No channels are currently registered.", nil
		}

		var b strings.Builder
		b.WriteString("Channel statuses:\n")
		for _, st := range statuses {
			b.WriteString("- ")
			b.WriteString(formatChannelStatusLine(st))
			b.WriteString("\n")
		}
		return strings.TrimSpace(b.String()), nil
	}

	st, err := t.reader.ChannelStatus(channelType)
	if err != nil {
		return "", fmt.Errorf("channel status failed: %w", err)
	}
	return formatChannelStatusDetail(st), nil
}

func formatChannelStatusLine(st ChannelStatus) string {
	parts := []string{
		fmt.Sprintf("%s: running=%t", st.Name, st.Running),
		fmt.Sprintf("healthy=%t", st.Healthy),
	}
	if st.Connected {
		parts = append(parts, "connected=true")
	} else if st.Name == "whatsapp" {
		parts = append(parts, "connected=false")
		if st.QRAvailable {
			parts = append(parts, "qr=available")
		}
	}
	return strings.Join(parts, ", ")
}

func formatChannelStatusDetail(st ChannelStatus) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("channel: %s\n", st.Name))
	b.WriteString(fmt.Sprintf("running: %t\n", st.Running))
	b.WriteString(fmt.Sprintf("healthy: %t\n", st.Healthy))
	if st.LastHealthError != "" {
		b.WriteString(fmt.Sprintf("last_health_error: %s\n", st.LastHealthError))
	}
	if st.Name == "whatsapp" {
		b.WriteString(fmt.Sprintf("connected: %t\n", st.Connected))
		b.WriteString(fmt.Sprintf("qr_available: %t\n", st.QRAvailable))
		if st.QREvent != "" {
			b.WriteString(fmt.Sprintf("qr_event: %s\n", st.QREvent))
		}
	}
	if st.Message != "" {
		b.WriteString(fmt.Sprintf("message: %s\n", st.Message))
	}
	return strings.TrimSpace(b.String())
}
