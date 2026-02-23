package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	w, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Identity != "" || w.UserCtx != "" || w.Memory != "" {
		t.Error("empty dir should produce empty workspace")
	}
	if w.BuildContextBlock() != "" {
		t.Error("empty workspace should produce empty context block")
	}
}

func TestLoadWithFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "identity.md"), []byte("You are Jarvis, a helpful assistant."), 0644)
	os.WriteFile(filepath.Join(dir, "user.md"), []byte("I am Idris, a Go developer."), 0644)
	os.WriteFile(filepath.Join(dir, "memory.md"), []byte("My server IP is 192.168.1.50."), 0644)

	w, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Identity == "" {
		t.Error("identity should be loaded")
	}
	if w.UserCtx == "" {
		t.Error("user context should be loaded")
	}
	if w.Memory == "" {
		t.Error("memory should be loaded")
	}

	block := w.BuildContextBlock()
	if block == "" {
		t.Error("context block should not be empty")
	}
}

func TestLoadIdentityFallback_OpenClawFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte("You are from OpenClaw identity."), 0644)

	w, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Identity == "" {
		t.Fatal("expected fallback identity to be loaded")
	}
}

func TestLoadIdentityFallback_SoulFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are from SOUL identity."), 0644)

	w, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Identity == "" {
		t.Fatal("expected SOUL fallback identity to be loaded")
	}
}

func TestListSkillsEmpty(t *testing.T) {
	dir := t.TempDir()
	skills, err := ListSkills(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestListAndLoadSkills(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	os.MkdirAll(skillsDir, 0755)
	os.WriteFile(filepath.Join(skillsDir, "code-review.md"), []byte("# Code Review\nBe thorough."), 0644)
	os.WriteFile(filepath.Join(skillsDir, "debug.md"), []byte("# Debug\nThink step by step."), 0644)

	skills, err := ListSkills(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}

	content, err := LoadSkill(dir, "code-review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content == "" {
		t.Error("skill content should not be empty")
	}
}

func TestLoadSkillNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadSkill(dir, "nonexistent")
	if err == nil {
		t.Error("expected error for missing skill")
	}
}

func TestSkillInstallToggleDelete(t *testing.T) {
	dir := t.TempDir()

	if err := InstallSkill(dir, "writer", "# Writer\nUse concise language.", true); err != nil {
		t.Fatalf("InstallSkill enabled: %v", err)
	}

	entries, err := ListSkillEntries(dir)
	if err != nil {
		t.Fatalf("ListSkillEntries after install: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "writer" || !entries[0].Enabled {
		t.Fatalf("unexpected entries after install: %+v", entries)
	}

	if err := SetSkillEnabled(dir, "writer", false); err != nil {
		t.Fatalf("SetSkillEnabled false: %v", err)
	}
	entries, err = ListSkillEntries(dir)
	if err != nil {
		t.Fatalf("ListSkillEntries after disable: %v", err)
	}
	if len(entries) != 1 || entries[0].Enabled {
		t.Fatalf("expected disabled skill entry, got %+v", entries)
	}

	if err := SetSkillEnabled(dir, "writer", true); err != nil {
		t.Fatalf("SetSkillEnabled true: %v", err)
	}
	skills, err := ListSkills(dir)
	if err != nil {
		t.Fatalf("ListSkills after enable: %v", err)
	}
	if len(skills) != 1 || skills[0] != "writer" {
		t.Fatalf("unexpected enabled skill list: %+v", skills)
	}

	if err := DeleteSkill(dir, "writer"); err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	entries, err = ListSkillEntries(dir)
	if err != nil {
		t.Fatalf("ListSkillEntries after delete: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no skills after delete, got %+v", entries)
	}
}
