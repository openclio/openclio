package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openclio/openclio/internal/config"
	internlog "github.com/openclio/openclio/internal/logger"
	"github.com/openclio/openclio/internal/mcp"
	"github.com/openclio/openclio/internal/tools"
)

func TestMCPMainHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_MAIN_HELPER") != "1" {
		return
	}

	eventFile := strings.TrimSpace(os.Getenv("MCP_MAIN_HELPER_EVENT_FILE"))
	recordEvent := func(event string) {
		if eventFile == "" {
			return
		}
		f, err := os.OpenFile(eventFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return
		}
		_, _ = f.WriteString(event + "\n")
		_ = f.Close()
	}

	recordEvent("start")

	toolNames := []string{"ping"}
	if raw := strings.TrimSpace(os.Getenv("MCP_MAIN_HELPER_TOOLS")); raw != "" {
		parts := strings.Split(raw, ",")
		toolNames = toolNames[:0]
		for _, p := range parts {
			name := strings.TrimSpace(p)
			if name != "" {
				toolNames = append(toolNames, name)
			}
		}
		if len(toolNames) == 0 {
			toolNames = []string{"ping"}
		}
	}

	failFirstList := os.Getenv("MCP_MAIN_HELPER_FAIL_FIRST_LIST") == "1"
	failListCallsRemaining := 0
	if raw := strings.TrimSpace(os.Getenv("MCP_MAIN_HELPER_FAIL_LIST_CALLS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			failListCallsRemaining = n
		}
	}
	if failFirstList && failListCallsRemaining == 0 {
		failListCallsRemaining = 1
	}
	listCalls := 0

	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var req map[string]any
		_ = json.Unmarshal([]byte(line), &req)
		id, hasID := req["id"]
		method, _ := req["method"].(string)
		if !hasID {
			continue
		}

		switch method {
		case "initialize":
			writeMCPHelperRPC(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
				},
			})
		case "tools/list":
			listCalls++
			recordEvent(fmt.Sprintf("list:%d", listCalls))
			if failListCallsRemaining > 0 {
				failListCallsRemaining--
				writeMCPHelperRPC(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"error": map[string]any{
						"code":    -32001,
						"message": "simulated health failure",
					},
				})
				continue
			}

			toolsOut := make([]map[string]any, 0, len(toolNames))
			for _, name := range toolNames {
				toolsOut = append(toolsOut, map[string]any{
					"name":        name,
					"description": "test tool",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
				})
			}
			writeMCPHelperRPC(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"tools": toolsOut,
				},
			})
		case "tools/call":
			writeMCPHelperRPC(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"content": []map[string]any{
						{
							"type": "text",
							"text": "ok",
						},
					},
				},
			})
		default:
			writeMCPHelperRPC(map[string]any{
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

func writeMCPHelperRPC(v map[string]any) {
	raw, _ := json.Marshal(v)
	_, _ = os.Stdout.Write(append(raw, '\n'))
}

func TestStartMCPHealthLoop_RestartsUnhealthyServer(t *testing.T) {
	eventsFile := filepath.Join(t.TempDir(), "mcp-events.log")
	srv := mcp.NewServer(
		"health-helper",
		os.Args[0],
		[]string{"-test.run=TestMCPMainHelperProcess", "--"},
		map[string]string{
			"GO_WANT_MCP_MAIN_HELPER":         "1",
			"MCP_MAIN_HELPER_EVENT_FILE":      eventsFile,
			"MCP_MAIN_HELPER_FAIL_FIRST_LIST": "1",
		},
	)

	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startCancel()
	if err := srv.Start(startCtx); err != nil {
		t.Fatalf("starting helper server: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(stopCtx)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startMCPHealthLoopWithInterval(ctx, []*mcp.Server{srv}, internlog.New("debug", "stderr"), 50*time.Millisecond)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(eventsFile)
		if strings.Count(string(data), "start\n") >= 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, _ := os.ReadFile(eventsFile)
	t.Fatalf("expected MCP health loop to restart server (start count >=2), events:\n%s", string(data))
}

func TestRegisterMCPTools_SkipsBuiltInNameCollisions(t *testing.T) {
	registry := tools.NewRegistry(config.DefaultConfig().Tools, t.TempDir(), "")

	servers, err := registerMCPTools(registry, []config.MCPServerConfig{
		{
			Name:    "collision",
			Command: os.Args[0],
			Args:    []string{"-test.run=TestMCPMainHelperProcess", "--"},
			Env: map[string]string{
				"GO_WANT_MCP_MAIN_HELPER": "1",
				"MCP_MAIN_HELPER_TOOLS":   "exec,custom_tool",
			},
		},
	}, internlog.New("debug", "stderr"))
	if err != nil {
		t.Fatalf("registerMCPTools failed: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for _, srv := range servers {
			_ = srv.Stop(stopCtx)
		}
	}()

	if registry.HasTool("mcp_collision_exec") {
		t.Fatalf("expected colliding MCP tool to be skipped")
	}
	if !registry.HasTool("mcp_collision_custom_tool") {
		t.Fatalf("expected non-colliding MCP tool to be registered")
	}
}

func TestMCPHealthSupervisor_SnapshotAndManualRestart(t *testing.T) {
	srv := mcp.NewServer(
		"status-helper",
		os.Args[0],
		[]string{"-test.run=TestMCPMainHelperProcess", "--"},
		map[string]string{
			"GO_WANT_MCP_MAIN_HELPER": "1",
		},
	)
	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startCancel()
	if err := srv.Start(startCtx); err != nil {
		t.Fatalf("starting helper server: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(stopCtx)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := newMCPHealthSupervisor([]*mcp.Server{srv}, internlog.New("debug", "stderr"))
	supervisor.checkTimeout = 200 * time.Millisecond
	supervisor.restartTimeout = 2 * time.Second
	supervisor.initialBackoff = 20 * time.Millisecond
	supervisor.maxBackoff = 100 * time.Millisecond
	supervisor.Start(ctx, 50*time.Millisecond)

	var found bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range supervisor.SnapshotMCPStatus() {
			if st.Name == "status-helper" {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatalf("expected status-helper in supervisor snapshot")
	}

	if err := supervisor.RestartMCPServer("status-helper"); err != nil {
		t.Fatalf("manual restart failed: %v", err)
	}
	statuses := supervisor.SnapshotMCPStatus()
	restarts := -1
	for _, st := range statuses {
		if st.Name == "status-helper" {
			restarts = st.RestartCount
			if st.Status != "healthy" {
				t.Fatalf("expected healthy status after manual restart, got %+v", st)
			}
			break
		}
	}
	if restarts < 1 {
		t.Fatalf("expected restart_count >= 1 after manual restart, got %d", restarts)
	}
}

func TestMCPHealthSupervisor_DisablesAfterMaxRestartFailures(t *testing.T) {
	broken := mcp.NewServer("broken-helper", "/definitely/missing/binary", nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := newMCPHealthSupervisor([]*mcp.Server{broken}, internlog.New("debug", "stderr"))
	supervisor.maxFailures = 2
	supervisor.initialBackoff = 20 * time.Millisecond
	supervisor.maxBackoff = 50 * time.Millisecond
	supervisor.checkTimeout = 50 * time.Millisecond
	supervisor.restartTimeout = 150 * time.Millisecond
	supervisor.Start(ctx, 20*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range supervisor.SnapshotMCPStatus() {
			if st.Name == "broken-helper" && st.Disabled {
				if st.Status != "offline" {
					t.Fatalf("expected offline status when disabled, got %+v", st)
				}
				if st.ConsecutiveFailures < 2 {
					t.Fatalf("expected at least 2 consecutive failures, got %+v", st)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("expected broken-helper to become disabled after max failures, got %+v", supervisor.SnapshotMCPStatus())
}
