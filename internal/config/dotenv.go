package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadDotEnv loads KEY=VALUE pairs from a dotenv file as fallback env vars.
// Existing process environment variables always win and are never overwritten.
func LoadDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading dotenv file %s: %w", path, err)
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		key, value, ok := parseDotEnvLine(line)
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
	return nil
}

// UpsertDotEnvKey atomically inserts or updates one key in a dotenv file.
// Atomicity is achieved by writing to a sibling temp file then renaming.
func UpsertDotEnvKey(path, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("dotenv key cannot be empty")
	}
	if strings.ContainsAny(key, " \t=") {
		return fmt.Errorf("invalid dotenv key %q", key)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating dotenv directory %s: %w", dir, err)
	}

	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading dotenv file %s: %w", path, err)
	}

	entry := key + "=" + formatDotEnvValue(value)
	updated := false
	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		k, _, ok := parseDotEnvLine(line)
		if !ok {
			if strings.TrimSpace(line) == "" {
				continue
			}
			out = append(out, line)
			continue
		}
		if k == key {
			if !updated {
				out = append(out, entry)
				updated = true
			}
			continue
		}
		out = append(out, line)
	}
	if !updated {
		out = append(out, entry)
	}

	content := strings.Join(out, "\n") + "\n"
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing dotenv temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename %s -> %s failed: %w", tmpPath, path, err)
	}
	return nil
}

func parseDotEnvLine(raw string) (key, value string, ok bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key = strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", false
	}
	value = strings.TrimSpace(parts[1])
	if value == "" {
		return key, "", true
	}

	// Strip inline comments for unquoted values.
	if !strings.HasPrefix(value, `"`) && !strings.HasPrefix(value, `'`) {
		if idx := strings.Index(value, " #"); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		return key, value, true
	}

	// Quoted values.
	if strings.HasPrefix(value, `"`) {
		if uq, err := strconv.Unquote(value); err == nil {
			return key, uq, true
		}
		return key, strings.Trim(value, `"`), true
	}
	if strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`) && len(value) >= 2 {
		return key, value[1 : len(value)-1], true
	}
	return key, value, true
}

func formatDotEnvValue(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, " \t#\"'") {
		return strconv.Quote(v)
	}
	return v
}
