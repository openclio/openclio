package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AgentProfile represents one persisted profile used by dashboard agent controls.
type AgentProfile struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	SystemPrompt string    `json:"system_prompt"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AgentProfileStore provides CRUD for agent profiles.
type AgentProfileStore struct {
	db *sql.DB
}

// NewAgentProfileStore creates a new AgentProfileStore.
func NewAgentProfileStore(db *DB) *AgentProfileStore {
	return &AgentProfileStore{db: db.Conn()}
}

// List returns all agent profiles ordered by name.
func (s *AgentProfileStore) List() ([]AgentProfile, error) {
	rows, err := s.db.Query(`
		SELECT id, name, description, provider, model, system_prompt, is_active, created_at, updated_at
		FROM agent_profiles
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing agent profiles: %w", err)
	}
	defer rows.Close()

	out := make([]AgentProfile, 0)
	for rows.Next() {
		profile, err := scanAgentProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning agent profile: %w", err)
		}
		out = append(out, *profile)
	}
	return out, rows.Err()
}

// Get returns one profile by ID.
func (s *AgentProfileStore) Get(id string) (*AgentProfile, error) {
	row := s.db.QueryRow(`
		SELECT id, name, description, provider, model, system_prompt, is_active, created_at, updated_at
		FROM agent_profiles
		WHERE id = ?
	`, id)
	profile, err := scanAgentProfile(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting agent profile %q: %w", id, err)
	}
	return profile, nil
}

// GetActive returns the currently active profile, if any.
func (s *AgentProfileStore) GetActive() (*AgentProfile, error) {
	row := s.db.QueryRow(`
		SELECT id, name, description, provider, model, system_prompt, is_active, created_at, updated_at
		FROM agent_profiles
		WHERE is_active = 1
		ORDER BY updated_at DESC
		LIMIT 1
	`)
	profile, err := scanAgentProfile(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting active agent profile: %w", err)
	}
	return profile, nil
}

// Create inserts a new profile.
func (s *AgentProfileStore) Create(profile AgentProfile) (*AgentProfile, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	active := 0
	if profile.IsActive {
		active = 1
	}

	_, err := s.db.Exec(`
		INSERT INTO agent_profiles (id, name, description, provider, model, system_prompt, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, profile.Name, profile.Description, profile.Provider, profile.Model, profile.SystemPrompt, active, now, now)
	if err != nil {
		return nil, fmt.Errorf("creating agent profile %q: %w", profile.Name, err)
	}

	if profile.IsActive {
		if err := s.Activate(id); err != nil {
			return nil, err
		}
	}

	return s.Get(id)
}

// Update mutates one profile by ID.
func (s *AgentProfileStore) Update(id string, profile AgentProfile) error {
	res, err := s.db.Exec(`
		UPDATE agent_profiles
		SET name = ?, description = ?, provider = ?, model = ?, system_prompt = ?, updated_at = datetime('now')
		WHERE id = ?
	`, profile.Name, profile.Description, profile.Provider, profile.Model, profile.SystemPrompt, id)
	if err != nil {
		return fmt.Errorf("updating agent profile %q: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes one profile by ID.
func (s *AgentProfileStore) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM agent_profiles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting agent profile %q: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Activate marks exactly one profile active (others inactive).
func (s *AgentProfileStore) Activate(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("activating agent profile %q: begin tx: %w", id, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE agent_profiles SET is_active = 0, updated_at = datetime('now') WHERE is_active = 1`); err != nil {
		return fmt.Errorf("activating agent profile %q: clear previous: %w", id, err)
	}
	res, err := tx.Exec(`UPDATE agent_profiles SET is_active = 1, updated_at = datetime('now') WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("activating agent profile %q: set active: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("activating agent profile %q: commit: %w", id, err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanAgentProfile(row rowScanner) (*AgentProfile, error) {
	var profile AgentProfile
	var isActiveInt int
	err := row.Scan(
		&profile.ID,
		&profile.Name,
		&profile.Description,
		&profile.Provider,
		&profile.Model,
		&profile.SystemPrompt,
		&isActiveInt,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	profile.IsActive = isActiveInt == 1
	return &profile, nil
}
