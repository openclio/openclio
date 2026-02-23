package storage

import "testing"

func TestAgentProfileStoreCRUDAndActivation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	profiles := NewAgentProfileStore(db)
	sessions := NewSessionStore(db)

	p1, err := profiles.Create(AgentProfile{
		Name:         "default-assistant",
		Description:  "General purpose profile",
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		SystemPrompt: "You are helpful.",
	})
	if err != nil {
		t.Fatalf("create profile 1: %v", err)
	}
	p2, err := profiles.Create(AgentProfile{
		Name:         "research-assistant",
		Description:  "Research profile",
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-20250514",
		SystemPrompt: "You are rigorous.",
	})
	if err != nil {
		t.Fatalf("create profile 2: %v", err)
	}

	if err := profiles.Activate(p2.ID); err != nil {
		t.Fatalf("activate profile 2: %v", err)
	}
	active, err := profiles.GetActive()
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.ID != p2.ID {
		t.Fatalf("expected active profile %s, got %s", p2.ID, active.ID)
	}

	if err := profiles.Update(p1.ID, AgentProfile{
		Name:         "default-assistant-v2",
		Description:  "General purpose profile updated",
		Provider:     "openai",
		Model:        "gpt-4.1-mini",
		SystemPrompt: "You are concise.",
	}); err != nil {
		t.Fatalf("update profile 1: %v", err)
	}

	session, err := sessions.Create("ws", "user-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sessions.BindAgentProfile(session.ID, p1.ID); err != nil {
		t.Fatalf("bind session profile: %v", err)
	}
	reloaded, err := sessions.Get(session.ID)
	if err != nil {
		t.Fatalf("get session after bind: %v", err)
	}
	if reloaded.AgentProfileID != p1.ID {
		t.Fatalf("expected bound profile %s, got %s", p1.ID, reloaded.AgentProfileID)
	}

	if err := profiles.Delete(p1.ID); err != nil {
		t.Fatalf("delete profile 1: %v", err)
	}
	if _, err := profiles.Get(p1.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
