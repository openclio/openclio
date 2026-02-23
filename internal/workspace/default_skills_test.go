package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedDefaultSkillsCreatesBundledSkills(t *testing.T) {
	dir := t.TempDir()

	if err := SeedDefaultSkills(dir); err != nil {
		t.Fatalf("SeedDefaultSkills: %v", err)
	}

	entries, err := ListSkillEntries(dir)
	if err != nil {
		t.Fatalf("ListSkillEntries: %v", err)
	}
	if len(entries) != len(bundledDefaultSkills) {
		t.Fatalf("expected %d bundled skills, got %d", len(bundledDefaultSkills), len(entries))
	}

	got := make(map[string]SkillEntry, len(entries))
	for _, entry := range entries {
		got[entry.Name] = entry
	}

	for _, expected := range bundledDefaultSkills {
		entry, ok := got[expected.Name]
		if !ok {
			t.Fatalf("missing default skill %q", expected.Name)
		}
		if !entry.Enabled {
			t.Fatalf("default skill %q should be enabled", expected.Name)
		}
	}
}

func TestSeedDefaultSkillsDoesNotOverwriteExistingSkill(t *testing.T) {
	dir := t.TempDir()

	if err := SeedDefaultSkills(dir); err != nil {
		t.Fatalf("initial SeedDefaultSkills: %v", err)
	}

	targetPath := filepath.Join(dir, "skills", "code-review.md")
	customContent := "# Code Review\nCUSTOM USER CONTENT\n"
	if err := os.WriteFile(targetPath, []byte(customContent), 0600); err != nil {
		t.Fatalf("overwrite custom skill: %v", err)
	}

	if err := SeedDefaultSkills(dir); err != nil {
		t.Fatalf("second SeedDefaultSkills: %v", err)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read custom skill: %v", err)
	}
	if string(data) != customContent {
		t.Fatalf("expected custom content to remain unchanged, got:\n%s", string(data))
	}
}

func TestSeedDefaultSkillsRespectsDisabledSkill(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0700); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	disabledPath := filepath.Join(skillsDir, "bug-triage.disabled.md")
	disabledContent := "# Bug Triage\nDISABLED BY USER\n"
	if err := os.WriteFile(disabledPath, []byte(disabledContent), 0600); err != nil {
		t.Fatalf("write disabled skill: %v", err)
	}

	if err := SeedDefaultSkills(dir); err != nil {
		t.Fatalf("SeedDefaultSkills: %v", err)
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "bug-triage.md")); !os.IsNotExist(err) {
		t.Fatalf("expected bug-triage.md to stay absent when disabled file exists, err=%v", err)
	}

	data, err := os.ReadFile(disabledPath)
	if err != nil {
		t.Fatalf("read disabled skill: %v", err)
	}
	if string(data) != disabledContent {
		t.Fatalf("expected disabled content to remain unchanged, got:\n%s", string(data))
	}
}
