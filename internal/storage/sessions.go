package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Session represents a conversation session.
type Session struct {
	ID             string    `json:"id"`
	Channel        string    `json:"channel"`
	SenderID       string    `json:"sender_id"`
	CreatedAt      time.Time `json:"created_at"`
	LastActive     time.Time `json:"last_active"`
	Metadata       string    `json:"metadata"`
	AgentProfileID string    `json:"agent_profile_id,omitempty"`
}

// SessionStore provides CRUD operations for sessions.
type SessionStore struct {
	db *sql.DB
}

// NewSessionStore creates a new SessionStore.
func NewSessionStore(db *DB) *SessionStore {
	return &SessionStore{db: db.Conn()}
}

// Create inserts a new session and returns it.
func (s *SessionStore) Create(channel, senderID string) (*Session, error) {
	id := uuid.New().String()
	now := time.Now().UTC()

	_, err := s.db.Exec(
		"INSERT INTO sessions (id, channel, sender_id, created_at, last_active) VALUES (?, ?, ?, ?, ?)",
		id, channel, senderID, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	return &Session{
		ID:         id,
		Channel:    channel,
		SenderID:   senderID,
		CreatedAt:  now,
		LastActive: now,
		Metadata:   "{}",
	}, nil
}

// Get retrieves a session by ID.
func (s *SessionStore) Get(id string) (*Session, error) {
	session := &Session{}
	err := s.db.QueryRow(
		"SELECT id, channel, sender_id, created_at, last_active, metadata, agent_profile_id FROM sessions WHERE id = ?",
		id,
	).Scan(&session.ID, &session.Channel, &session.SenderID, &session.CreatedAt, &session.LastActive, &session.Metadata, &session.AgentProfileID)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting session %s: %w", id, err)
	}
	return session, nil
}

// GetByChannelSender returns the most recent session for a channel/sender pair.
func (s *SessionStore) GetByChannelSender(channel, senderID string) (*Session, error) {
	session := &Session{}
	err := s.db.QueryRow(
		`SELECT id, channel, sender_id, created_at, last_active, metadata, agent_profile_id
		 FROM sessions
		 WHERE channel = ? AND sender_id = ?
		 ORDER BY last_active DESC
		 LIMIT 1`,
		channel, senderID,
	).Scan(&session.ID, &session.Channel, &session.SenderID, &session.CreatedAt, &session.LastActive, &session.Metadata, &session.AgentProfileID)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting session by channel/sender: %w", err)
	}
	return session, nil
}

// List returns all sessions ordered by last activity.
func (s *SessionStore) List(limit int) ([]Session, error) {
	rows, err := s.db.Query(
		"SELECT id, channel, sender_id, created_at, last_active, metadata, agent_profile_id FROM sessions ORDER BY last_active DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var session Session
		if err := rows.Scan(&session.ID, &session.Channel, &session.SenderID, &session.CreatedAt, &session.LastActive, &session.Metadata, &session.AgentProfileID); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// Count returns the total number of sessions.
func (s *SessionStore) Count() (int, error) {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); err != nil {
		return 0, fmt.Errorf("counting sessions: %w", err)
	}
	return count, nil
}

// UpdateLastActive updates the last_active timestamp.
func (s *SessionStore) UpdateLastActive(id string) error {
	_, err := s.db.Exec("UPDATE sessions SET last_active = datetime('now') WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("updating session %s: %w", id, err)
	}
	return nil
}

// Delete removes a session and all its messages (cascade).
func (s *SessionStore) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting session %s: %w", id, err)
	}
	return nil
}

// BindAgentProfile binds one session to an agent profile id (or empty to clear).
func (s *SessionStore) BindAgentProfile(id, profileID string) error {
	result, err := s.db.Exec("UPDATE sessions SET agent_profile_id = ? WHERE id = ?", profileID, id)
	if err != nil {
		return fmt.Errorf("binding session %s to agent profile %s: %w", id, profileID, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateMetadata updates the metadata JSON blob for a session.
func (s *SessionStore) UpdateMetadata(id, metadata string) error {
	_, err := s.db.Exec("UPDATE sessions SET metadata = ? WHERE id = ?", metadata, id)
	if err != nil {
		return fmt.Errorf("updating metadata for session %s: %w", id, err)
	}
	return nil
}
