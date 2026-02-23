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

// ChatStream implements the Streamer interface for Anthropic.
// It uses Anthropic's streaming Messages API (text/event-stream).
func (p *AnthropicProvider) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	// Re-use the same message-building logic as Chat by building the request body
	msgs, tools, model, maxTokens, sysBlocks := p.buildRequest(req)

	apiReq := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    sysBlocks,
		Messages:  msgs,
		Tools:     tools,
		Stream:    true,
	}

	body, _ := json.Marshal(apiReq)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Anthropic stream API: %w", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("Anthropic stream API error (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// pendingToolCall accumulates streaming tool-use input JSON by block index.
		type pendingToolCall struct {
			id    string
			name  string
			input strings.Builder
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

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" || data == "" {
				continue
			}

			var evt anthropicStreamEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				continue
			}

			switch evt.Type {
			case "content_block_start":
				if evt.ContentBlock != nil && evt.ContentBlock.Type == "tool_use" {
					toolBlocks[evt.Index] = &pendingToolCall{
						id:   evt.ContentBlock.ID,
						name: evt.ContentBlock.Name,
					}
				}
			case "content_block_delta":
				switch evt.Delta.Type {
				case "text_delta":
					if evt.Delta.Text != "" {
						if !send(StreamChunk{Text: evt.Delta.Text}) {
							return
						}
					}
				case "input_json_delta":
					if tb, ok := toolBlocks[evt.Index]; ok {
						tb.input.WriteString(evt.Delta.PartialJSON)
					}
				}
			case "message_stop":
				// Build ToolCalls from accumulated tool blocks.
				var toolCalls []ToolCall
				for _, tb := range toolBlocks {
					args := json.RawMessage(tb.input.String())
					if len(args) == 0 {
						args = json.RawMessage("{}")
					}
					toolCalls = append(toolCalls, ToolCall{
						ID:        tb.id,
						Name:      tb.name,
						Arguments: args,
					})
				}
				send(StreamChunk{ToolCalls: toolCalls, Done: true})
				return
			case "error":
				send(StreamChunk{Error: fmt.Errorf("Anthropic stream error: %s", evt.Error.Message), Done: true})
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

// Stream implements the generic Streamer interface.
func (p *AnthropicProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	return p.ChatStream(ctx, req)
}

// ── Anthropic streaming types ────────────────────────────────────────────────

type anthropicStreamEvent struct {
	Type         string                   `json:"type"`
	Index        int                      `json:"index"`
	ContentBlock *anthropicContentBlock   `json:"content_block,omitempty"`
	Delta        anthropicStreamDelta     `json:"delta"`
	Error        anthropicStreamErrDetail `json:"error"`
}

// anthropicContentBlock is the payload of a content_block_start event.
type anthropicContentBlock struct {
	Type string `json:"type"` // "text" or "tool_use"
	ID   string `json:"id"`
	Name string `json:"name"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type"` // "text_delta" or "input_json_delta"
	Text        string `json:"text"`
	PartialJSON string `json:"partial_json"`
}

type anthropicStreamErrDetail struct {
	Message string `json:"message"`
}

// ── buildRequest helper (DRY with Chat) ─────────────────────────────────────

func (p *AnthropicProvider) buildRequest(req ChatRequest) ([]anthropicMessage, []anthropicTool, string, int, []anthropicContent) {
	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			continue
		}
		role := m.Role
		if role == "tool" {
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
		if role == "assistant" && len(m.ToolCalls) > 0 {
			var blocks []map[string]interface{}
			if m.Content != "" {
				blocks = append(blocks, map[string]interface{}{"type": "text", "text": m.Content})
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

	// Build tools and add cache block to the very last tool
	var tools []anthropicTool
	for i, t := range req.Tools {
		tool := anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		}
		if i == len(req.Tools)-1 {
			tool.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
		}
		tools = append(tools, tool)
	}

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

	return msgs, tools, model, maxTokens, sysBlocks
}
