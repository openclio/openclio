package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Stream implements the Streamer interface for OpenAI.
// It uses OpenAI's streaming Chat Completions API (text/event-stream).
func (p *OpenAIProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	msgs, tools, model, maxTokens := p.buildRequest(req)

	apiReq := struct {
		openAIRequest
		Stream bool `json:"stream"`
	}{
		openAIRequest: openAIRequest{
			Model:     model,
			Messages:  msgs,
			Tools:     tools,
			MaxTokens: maxTokens,
		},
		Stream: true,
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling OpenAI stream API: %w", err)
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		resp.Body.Close()
		bodyText := strings.TrimSpace(string(respBody))
		if bodyText != "" {
			return nil, fmt.Errorf("OpenAI stream API error (HTTP %d): %s", resp.StatusCode, bodyText)
		}
		return nil, fmt.Errorf("OpenAI stream API error (HTTP %d)", resp.StatusCode)
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// pendingToolCall accumulates streaming tool-call argument JSON by index.
		type pendingToolCall struct {
			id   string
			name string
			args strings.Builder
		}
		toolBlocks := map[int]*pendingToolCall{}

		send := func(chunk StreamChunk) bool {
			select {
			case ch <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// buildToolCalls converts accumulated blocks into finished ToolCall values.
		buildToolCalls := func() []ToolCall {
			if len(toolBlocks) == 0 {
				return nil
			}
			out := make([]ToolCall, 0, len(toolBlocks))
			for i := 0; i < len(toolBlocks); i++ {
				tb, ok := toolBlocks[i]
				if !ok {
					continue
				}
				args := json.RawMessage(tb.args.String())
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				out = append(out, ToolCall{ID: tb.id, Name: tb.name, Arguments: args})
			}
			return out
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				// Fallback sentinel — emit any accumulated tool calls, then Done.
				send(StreamChunk{ToolCalls: buildToolCalls(), Done: true})
				return
			}
			if data == "" {
				continue
			}

			var evt openAIStreamEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				continue
			}
			if len(evt.Choices) == 0 {
				continue
			}

			choice := evt.Choices[0]
			delta := choice.Delta

			// Stream text tokens.
			if delta.Content != "" {
				if !send(StreamChunk{Text: delta.Content}) {
					return
				}
			}

			// Accumulate tool call deltas by index.
			for _, tc := range delta.ToolCalls {
				tb, ok := toolBlocks[tc.Index]
				if !ok {
					tb = &pendingToolCall{}
					toolBlocks[tc.Index] = tb
				}
				if tc.ID != "" {
					tb.id = tc.ID
				}
				if tc.Function.Name != "" {
					tb.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					tb.args.WriteString(tc.Function.Arguments)
				}
			}

			// Handle finish reasons.
			switch choice.FinishReason {
			case "tool_calls":
				send(StreamChunk{ToolCalls: buildToolCalls(), Done: true})
				return
			case "stop", "length":
				send(StreamChunk{Done: true})
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamChunk{Error: fmt.Errorf("reading stream: %w", err), Done: true}
		} else {
			ch <- StreamChunk{Done: true}
		}
	}()

	return ch, nil
}

// ── OpenAI streaming types ───────────────────────────────────────────────────

type openAIStreamEvent struct {
	Choices []openAIStreamChoice `json:"choices"`
}

type openAIStreamChoice struct {
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason string            `json:"finish_reason"`
}

type openAIStreamDelta struct {
	Content   string                      `json:"content"`
	ToolCalls []openAIStreamToolCallDelta `json:"tool_calls,omitempty"`
}

// openAIStreamToolCallDelta carries an incremental tool-call update.
// Only the first delta for a given index carries id, type, and function.name;
// subsequent deltas carry only function.arguments fragments.
type openAIStreamToolCallDelta struct {
	Index    int                   `json:"index"`
	ID       string                `json:"id,omitempty"`
	Type     string                `json:"type,omitempty"`
	Function openAIStreamFuncDelta `json:"function"`
}

type openAIStreamFuncDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ── buildRequest helper (DRY with Chat) ─────────────────────────────────────

func (p *OpenAIProvider) buildRequest(req ChatRequest) ([]openAIMessage, []openAITool, string, int) {
	msgs := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		msgs = append(msgs, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		msg := openAIMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
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
	return msgs, tools, model, req.MaxTokens
}
