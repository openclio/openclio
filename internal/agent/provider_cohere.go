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

// CohereProvider calls the Cohere Chat API v2.
// Supports Command R and Command R+ — particularly strong for RAG workloads.
type CohereProvider struct {
	apiKey string
	model  string
}

// NewCohereProvider creates a Cohere provider.
// apiKeyEnv is the environment variable name holding the Cohere API key.
func NewCohereProvider(apiKeyEnv, model string) (*CohereProvider, error) {
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("environment variable %s is not set", apiKeyEnv)
	}
	if model == "" {
		model = "command-r-plus-08-2024"
	}
	return &CohereProvider{apiKey: key, model: model}, nil
}

func (p *CohereProvider) Name() string { return "cohere" }

// ── Cohere v2 API types ───────────────────────────────────────────────────────

type cohereRequest struct {
	Model    string          `json:"model"`
	Messages []cohereMessage `json:"messages"`
	Tools    []cohereTool    `json:"tools,omitempty"`
}

type cohereMessage struct {
	Role       string           `json:"role"`
	Content    interface{}      `json:"content"` // string for user/system; []cohereContent for assistant
	ToolCalls  []cohereToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type cohereContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type cohereTool struct {
	Type     string             `json:"type"`
	Function cohereToolFunction `json:"function"`
}

type cohereToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type cohereToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function cohereToolCallFunc `json:"function"`
}

type cohereToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type cohereResponse struct {
	Message cohereRespMsg `json:"message"`
	Usage   cohereUsage   `json:"usage"`
}

type cohereRespMsg struct {
	Role      string           `json:"role"`
	Content   []cohereContent  `json:"content"`
	ToolCalls []cohereToolCall `json:"tool_calls,omitempty"`
}

type cohereUsage struct {
	BilledUnits cohereTokenCount `json:"billed_units"`
}

type cohereTokenCount struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ── Chat implementation ───────────────────────────────────────────────────────

func (p *CohereProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	msgs := make([]cohereMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		msgs = append(msgs, cohereMessage{Role: "system", Content: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		msg := cohereMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, cohereToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: cohereToolCallFunc{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				})
			}
		}
		msgs = append(msgs, msg)
	}

	var tools []cohereTool
	for _, t := range req.Tools {
		tools = append(tools, cohereTool{
			Type: "function",
			Function: cohereToolFunction{
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

	apiReq := cohereRequest{
		Model:    model,
		Messages: msgs,
		Tools:    tools,
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.cohere.com/v2/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Cohere API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Cohere API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result cohereResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	chatResp := &ChatResponse{
		Usage: Usage{
			InputTokens:  result.Usage.BilledUnits.InputTokens,
			OutputTokens: result.Usage.BilledUnits.OutputTokens,
		},
		StopReason: "stop",
	}

	for _, c := range result.Message.Content {
		if c.Type == "text" {
			chatResp.Content += c.Text
		}
	}

	for _, tc := range result.Message.ToolCalls {
		chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	return chatResp, nil
}
