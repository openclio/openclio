package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Memory represents a single memory entry.
type Memory struct {
	ID        string                 `json:"id"`
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// Store provides persistent storage for memories.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) a SQLite DB at dsn and ensures schema exists.
func NewStore(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	schema := `
	CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		content TEXT NOT NULL,
		metadata TEXT,
		created_at DATETIME NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying DB.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Add inserts a new memory and returns its ID.
func (s *Store) Add(ctx context.Context, content string, metadata map[string]interface{}) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil store")
	}
	id := uuid.New().String()
	metaBytes := []byte(nil)
	var err error
	if metadata != nil {
		metaBytes, err = json.Marshal(metadata)
		if err != nil {
			return "", fmt.Errorf("marshal metadata: %w", err)
		}
	}
	created := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, "INSERT INTO memories(id, content, metadata, created_at) VALUES(?,?,?,?)", id, content, string(metaBytes), created)
	if err != nil {
		return "", fmt.Errorf("insert memory: %w", err)
	}
	return id, nil
}

// Get returns a memory by ID.
func (s *Store) Get(ctx context.Context, id string) (*Memory, error) {
	if s == nil {
		return nil, fmt.Errorf("nil store")
	}
	row := s.db.QueryRowContext(ctx, "SELECT id, content, metadata, created_at FROM memories WHERE id = ?", id)
	var m Memory
	var metaStr sql.NullString
	var createdStr string
	if err := row.Scan(&m.ID, &m.Content, &metaStr, &createdStr); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query memory: %w", err)
	}
	if metaStr.Valid && metaStr.String != "" {
		var mm map[string]interface{}
		if err := json.Unmarshal([]byte(metaStr.String), &mm); err == nil {
			m.Metadata = mm
		}
	}
	created, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		// fallback: try sqlite DATETIME parsing
		created = time.Now().UTC()
	}
	m.CreatedAt = created
	return &m, nil
}

// List returns memories ordered by created_at desc.
func (s *Store) List(ctx context.Context, limit, offset int) ([]Memory, error) {
	if s == nil {
		return nil, fmt.Errorf("nil store")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id, content, metadata, created_at FROM memories ORDER BY created_at DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()
	var res []Memory
	for rows.Next() {
		var m Memory
		var metaStr sql.NullString
		var createdStr string
		if err := rows.Scan(&m.ID, &m.Content, &metaStr, &createdStr); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if metaStr.Valid && metaStr.String != "" {
			var mm map[string]interface{}
			if err := json.Unmarshal([]byte(metaStr.String), &mm); err == nil {
				m.Metadata = mm
			}
		}
		created, err := time.Parse(time.RFC3339Nano, createdStr)
		if err != nil {
			created = time.Now().UTC()
		}
		m.CreatedAt = created
		res = append(res, m)
	}
	return res, nil
}

// Delete removes a memory by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil {
		return fmt.Errorf("nil store")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM memories WHERE id = ?", id)
	return err
}

// Search finds memories matching the query using a simple occurrence heuristic.
// Returns up to k results ordered by occurrences desc then created_at desc.
func (s *Store) Search(ctx context.Context, query string, k int) ([]Memory, error) {
	if s == nil {
		return nil, fmt.Errorf("nil store")
	}
	if query == "" {
		// return recent items
		return s.List(ctx, k, 0)
	}
	if k <= 0 {
		k = 10
	}
	// Use a simple occurrence count heuristic.
	q := `
	SELECT id, content, metadata, created_at,
		((length(lower(content)) - length(replace(lower(content), lower(?), ''))) / nullif(length(?),1)) AS occurrences
	FROM memories
	ORDER BY occurrences DESC, created_at DESC
	LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, q, query, query, k)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()
	var res []Memory
	for rows.Next() {
		var m Memory
		var metaStr sql.NullString
		var createdStr string
		var occ sql.NullFloat64
		if err := rows.Scan(&m.ID, &m.Content, &metaStr, &createdStr, &occ); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if metaStr.Valid && metaStr.String != "" {
			var mm map[string]interface{}
			if err := json.Unmarshal([]byte(metaStr.String), &mm); err == nil {
				m.Metadata = mm
			}
		}
		created, err := time.Parse(time.RFC3339Nano, createdStr)
		if err != nil {
			created = time.Now().UTC()
		}
		m.CreatedAt = created
		// score could be attached in metadata if needed
		_ = occ
		res = append(res, m)
	}
	return res, nil
}
