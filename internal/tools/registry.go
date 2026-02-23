package tools

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// ToolFunc is the canonical function signature for a registered tool.
// payload is a free-form map serializable to/from JSON for tool inputs.
type ToolFunc func(ctx context.Context, payload map[string]any) (any, error)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]ToolFunc)
)

// RegisterTool registers a tool by name. It returns an error if the name is
// empty, the function is nil, or a tool with the same name is already registered.
func RegisterTool(name string, fn ToolFunc) error {
	if name == "" || fn == nil {
		return fmt.Errorf("invalid tool registration")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}
	registry[name] = fn
	return nil
}

// ReplaceTool registers or replaces a tool by name. Use when overriding noop
// placeholders with real implementations during init.
func ReplaceTool(name string, fn ToolFunc) error {
	if name == "" || fn == nil {
		return fmt.Errorf("invalid tool registration")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = fn
	return nil
}

// GetTool returns the registered ToolFunc and a boolean indicating presence.
func GetTool(name string) (ToolFunc, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	fn, ok := registry[name]
	return fn, ok
}

// CallTool invokes a registered tool by name with the given payload.
// Returns an error if the tool is not found or the tool returns an error.
func CallTool(ctx context.Context, name string, payload map[string]any) (any, error) {
	fn, ok := GetTool(name)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	// Permission gate
	if !IsToolAllowed(name) {
		return nil, fmt.Errorf("tool %s is not permitted by runtime policy", name)
	}
	// Record metric
	IncToolCall(name)
	return fn(ctx, payload)
}

// ListTools returns a sorted list of registered tool names.
func ListTools() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
