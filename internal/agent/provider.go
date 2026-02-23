package agent

import (
	"context"
	"encoding/json"
)

// Provider is the interface all LLM providers implement.
type Provider interface {
	// Chat sends a request and returns the complete response.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// Name returns the provider name (e.g. "anthropic", "openai", "ollama").
	Name() string
}

// Streamer is an optional interface providers may implement to support
// real-time token streaming via Server-Sent Events.
// If a provider does not implement this interface the gateway falls back
// to a full (buffered) response.
type Streamer interface {
	Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
}

// StreamChunk is a single token (or final sentinel) from a streaming response.
type StreamChunk struct {
	Text      string     // token text (may be empty on final chunk)
	ToolCalls []ToolCall // non-empty when the model requests tool calls (on the Done chunk)
	Done      bool       // true on the last chunk
	Error     error      // non-nil if the stream encountered an error
}

// ChatRequest is a request to an LLM provider.
type ChatRequest struct {
	SystemPrompt string    `json:"system_prompt"`
	Messages     []Message `json:"messages"`
	Tools        []ToolDef `json:"tools,omitempty"`
	MaxTokens    int       `json:"max_tokens"`
	Model        string    `json:"model"`
}

// Message is a single message in the conversation.
type Message struct {
	Role       string     `json:"role"` // user, assistant, system, tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // for tool result messages
}

// ToolDef describes a tool the model can call.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// ToolCall is a tool invocation requested by the model.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ChatResponse is the response from an LLM provider.
type ChatResponse struct {
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Usage      Usage      `json:"usage"`
	StopReason string     `json:"stop_reason"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_input_tokens,omitempty"`
}
