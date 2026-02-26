package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OllamaProvider calls the Ollama local API.
// Zero cost, full privacy — runs on your machine.
type OllamaProvider struct {
	model   string
	baseURL string
}

// ollamaBaseURL normalizes an Ollama base URL to use 127.0.0.1 so we hit IPv4 (Ollama often binds to 127.0.0.1 only).
func ollamaBaseURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "http://127.0.0.1:11434"
	}
	if strings.Contains(raw, "//localhost") {
		raw = strings.Replace(raw, "//localhost", "//127.0.0.1", 1)
	}
	return raw
}

// NewOllamaProvider creates an Ollama provider with default base URL (127.0.0.1:11434).
func NewOllamaProvider(model string) *OllamaProvider {
	return NewOllamaProviderWithBaseURL(model, "")
}

// NewOllamaProviderWithBaseURL creates an Ollama provider with the given base URL (empty = default).
func NewOllamaProviderWithBaseURL(model, baseURL string) *OllamaProvider {
	return &OllamaProvider{
		model:   model,
		baseURL: ollamaBaseURL(baseURL),
	}
}

func (p *OllamaProvider) Name() string { return "ollama" }

// Ollama API types
type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
	Stream   bool            `json:"stream"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaFunction `json:"function"`
}

type ollamaFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ollamaToolCall struct {
	Function ollamaFunctionCall `json:"function"`
}

type ollamaFunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ollamaResponse struct {
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
	Error           string        `json:"error,omitempty"`
}

func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages
	msgs := make([]ollamaMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		msg := ollamaMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		msgs = append(msgs, msg)
	}

	// Convert tools
	var tools []ollamaTool
	for _, t := range req.Tools {
		tools = append(tools, ollamaTool{
			Type: "function",
			Function: ollamaFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	model := req.Model
	if model == "" {
		model = p.model
	}

	apiReq := ollamaRequest{
		Model:    model,
		Messages: msgs,
		Tools:    tools,
		Stream:   false, // non-streaming for now
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Ollama API (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Ollama API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result ollamaResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("Ollama error: %s", result.Error)
	}

	chatResp := &ChatResponse{
		Content: result.Message.Content,
		Usage: Usage{
			InputTokens:  result.PromptEvalCount,
			OutputTokens: result.EvalCount,
		},
		StopReason: "stop",
	}

	// Parse tool calls
	for i, tc := range result.Message.ToolCalls {
		chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
			ID:        fmt.Sprintf("ollama_%d", i),
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return chatResp, nil
}
