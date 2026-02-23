package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) *DB {
	// Use an in-memory database for fast testing
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	if err := db.Migrate(); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	return db
}

func TestDB_OpenAndMigrate(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	if db.Conn() == nil {
		t.Fatal("expected valid connection")
	}
}

func TestSessionStore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ss := NewSessionStore(db)

	// Create
	s, err := ss.Create("test-plugin", "user-123")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	if s.ID == "" {
		t.Error("expected non-empty session ID")
	}

	// Update Last Active
	if err := ss.UpdateLastActive(s.ID); err != nil {
		t.Errorf("failed to update last active: %v", err)
	}

	// Get
	sByID, err := ss.Get(s.ID)
	if err != nil {
		t.Errorf("failed to get by ID: %v", err)
	}
	if sByID.ID != s.ID {
		t.Errorf("Get returned wrong session")
	}

	// GetByChannelSender
	sByPair, err := ss.GetByChannelSender("test-plugin", "user-123")
	if err != nil {
		t.Errorf("failed to get by channel/sender: %v", err)
	}
	if sByPair.ID != s.ID {
		t.Errorf("GetByChannelSender returned wrong session")
	}

	// List
	sessions, err := ss.List(10)
	if err != nil {
		t.Errorf("failed to list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session in list")
	}
}

func TestMessageStore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ss := NewSessionStore(db)
	s, _ := ss.Create("test", "user-1")

	ms := NewMessageStore(db)

	// Add messages
	_, err := ms.Insert(s.ID, "user", "hello", 10)
	if err != nil {
		t.Fatalf("failed to insert message: %v", err)
	}

	_, err = ms.Insert(s.ID, "assistant", "hi there", 15)
	if err != nil {
		t.Fatalf("failed to insert message: %v", err)
	}

	// Count
	count, err := ms.CountBySession(s.ID)
	if err != nil {
		t.Fatalf("failed to count messages: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}

	// GetRecent
	history, err := ms.GetRecent(s.ID, 10)
	if err != nil {
		t.Fatalf("failed to list messages: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 messages, got %d", len(history))
	}
}

func TestMessageStoreCompactionArchive(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ss := NewSessionStore(db)
	s, _ := ss.Create("test", "user-1")
	ms := NewMessageStore(db)

	for i := 0; i < 6; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := ms.Insert(s.ID, role, "msg", 5); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond) // ensure stable ordering
	}

	old, err := ms.GetOldMessages(s.ID, 2) // keep 4 recent messages
	if err != nil {
		t.Fatalf("GetOldMessages: %v", err)
	}
	if len(old) != 2 {
		t.Fatalf("expected 2 old messages, got %d", len(old))
	}

	archived, err := ms.ArchiveMessages(s.ID, old[len(old)-1].ID)
	if err != nil {
		t.Fatalf("ArchiveMessages: %v", err)
	}
	if archived < 2 {
		t.Fatalf("expected at least 2 archived rows, got %d", archived)
	}

	activeCount, err := ms.CountBySession(s.ID)
	if err != nil {
		t.Fatalf("CountBySession: %v", err)
	}
	if activeCount != 4 {
		t.Fatalf("expected 4 active messages after archive, got %d", activeCount)
	}
}

func TestEnforceRetention(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	sessions := NewSessionStore(db)
	messages := NewMessageStore(db)

	oldSession, err := sessions.Create("test", "old-user")
	if err != nil {
		t.Fatalf("create old session: %v", err)
	}
	if _, err := db.Conn().Exec(
		"UPDATE sessions SET last_active = ? WHERE id = ?",
		time.Now().UTC().AddDate(0, 0, -10),
		oldSession.ID,
	); err != nil {
		t.Fatalf("set old session last_active: %v", err)
	}

	newSession, err := sessions.Create("test", "new-user")
	if err != nil {
		t.Fatalf("create new session: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := messages.Insert(newSession.ID, "user", "msg", 1); err != nil {
			t.Fatalf("insert message %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	result, err := db.EnforceRetention(7, 2)
	if err != nil {
		t.Fatalf("EnforceRetention: %v", err)
	}
	if result.DeletedSessions == 0 {
		t.Fatalf("expected at least one deleted session")
	}
	if result.DeletedMessages < 2 {
		t.Fatalf("expected at least two trimmed messages, got %d", result.DeletedMessages)
	}

	if _, err := sessions.Get(oldSession.ID); err != ErrNotFound {
		t.Fatalf("expected old session to be deleted, got err=%v", err)
	}

	count, err := messages.CountBySession(newSession.ID)
	if err != nil {
		t.Fatalf("CountBySession: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 messages retained for new session, got %d", count)
	}
}

func TestActionLogStore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewActionLogStore(db)
	if err := store.RecordWriteFile("/tmp/example.txt", true, "before", "after", true, ""); err != nil {
		t.Fatalf("RecordWriteFile: %v", err)
	}
	if err := store.RecordExec("echo hello", "/tmp", "hello\n", false, "exit code 1"); err != nil {
		t.Fatalf("RecordExec: %v", err)
	}

	entries, err := store.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	foundWrite := false
	foundExec := false
	for _, e := range entries {
		switch e.ToolName {
		case "write_file":
			foundWrite = true
			if !e.BeforeExists || e.BeforeContent != "before" || e.AfterContent != "after" {
				t.Fatalf("unexpected write_file entry: %+v", e)
			}
		case "exec":
			foundExec = true
			if e.Command != "echo hello" || e.WorkDir != "/tmp" || e.Success {
				t.Fatalf("unexpected exec entry: %+v", e)
			}
		}
	}
	if !foundWrite || !foundExec {
		t.Fatalf("expected both write_file and exec entries, got %+v", entries)
	}

	got, err := store.Get(entries[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != entries[0].ID {
		t.Fatalf("expected id %d, got %d", entries[0].ID, got.ID)
	}
}

func TestEmbeddingErrorStore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewEmbeddingErrorStore(db)
	if err := store.RecordEmbeddingError("context_assemble", "network timeout"); err != nil {
		t.Fatalf("RecordEmbeddingError #1: %v", err)
	}
	if err := store.RecordEmbeddingError("context_assemble", "network timeout"); err != nil {
		t.Fatalf("RecordEmbeddingError #2: %v", err)
	}
	if err := store.RecordEmbeddingError("message_index", "quota exceeded"); err != nil {
		t.Fatalf("RecordEmbeddingError #3: %v", err)
	}

	summary, err := store.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.TotalCount != 3 {
		t.Fatalf("expected total_count=3, got %d", summary.TotalCount)
	}
	if summary.UniqueCount != 2 {
		t.Fatalf("expected unique_count=2, got %d", summary.UniqueCount)
	}
	if summary.LastSeen.IsZero() {
		t.Fatalf("expected non-zero last_seen")
	}
}
