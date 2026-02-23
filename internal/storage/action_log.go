package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// ActionLogEntry is one persisted tool-action record.
type ActionLogEntry struct {
	ID            int64     `json:"id"`
	ToolName      string    `json:"tool_name"`
	TargetPath    string    `json:"target_path,omitempty"`
	BeforeExists  bool      `json:"before_exists"`
	BeforeContent string    `json:"before_content,omitempty"`
	AfterContent  string    `json:"after_content,omitempty"`
	Command       string    `json:"command,omitempty"`
	WorkDir       string    `json:"work_dir,omitempty"`
	Output        string    `json:"output,omitempty"`
	Success       bool      `json:"success"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ActionLogStore provides persistence for tool action logs.
type ActionLogStore struct {
	db *sql.DB
}

// NewActionLogStore creates a new ActionLogStore.
func NewActionLogStore(db *DB) *ActionLogStore {
	return &ActionLogStore{db: db.Conn()}
}

// RecordWriteFile stores one write_file action snapshot.
func (s *ActionLogStore) RecordWriteFile(
	targetPath string,
	beforeExists bool,
	beforeContent string,
	afterContent string,
	success bool,
	errMsg string,
) error {
	successInt := 0
	if success {
		successInt = 1
	}
	beforeExistsInt := 0
	if beforeExists {
		beforeExistsInt = 1
	}

	_, err := ExecWithRetry(
		s.db,
		`INSERT INTO action_log (
			tool_name, target_path, before_exists, before_content, after_content, success, error
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"write_file", targetPath, beforeExistsInt, beforeContent, afterContent, successInt, errMsg,
	)
	if err != nil {
		return fmt.Errorf("recording write_file action: %w", err)
	}
	return nil
}

// RecordExec stores one exec action snapshot.
func (s *ActionLogStore) RecordExec(command, workDir, output string, success bool, errMsg string) error {
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := ExecWithRetry(
		s.db,
		`INSERT INTO action_log (
			tool_name, command, work_dir, output, success, error
		) VALUES (?, ?, ?, ?, ?, ?)`,
		"exec", command, workDir, output, successInt, errMsg,
	)
	if err != nil {
		return fmt.Errorf("recording exec action: %w", err)
	}
	return nil
}

// List returns most recent action log entries.
func (s *ActionLogStore) List(limit int) ([]ActionLogEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT id, tool_name, target_path, before_exists, before_content, after_content, command, work_dir, output, success, error, created_at
		 FROM action_log
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing action log entries: %w", err)
	}
	defer rows.Close()

	entries := make([]ActionLogEntry, 0, limit)
	for rows.Next() {
		entry, err := scanActionLogEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating action log entries: %w", err)
	}
	return entries, nil
}

// Get returns one action log entry by id.
func (s *ActionLogStore) Get(id int64) (*ActionLogEntry, error) {
	row := s.db.QueryRow(
		`SELECT id, tool_name, target_path, before_exists, before_content, after_content, command, work_dir, output, success, error, created_at
		 FROM action_log
		 WHERE id = ?`,
		id,
	)

	var (
		entry         ActionLogEntry
		targetPath    sql.NullString
		beforeExists  int
		beforeContent sql.NullString
		afterContent  sql.NullString
		command       sql.NullString
		workDir       sql.NullString
		output        sql.NullString
		success       int
		errMsg        sql.NullString
	)
	if err := row.Scan(
		&entry.ID,
		&entry.ToolName,
		&targetPath,
		&beforeExists,
		&beforeContent,
		&afterContent,
		&command,
		&workDir,
		&output,
		&success,
		&errMsg,
		&entry.CreatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting action log entry %d: %w", id, err)
	}

	entry.TargetPath = targetPath.String
	entry.BeforeExists = beforeExists == 1
	entry.BeforeContent = beforeContent.String
	entry.AfterContent = afterContent.String
	entry.Command = command.String
	entry.WorkDir = workDir.String
	entry.Output = output.String
	entry.Success = success == 1
	entry.Error = errMsg.String
	return &entry, nil
}

func scanActionLogEntry(scanner interface {
	Scan(dest ...any) error
}) (ActionLogEntry, error) {
	var (
		entry         ActionLogEntry
		targetPath    sql.NullString
		beforeExists  int
		beforeContent sql.NullString
		afterContent  sql.NullString
		command       sql.NullString
		workDir       sql.NullString
		output        sql.NullString
		success       int
		errMsg        sql.NullString
	)
	if err := scanner.Scan(
		&entry.ID,
		&entry.ToolName,
		&targetPath,
		&beforeExists,
		&beforeContent,
		&afterContent,
		&command,
		&workDir,
		&output,
		&success,
		&errMsg,
		&entry.CreatedAt,
	); err != nil {
		return ActionLogEntry{}, fmt.Errorf("scanning action log entry: %w", err)
	}
	entry.TargetPath = targetPath.String
	entry.BeforeExists = beforeExists == 1
	entry.BeforeContent = beforeContent.String
	entry.AfterContent = afterContent.String
	entry.Command = command.String
	entry.WorkDir = workDir.String
	entry.Output = output.String
	entry.Success = success == 1
	entry.Error = errMsg.String
	return entry, nil
}
