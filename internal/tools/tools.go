// Package tools provides the built-in tool system for the agent.
// Tools include exec, read_file, write_file, edit_file, list_dir, and web_fetch.
// Each tool implements the Tool interface.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	"github.com/openclio/openclio/internal/storage"
)

// Tool is the interface all tools implement.
type Tool interface {
	// Name returns the tool name (e.g. "exec", "read_file").
	Name() string

	// Description returns a short description for the LLM.
	Description() string

	// Schema returns the JSON Schema for the tool's parameters.
	Schema() json.RawMessage

	// Execute runs the tool with the given parameters.
	Execute(ctx context.Context, params json.RawMessage) (string, error)
}

// Registry holds all registered tools and routes execution.
// It implements agent.ToolExecutor.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// ProviderSwitcher swaps the active provider/model at runtime.
type ProviderSwitcher interface {
	SwitchProvider(providerName, modelName string) error
}

// ChannelConnector connects a channel adapter at runtime.
type ChannelConnector interface {
	ConnectChannel(channelType string, credentials map[string]string) error
}

// ChannelLifecycleController controls runtime channel lifecycle actions.
type ChannelLifecycleController interface {
	DisconnectChannel(channelType string) error
}

// ChannelStatus summarizes runtime channel health/connectivity.
type ChannelStatus struct {
	Name            string
	Running         bool
	Healthy         bool
	Connected       bool
	QRAvailable     bool
	QREvent         string
	LastHealthError string
	Message         string
}

// ChannelStatusReader provides runtime channel status snapshots.
type ChannelStatusReader interface {
	ChannelStatus(channelType string) (ChannelStatus, error)
	ListChannelStatuses() ([]ChannelStatus, error)
}

// DelegationExecutor runs sub-agent delegation tasks.
type DelegationExecutor interface {
	Delegate(ctx context.Context, objective string, tasks []string) (string, error)
}

// Stores provides optional persistence hooks for tools.
type Stores struct {
	Privacy          *storage.PrivacyStore
	ActionLog        *storage.ActionLogStore
	ProviderSwitcher ProviderSwitcher
	ChannelConnector ChannelConnector
	ChannelLifecycle ChannelLifecycleController
	ChannelStatus    ChannelStatusReader
	Delegation       DelegationExecutor
}

// NewRegistry creates a registry with all enabled tools.
func NewRegistry(cfg config.ToolsConfig, workDir, dataDir string, stores ...Stores) *Registry {
	r := &Registry{
		tools: make(map[string]Tool),
	}
	var storeCfg Stores
	if len(stores) > 0 {
		storeCfg = stores[0]
	}

	// Register core tools
	scrub := cfg.ScrubOutput
	execTool := NewExecTool(cfg.Exec, workDir, cfg.MaxOutputSize, scrub)
	execTool.SetPrivacyStore(storeCfg.Privacy)
	execTool.SetActionLogStore(storeCfg.ActionLog)
	r.Register(execTool)

	readTool := NewReadFileTool(workDir, scrub)
	readTool.SetPrivacyStore(storeCfg.Privacy)
	r.Register(readTool)
	writeTool := NewWriteFileTool(workDir)
	writeTool.SetActionLogStore(storeCfg.ActionLog)
	r.Register(writeTool)
	r.Register(NewEditFileTool(workDir))
	r.Register(NewListDirTool(workDir))
	webFetchTool := NewWebFetchTool()
	r.Register(webFetchTool)
	if cfg.Browser.Enabled {
		browserTool := NewBrowserTool(cfg.Browser)
		r.Register(browserTool)
		webFetchTool.SetBrowserFallback(browserTool)
	}

	// Register web search tool
	if cfg.WebSearch != nil {
		apiKey := os.Getenv(cfg.WebSearch.APIKeyEnv)
		r.Register(NewWebSearchTool(cfg.WebSearch.Provider, apiKey))
	}

	// Register basic image analyze tool (placeholder implementation).
	r.Register(NewImageAnalyzeTool())
	// Register image generation tool (requires OPENAI_API_KEY)
	r.Register(NewImageGenerateTool())

	// Register memory tool
	if dataDir != "" {
		r.Register(NewMemoryWriteTool(dataDir))
	}
	if storeCfg.ProviderSwitcher != nil {
		r.Register(NewSwitchModelTool(storeCfg.ProviderSwitcher))
	}
	if storeCfg.ChannelConnector != nil {
		connectTool := NewConnectChannelTool(storeCfg.ChannelConnector)
		if storeCfg.ChannelStatus != nil {
			connectTool.SetStatusReader(storeCfg.ChannelStatus)
		}
		if storeCfg.ChannelLifecycle != nil {
			connectTool.SetLifecycleController(storeCfg.ChannelLifecycle)
		}
		r.Register(connectTool)
	}
	if storeCfg.ChannelStatus != nil {
		r.Register(NewChannelStatusTool(storeCfg.ChannelStatus))
	}
	if storeCfg.Delegation != nil {
		r.Register(NewDelegateTool(storeCfg.Delegation))
	}

	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Unregister removes a tool from the registry by name.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Get returns a registered tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

// Execute runs a tool by name. Implements agent.ToolExecutor.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	if !IsToolAllowed(name) {
		return "", fmt.Errorf("tool %s is not permitted by runtime policy", name)
	}
	return tool.Execute(ctx, args)
}

// ListNames returns all registered tool names. Implements agent.ToolExecutor.
func (r *Registry) ListNames() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}

// HasTool checks if a tool exists in the registry. Implements agent.ToolExecutor.
func (r *Registry) HasTool(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

// ListTools returns all registered tools with their schemas.
func (r *Registry) ListTools() []Tool {
	names := r.ListNames()
	tools := make([]Tool, 0, len(names))
	r.mu.RLock()
	for _, name := range names {
		tool, ok := r.tools[name]
		if !ok {
			continue
		}
		tools = append(tools, tool)
	}
	r.mu.RUnlock()
	return tools
}

// ToolDefinitions returns provider-facing tool metadata.
func (r *Registry) ToolDefinitions() []agent.ToolDef {
	defs := make([]agent.ToolDef, 0, len(r.tools))
	for _, t := range r.ListTools() {
		defs = append(defs, agent.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
	}
	return defs
}
