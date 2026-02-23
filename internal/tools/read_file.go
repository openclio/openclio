package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/openclio/openclio/internal/storage"
)

// ReadFileTool reads file contents.
type ReadFileTool struct {
	workDir     string
	scrubOutput bool
	privacy     *storage.PrivacyStore
}

func NewReadFileTool(workDir string, scrubOutput bool) *ReadFileTool {
	return &ReadFileTool{workDir: workDir, scrubOutput: scrubOutput}
}

// SetPrivacyStore attaches optional privacy redaction persistence.
func (t *ReadFileTool) SetPrivacyStore(store *storage.PrivacyStore) {
	t.privacy = store
}

func (t *ReadFileTool) Name() string        { return "read_file" }
func (t *ReadFileTool) Description() string { return "Read the contents of a file" }
func (t *ReadFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path to the file to read"}
		},
		"required": ["path"]
	}`)
}

type readFileParams struct {
	Path string `json:"path"`
}

func (t *ReadFileTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p readFileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}

	// Validate path
	safePath, err := ValidatePath(p.Path, t.workDir)
	if err != nil {
		return "", err
	}

	// Check file info
	info, err := os.Stat(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", p.Path)
		}
		return "", fmt.Errorf("accessing file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, use list_dir instead", p.Path)
	}

	// Size limit: 1MB
	if info.Size() > 1024*1024 {
		return "", fmt.Errorf("file too large (%d bytes, max 1MB)", info.Size())
	}

	// Check for binary
	data, err := os.ReadFile(safePath)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}

	if isBinary(data) {
		return fmt.Sprintf("[binary file: %s (%d bytes)]", p.Path, len(data)), nil
	}

	content := string(data)
	if t.scrubOutput {
		var redactions int
		content, redactions = ScrubToolOutputWithCount(content)
		if redactions > 0 && t.privacy != nil {
			_ = t.privacy.RecordRedaction("secret", redactions)
		}
		if redactions > 0 {
			IncRedactions(redactions)
		}
	}
	return content, nil
}

// isBinary checks if content appears to be binary (contains null bytes).
func isBinary(data []byte) bool {
	checkLen := 512
	if len(data) < checkLen {
		checkLen = len(data)
	}
	return strings.Contains(string(data[:checkLen]), "\x00")
}
