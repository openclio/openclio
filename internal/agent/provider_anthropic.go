package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// AnthropicProvider calls the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey  string
	model   string
	baseURL string
}

// NewAnthropicProvider creates an Anthropic provider.
func NewAnthropicProvider(apiKeyEnv, model string) (*AnthropicProvider, error) {
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("environment variable %s is not set", apiKeyEnv)
	}
	return &AnthropicProvider{
		apiKey:  key,
		model:   model,
		baseURL: "https://api.anthropic.com",
	}, nil
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

// Anthropic API types
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    []anthropicContent `json:"system,omitempty"` // changed to content blocks for caching
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"` // true for streaming responses
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []anthropicContent
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"` // always "ephemeral"
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
	Error      *anthropicError    `json:"error,omitempty"`
}

type anthropicContent struct {
	Type         string                 `json:"type"` // "text" or "tool_use"
	Text         string                 `json:"text,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        json.RawMessage        `json:"input,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (p *AnthropicProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages
	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			continue // system handled separately in Anthropic
		}

		role := m.Role
		if role == "tool" {
			// Anthropic expects tool results as user messages with tool_result content
			msgs = append(msgs, anthropicMessage{
				Role: "user",
				Content: []map[string]interface{}{
					{
						"type":        "tool_result",
						"tool_use_id": m.ToolCallID,
						"content":     m.Content,
					},
				},
			})
			continue
		}

		// If assistant message has tool calls, format as content blocks
		if role == "assistant" && len(m.ToolCalls) > 0 {
			var blocks []map[string]interface{}
			if m.Content != "" {
				blocks = append(blocks, map[string]interface{}{
					"type": "text",
					"text": m.Content,
				})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": tc.Arguments,
				})
			}
			msgs = append(msgs, anthropicMessage{Role: "assistant", Content: blocks})
			continue
		}

		msgs = append(msgs, anthropicMessage{Role: role, Content: m.Content})
	}

	// Build tools and add cache block to the very last tool (Anthropic standard for large toolsets)
	var tools []anthropicTool
	for i, t := range req.Tools {
		tool := anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		}
		// Cache the last tool definition (groups the entire tools list in Anthropic's eyes)
		if i == len(req.Tools)-1 {
			tool.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
		}
		tools = append(tools, tool)
	}

	// Build request
	model := req.Model
	if model == "" {
		model = p.model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	// Format system prompt as content array with cache block
	var sysBlocks []anthropicContent
	if req.SystemPrompt != "" {
		sysBlocks = append(sysBlocks, anthropicContent{
			Type:         "text",
			Text:         req.SystemPrompt,
			CacheControl: &anthropicCacheControl{Type: "ephemeral"},
		})
	}

	apiReq := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    sysBlocks,
		Messages:  msgs,
		Tools:     tools,
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Anthropic API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result anthropicResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("Anthropic API error: %s", result.Error.Message)
	}

	// Parse response content blocks
	chatResp := &ChatResponse{
		Usage: Usage{
			InputTokens:         result.Usage.InputTokens,
			OutputTokens:        result.Usage.OutputTokens,
			CacheReadTokens:     result.Usage.CacheReadInputTokens,
			CacheCreationTokens: result.Usage.CacheCreationInputTokens,
		},
		StopReason: result.StopReason,
	}

	for _, block := range result.Content {
		switch block.Type {
		case "text":
			chatResp.Content += block.Text
		case "tool_use":
			chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	return chatResp, nil
}
