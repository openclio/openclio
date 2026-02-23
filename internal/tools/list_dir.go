package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ListDirTool lists directory contents.
type ListDirTool struct {
	workDir string
}

func NewListDirTool(workDir string) *ListDirTool {
	return &ListDirTool{workDir: workDir}
}

func (t *ListDirTool) Name() string        { return "list_dir" }
func (t *ListDirTool) Description() string { return "List files and directories in a path" }
func (t *ListDirTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Directory path to list"}
		},
		"required": ["path"]
	}`)
}

type listDirParams struct {
	Path string `json:"path"`
}

func (t *ListDirTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p listDirParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}

	safePath, err := ValidatePath(p.Path, t.workDir)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("directory not found: %s", p.Path)
		}
		return "", fmt.Errorf("accessing path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is a file, not a directory", p.Path)
	}

	entries, err := os.ReadDir(safePath)
	if err != nil {
		return "", fmt.Errorf("reading directory: %w", err)
	}

	var result strings.Builder
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		typeChar := "-"
		if entry.IsDir() {
			typeChar = "d"
		}

		if entry.IsDir() {
			result.WriteString(fmt.Sprintf("%s %s/\n", typeChar, entry.Name()))
		} else {
			result.WriteString(fmt.Sprintf("%s %8d  %s\n", typeChar, info.Size(), entry.Name()))
		}
	}

	if result.Len() == 0 {
		return "[empty directory]", nil
	}

	return result.String(), nil
}
