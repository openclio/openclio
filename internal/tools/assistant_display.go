package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openclio/openclio/internal/workspace"
)

// AssistantDisplayTool lets the agent update its own display name and icon when the user asks.
type AssistantDisplayTool struct {
	dataDir string
}

// NewAssistantDisplayTool creates a tool that writes to ~/.openclio/assistant.yaml and updates identity.md.
func NewAssistantDisplayTool(dataDir string) *AssistantDisplayTool {
	return &AssistantDisplayTool{dataDir: dataDir}
}

func (t *AssistantDisplayTool) Name() string { return "set_assistant_display" }

func (t *AssistantDisplayTool) Description() string {
	return "Update the assistant's display name and/or icon (emoji) when the user asks to change what you're called or your avatar. Saves to assistant identity and updates the UI. Use when the user says e.g. 'call yourself X', 'change your name to Y', or 'use this emoji'."
}

func (t *AssistantDisplayTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "New display name for the assistant"},
			"icon": {"type": "string", "description": "New icon/emoji (e.g. 🤖 ✨ 🎯)"}
		}
	}`)
}

type setAssistantDisplayParams struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
}

func (t *AssistantDisplayTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	if t.dataDir == "" {
		return "", fmt.Errorf("assistant display not configured (no data dir)")
	}
	var p setAssistantDisplayParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	name := strings.TrimSpace(p.Name)
	icon := strings.TrimSpace(p.Icon)
	if name == "" && icon == "" {
		return "", fmt.Errorf("provide at least one of name or icon")
	}
	// Load current so we only change what was provided
	current, _ := workspace.LoadAssistantDisplay(t.dataDir)
	if name == "" {
		name = current.Name
		if name == "" {
			name = "Clio"
		}
	}
	if icon == "" {
		icon = current.Icon
		if icon == "" {
			icon = "🤖"
		}
	}
	if err := workspace.SaveAssistantDisplay(t.dataDir, name, icon); err != nil {
		return "", fmt.Errorf("saving assistant display: %w", err)
	}
	if err := workspace.UpdateIdentityFileWithName(t.dataDir, name); err != nil {
		// Non-fatal; identity.md update is best-effort
	}
	return fmt.Sprintf("Assistant display updated: name=%q, icon=%s. The UI and future replies will use these.", name, icon), nil
}
