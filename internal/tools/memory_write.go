package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MemoryWriteTool allows the agent to append facts to the user's persistent memory.md file.
// It bypasses the standard write_file's workspace restriction since memory.md lives in ~/.openclio/.
type MemoryWriteTool struct {
	memoryFilePath string
}

// NewMemoryWriteTool creates a tool that writes exclusively to memory.md in the dataDir.
func NewMemoryWriteTool(dataDir string) *MemoryWriteTool {
	return &MemoryWriteTool{
		memoryFilePath: filepath.Join(dataDir, "memory.md"),
	}
}

func (t *MemoryWriteTool) Name() string { return "memory_write" }
func (t *MemoryWriteTool) Description() string {
	return "Append persistent user facts, preferences, project details, or working style to permanent memory for future sessions."
}
func (t *MemoryWriteTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"fact": {"type": "string", "description": "The fact or preference to remember"}
		},
		"required": ["fact"]
	}`)
}

type memoryWriteParams struct {
	Fact string `json:"fact"`
}

func (t *MemoryWriteTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p memoryWriteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}

	if p.Fact == "" {
		return "", fmt.Errorf("fact cannot be empty")
	}

	// Format as a bullet point
	appendStr := fmt.Sprintf("- %s\n", p.Fact)

	// Ensure directory exists
	dir := filepath.Dir(t.memoryFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating directory: %w", err)
	}

	// Append to file
	f, err := os.OpenFile(t.memoryFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("opening memory file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(appendStr); err != nil {
		return "", fmt.Errorf("writing to memory file: %w", err)
	}

	return "Fact successfully saved to permanent memory.", nil
}
