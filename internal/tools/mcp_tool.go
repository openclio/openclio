package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/openclio/openclio/internal/mcp"
)

var mcpToolNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

// MCPTool bridges one MCP server tool into the native tool registry.
type MCPTool struct {
	toolName   string
	serverName string
	remoteName string
	server     *mcp.Server
	desc       string
	schema     json.RawMessage
}

// NewMCPTool creates a registry-compatible wrapper for one MCP tool.
func NewMCPTool(serverName string, server *mcp.Server, tool mcp.Tool) *MCPTool {
	localName := sanitizeMCPToolName(fmt.Sprintf("mcp_%s_%s", serverName, tool.Name))
	desc := strings.TrimSpace(tool.Description)
	if desc == "" {
		desc = fmt.Sprintf("MCP tool %q from server %q", tool.Name, serverName)
	}
	schema := tool.InputSchema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return &MCPTool{
		toolName:   localName,
		serverName: serverName,
		remoteName: tool.Name,
		server:     server,
		desc:       desc,
		schema:     schema,
	}
}

func (t *MCPTool) Name() string        { return t.toolName }
func (t *MCPTool) Description() string { return t.desc }
func (t *MCPTool) Schema() json.RawMessage {
	return t.schema
}

func (t *MCPTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args map[string]any
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &args); err != nil {
			return "", fmt.Errorf("invalid params for mcp tool %s: %w", t.toolName, err)
		}
	}
	out, err := t.server.CallTool(ctx, t.remoteName, args)
	if err != nil {
		return "", fmt.Errorf("mcp %s/%s failed: %w", t.serverName, t.remoteName, err)
	}
	return out, nil
}

func sanitizeMCPToolName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "mcp_tool"
	}
	name := mcpToolNameSanitizer.ReplaceAllString(raw, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return "mcp_tool"
	}
	return strings.ToLower(name)
}
