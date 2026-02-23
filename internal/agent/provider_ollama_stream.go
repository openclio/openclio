package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Stream implements the Streamer interface for Ollama.
// Ollama returns newline-delimited JSON (NDJSON) when stream=true.
// Each line is a partial ollamaResponse; the final line has done=true and
// may carry tool_calls in the message field.
func (p *OllamaProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	msgs := make([]ollamaMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		msg := ollamaMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		// Carry tool calls on assistant messages.
		for i, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, ollamaToolCall{
				Function: ollamaFunctionCall{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
			_ = i
		}
		msgs = append(msgs, msg)
	}

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
		Stream:   true,
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Ollama stream API (is Ollama running?): %w", err)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("Ollama stream API error (HTTP %d)", resp.StatusCode)
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		send := func(chunk StreamChunk) bool {
			select {
			case ch <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var evt ollamaResponse
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}

			if evt.Error != "" {
				send(StreamChunk{Error: fmt.Errorf("Ollama error: %s", evt.Error), Done: true})
				return
			}

			// Emit text token from this chunk.
			if evt.Message.Content != "" {
				if !send(StreamChunk{Text: evt.Message.Content}) {
					return
				}
			}

			if evt.Done {
				// Final chunk — collect any tool calls from the message.
				var toolCalls []ToolCall
				for i, tc := range evt.Message.ToolCalls {
					toolCalls = append(toolCalls, ToolCall{
						ID:        fmt.Sprintf("ollama_%d", i),
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					})
				}
				send(StreamChunk{ToolCalls: toolCalls, Done: true})
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamChunk{Error: fmt.Errorf("reading Ollama stream: %w", err), Done: true}
		} else {
			ch <- StreamChunk{Done: true}
		}
	}()

	return ch, nil
}
