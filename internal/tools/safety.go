package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ValidatePath checks that a path is within the allowed root directory.
// Prevents path traversal attacks (../../etc/passwd) and symlink escapes.
// Works correctly even if the target path (and its parent dirs) don't exist yet.
func ValidatePath(path, allowedRoot string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}

	// Resolve allowedRoot symlinks first (macOS: /var → /private/var)
	realRoot, err := filepath.EvalSymlinks(allowedRoot)
	if err != nil {
		realRoot = allowedRoot // fallback if root doesn't exist yet
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}

	// Try to resolve the full path. Walk up to the first existing ancestor
	// so we can check symlinks without requiring the target to exist.
	checkPath := absPath
	for {
		// Use Lstat to detect if the checkPath itself is a symlink, reducing TOCTOU risk
		info, err := os.Lstat(checkPath)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				// It's a symlink, so we must evaluate it
				realPath, err := filepath.EvalSymlinks(checkPath)
				if err != nil {
					return "", fmt.Errorf("resolving symlink %s: %w", checkPath, err)
				}
				if !strings.HasPrefix(realPath+string(os.PathSeparator), realRoot+string(os.PathSeparator)) && realPath != realRoot {
					return "", fmt.Errorf("symlink %s resolves outside allowed directory %s", path, allowedRoot)
				}
			} else {
				// Base path is not a symlink, so its parents have already been validated or it's safe
				realPath, err := filepath.EvalSymlinks(checkPath)
				if err == nil {
					if !strings.HasPrefix(realPath+string(os.PathSeparator), realRoot+string(os.PathSeparator)) && realPath != realRoot {
						return "", fmt.Errorf("path %s resolves outside allowed directory %s", path, allowedRoot)
					}
				}
			}
			return absPath, nil
		}

		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolving path: %w", err)
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath {
			// Reached filesystem root — just do a lexical check
			break
		}
		checkPath = parent
	}

	// Lexical fallback: clean path doesn't require EvalSymlinks
	clean := filepath.Clean(absPath)
	if !strings.HasPrefix(clean+string(os.PathSeparator), realRoot+string(os.PathSeparator)) && clean != realRoot {
		return "", fmt.Errorf("path %s is outside allowed directory %s", path, allowedRoot)
	}

	return absPath, nil
}

var dangerousPatterns = []string{
	":(){ :|:& };:", // fork bomb
}

var (
	rmRegex   = regexp.MustCompile(`rm\s+-r[fF]?\s+(/|~|/\*)`)
	mkfsRegex = regexp.MustCompile(`\bmkfs([.][a-z0-9]+)?\b`)
)

// IsDangerous checks if a command matches known destructive patterns.
func IsDangerous(command string) (bool, string) {
	lower := strings.ToLower(strings.TrimSpace(command))

	// 1. Block rm -rf variations targeting root/home paths.
	if rmRegex.MatchString(lower) {
		return true, "blocked dangerous pattern: rm -rf /"
	}

	// 2. Block filesystem formatting commands.
	if mkfsRegex.MatchString(lower) {
		return true, "blocked dangerous pattern: mkfs"
	}

	// 3. Block known fork bomb pattern.
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true, fmt.Sprintf("blocked dangerous pattern: %s", pattern)
		}
	}

	return false, ""
}
