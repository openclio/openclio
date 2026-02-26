// Package workspace: assistant display name and icon (for UI and identity).
// Stored in ~/.openclio/assistant.yaml. Updates to name are synced into identity.md.

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	assistantFileName = "assistant.yaml"
	defaultName       = "Clio"
	defaultIcon       = "🤖"
)

// AssistantDisplay holds the user-facing assistant name and icon (emoji).
type AssistantDisplay struct {
	Name string `yaml:"name"`
	Icon string `yaml:"icon"`
}

// LoadAssistantDisplay reads name and icon from ~/.openclio/assistant.yaml.
// Returns defaults (Clio, 🤖) if the file is missing or invalid.
func LoadAssistantDisplay(dataDir string) (AssistantDisplay, error) {
	out := AssistantDisplay{Name: defaultName, Icon: defaultIcon}
	if dataDir == "" {
		return out, nil
	}
	path := filepath.Join(dataDir, assistantFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("reading assistant config: %w", err)
	}
	var raw struct {
		Name string `yaml:"name"`
		Icon string `yaml:"icon"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return out, nil
	}
	if raw.Name != "" {
		out.Name = strings.TrimSpace(raw.Name)
	}
	if raw.Icon != "" {
		out.Icon = strings.TrimSpace(raw.Icon)
	}
	return out, nil
}

// SaveAssistantDisplay writes name and icon to assistant.yaml.
func SaveAssistantDisplay(dataDir string, name, icon string) error {
	if dataDir == "" {
		return fmt.Errorf("data dir required to save assistant display")
	}
	if name == "" {
		name = defaultName
	}
	if icon == "" {
		icon = defaultIcon
	}
	path := filepath.Join(dataDir, assistantFileName)
	payload := map[string]string{"name": strings.TrimSpace(name), "icon": strings.TrimSpace(icon)}
	data, err := yaml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling assistant config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", assistantFileName, err)
	}
	return nil
}

// UpdateIdentityFileWithName updates identity.md so the first "I am X" / "You are X" line uses newName.
// Called when the user changes the assistant name so the LLM sees the updated name.
func UpdateIdentityFileWithName(dataDir string, newName string) error {
	if dataDir == "" || newName == "" {
		return nil
	}
	newName = strings.TrimSpace(newName)
	path := filepath.Join(dataDir, "identity.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading identity.md: %w", err)
	}
	content := string(data)
	// Replace common identity intro patterns with the new name (first match only).
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^(\s*I am )\S+([ —\-].*)$`),
		regexp.MustCompile(`(?m)^(\s*You are )\S+([ —\-\.].*)$`),
		regexp.MustCompile(`(?m)^(\s*I am )\S+(\.\s*)$`),
		regexp.MustCompile(`(?m)^(\s*You are )\S+(\.\s*)$`),
	}
	head := content
	if len(head) > 600 {
		head = content[:600]
	}
	for _, re := range patterns {
		if re.MatchString(head) {
			content = re.ReplaceAllString(content, "$1"+newName+"$2")
			break
		}
	}
	return os.WriteFile(path, []byte(content), 0600)
}
