package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// EmbeddingErrorSummary aggregates tracked embedding errors.
type EmbeddingErrorSummary struct {
	TotalCount  int64     `json:"total_count"`
	UniqueCount int64     `json:"unique_count"`
	LastSeen    time.Time `json:"last_seen,omitempty"`
}

// EmbeddingErrorStore persists embedding error counters.
type EmbeddingErrorStore struct {
	db *sql.DB
}

// NewEmbeddingErrorStore creates a new EmbeddingErrorStore.
func NewEmbeddingErrorStore(db *DB) *EmbeddingErrorStore {
	return &EmbeddingErrorStore{db: db.Conn()}
}

// RecordEmbeddingError records one embedding error occurrence.
func (s *EmbeddingErrorStore) RecordEmbeddingError(source, errorMessage string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "unknown"
	}
	errorMessage = strings.TrimSpace(errorMessage)
	if errorMessage == "" {
		return nil
	}

	_, err := ExecWithRetry(
		s.db,
		`INSERT INTO embedding_errors (source, error, count, first_seen, last_seen)
		 VALUES (?, ?, 1, datetime('now'), datetime('now'))
		 ON CONFLICT(source, error) DO UPDATE SET
		   count = count + 1,
		   last_seen = datetime('now')`,
		source, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("recording embedding error: %w", err)
	}
	return nil
}

// Summary returns aggregate embedding error metrics.
func (s *EmbeddingErrorStore) Summary() (EmbeddingErrorSummary, error) {
	var (
		out      EmbeddingErrorSummary
		lastSeen sql.NullString
	)
	if err := s.db.QueryRow(
		`SELECT COALESCE(SUM(count), 0), COUNT(*), MAX(last_seen) FROM embedding_errors`,
	).Scan(&out.TotalCount, &out.UniqueCount, &lastSeen); err != nil {
		return EmbeddingErrorSummary{}, fmt.Errorf("reading embedding error summary: %w", err)
	}
	if lastSeen.Valid {
		if ts, err := parseSQLiteTimestamp(lastSeen.String); err == nil {
			out.LastSeen = ts
		}
	}
	return out, nil
}

func parseSQLiteTimestamp(raw string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, raw)
}
