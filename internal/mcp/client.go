// Package mcp provides a lightweight MCP (Model Context Protocol) stdio client.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const defaultProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Tool is an MCP server tool declaration.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type toolsListResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type toolCallResult struct {
	Content           []toolContentItem `json:"content,omitempty"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
}

type toolContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Server is one running MCP stdio server process.
type Server struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string

	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	stderrBuf  strings.Builder
	exitCh     chan error
	lastExit   string
	lastExitAt time.Time
	mu         sync.Mutex
	nextID     int64
}

// NewServer creates an MCP stdio server client.
func NewServer(name, command string, args []string, env map[string]string) *Server {
	return &Server{
		Name:    strings.TrimSpace(name),
		Command: strings.TrimSpace(command),
		Args:    append([]string(nil), args...),
		Env:     cloneMap(env),
	}
}

// Start launches the server process and performs MCP initialize handshake.
func (s *Server) Start(ctx context.Context) error {
	if s.Command == "" {
		return fmt.Errorf("mcp server %q: command is required", s.Name)
	}
	if s.Name == "" {
		return fmt.Errorf("mcp server name is required")
	}

	cmd := exec.Command(s.Command, s.Args...)

	// Environment: start from current env and overlay configured values.
	cmd.Env = os.Environ()
	for k, raw := range s.Env {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		val := strings.TrimSpace(raw)
		if strings.HasPrefix(val, "$") {
			val = os.Getenv(strings.TrimPrefix(val, "$"))
		} else if envVal := os.Getenv(val); envVal != "" {
			// If value matches an env var name, treat it as an indirection.
			val = envVal
		}
		cmd.Env = append(cmd.Env, key+"="+val)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp server %q: opening stdin: %w", s.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp server %q: opening stdout: %w", s.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mcp server %q: opening stderr: %w", s.Name, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp server %q: start failed: %w", s.Name, err)
	}

	go s.captureStderr(stderr)
	exitCh := make(chan error, 1)
	go s.watchProcess(cmd, exitCh)

	s.mu.Lock()
	s.cmd = cmd
	s.stdin = stdin
	s.stdout = bufio.NewReader(stdout)
	s.stderrBuf.Reset()
	s.exitCh = exitCh
	s.lastExit = ""
	s.lastExitAt = time.Time{}
	s.nextID = 1
	s.mu.Unlock()

	// Initialize handshake.
	initCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var initResult map[string]any
	if err := s.callLocked(initCtx, "initialize", map[string]any{
		"protocolVersion": defaultProtocolVersion,
		"clientInfo": map[string]any{
			"name":    "agent",
			"version": "1.0",
		},
		"capabilities": map[string]any{},
	}, &initResult); err != nil {
		_ = s.Stop(context.Background())
		return fmt.Errorf("mcp server %q: initialize failed: %w", s.Name, err)
	}

	_ = s.notify(initCtx, "notifications/initialized", map[string]any{})
	return nil
}

// Stop terminates the MCP server process.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	stdin := s.stdin
	exitCh := s.exitCh
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	wait := exitCh
	if wait == nil {
		wait = make(chan error)
	}

	select {
	case err := <-wait:
		if err == nil {
			return nil
		}
		// A non-zero exit during shutdown is not considered fatal.
		return nil
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		if exitCh != nil {
			<-exitCh
		}
		return ctx.Err()
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		if exitCh != nil {
			<-exitCh
		}
		return nil
	}
}

func (s *Server) watchProcess(cmd *exec.Cmd, exitCh chan error) {
	err := cmd.Wait()

	s.mu.Lock()
	if s.cmd == cmd {
		s.cmd = nil
		s.stdin = nil
		s.stdout = nil
	}
	if err != nil {
		s.lastExit = err.Error()
	} else {
		s.lastExit = ""
	}
	s.lastExitAt = time.Now().UTC()
	s.mu.Unlock()

	select {
	case exitCh <- err:
	default:
	}
	close(exitCh)
}

// ExitCh returns the current process-exit notification channel.
func (s *Server) ExitCh() <-chan error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCh
}

// LastExit reports the latest process exit time and error text.
func (s *Server) LastExit() (time.Time, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastExitAt, s.lastExit
}

// ListTools returns all tools from the MCP server (handles pagination).
func (s *Server) ListTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result toolsListResult
		if err := s.callLocked(ctx, "tools/list", params, &result); err != nil {
			return nil, err
		}
		all = append(all, result.Tools...)
		if strings.TrimSpace(result.NextCursor) == "" {
			break
		}
		cursor = result.NextCursor
	}

	// Normalize missing schemas to empty object schemas.
	for i := range all {
		if len(all[i].InputSchema) == 0 {
			all[i].InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
	}
	return all, nil
}

// CallTool invokes one MCP tool and returns a text representation of the output.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	var result toolCallResult
	if args == nil {
		args = map[string]any{}
	}
	if err := s.callLocked(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	}, &result); err != nil {
		return "", err
	}

	var parts []string
	for _, item := range result.Content {
		switch item.Type {
		case "text":
			if strings.TrimSpace(item.Text) != "" {
				parts = append(parts, item.Text)
			}
		default:
			blob, _ := json.Marshal(item)
			parts = append(parts, string(blob))
		}
	}
	if len(parts) == 0 && len(result.StructuredContent) > 0 {
		parts = append(parts, string(result.StructuredContent))
	}
	out := strings.TrimSpace(strings.Join(parts, "\n"))
	if out == "" {
		out = "[no output]"
	}
	if result.IsError {
		return "", errors.New(out)
	}
	return out, nil
}

func (s *Server) callLocked(ctx context.Context, method string, params any, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stdin == nil || s.stdout == nil {
		return fmt.Errorf("mcp server %q is not running", s.Name)
	}

	id := atomic.AddInt64(&s.nextID, 1)
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	line, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request %s: %w", method, err)
	}
	line = append(line, '\n')
	if _, err := s.stdin.Write(line); err != nil {
		return fmt.Errorf("write request %s: %w%s", method, err, s.stderrSuffix())
	}

	wantID := strconv.FormatInt(id, 10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		respLine, err := s.stdout.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("mcp server %q closed stdout%s", s.Name, s.stderrSuffix())
			}
			return fmt.Errorf("read response for %s: %w%s", method, err, s.stderrSuffix())
		}
		respLine = bytesTrimSpace(respLine)
		if len(respLine) == 0 {
			continue
		}

		var env rpcEnvelope
		if err := json.Unmarshal(respLine, &env); err != nil {
			// Ignore malformed lines from server logs.
			continue
		}

		// Notifications (no ID) are ignored by request/response flow.
		gotID := strings.TrimSpace(string(env.ID))
		gotID = strings.Trim(gotID, `"`)
		if gotID == "" || gotID != wantID {
			continue
		}

		if env.Error != nil {
			return fmt.Errorf("mcp error %d: %s", env.Error.Code, env.Error.Message)
		}
		if out != nil {
			if err := json.Unmarshal(env.Result, out); err != nil {
				return fmt.Errorf("decode result for %s: %w", method, err)
			}
		}
		return nil
	}
}

func (s *Server) notify(ctx context.Context, method string, params any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin == nil {
		return fmt.Errorf("mcp server %q is not running", s.Name)
	}
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = s.stdin.Write(line)
	return err
}

func (s *Server) captureStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	// Increase scanner limit so long startup stack traces are captured.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 256*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.mu.Lock()
		if s.stderrBuf.Len() > 0 {
			s.stderrBuf.WriteString(" | ")
		}
		s.stderrBuf.WriteString(line)
		s.mu.Unlock()
	}
}

func (s *Server) stderrSuffix() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stderrBuf.Len() == 0 {
		return ""
	}
	return " (stderr: " + s.stderrBuf.String() + ")"
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	return b
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
