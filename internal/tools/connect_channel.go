package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ConnectChannelTool lets users connect messaging channel adapters at runtime
// through an injected ChannelConnector.
type ConnectChannelTool struct {
	connector ChannelConnector
	status    ChannelStatusReader
	lifecycle ChannelLifecycleController
}

// NewConnectChannelTool creates a connect_channel tool.
func NewConnectChannelTool(connector ChannelConnector) *ConnectChannelTool {
	return &ConnectChannelTool{connector: connector}
}

// SetStatusReader wires optional runtime channel status checks.
func (t *ConnectChannelTool) SetStatusReader(reader ChannelStatusReader) {
	t.status = reader
}

// SetLifecycleController wires optional runtime lifecycle controls.
func (t *ConnectChannelTool) SetLifecycleController(controller ChannelLifecycleController) {
	t.lifecycle = controller
}

func (t *ConnectChannelTool) Name() string { return "connect_channel" }

func (t *ConnectChannelTool) Description() string {
	return "Connect a new messaging channel (Slack, Telegram, Discord, WhatsApp) at runtime. Slack/Telegram/Discord use bot tokens; WhatsApp uses QR linking and does not require a token. For a fresh WhatsApp QR, set force_reconnect=true."
}

func (t *ConnectChannelTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_type": {
				"type": "string",
				"enum": ["slack", "telegram", "discord", "whatsapp"],
				"description": "Channel type to connect"
			},
			"token": {
				"type": "string",
				"description": "Bot token (xoxb-... for Slack, bot token for Telegram/Discord). Not required for WhatsApp QR login."
			},
			"extra": {
				"type": "object",
				"additionalProperties": {"type": "string"},
				"description": "Optional extra params like app_id (Discord) or data_dir (WhatsApp session store)."
			},
			"force_reconnect": {
				"type": "boolean",
				"description": "WhatsApp only. If true and already connected, disconnects first then reconnects to generate a fresh QR code."
			}
		},
		"required": ["channel_type"]
	}`)
}

type connectChannelParams struct {
	ChannelType    string         `json:"channel_type"`
	Token          string         `json:"token"`
	Extra          map[string]any `json:"extra,omitempty"`
	ForceReconnect bool           `json:"force_reconnect,omitempty"`
}

func (t *ConnectChannelTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	_ = ctx
	if t.connector == nil {
		return "", fmt.Errorf("connect_channel is unavailable")
	}

	var p connectChannelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}

	channelType := strings.ToLower(strings.TrimSpace(p.ChannelType))
	token := strings.TrimSpace(p.Token)
	if channelType == "" {
		return "", fmt.Errorf("channel_type is required")
	}
	switch channelType {
	case "slack", "telegram", "discord", "whatsapp":
	default:
		return "", fmt.Errorf("unsupported channel_type %q (supported: slack, telegram, discord, whatsapp)", channelType)
	}
	if channelType != "whatsapp" && token == "" {
		return "", fmt.Errorf("token is required for %s", channelType)
	}

	if channelType == "whatsapp" {
		if p.ForceReconnect {
			if t.lifecycle == nil {
				return "", fmt.Errorf("whatsapp force_reconnect requires lifecycle control")
			}
			if err := t.lifecycle.DisconnectChannel("whatsapp"); err != nil {
				lower := strings.ToLower(err.Error())
				if !strings.Contains(lower, "not connected") {
					return "", fmt.Errorf("disconnecting whatsapp before reconnect failed: %w", err)
				}
			}
		} else if t.status != nil {
			if st, err := t.status.ChannelStatus("whatsapp"); err == nil && st.Connected {
				return "whatsapp is already connected to openclio. QR codes are shown only for linking. Ask the user if they want to relink, then call connect_channel again with force_reconnect=true.", nil
			}
		}
	}

	creds := make(map[string]string)
	if token != "" {
		creds["token"] = token
	}
	if channelType == "whatsapp" && p.ForceReconnect {
		creds["force_reconnect"] = "true"
	}
	for k, v := range p.Extra {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		creds[k] = strings.TrimSpace(fmt.Sprint(v))
	}

	if err := t.connector.ConnectChannel(channelType, creds); err != nil {
		return "", fmt.Errorf("connecting channel failed: %w", err)
	}

	if channelType == "whatsapp" {
		if t.status != nil {
			if st, err := t.status.ChannelStatus("whatsapp"); err == nil {
				if st.Connected {
					return "status=connected; whatsapp is connected to openclio.", nil
				}
				if st.QRAvailable {
					return "status=awaiting_qr; whatsapp pairing started. Scan the QR shown in openclio webchat (Linked Devices -> Link a Device).", nil
				}
				if st.QREvent != "" {
					return fmt.Sprintf("status=pairing; whatsapp state=%s. QR will appear in openclio webchat when ready.", st.QREvent), nil
				}
			}
		}
		return "status=pairing; whatsapp setup started. Not connected yet. Scan the QR in openclio webchat (Linked Devices -> Link a Device).", nil
	}

	return fmt.Sprintf("Connected %s channel", channelType), nil
}
