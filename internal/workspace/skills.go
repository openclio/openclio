package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ListSkills returns the names of available skill files in ~/.openclio/skills/.
func ListSkills(dataDir string) ([]string, error) {
	entries, err := ListSkillEntries(dataDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Enabled {
			names = append(names, e.Name)
		}
	}
	return names, nil
}

// SkillEntry describes one discovered skill file and its enabled state.
type SkillEntry struct {
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Path      string    `json:"path"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListSkillEntries returns all skills with enable state.
func ListSkillEntries(dataDir string) ([]SkillEntry, error) {
	skillsDir := filepath.Join(dataDir, "skills")
	dirEntries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading skills dir: %w", err)
	}

	byName := map[string]SkillEntry{}
	for _, e := range dirEntries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		enabled := false
		baseName := ""
		switch {
		case strings.HasSuffix(name, ".disabled.md"):
			baseName = strings.TrimSuffix(name, ".disabled.md")
			enabled = false
		case strings.HasSuffix(name, ".md"):
			baseName = strings.TrimSuffix(name, ".md")
			enabled = true
		default:
			continue
		}
		if strings.TrimSpace(baseName) == "" {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}
		entry := SkillEntry{
			Name:      baseName,
			Enabled:   enabled,
			Path:      filepath.Join(skillsDir, name),
			UpdatedAt: info.ModTime().UTC(),
		}
		if prev, ok := byName[baseName]; ok {
			// Prefer enabled version when both exist.
			if !prev.Enabled && enabled {
				byName[baseName] = entry
				continue
			}
			// Otherwise keep the most recently modified entry.
			if entry.UpdatedAt.After(prev.UpdatedAt) {
				byName[baseName] = entry
			}
			continue
		}
		byName[baseName] = entry
	}

	out := make([]SkillEntry, 0, len(byName))
	for _, e := range byName {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// InstallSkill writes (or overwrites) one skill markdown file.
func InstallSkill(dataDir, name, content string, enabled bool) error {
	name, err := sanitizeSkillName(name)
	if err != nil {
		return err
	}
	skillsDir := filepath.Join(dataDir, "skills")
	if err := os.MkdirAll(skillsDir, 0700); err != nil {
		return fmt.Errorf("creating skills dir: %w", err)
	}

	if strings.TrimSpace(content) == "" {
		content = "# " + name + "\n"
	}

	enabledPath, disabledPath := skillFilePaths(dataDir, name)
	targetPath := enabledPath
	stalePath := disabledPath
	if !enabled {
		targetPath = disabledPath
		stalePath = enabledPath
	}
	if err := os.WriteFile(targetPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing skill %q: %w", name, err)
	}
	_ = os.Remove(stalePath)
	return nil
}

// SetSkillEnabled toggles one skill between enabled and disabled states.
func SetSkillEnabled(dataDir, name string, enabled bool) error {
	name, err := sanitizeSkillName(name)
	if err != nil {
		return err
	}
	enabledPath, disabledPath := skillFilePaths(dataDir, name)
	sourcePath := disabledPath
	targetPath := enabledPath
	if !enabled {
		sourcePath = enabledPath
		targetPath = disabledPath
	}

	if _, err := os.Stat(targetPath); err == nil {
		return nil
	}
	if _, err := os.Stat(sourcePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("skill %q not found", name)
		}
		return fmt.Errorf("checking skill %q state: %w", name, err)
	}
	if err := os.Rename(sourcePath, targetPath); err != nil {
		return fmt.Errorf("updating skill %q state: %w", name, err)
	}
	return nil
}

// DeleteSkill removes one skill from both enabled and disabled forms.
func DeleteSkill(dataDir, name string) error {
	name, err := sanitizeSkillName(name)
	if err != nil {
		return err
	}
	enabledPath, disabledPath := skillFilePaths(dataDir, name)
	var removed bool
	if err := os.Remove(enabledPath); err == nil {
		removed = true
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting skill %q: %w", name, err)
	}
	if err := os.Remove(disabledPath); err == nil {
		removed = true
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting disabled skill %q: %w", name, err)
	}
	if !removed {
		return fmt.Errorf("skill %q not found", name)
	}
	return nil
}

// LoadSkill reads a skill by name from ~/.openclio/skills/<name>.md.
// Skills are loaded on-demand (NOT injected into every prompt).
func LoadSkill(dataDir, name string) (string, error) {
	// Sanitize name — prevent path traversal
	var err error
	name, err = sanitizeSkillName(name)
	if err != nil {
		return "", err
	}

	path := filepath.Join(dataDir, "skills", name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("skill %q not found (check ~/.openclio/skills/%s.md)", name, name)
		}
		return "", fmt.Errorf("reading skill %s: %w", name, err)
	}

	return string(data), nil
}

func sanitizeSkillName(name string) (string, error) {
	name = strings.TrimSpace(filepath.Base(name))
	name = strings.TrimSuffix(name, ".md")
	name = strings.TrimSuffix(name, ".disabled")
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid skill name: %s", name)
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid skill name: %s", name)
	}
	return name, nil
}

func skillFilePaths(dataDir, name string) (enabledPath string, disabledPath string) {
	skillsDir := filepath.Join(dataDir, "skills")
	return filepath.Join(skillsDir, name+".md"), filepath.Join(skillsDir, name+".disabled.md")
}
