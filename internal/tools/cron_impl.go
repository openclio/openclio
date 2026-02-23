package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclio/openclio/internal/storage"
)

func init() {
	// Replace noop cron tools with DB-backed implementations.
	_ = ReplaceTool("cron_create", cronCreateTool)
	_ = ReplaceTool("cron_list", cronListTool)
	_ = ReplaceTool("cron_delete", cronDeleteTool)
	_ = ReplaceTool("cron_run_now", cronRunNowTool)
}

func dbPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".openclio", "data.db"), nil
}

func openDB() (*storage.DB, error) {
	p, err := dbPath()
	if err != nil {
		return nil, err
	}
	return storage.Open(p)
}

func cronCreateTool(ctx context.Context, payload map[string]any) (any, error) {
	nameI, ok := payload["name"]
	if !ok {
		return nil, fmt.Errorf("name is required")
	}
	name, _ := nameI.(string)
	schedule, _ := payload["schedule"].(string)
	trigger, _ := payload["trigger"].(string)
	prompt, _ := payload["prompt"].(string)
	channel, _ := payload["channel"].(string)
	sessionMode, _ := payload["session_mode"].(string)
	timeoutSec := 0
	if t, ok := payload["timeout_seconds"]; ok {
		switch v := t.(type) {
		case int:
			timeoutSec = v
		case float64:
			timeoutSec = int(v)
		}
	}
	enabled := true
	if e, ok := payload["enabled"]; ok {
		if b, ok := e.(bool); ok {
			enabled = b
		}
	}

	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store := storage.NewCronJobStore(db)
	job, err := store.Create(storage.CronJob{
		Name:        strings.TrimSpace(name),
		Schedule:    strings.TrimSpace(schedule),
		Trigger:     strings.TrimSpace(trigger),
		Prompt:      prompt,
		Channel:     strings.TrimSpace(channel),
		SessionMode: strings.TrimSpace(sessionMode),
		TimeoutSec:  timeoutSec,
		Enabled:     enabled,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":              job.ID,
		"name":            job.Name,
		"schedule":        job.Schedule,
		"trigger":         job.Trigger,
		"prompt":          job.Prompt,
		"channel":         job.Channel,
		"session_mode":    job.SessionMode,
		"timeout_seconds": job.TimeoutSec,
		"enabled":         job.Enabled,
	}, nil
}

func cronListTool(ctx context.Context, payload map[string]any) (any, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store := storage.NewCronJobStore(db)
	jobs, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, map[string]any{
			"id":              j.ID,
			"name":            j.Name,
			"schedule":        j.Schedule,
			"trigger":         j.Trigger,
			"prompt":          j.Prompt,
			"channel":         j.Channel,
			"session_mode":    j.SessionMode,
			"timeout_seconds": j.TimeoutSec,
			"enabled":         j.Enabled,
			"created_at":      j.CreatedAt,
			"updated_at":      j.UpdatedAt,
		})
	}
	return out, nil
}

func cronDeleteTool(ctx context.Context, payload map[string]any) (any, error) {
	nameI, ok := payload["name"]
	if !ok {
		return nil, fmt.Errorf("name is required")
	}
	name, _ := nameI.(string)
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store := storage.NewCronJobStore(db)
	if err := store.DeleteByName(strings.TrimSpace(name)); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": name}, nil
}

func cronRunNowTool(ctx context.Context, payload map[string]any) (any, error) {
	// Runtime trigger requires the gateway scheduler to be running.
	// This tool records a manual request in cron_history so operators can see intent,
	// but does not execute the agent run when the scheduler is not accessible here.
	nameI, ok := payload["name"]
	if !ok {
		return nil, fmt.Errorf("name is required")
	}
	name, _ := nameI.(string)
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// Insert a history row marking manual trigger requested.
	_, err = db.Conn().Exec(`INSERT INTO cron_history (job_name, duration_ms, success, output) VALUES (?, ?, ?, ?)`, strings.TrimSpace(name), 0, 0, "manual trigger requested via tool (scheduler must be running to execute)")
	if err != nil {
		return nil, fmt.Errorf("failed to record manual trigger: %w", err)
	}
	return map[string]any{"ok": true, "job": name, "note": "manual trigger recorded; runtime scheduler must be running to execute the job"}, nil
}
