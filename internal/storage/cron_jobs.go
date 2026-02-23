package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// CronJob is one persisted scheduler job definition.
type CronJob struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Schedule    string    `json:"schedule"`
	Trigger     string    `json:"trigger,omitempty"`
	Prompt      string    `json:"prompt"`
	Channel     string    `json:"channel"`
	SessionMode string    `json:"session_mode"`
	TimeoutSec  int       `json:"timeout_seconds"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CronJobStore provides CRUD operations for persisted cron jobs.
type CronJobStore struct {
	db *sql.DB
}

// NewCronJobStore creates a new CronJobStore.
func NewCronJobStore(db *DB) *CronJobStore {
	return &CronJobStore{db: db.Conn()}
}

// List returns all persisted cron jobs.
func (s *CronJobStore) List() ([]CronJob, error) {
	rows, err := s.db.Query(`
		SELECT id, name, schedule, trigger, prompt, channel, session_mode, timeout_seconds, enabled, created_at, updated_at
		FROM cron_jobs
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing cron jobs: %w", err)
	}
	defer rows.Close()

	var out []CronJob
	for rows.Next() {
		var job CronJob
		var enabledInt int
		if err := rows.Scan(
			&job.ID,
			&job.Name,
			&job.Schedule,
			&job.Trigger,
			&job.Prompt,
			&job.Channel,
			&job.SessionMode,
			&job.TimeoutSec,
			&enabledInt,
			&job.CreatedAt,
			&job.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning cron job: %w", err)
		}
		job.Enabled = enabledInt == 1
		out = append(out, job)
	}
	return out, rows.Err()
}

// GetByName loads one persisted cron job by name.
func (s *CronJobStore) GetByName(name string) (*CronJob, error) {
	job := &CronJob{}
	var enabledInt int
	err := s.db.QueryRow(`
		SELECT id, name, schedule, trigger, prompt, channel, session_mode, timeout_seconds, enabled, created_at, updated_at
		FROM cron_jobs
		WHERE name = ?
	`, name).Scan(
		&job.ID,
		&job.Name,
		&job.Schedule,
		&job.Trigger,
		&job.Prompt,
		&job.Channel,
		&job.SessionMode,
		&job.TimeoutSec,
		&enabledInt,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting cron job %q: %w", name, err)
	}
	job.Enabled = enabledInt == 1
	return job, nil
}

// Create inserts one persisted cron job definition.
func (s *CronJobStore) Create(job CronJob) (*CronJob, error) {
	mode := strings.ToLower(strings.TrimSpace(job.SessionMode))
	if mode == "" {
		mode = "isolated"
	}
	timeoutSec := job.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	enabled := 0
	if job.Enabled {
		enabled = 1
	}
	now := time.Now().UTC()
	result, err := s.db.Exec(`
		INSERT INTO cron_jobs (name, schedule, trigger, prompt, channel, session_mode, timeout_seconds, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.Name, job.Schedule, job.Trigger, job.Prompt, job.Channel, mode, timeoutSec, enabled, now, now)
	if err != nil {
		return nil, fmt.Errorf("creating cron job %q: %w", job.Name, err)
	}
	id, _ := result.LastInsertId()
	return &CronJob{
		ID:          id,
		Name:        job.Name,
		Schedule:    job.Schedule,
		Trigger:     strings.TrimSpace(job.Trigger),
		Prompt:      job.Prompt,
		Channel:     job.Channel,
		SessionMode: mode,
		TimeoutSec:  timeoutSec,
		Enabled:     enabled == 1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// UpdateByName updates mutable fields for a persisted cron job definition.
func (s *CronJobStore) UpdateByName(name string, job CronJob) error {
	mode := strings.ToLower(strings.TrimSpace(job.SessionMode))
	if mode == "" {
		mode = "isolated"
	}
	timeoutSec := job.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	result, err := s.db.Exec(`
		UPDATE cron_jobs
		SET schedule = ?, trigger = ?, prompt = ?, channel = ?, session_mode = ?, timeout_seconds = ?, updated_at = datetime('now')
		WHERE name = ?
	`, job.Schedule, strings.TrimSpace(job.Trigger), job.Prompt, job.Channel, mode, timeoutSec, name)
	if err != nil {
		return fmt.Errorf("updating cron job %q: %w", name, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEnabled toggles whether a persisted cron job is active.
func (s *CronJobStore) SetEnabled(name string, enabled bool) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	result, err := s.db.Exec(`
		UPDATE cron_jobs
		SET enabled = ?, updated_at = datetime('now')
		WHERE name = ?
	`, enabledInt, name)
	if err != nil {
		return fmt.Errorf("updating cron job %q enabled=%t: %w", name, enabled, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteByName deletes one persisted cron job definition.
func (s *CronJobStore) DeleteByName(name string) error {
	result, err := s.db.Exec("DELETE FROM cron_jobs WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("deleting cron job %q: %w", name, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
