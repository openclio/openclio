package tools

import (
	"os"
	"strings"
)

// IsToolAllowed checks runtime permission gates for tool execution.
// If OPENCLIO_ALLOWED_TOOLS is unset, allow all tools. Otherwise it's a
// comma-separated allowlist (tool names).
func IsToolAllowed(toolName string) bool {
	raw := os.Getenv("OPENCLIO_ALLOWED_TOOLS")
	if strings.TrimSpace(raw) == "" {
		return true
	}
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		if strings.TrimSpace(p) == toolName {
			return true
		}
	}
	return false
}
