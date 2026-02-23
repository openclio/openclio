// Package storage provides the SQLite persistence layer for the agent.
// All data — sessions, messages, memories, and tool results — is stored
// in a single SQLite database file.
package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database connection with agent-specific operations.
type DB struct {
	conn *sql.DB
	path string
}

// Open creates or opens a SQLite database at the given path.
// It sets optimal pragmas for performance and safety.
func Open(path string) (*DB, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating data directory %s: %w", dir, err)
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}

	// Apply key pragmas explicitly so behaviour is consistent across drivers.
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA auto_vacuum = INCREMENTAL;",
	} {
		if _, err := conn.Exec(pragma); err != nil {
			conn.Close()
			return nil, fmt.Errorf("applying sqlite pragma %q: %w", pragma, err)
		}
	}

	// Verify connection works
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("connecting to database %s: %w", path, err)
	}

	// Set file permissions to owner-only (security: Layer 5)
	if err := os.Chmod(path, 0600); err != nil {
		conn.Close()
		return nil, fmt.Errorf("setting database file permissions: %w", err)
	}

	return &DB{conn: conn, path: path}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying sql.DB for direct queries.
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// IncrementalVacuum reclaims free pages when auto_vacuum=INCREMENTAL is enabled.
func (db *DB) IncrementalVacuum() error {
	if _, err := db.conn.Exec("PRAGMA incremental_vacuum;"); err != nil {
		return fmt.Errorf("running incremental vacuum: %w", err)
	}
	return nil
}
