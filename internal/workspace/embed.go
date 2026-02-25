// Package workspace handles embedded default templates and user workspace files.
package workspace

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/*.md
var templatesFS embed.FS

//go:embed templates/skills/*.md
var skillsFS embed.FS

// InstallDefaults installs all default templates to the user's data directory.
// This is called during 'openclio init' to give users a complete setup.
func InstallDefaults(dataDir string, assistantName string) error {
	// Create directories
	skillsDir := filepath.Join(dataDir, "skills")
	if err := os.MkdirAll(skillsDir, 0700); err != nil {
		return fmt.Errorf("creating skills directory: %w", err)
	}

	// Install SOUL.md as identity.md template
	if err := installTemplate(templatesFS, "templates/SOUL.md", filepath.Join(dataDir, "identity.md"), assistantName); err != nil {
		return fmt.Errorf("installing identity: %w", err)
	}

	// Install VISION.md as documentation
	if err := installTemplate(templatesFS, "templates/VISION.md", filepath.Join(dataDir, "PHILOSOPHY.md"), ""); err != nil {
		return fmt.Errorf("installing philosophy: %w", err)
	}

	// Install AGENTS.md for power users
	if err := installTemplate(templatesFS, "templates/AGENTS.md", filepath.Join(dataDir, "AGENTS_REFERENCE.md"), ""); err != nil {
		return fmt.Errorf("installing agents reference: %w", err)
	}

	// Install rich memory.md template
	if err := installTemplate(templatesFS, "templates/memory.md", filepath.Join(dataDir, "memory.md"), assistantName); err != nil {
		return fmt.Errorf("installing memory: %w", err)
	}

	// Install user.md template
	if err := installTemplate(templatesFS, "templates/user.md", filepath.Join(dataDir, "user.md"), assistantName); err != nil {
		return fmt.Errorf("installing user: %w", err)
	}

	// Install default skills
	if err := installSkills(skillsDir); err != nil {
		return fmt.Errorf("installing skills: %w", err)
	}

	return nil
}

func installTemplate(fs embed.FS, src, dst, assistantName string) error {
	// Skip if user already has the file (preserve their customizations)
	if _, err := os.Stat(dst); err == nil {
		return nil // File exists, don't overwrite
	}

	content, err := fs.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading embedded template %s: %w", src, err)
	}

	// Replace placeholders
	contentStr := string(content)
	if assistantName != "" {
		contentStr = replacePlaceholders(contentStr, assistantName)
	}

	if err := os.WriteFile(dst, []byte(contentStr), 0600); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}

	return nil
}

func replacePlaceholders(content, assistantName string) string {
	// Replace default name with user's chosen name
	content = replaceAll(content, "{{NAME}}", assistantName)
	content = replaceAll(content, "openclio", assistantName)
	content = replaceAll(content, "OpenClio", assistantName)
	return content
}

// InstallUserProfile installs the user profile with the provided content.
// This overwrites the template user.md with actual user data.
func InstallUserProfile(dataDir string, userProfile string) error {
	userPath := filepath.Join(dataDir, "user.md")
	
	// Read the template
	content, err := templatesFS.ReadFile("templates/user.md")
	if err != nil {
		// Fallback: just write the profile directly
		return os.WriteFile(userPath, []byte(userProfile+"\n"), 0600)
	}
	
	// Replace the placeholder with actual content
	contentStr := strings.ReplaceAll(string(content), "{{USER_PROFILE}}", userProfile)
	return os.WriteFile(userPath, []byte(contentStr), 0600)
}

func replaceAll(s, old, new string) string {
	// Simple replacement - in production use strings.ReplaceAll
	// but preserve code references
	result := ""
	i := 0
	for i < len(s) {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			// Don't replace inside backticks or code blocks
			prevNewline := findPrevNewline(s, i)
			line := s[prevNewline:i]
			if !isCodeLine(line) {
				result += new
			} else {
				result += old
			}
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}

func isCodeLine(line string) bool {
	trimmed := ""
	for _, c := range line {
		if c != ' ' && c != '\t' {
			trimmed = string(c)
			break
		}
	}
	return trimmed == "`" || trimmed == "-" || trimmed == "*"
}

func findPrevNewline(s string, pos int) int {
	for i := pos - 1; i >= 0; i-- {
		if s[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

func installSkills(skillsDir string) error {
	entries, err := skillsFS.ReadDir("templates/skills")
	if err != nil {
		return fmt.Errorf("reading skills directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		srcPath := "templates/skills/" + entry.Name()
		dstPath := filepath.Join(skillsDir, entry.Name())

		// Skip if user already has this skill
		if _, err := os.Stat(dstPath); err == nil {
			continue
		}

		content, err := skillsFS.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("reading skill %s: %w", entry.Name(), err)
		}

		if err := os.WriteFile(dstPath, content, 0600); err != nil {
			return fmt.Errorf("writing skill %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// HasFullInstall checks if the user has the complete template set installed.
func HasFullInstall(dataDir string) bool {
	required := []string{
		"identity.md",
		"PHILOSOPHY.md",
		"AGENTS_REFERENCE.md",
		"memory.md",
		"user.md",
	}

	for _, file := range required {
		path := filepath.Join(dataDir, file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return false
		}
	}
	return true
}
