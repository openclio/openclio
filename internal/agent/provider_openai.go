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

// OpenAIProvider calls the OpenAI Chat Completions API.
// Also works with any OpenAI-compatible API (Groq, Together, etc.).
type OpenAIProvider struct {
	apiKey  string
	model   string
	baseURL string
}

// NewOpenAIProvider creates an OpenAI provider.
func NewOpenAIProvider(apiKeyEnv, model string) (*OpenAIProvider, error) {
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("environment variable %s is not set", apiKeyEnv)
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAIProvider{
		apiKey:  key,
		model:   model,
		baseURL: baseURL,
	}, nil
}

func (p *OpenAIProvider) Name() string { return "openai" }

// OpenAI API types
type openAIRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	Tools     []openAITool    `json:"tools,omitempty"`
	MaxTokens int             `json:"max_tokens,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
	Error   *openAIError   `json:"error,omitempty"`
}

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type openAIError struct {
	Message string `json:"message"`
}

func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages
	msgs := make([]openAIMessage, 0, len(req.Messages)+1)

	// System prompt as first message
	if req.SystemPrompt != "" {
		msgs = append(msgs, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		msg := openAIMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		// Convert tool calls
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: openAIFunctionCall{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		msgs = append(msgs, msg)
	}

	// Convert tools
	var tools []openAITool
	for _, t := range req.Tools {
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIFunction{
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

	apiReq := openAIRequest{
		Model:     model,
		Messages:  msgs,
		Tools:     tools,
		MaxTokens: req.MaxTokens,
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling OpenAI API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("OpenAI API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result openAIResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("OpenAI API error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI API returned no choices")
	}

	choice := result.Choices[0]
	chatResp := &ChatResponse{
		Content: choice.Message.Content,
		Usage: Usage{
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
		},
		StopReason: choice.FinishReason,
	}

	// Parse tool calls
	for _, tc := range choice.Message.ToolCalls {
		chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	return chatResp, nil
}
