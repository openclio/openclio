package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func init() {
	_ = ReplaceTool("process_spawn", processSpawnTool)
	_ = ReplaceTool("process_list", processListTool)
	_ = ReplaceTool("process_kill", processKillTool)
	_ = ReplaceTool("process_read", processReadTool)
}

type procEntry struct {
	ID        string
	Cmd       *exec.Cmd
	Stdout    string
	Stderr    string
	StartedAt time.Time
	ExitAt    time.Time
	ExitCode  *int
	mu        sync.Mutex
}

var (
	processMu   sync.Mutex
	processes   = make(map[string]*procEntry)
	processSeq  int64
	processBase = filepath.Join(os.TempDir(), "openclio", "processes")
)

func ensureBaseDir() error {
	return os.MkdirAll(processBase, 0700)
}

func newProcID() string {
	processMu.Lock()
	processSeq++
	id := fmt.Sprintf("p-%d-%d", time.Now().UnixNano(), processSeq)
	processMu.Unlock()
	return id
}

func startCaptureFiles(id string) (stdoutPath, stderrPath string, err error) {
	if err := ensureBaseDir(); err != nil {
		return "", "", err
	}
	dir := filepath.Join(processBase, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	stdoutPath = filepath.Join(dir, "stdout.log")
	stderrPath = filepath.Join(dir, "stderr.log")
	return stdoutPath, stderrPath, nil
}

func processSpawnTool(ctx context.Context, payload map[string]any) (any, error) {
	cmdRaw, ok := payload["command"]
	if !ok {
		return nil, fmt.Errorf("command is required")
	}
	cmdStr, _ := cmdRaw.(string)
	if strings.TrimSpace(cmdStr) == "" {
		return nil, fmt.Errorf("command cannot be empty")
	}
	wd, _ := payload["work_dir"].(string)
	envIface, _ := payload["env"].([]any)
	var env []string
	for _, e := range envIface {
		if s, ok := e.(string); ok {
			env = append(env, s)
		}
	}

	id := newProcID()
	stdoutPath, stderrPath, err := startCaptureFiles(id)
	if err != nil {
		return nil, err
	}

	// Run via shell for convenience
	cmd := exec.Command("sh", "-lc", cmdStr)
	if wd != "" {
		cmd.Dir = wd
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	stdoutF, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	stderrF, err := os.Create(stderrPath)
	if err != nil {
		stdoutF.Close()
		return nil, err
	}

	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		stdoutF.Close()
		stderrF.Close()
		return nil, err
	}

	e := &procEntry{
		ID:        id,
		Cmd:       cmd,
		Stdout:    stdoutPath,
		Stderr:    stderrPath,
		StartedAt: time.Now().UTC(),
	}
	processMu.Lock()
	processes[id] = e
	processMu.Unlock()

	// stream pipes to files
	go func() {
		defer stdoutF.Close()
		io.Copy(stdoutF, stdoutPipe)
	}()
	go func() {
		defer stderrF.Close()
		io.Copy(stderrF, stderrPipe)
	}()

	// wait and record exit
	go func() {
		err := cmd.Wait()
		e.mu.Lock()
		defer e.mu.Unlock()
		now := time.Now().UTC()
		e.ExitAt = now
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				code = -1
			}
		}
		e.ExitCode = &code
	}()

	return map[string]any{"id": id, "stdout": stdoutPath, "stderr": stderrPath}, nil
}

func processListTool(ctx context.Context, payload map[string]any) (any, error) {
	processMu.Lock()
	defer processMu.Unlock()
	out := make([]map[string]any, 0, len(processes))
	for id, e := range processes {
		e.mu.Lock()
		running := e.ExitCode == nil
		var exitCode any
		if e.ExitCode != nil {
			exitCode = *e.ExitCode
		}
		out = append(out, map[string]any{
			"id":         id,
			"running":    running,
			"started_at": e.StartedAt,
			"exit_at":    e.ExitAt,
			"exit_code":  exitCode,
			"stdout":     e.Stdout,
			"stderr":     e.Stderr,
		})
		e.mu.Unlock()
	}
	return out, nil
}

func processKillTool(ctx context.Context, payload map[string]any) (any, error) {
	id, _ := payload["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	processMu.Lock()
	e, ok := processes[id]
	processMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("process not found")
	}
	if e.Cmd.Process == nil {
		return nil, fmt.Errorf("process has no underlying OS process")
	}
	if err := e.Cmd.Process.Kill(); err != nil {
		return nil, err
	}
	return map[string]any{"killed": true, "id": id}, nil
}

func tailFile(path string, maxLines int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// read whole file (ok for moderate sizes)
	scanner := bufio.NewScanner(f)
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) <= maxLines {
		return lines, nil
	}
	return lines[len(lines)-maxLines:], nil
}

func processReadTool(ctx context.Context, payload map[string]any) (any, error) {
	id, _ := payload["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	stream := "stdout"
	if s, ok := payload["stream"].(string); ok && s != "" {
		stream = s
	}
	lines := 200
	if l, ok := payload["lines"].(float64); ok {
		lines = int(l)
	}
	processMu.Lock()
	e, ok := processes[id]
	processMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("process not found")
	}
	var path string
	if stream == "stderr" {
		path = e.Stderr
	} else {
		path = e.Stdout
	}
	t, err := tailFile(path, lines)
	if err != nil {
		return nil, err
	}
	return map[string]any{"lines": t, "path": path}, nil
}
