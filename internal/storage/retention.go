package storage

import (
	"fmt"
	"time"
)

// RetentionResult reports rows pruned by retention enforcement.
type RetentionResult struct {
	DeletedSessions int64
	DeletedMessages int64
}

// EnforceRetention prunes sessions and messages based on configured limits.
func (db *DB) EnforceRetention(sessionsDays, messagesPerSession int) (RetentionResult, error) {
	var result RetentionResult

	if sessionsDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -sessionsDays)
		execResult, err := ExecWithRetry(db.conn, "DELETE FROM sessions WHERE last_active < ?", cutoff)
		if err != nil {
			return result, fmt.Errorf("deleting expired sessions: %w", err)
		}
		rows, _ := execResult.RowsAffected()
		result.DeletedSessions = rows
	}

	if messagesPerSession > 0 {
		execResult, err := ExecWithRetry(db.conn, `
			DELETE FROM messages
			WHERE id IN (
				SELECT id
				FROM (
					SELECT id,
					       ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY created_at DESC, id DESC) AS rn
					FROM messages
				) ranked
				WHERE rn > ?
			)
		`, messagesPerSession)
		if err != nil {
			return result, fmt.Errorf("trimming messages per session: %w", err)
		}
		rows, _ := execResult.RowsAffected()
		result.DeletedMessages = rows
	}

	return result, nil
}
