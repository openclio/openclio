package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestServerStartListCallAndStop(t *testing.T) {
	t.Setenv("MCP_HELPER_FLAG", "1")

	srv := NewServer(
		"helper",
		os.Args[0],
		[]string{"-test.run=TestMCPHelperProcess", "--"},
		map[string]string{
			"GO_WANT_MCP_HELPER": "MCP_HELPER_FLAG",
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		_ = srv.Stop(context.Background())
	}()

	tools, err := srv.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Fatalf("expected tool name echo, got %q", tools[0].Name)
	}

	out, err := srv.CallTool(ctx, "echo", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("CallTool() failed: %v", err)
	}
	if strings.TrimSpace(out) != "echo: hello" {
		t.Fatalf("unexpected tool output: %q", out)
	}
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}

	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}

		var req map[string]any
		_ = json.Unmarshal([]byte(line), &req)
		id, hasID := req["id"]
		method, _ := req["method"].(string)

		if !hasID {
			// notification
			continue
		}

		switch method {
		case "initialize":
			writeRPC(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": defaultProtocolVersion,
					"capabilities":    map[string]any{},
				},
			})
		case "tools/list":
			writeRPC(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "echo",
							"description": "Echoes the message argument",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"message": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			})
		case "tools/call":
			params, _ := req["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			msg, _ := args["message"].(string)
			writeRPC(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"content": []map[string]any{
						{
							"type": "text",
							"text": fmt.Sprintf("echo: %s", msg),
						},
					},
				},
			})
		default:
			writeRPC(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"error": map[string]any{
					"code":    -32601,
					"message": "method not found",
				},
			})
		}
	}

	os.Exit(0)
}

func writeRPC(v map[string]any) {
	raw, _ := json.Marshal(v)
	_, _ = os.Stdout.Write(append(raw, '\n'))
}
