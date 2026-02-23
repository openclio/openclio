package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrStorageLocked is returned when the SQLite database cannot be accessed
// because it is locked by another writer.
var ErrStorageLocked = errors.New("[E3002] database is locked — another process may be accessing it")

// ExecWithRetry executes a SQL statement, retrying on SQLITE_BUSY / "database is locked"
// up to maxAttempts times with a short backoff. This covers the rare case where
// busy_timeout=5000 is not sufficient (e.g., schema changes holding write locks).
func ExecWithRetry(db *sql.DB, stmt string, args ...any) (sql.Result, error) {
	const maxAttempts = 3
	delay := 100 * time.Millisecond

	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		result, err := db.Exec(stmt, args...)
		if err == nil {
			return result, nil
		}
		msg := err.Error()
		if !strings.Contains(msg, "SQLITE_BUSY") && !strings.Contains(msg, "database is locked") {
			return nil, err // not a lock error — fail immediately
		}
		lastErr = err
		time.Sleep(delay)
		delay *= 2
	}
	return nil, fmt.Errorf("%w: %v", ErrStorageLocked, lastErr)
}
