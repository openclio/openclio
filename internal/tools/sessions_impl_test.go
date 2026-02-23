package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclio/openclio/internal/storage"
)

func TestSessionsToolsLifecycle(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("setenv HOME: %v", err)
	}
	dbPath := filepath.Join(tmp, ".openclio", "data.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	defer db.Close()

	ss := storage.NewSessionStore(db)
	ms := storage.NewMessageStore(db)
	ap := storage.NewAgentProfileStore(db)

	// create session
	sess, err := ss.Create("api", "user-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// insert a message
	if _, err := ms.Insert(sess.ID, "user", "hello", 3); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	// create agent profile
	if _, err := ap.Create(storage.AgentProfile{Name: "Test", Provider: "anthropic", Model: "test", IsActive: true}); err != nil {
		t.Fatalf("create agent profile: %v", err)
	}

	// sessions_list
	_, err = CallTool(ctx, "sessions_list", map[string]any{"limit": 10})
	if err != nil {
		t.Fatalf("sessions_list failed: %v", err)
	}

	// sessions_history
	_, err = CallTool(ctx, "sessions_history", map[string]any{"session_id": sess.ID})
	if err != nil {
		t.Fatalf("sessions_history failed: %v", err)
	}

	// sessions_send
	_, err = CallTool(ctx, "sessions_send", map[string]any{"session_id": sess.ID, "role": "assistant", "content": "reply"})
	if err != nil {
		t.Fatalf("sessions_send failed: %v", err)
	}

	// sessions_status
	_, err = CallTool(ctx, "sessions_status", map[string]any{"session_id": sess.ID})
	if err != nil {
		t.Fatalf("sessions_status failed: %v", err)
	}

	// agents_list
	_, err = CallTool(ctx, "agents_list", map[string]any{})
	if err != nil {
		t.Fatalf("agents_list failed: %v", err)
	}
}
