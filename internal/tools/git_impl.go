package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func init() {
	_ = ReplaceTool("git_status", gitStatusTool)
	_ = ReplaceTool("git_diff", gitDiffTool)
	_ = ReplaceTool("git_commit", gitCommitTool)
	_ = ReplaceTool("git_log", gitLogTool)
	_ = ReplaceTool("git_branch", gitBranchTool)
}

func gitCmdOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %v: %v: %s", args, err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

func gitStatusTool(ctx context.Context, payload map[string]any) (any, error) {
	dir, _ := payload["repo_path"].(string)
	out, err := gitCmdOutput(dir, "status", "--porcelain", "--branch")
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": out}, nil
}

func gitDiffTool(ctx context.Context, payload map[string]any) (any, error) {
	dir, _ := payload["repo_path"].(string)
	staged := false
	if s, ok := payload["staged"]; ok {
		if b, ok := s.(bool); ok {
			staged = b
		}
	}
	var args []string
	if staged {
		args = []string{"diff", "--staged", "--no-color"}
	} else {
		args = []string{"diff", "--no-color"}
	}
	if p, ok := payload["path"].(string); ok && strings.TrimSpace(p) != "" {
		args = append(args, "--", p)
	}
	out, err := gitCmdOutput(dir, args...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"diff": out}, nil
}

func gitCommitTool(ctx context.Context, payload map[string]any) (any, error) {
	dir, _ := payload["repo_path"].(string)
	msgI, ok := payload["message"]
	if !ok {
		return nil, fmt.Errorf("message is required")
	}
	msg, _ := msgI.(string)
	if strings.TrimSpace(msg) == "" {
		return nil, fmt.Errorf("message cannot be empty")
	}
	// optional files to add
	if files, ok := payload["files"]; ok {
		switch v := files.(type) {
		case []any:
			args := append([]string{"add"}, anySliceToStrings(v)...)
			if _, err := gitCmdOutput(dir, args...); err != nil {
				return nil, err
			}
		case []string:
			args := append([]string{"add"}, v...)
			if _, err := gitCmdOutput(dir, args...); err != nil {
				return nil, err
			}
		}
	}
	out, err := gitCmdOutput(dir, "commit", "-m", msg)
	if err != nil {
		return nil, err
	}
	return map[string]any{"commit": out}, nil
}

func gitLogTool(ctx context.Context, payload map[string]any) (any, error) {
	dir, _ := payload["repo_path"].(string)
	n := 20
	if ni, ok := payload["n"]; ok {
		switch v := ni.(type) {
		case int:
			n = v
		case float64:
			n = int(v)
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				n = parsed
			}
		}
	}
	format := "%H|%an|%ad|%s"
	out, err := gitCmdOutput(dir, "log", "--pretty=format:"+format, "-n", fmt.Sprintf("%d", n))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	outArr := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		parts := strings.SplitN(l, "|", 4)
		if len(parts) < 4 {
			continue
		}
		outArr = append(outArr, map[string]any{
			"hash":    parts[0],
			"author":  parts[1],
			"date":    parts[2],
			"message": parts[3],
		})
	}
	return outArr, nil
}

func gitBranchTool(ctx context.Context, payload map[string]any) (any, error) {
	dir, _ := payload["repo_path"].(string)
	out, err := gitCmdOutput(dir, "branch", "--all", "--no-color")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	branches := make([]string, 0, len(lines))
	for _, l := range lines {
		branches = append(branches, strings.TrimSpace(strings.TrimPrefix(l, "* ")))
	}
	return branches, nil
}

func anySliceToStrings(arr []any) []string {
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
