// Package workspace loads optional user personalization files from ~/.openclio/.
// Files: identity.md (agent persona), user.md (user context), memory.md (facts).
// All content is auto-compressed to stay within token budgets.
package workspace

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentctx "github.com/openclio/openclio/internal/context"
)

// Workspace holds loaded personalization content.
type Workspace struct {
	Identity      string // from identity.md (~50 tokens max)
	UserCtx       string // from user.md (~50 tokens max)
	Memory        string // from memory.md (~500 tokens max)
	AssistantName string // from assistant.yaml (UI and prompt)
	AssistantIcon string // from assistant.yaml (UI only)

	mu         sync.RWMutex
	checksums  map[string]string // filename → sha256
	lastLoaded time.Time
	dataDir    string
}

// Load reads workspace files from the data directory.
func Load(dataDir string) (*Workspace, error) {
	w := &Workspace{
		checksums: make(map[string]string),
		dataDir:   dataDir,
	}
	if err := w.reload(); err != nil {
		return nil, err
	}
	return w, nil
}

// Empty returns a Workspace with no personalization content.
// Use this instead of falling back to os.TempDir() when workspace files are unavailable.
func Empty() *Workspace {
	return &Workspace{checksums: make(map[string]string)}
}

const (
	maxIdentityTokens = 100 // ~400 chars
	maxUserTokens     = 100 // ~400 chars
	maxMemoryTokens   = 500 // ~2000 chars
)

func (w *Workspace) reload() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.reloadUnlocked()
}

func (w *Workspace) reloadUnlocked() error {
	identity, _ := w.readIdentity()
	userCtx, _ := w.readFile("user.md", maxUserTokens)
	memory, _ := w.readFile("memory.md", maxMemoryTokens)
	display, _ := LoadAssistantDisplay(w.dataDir)

	w.Identity = identity
	w.UserCtx = userCtx
	w.Memory = memory
	w.AssistantName = display.Name
	w.AssistantIcon = display.Icon
	w.lastLoaded = time.Now()
	return nil
}

func (w *Workspace) readIdentity() (string, error) {
	for _, name := range []string{"identity.md", "IDENTITY.md", "SOUL.md"} {
		content, err := w.readFile(name, maxIdentityTokens)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(content) != "" {
			return content, nil
		}
	}
	return "", nil
}

// readFile reads a file and compresses it to maxTokens.
func (w *Workspace) readFile(name string, maxTokens int) (string, error) {
	path := filepath.Join(w.dataDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // file is optional
		}
		return "", fmt.Errorf("reading %s: %w", name, err)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", nil
	}

	// Update checksum
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	w.checksums[name] = sum

	// Compress if over token budget
	estimated := agentctx.EstimateTokens(content)
	if estimated > maxTokens {
		// Truncate to approximately maxTokens worth of characters
		maxChars := maxTokens * 4 // ~4 chars per token
		if len(content) > maxChars {
			content = content[:maxChars] + "\n[...truncated to fit token budget]"
		}
	}

	return content, nil
}

// RefreshIfChanged re-reads files if their checksums have changed.
func (w *Workspace) RefreshIfChanged() {
	w.mu.Lock()
	defer w.mu.Unlock()

	dataDir := w.dataDir
	needsReload := false
	files := []string{"identity.md", "IDENTITY.md", "SOUL.md", "user.md", "memory.md"}
	if dataDir != "" {
		files = append(files, assistantFileName)
	}
	for _, name := range files {
		path := filepath.Join(dataDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(data))
		prev := w.checksums[name]
		if sum != prev {
			needsReload = true
			break
		}
	}

	if needsReload {
		w.reloadUnlocked()
	}
}

// BuildContextBlock returns a formatted string to inject into the system prompt.
func (w *Workspace) BuildContextBlock() string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var parts []string

	if w.Identity != "" {
		parts = append(parts, w.Identity)
	}

	if w.UserCtx != "" {
		parts = append(parts, "About the user: "+w.UserCtx)
	}

	if w.Memory != "" {
		parts = append(parts, "[User memory]\n"+w.Memory)
	}

	return strings.Join(parts, "\n\n")
}
