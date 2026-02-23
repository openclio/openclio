package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Stream implements the Streamer interface for Gemini.
// It uses the streamGenerateContent endpoint with alt=sse, which returns
// Server-Sent Events where each data payload is a full (partial) geminiResponse.
// Text parts are emitted as tokens; function call parts are collected and
// emitted on the final Done chunk.
func (p *GeminiProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	// Build the request body using the same structure as Chat.
	apiReq := geminiRequest{}

	if req.SystemPrompt != "" {
		apiReq.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		}
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			content := geminiContent{Role: "model"}
			if m.Content != "" {
				content.Parts = append(content.Parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				content.Parts = append(content.Parts, geminiPart{
					FunctionCall: &geminiFuncCall{Name: tc.Name, Args: tc.Arguments},
				})
			}
			apiReq.Contents = append(apiReq.Contents, content)

		case "tool":
			resp := json.RawMessage(fmt.Sprintf(`{"output":%q}`, m.Content))
			functionName := m.ToolCallID
			if functionName == "" {
				functionName = "tool_result"
			}
			apiReq.Contents = append(apiReq.Contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResp: &geminiFuncResp{Name: functionName, Response: resp},
				}},
			})

		default:
			if m.Role == "system" {
				continue
			}
			apiReq.Contents = append(apiReq.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			})
		}
	}

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
		return nil, fmt.Errorf("marshaling stream request: %w", err)
	}

	// alt=sse switches the response to Server-Sent Events format.
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s",
		p.model, p.apiKey,
	)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling Gemini stream API: %w", err)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("Gemini stream API error (HTTP %d)", resp.StatusCode)
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// Accumulate function calls across chunks (they usually arrive together
		// in a single chunk, but collect defensively).
		var pendingToolCalls []ToolCall

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
			if data == "" {
				continue
			}

			var evt geminiResponse
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				continue
			}

			if evt.Error != nil {
				send(StreamChunk{
					Error: fmt.Errorf("Gemini stream error %d: %s", evt.Error.Code, evt.Error.Message),
					Done:  true,
				})
				return
			}

			if len(evt.Candidates) == 0 {
				continue
			}

			candidate := evt.Candidates[0]

			// Extract text and function call parts from this chunk.
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					if !send(StreamChunk{Text: part.Text}) {
						return
					}
				}
				if part.FunctionCall != nil {
					pendingToolCalls = append(pendingToolCalls, ToolCall{
						ID:        part.FunctionCall.Name,
						Name:      part.FunctionCall.Name,
						Arguments: part.FunctionCall.Args,
					})
				}
			}

			// A non-empty finishReason signals the last chunk.
			if candidate.FinishReason != "" {
				send(StreamChunk{ToolCalls: pendingToolCalls, Done: true})
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- StreamChunk{Error: fmt.Errorf("reading Gemini stream: %w", err), Done: true}
		} else {
			// Emit any accumulated tool calls on stream end.
			ch <- StreamChunk{ToolCalls: pendingToolCalls, Done: true}
		}
	}()

	return ch, nil
}
