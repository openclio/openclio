package tools

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const gitContextOutputLimit = 20

// GetGitContext returns a compact git state block for system prompt injection.
// Returns empty string when dir is not inside a git worktree.
func GetGitContext(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}

	if out, err := runGit(absDir, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return ""
	}

	branch, _ := runGit(absDir, "branch", "--show-current")
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "(detached)"
	}

	status, _ := runGit(absDir, "status", "--short")
	commits, _ := runGit(absDir, "log", "--oneline", "-5")
	status = truncateLines(strings.TrimSpace(status), gitContextOutputLimit)
	commits = truncateLines(strings.TrimSpace(commits), gitContextOutputLimit)
	if status == "" {
		status = "(clean)"
	}
	if commits == "" {
		commits = "(no commits)"
	}

	return fmt.Sprintf("[Git context]\nBranch: %s\nModified:\n%s\nRecent commits:\n%s", branch, status, commits)
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()
	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return stdout.String(), nil
	case <-time.After(900 * time.Millisecond):
		_ = cmd.Process.Kill()
		<-done
		return "", fmt.Errorf("git command timed out")
	}
}

func truncateLines(input string, maxLines int) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	lines := strings.Split(input, "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	lines = append(lines[:maxLines], fmt.Sprintf("... (%d more)", len(lines)-maxLines))
	return strings.Join(lines, "\n")
}
