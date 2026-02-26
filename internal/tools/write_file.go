package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openclio/openclio/internal/storage"
)

// WriteFileTool creates or overwrites files. allowedRoots: paths under these dirs are allowed.
type WriteFileTool struct {
	allowedRoots []string
	actionLog    *storage.ActionLogStore
}

func NewWriteFileTool(allowedRoots []string) *WriteFileTool {
	return &WriteFileTool{allowedRoots: allowedRoots}
}

// SetActionLogStore attaches optional action log persistence.
func (t *WriteFileTool) SetActionLogStore(store *storage.ActionLogStore) {
	t.actionLog = store
}

func (t *WriteFileTool) Name() string        { return "write_file" }
func (t *WriteFileTool) Description() string { return "Create or overwrite a file with content" }
func (t *WriteFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path to the file to write"},
			"content": {"type": "string", "description": "Content to write to the file"}
		},
		"required": ["path", "content"]
	}`)
}

type writeFileParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteFileTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p writeFileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}

	// Validate path (may be under any allowed root when allow_system_access is on)
	absPath, err := ValidatePathUnderAny(p.Path, t.allowedRoots)
	if err != nil {
		return "", err
	}

	beforeExists := false
	beforeContent := ""
	if data, err := os.ReadFile(absPath); err == nil {
		beforeExists = true
		beforeContent = string(data)
	} else if !os.IsNotExist(err) {
		snapshotErr := fmt.Errorf("reading existing file for snapshot: %w", err)
		t.recordAction(absPath, beforeExists, beforeContent, p.Content, false, snapshotErr.Error())
		return "", snapshotErr
	}

	// Create parent directories after path is validated
	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		wrapped := fmt.Errorf("creating directories: %w", err)
		t.recordAction(absPath, beforeExists, beforeContent, p.Content, false, wrapped.Error())
		return "", wrapped
	}

	// Write file
	if err := os.WriteFile(absPath, []byte(p.Content), 0644); err != nil {
		wrapped := fmt.Errorf("writing file: %w", err)
		t.recordAction(absPath, beforeExists, beforeContent, p.Content, false, wrapped.Error())
		return "", wrapped
	}
	t.recordAction(absPath, beforeExists, beforeContent, p.Content, true, "")

	return fmt.Sprintf("Wrote %d bytes to %s", len(p.Content), p.Path), nil
}

func (t *WriteFileTool) recordAction(path string, beforeExists bool, beforeContent, afterContent string, success bool, errMsg string) {
	if t.actionLog == nil {
		return
	}
	_ = t.actionLog.RecordWriteFile(path, beforeExists, beforeContent, afterContent, success, errMsg)
}
