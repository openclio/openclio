package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EditFileTool performs search-and-replace patching on a file.
// The agent provides the exact content to find and the replacement —
// no full-file rewrites needed, and an error is returned if the
// target string is not found (preventing silent overwrites).
type EditFileTool struct {
	allowedRoots []string
}

func NewEditFileTool(allowedRoots []string) *EditFileTool {
	return &EditFileTool{allowedRoots: allowedRoots}
}

func (t *EditFileTool) Name() string { return "edit_file" }
func (t *EditFileTool) Description() string {
	return "Edit a file by replacing specific content. Provide old_content (exact string to find) and new_content (replacement). Fails if old_content is not found."
}
func (t *EditFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":           {"type": "string", "description": "Path to the file to edit"},
			"old_content":    {"type": "string", "description": "Exact string to find (must match exactly, including whitespace)"},
			"new_content":    {"type": "string", "description": "Replacement string"},
			"allow_multiple": {"type": "boolean", "description": "If true, replace all occurrences. Default: false (replace first only)"}
		},
		"required": ["path", "old_content", "new_content"]
	}`)
}

type editFileParams struct {
	Path          string `json:"path"`
	OldContent    string `json:"old_content"`
	NewContent    string `json:"new_content"`
	AllowMultiple bool   `json:"allow_multiple"`
}

func (t *EditFileTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p editFileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}

	if p.OldContent == "" {
		return "", fmt.Errorf("old_content must not be empty — use write_file to create files from scratch")
	}

	absPath, err := ValidatePathUnderAny(p.Path, t.allowedRoots)
	if err != nil {
		return "", err
	}

	// Read current file content
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}

	content := string(data)

	// Check the target string exists
	count := strings.Count(content, p.OldContent)
	if count == 0 {
		return "", fmt.Errorf(
			"old_content not found in %s — the file content may have changed; re-read the file and try again",
			p.Path,
		)
	}

	// Prevent unintended multi-replace
	if count > 1 && !p.AllowMultiple {
		return "", fmt.Errorf(
			"old_content matches %d occurrences in %s — set allow_multiple:true to replace all, or make old_content more specific",
			count, p.Path,
		)
	}

	// Apply the replacement
	var updated string
	if p.AllowMultiple {
		updated = strings.ReplaceAll(content, p.OldContent, p.NewContent)
	} else {
		updated = strings.Replace(content, p.OldContent, p.NewContent, 1)
	}

	// Write back with same permissions
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}

	if err := os.WriteFile(absPath, []byte(updated), info.Mode()); err != nil {
		return "", fmt.Errorf("writing file: %w", err)
	}

	replacements := 1
	if p.AllowMultiple {
		replacements = count
	}

	return fmt.Sprintf("Edited %s — replaced %d occurrence(s) of the target string", p.Path, replacements), nil
}
