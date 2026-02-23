package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Allowlist controls which external users can interact with the agent on
// each channel adapter. This implements Layer 2 of the security model:
// "Unknown senders on channels require owner approval before the agent responds."
//
// By default (allow_all = true), all senders are permitted — matching
// the behaviour of most self-hosted bots. Set allow_all to false in config
// to enable strict mode.
type Allowlist struct {
	mu       sync.RWMutex
	allowed  map[string]bool // "adapter:userID" → true
	allowAll bool
	path     string // file path to persist the list
}

// NewAllowlist creates an allowlist. If allowAll is true every sender is
// permitted automatically (default, convenient for single-user setups).
// The list is persisted to path so approvals survive restarts.
func NewAllowlist(dataDir string, allowAll bool) *Allowlist {
	al := &Allowlist{
		allowed:  make(map[string]bool),
		allowAll: allowAll,
		path:     filepath.Join(dataDir, "allowed_senders.txt"),
	}
	al.load()
	return al
}

// IsAllowed reports whether the given sender on the given adapter may interact with the agent.
func (al *Allowlist) IsAllowed(adapterName, userID string) bool {
	al.mu.RLock()
	defer al.mu.RUnlock()
	if al.allowAll {
		return true
	}
	return al.allowed[key(adapterName, userID)]
}

// Approve adds a sender to the allowlist and persists it.
func (al *Allowlist) Approve(adapterName, userID string) error {
	al.mu.Lock()
	al.allowed[key(adapterName, userID)] = true
	al.mu.Unlock()
	return al.save()
}

// Revoke removes a sender from the allowlist.
func (al *Allowlist) Revoke(adapterName, userID string) error {
	al.mu.Lock()
	delete(al.allowed, key(adapterName, userID))
	al.mu.Unlock()
	return al.save()
}

// List returns all currently approved senders.
func (al *Allowlist) List() []string {
	al.mu.RLock()
	defer al.mu.RUnlock()
	out := make([]string, 0, len(al.allowed))
	for k := range al.allowed {
		out = append(out, k)
	}
	return out
}

// AllowAll reports whether strict allowlist mode is disabled.
func (al *Allowlist) AllowAll() bool {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.allowAll
}

// SetAllowAll toggles strict mode at runtime.
func (al *Allowlist) SetAllowAll(v bool) {
	al.mu.Lock()
	defer al.mu.Unlock()
	al.allowAll = v
}

func key(adapterName, userID string) string {
	return adapterName + ":" + userID
}

// load reads persisted approvals from disk. Errors are silently ignored
// (missing file is treated as empty list).
func (al *Allowlist) load() {
	al.mu.Lock()
	defer al.mu.Unlock()
	data, err := os.ReadFile(al.path)
	if err != nil {
		return
	}
	loaded := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			loaded[line] = true
		}
	}
	al.allowed = loaded
}

// save flushes the current allowlist to disk.
func (al *Allowlist) save() error {
	al.mu.RLock()
	var lines []string
	for k := range al.allowed {
		lines = append(lines, k)
	}
	al.mu.RUnlock()
	sort.Strings(lines)

	content := "# Approved senders — format: adapter:userID\n" + strings.Join(lines, "\n") + "\n"
	if err := os.MkdirAll(filepath.Dir(al.path), 0700); err != nil {
		return fmt.Errorf("allowlist: creating data dir: %w", err)
	}

	// Write to a temp file and atomically rename into place.
	tmpPath := al.path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("allowlist: writing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, al.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("allowlist: replacing file: %w", err)
	}

	if err := os.Chmod(al.path, 0600); err != nil {
		return fmt.Errorf("allowlist: saving: %w", err)
	}
	return nil
}
