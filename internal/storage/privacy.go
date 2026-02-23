package storage

import (
	"database/sql"
	"fmt"
)

// PrivacyStore persists redaction counters used by privacy reporting.
type PrivacyStore struct {
	db *sql.DB
}

// NewPrivacyStore creates a new PrivacyStore.
func NewPrivacyStore(db *DB) *PrivacyStore {
	return &PrivacyStore{db: db.Conn()}
}

// RecordRedaction records one redaction event for a given category.
func (s *PrivacyStore) RecordRedaction(category string, count int) error {
	if count <= 0 {
		return nil
	}
	_, err := ExecWithRetry(
		s.db,
		`INSERT INTO privacy_redactions (category, count) VALUES (?, ?)`,
		category, count,
	)
	if err != nil {
		return fmt.Errorf("recording privacy redaction: %w", err)
	}
	return nil
}

// TotalByCategory returns the cumulative redaction count for one category.
func (s *PrivacyStore) TotalByCategory(category string) (int64, error) {
	var total int64
	err := s.db.QueryRow(
		`SELECT COALESCE(SUM(count), 0) FROM privacy_redactions WHERE category = ?`,
		category,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("reading redaction total for category %q: %w", category, err)
	}
	return total, nil
}
