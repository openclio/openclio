package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclio/openclio/internal/config"
	"github.com/openclio/openclio/internal/storage"
)

// ExecTool runs shell commands.
type ExecTool struct {
	timeout             time.Duration
	maxOutputSize       int
	scrubOutput         bool
	sandbox             string
	allowedRoots        []string // first is primary workspace; when allow_system_access, home is also allowed
	dockerImage         string
	networkAccess       bool
	requireConfirmation bool
	privacy             *storage.PrivacyStore
	actionLog           *storage.ActionLogStore
}

func NewExecTool(cfg config.ExecToolConfig, allowedRoots []string, maxOutputSize int, scrubOutput bool) *ExecTool {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if maxOutputSize == 0 {
		maxOutputSize = 100 * 1024
	}
	dockerImage := strings.TrimSpace(cfg.DockerImage)
	if dockerImage == "" {
		dockerImage = "alpine:latest"
	}
	if len(allowedRoots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			allowedRoots = []string{cwd}
		}
	}
	return &ExecTool{
		timeout:              timeout,
		maxOutputSize:        maxOutputSize,
		scrubOutput:          scrubOutput,
		sandbox:              strings.ToLower(strings.TrimSpace(cfg.Sandbox)),
		allowedRoots:         allowedRoots,
		dockerImage:          dockerImage,
		networkAccess:        cfg.NetworkAccess,
		requireConfirmation:  cfg.RequireConfirmation,
	}
}

func (t *ExecTool) Name() string        { return "exec" }
func (t *ExecTool) Description() string { return "Run a shell command and return its output" }

// SetPrivacyStore attaches optional privacy redaction persistence.
func (t *ExecTool) SetPrivacyStore(store *storage.PrivacyStore) {
	t.privacy = store
}

// SetActionLogStore attaches optional action log persistence.
func (t *ExecTool) SetActionLogStore(store *storage.ActionLogStore) {
	t.actionLog = store
}

func (t *ExecTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "The shell command to execute"},
			"work_dir": {"type": "string", "description": "Optional working directory (must be inside workspace)"}
		},
		"required": ["command"]
	}`)
}

type execParams struct {
	Command string `json:"command"`
	WorkDir string `json:"work_dir,omitempty"`
}

func (t *ExecTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p execParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	if p.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	// Security: check dangerous patterns
	if dangerous, reason := IsDangerous(p.Command); dangerous {
		err := fmt.Errorf("command blocked: %s", reason)
		t.recordAction(p.Command, p.WorkDir, "", err)
		return "", err
	}

	if t.requireConfirmation {
		approved, err := t.confirmCommand(p.Command)
		if err != nil {
			t.recordAction(p.Command, p.WorkDir, "", err)
			return "", err
		}
		if !approved {
			err := fmt.Errorf("command cancelled by user confirmation policy")
			t.recordAction(p.Command, p.WorkDir, "", err)
			return "", err
		}
	}

	workDir, err := t.resolveWorkDir(p.WorkDir)
	if err != nil {
		wrapped := fmt.Errorf("invalid working directory: %w", err)
		t.recordAction(p.Command, p.WorkDir, "", wrapped)
		return "", wrapped
	}

	// Create command with timeout
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var cmd *exec.Cmd
	switch t.sandbox {
	case "", "none":
		cmd = exec.CommandContext(ctx, "sh", "-c", p.Command)
		cmd.Dir = workDir
	case "namespace":
		cmd = exec.CommandContext(ctx, "sh", "-c", p.Command)
		cmd.Dir = workDir
		if err := applyNamespaceSandbox(cmd, t.networkAccess); err != nil {
			return "", err
		}
	case "docker":
		out, runErr := t.executeInDocker(ctx, p.Command, workDir)
		t.recordAction(p.Command, workDir, out, runErr)
		return out, runErr
	default:
		err := fmt.Errorf("unknown sandbox mode %q", t.sandbox)
		t.recordAction(p.Command, workDir, "", err)
		return "", err
	}

	out, redactions, err := t.runCommand(ctx, cmd)
	if redactions > 0 {
		if t.privacy != nil {
			_ = t.privacy.RecordRedaction("secret", redactions)
		}
		IncRedactions(redactions)
	}
	t.recordAction(p.Command, workDir, out, err)
	return out, err
}

func (t *ExecTool) resolveWorkDir(requested string) (string, error) {
	base := ""
	if len(t.allowedRoots) > 0 {
		base = t.allowedRoots[0]
	}
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolving workspace directory: %w", err)
		}
		base = cwd
	}

	path := base
	if strings.TrimSpace(requested) != "" {
		path = requested
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
	}
	return ValidatePathUnderAny(path, t.allowedRoots)
}

func (t *ExecTool) executeInDocker(ctx context.Context, command, workDir string) (string, error) {
	networkMode := "none"
	if t.networkAccess {
		networkMode = "bridge"
	}

	args := []string{
		"run", "--rm",
		"--memory=256m",
		"--cpus=0.5",
		"--network=" + networkMode,
		"--read-only",
		"--tmpfs", "/tmp",
		"--volume", workDir + ":/workspace:rw",
		"--workdir", "/workspace",
		t.dockerImage,
		"sh", "-c", command,
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, redactions, err := t.runCommand(ctx, cmd)
	if redactions > 0 && t.privacy != nil {
		_ = t.privacy.RecordRedaction("secret", redactions)
	}
	return out, err
}

func (t *ExecTool) runCommand(ctx context.Context, cmd *exec.Cmd) (string, int, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: t.maxOutputSize}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: t.maxOutputSize}

	err := cmd.Run()

	var result strings.Builder
	if stdout.Len() > 0 {
		result.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if result.Len() > 0 {
			result.WriteString("\n--- stderr ---\n")
		}
		result.WriteString(stderr.String())
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("[command timed out after %s]\n%s", t.timeout, result.String()), 0, nil
		} else {
			return "", 0, fmt.Errorf("exec error: %w", err)
		}
	}

	if exitCode != 0 {
		result.WriteString(fmt.Sprintf("\n[exit code: %d]", exitCode))
	}

	if result.Len() == 0 {
		return "[no output]", 0, nil
	}

	out := result.String()
	redactions := 0
	if t.scrubOutput {
		out, redactions = ScrubToolOutputWithCount(out)
	}
	return out, redactions, nil
}

func (t *ExecTool) confirmCommand(command string) (bool, error) {
	if stat, err := os.Stdin.Stat(); err != nil || (stat.Mode()&os.ModeCharDevice == 0) {
		return false, fmt.Errorf("command confirmation requires an interactive terminal")
	}
	fmt.Printf("⚠️ Agent wants to run:\n  $ %s\n\nReply 'yes' to approve, 'no' to cancel: ", command)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("command confirmation required but unavailable: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "yes", "y":
		return true, nil
	default:
		return false, nil
	}
}

func (t *ExecTool) recordAction(command, workDir, output string, execErr error) {
	if t.actionLog == nil {
		return
	}
	success := execErr == nil
	errMsg := ""
	if execErr != nil {
		errMsg = execErr.Error()
	}
	_ = t.actionLog.RecordExec(command, workDir, output, success, errMsg)
}

// limitedWriter caps output to prevent memory issues.
type limitedWriter struct {
	buf     *bytes.Buffer
	limit   int
	written int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return len(p), nil // silently discard
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, err := w.buf.Write(p)
	w.written += n
	return n, err
}
