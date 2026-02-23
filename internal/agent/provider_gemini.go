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

// GeminiProvider calls the Google Gemini API via the generativelanguage REST endpoint.
// Supports function calling (tool use) through Gemini's functionDeclarations API.
type GeminiProvider struct {
	apiKey string
	model  string
}

// NewGeminiProvider creates a Gemini provider.
// apiKeyEnv is the environment variable name containing the Gemini API key.
func NewGeminiProvider(apiKeyEnv, model string) (*GeminiProvider, error) {
	key := os.Getenv(apiKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("environment variable %s is not set", apiKeyEnv)
	}
	if model == "" {
		model = "gemini-1.5-flash"
	}
	return &GeminiProvider{apiKey: key, model: model}, nil
}

func (p *GeminiProvider) Name() string { return "gemini" }

// ── Gemini API types ─────────────────────────────────────────────────────────

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	Tools             []geminiTool    `json:"tools,omitempty"`
	GenerationConfig  geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text         string          `json:"text,omitempty"`
	FunctionCall *geminiFuncCall `json:"functionCall,omitempty"`
	FunctionResp *geminiFuncResp `json:"functionResponse,omitempty"`
}

type geminiFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFuncResp struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type geminiGenConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata geminiUsage       `json:"usageMetadata"`
	Error         *geminiError      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── Chat implementation ───────────────────────────────────────────────────────

func (p *GeminiProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	apiReq := geminiRequest{}

	// System instruction (Gemini uses a dedicated field, not a message role)
	if req.SystemPrompt != "" {
		apiReq.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		}
	}

	// Convert messages
	// Gemini roles: "user" and "model" (not "assistant")
	// Tool results use role "user" with a functionResponse part
	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			// Assistant messages with tool calls
			content := geminiContent{Role: "model"}
			if m.Content != "" {
				content.Parts = append(content.Parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				content.Parts = append(content.Parts, geminiPart{
					FunctionCall: &geminiFuncCall{
						Name: tc.Name,
						Args: tc.Arguments,
					},
				})
			}
			apiReq.Contents = append(apiReq.Contents, content)

		case "tool":
			// Tool results — wrap in a functionResponse part under role "user"
			resp := json.RawMessage(fmt.Sprintf(`{"output":%q}`, m.Content))
			functionName := m.ToolCallID // we use ToolCallID to carry the function name for Gemini
			if functionName == "" {
				functionName = "tool_result"
			}
			apiReq.Contents = append(apiReq.Contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResp: &geminiFuncResp{
						Name:     functionName,
						Response: resp,
					},
				}},
			})

		default: // "user", "system" (system already handled)
			if m.Role == "system" {
				continue
			}
			apiReq.Contents = append(apiReq.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			})
		}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		var decls []geminiFuncDecl
		for _, t := range req.Tools {
			decls = append(decls, geminiFuncDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		apiReq.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}

	if req.MaxTokens > 0 {
		apiReq.GenerationConfig = geminiGenConfig{MaxOutputTokens: req.MaxTokens}
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		p.model, p.apiKey,
	)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Gemini API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gemini API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result geminiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("Gemini API error %d: %s", result.Error.Code, result.Error.Message)
	}

	if len(result.Candidates) == 0 {
		return nil, fmt.Errorf("Gemini API returned no candidates")
	}

	candidate := result.Candidates[0]
	chatResp := &ChatResponse{
		Usage: Usage{
			InputTokens:  result.UsageMetadata.PromptTokenCount,
			OutputTokens: result.UsageMetadata.CandidatesTokenCount,
		},
		StopReason: candidate.FinishReason,
	}

	// Parse parts — may contain text and/or function calls
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			chatResp.Content += part.Text
		}
		if part.FunctionCall != nil {
			chatResp.ToolCalls = append(chatResp.ToolCalls, ToolCall{
				ID:        part.FunctionCall.Name, // Gemini uses function name as ID
				Name:      part.FunctionCall.Name,
				Arguments: part.FunctionCall.Args,
			})
		}
	}

	return chatResp, nil
}
