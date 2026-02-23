package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/storage"
)

type migratedMessage struct {
	Role    string
	Content string
}

type migrateSummary struct {
	SessionsImported int
	MessagesImported int
	IdentityImported bool
	MemoryImported   bool
}

func runMigrateCmd(args []string, dataDir string, db *storage.DB) {
	if len(args) < 2 || strings.ToLower(args[0]) != "openclaw" {
		fmt.Fprintln(os.Stderr, "usage: openclio migrate openclaw <source_dir>")
		os.Exit(1)
	}

	sourceDir, err := expandPath(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid source path: %v\n", err)
		os.Exit(1)
	}

	summary, err := migrateOpenClaw(sourceDir, dataDir, db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: migration failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Migration complete.")
	fmt.Printf("  Source:   %s\n", sourceDir)
	fmt.Printf("  Sessions: %d\n", summary.SessionsImported)
	fmt.Printf("  Messages: %d\n", summary.MessagesImported)
	if summary.IdentityImported {
		fmt.Println("  Identity: imported")
	} else {
		fmt.Println("  Identity: not imported (already present or not found)")
	}
	if summary.MemoryImported {
		fmt.Println("  Memory:   imported")
	} else {
		fmt.Println("  Memory:   not imported (not found)")
	}
}

func migrateOpenClaw(sourceDir, dataDir string, db *storage.DB) (*migrateSummary, error) {
	info, err := os.Stat(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("reading source directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source path is not a directory: %s", sourceDir)
	}

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("ensuring data directory: %w", err)
	}

	summary := &migrateSummary{}
	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)

	identityImported, err := importIdentityFile(sourceDir, dataDir)
	if err != nil {
		return nil, err
	}
	summary.IdentityImported = identityImported

	memoryImported, err := importMemoryFile(sourceDir, dataDir)
	if err != nil {
		return nil, err
	}
	summary.MemoryImported = memoryImported

	var jsonlFiles []string
	if err := filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".jsonl") {
			jsonlFiles = append(jsonlFiles, path)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scanning source directory: %w", err)
	}

	for _, file := range jsonlFiles {
		parsed, err := parseOpenClawJSONL(file)
		if err != nil || len(parsed) == 0 {
			continue
		}

		session, err := sessions.Create("openclaw", filepath.Base(file))
		if err != nil {
			return nil, fmt.Errorf("creating migrated session for %s: %w", file, err)
		}
		summary.SessionsImported++

		for _, msg := range parsed {
			tokens := agentctx.EstimateTokens(msg.Content)
			if _, err := messages.Insert(session.ID, msg.Role, msg.Content, tokens); err != nil {
				return nil, fmt.Errorf("inserting migrated message from %s: %w", file, err)
			}
			summary.MessagesImported++
		}
	}

	return summary, nil
}

func parseOpenClawJSONL(path string) ([]migratedMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	var out []migratedMessage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		role := normalizeRole(stringField(obj["role"]))
		if role == "" {
			role = normalizeRole(stringField(obj["type"]))
		}
		if role == "" {
			role = "user"
		}

		content := extractContent(obj)
		if strings.TrimSpace(content) == "" {
			continue
		}

		out = append(out, migratedMessage{
			Role:    role,
			Content: content,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "assistant", "ai", "model":
		return "assistant"
	case "system":
		return "system"
	case "tool", "tool_result":
		return "tool_result"
	case "user", "human":
		return "user"
	default:
		return ""
	}
}

func extractContent(obj map[string]any) string {
	for _, key := range []string{"content", "text", "message"} {
		if v, ok := obj[key]; ok {
			if s := contentValue(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func contentValue(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case map[string]any:
		if s := stringField(t["text"]); s != "" {
			return s
		}
		if s := stringField(t["content"]); s != "" {
			return s
		}
	case []any:
		var parts []string
		for _, item := range t {
			if s := contentValue(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func stringField(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func importIdentityFile(sourceDir, dataDir string) (bool, error) {
	target := filepath.Join(dataDir, "identity.md")
	if _, err := os.Stat(target); err == nil {
		return false, nil // keep existing identity
	}

	for _, name := range []string{"identity.md", "IDENTITY.md", "SOUL.md"} {
		src := filepath.Join(sourceDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "" {
			continue
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return false, fmt.Errorf("writing migrated identity: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func importMemoryFile(sourceDir, dataDir string) (bool, error) {
	for _, name := range []string{"memory.md", "MEMORY.md"} {
		src := filepath.Join(sourceDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}

		target := filepath.Join(dataDir, "memory.md")
		f, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return false, fmt.Errorf("opening migrated memory file: %w", err)
		}
		defer f.Close()
		if _, err := f.WriteString("\n[Imported from OpenClaw]\n" + content + "\n"); err != nil {
			return false, fmt.Errorf("writing migrated memory: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func expandPath(path string) (string, error) {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = home
	} else if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return filepath.Abs(path)
}
