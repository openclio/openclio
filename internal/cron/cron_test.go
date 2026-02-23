package cron

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclio/openclio/internal/agent"
	"github.com/openclio/openclio/internal/config"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/storage"
)

type cronMockProvider struct{}

func (p *cronMockProvider) Name() string { return "cron-mock" }
func (p *cronMockProvider) Chat(_ context.Context, _ agent.ChatRequest) (*agent.ChatResponse, error) {
	return &agent.ChatResponse{
		Content: "ok",
		Usage: agent.Usage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}, nil
}

func setupCronTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cron-test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return db
}

func TestListJobsIncludesModeAndLastRun(t *testing.T) {
	db := setupCronTestDB(t)
	defer db.Close()

	s := NewScheduler(nil, nil, nil, nil, nil, db, nil)
	if err := s.Add(Job{
		Name:        "daily",
		Schedule:    "* * * * *",
		Prompt:      "hello",
		SessionMode: "shared",
	}); err != nil {
		t.Fatalf("Add() failed: %v", err)
	}

	if _, err := db.Conn().Exec(
		`INSERT INTO cron_history (job_name, ran_at, duration_ms, success, output) VALUES (?, datetime('now'), 10, 1, 'ok')`,
		"daily",
	); err != nil {
		t.Fatalf("insert history: %v", err)
	}

	jobs := s.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].SessionMode != "shared" {
		t.Fatalf("expected mode shared, got %q", jobs[0].SessionMode)
	}
	if jobs[0].LastRun.IsZero() {
		t.Fatal("expected last run to be populated")
	}
	if jobs[0].NextRun.IsZero() {
		t.Fatal("expected next run to be populated")
	}
}

func TestSelectSessionSharedReusesSession(t *testing.T) {
	db := setupCronTestDB(t)
	defer db.Close()

	sessions := storage.NewSessionStore(db)
	s := NewScheduler(nil, sessions, nil, nil, nil, db, nil)

	shared := Job{Name: "shared-job", SessionMode: "shared"}
	s1, err := s.selectSession(shared)
	if err != nil {
		t.Fatalf("selectSession(shared) #1: %v", err)
	}
	s2, err := s.selectSession(shared)
	if err != nil {
		t.Fatalf("selectSession(shared) #2: %v", err)
	}
	if s1.ID != s2.ID {
		t.Fatalf("expected shared session reuse, got %s vs %s", s1.ID, s2.ID)
	}

	isolated := Job{Name: "isolated-job", SessionMode: "isolated"}
	i1, err := s.selectSession(isolated)
	if err != nil {
		t.Fatalf("selectSession(isolated) #1: %v", err)
	}
	i2, err := s.selectSession(isolated)
	if err != nil {
		t.Fatalf("selectSession(isolated) #2: %v", err)
	}
	if i1.ID == i2.ID {
		t.Fatalf("expected isolated sessions to differ, both were %s", i1.ID)
	}
}

func TestLoadPersistedJobs_ConfigTakesPrecedence(t *testing.T) {
	db := setupCronTestDB(t)
	defer db.Close()

	store := storage.NewCronJobStore(db)
	if _, err := store.Create(storage.CronJob{
		Name:        "cfg-wins",
		Schedule:    "*/15 * * * *",
		Prompt:      "db prompt",
		SessionMode: "shared",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("create persisted duplicate: %v", err)
	}
	if _, err := store.Create(storage.CronJob{
		Name:        "db-job",
		Schedule:    "0 * * * *",
		Prompt:      "db only",
		SessionMode: "isolated",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("create persisted db job: %v", err)
	}

	s := NewScheduler(nil, nil, nil, nil, nil, db, nil)
	if err := s.Add(Job{
		Name:        "cfg-wins",
		Schedule:    "*/10 * * * *",
		Prompt:      "config prompt",
		SessionMode: "isolated",
	}); err != nil {
		t.Fatalf("Add config job: %v", err)
	}

	loaded, skipped, err := s.LoadPersistedJobs()
	if err != nil {
		t.Fatalf("LoadPersistedJobs: %v", err)
	}
	if loaded != 1 || skipped != 1 {
		t.Fatalf("expected loaded=1 skipped=1, got loaded=%d skipped=%d", loaded, skipped)
	}

	jobs := s.ListJobs()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 runtime jobs, got %d", len(jobs))
	}
}

func TestPersistentJobLifecycle(t *testing.T) {
	db := setupCronTestDB(t)
	defer db.Close()

	s := NewScheduler(nil, nil, nil, nil, nil, db, nil)
	st, err := s.CreatePersistent(Job{
		Name:        "db-crud",
		Schedule:    "*/5 * * * *",
		Prompt:      "hello",
		SessionMode: "shared",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("CreatePersistent: %v", err)
	}
	if st.Source != "db" || !st.Enabled {
		t.Fatalf("unexpected created status: %+v", st)
	}

	st, err = s.SetPersistentEnabled("db-crud", false)
	if err != nil {
		t.Fatalf("SetPersistentEnabled(false): %v", err)
	}
	if st.Enabled {
		t.Fatalf("expected enabled=false, got %+v", st)
	}

	if err := s.DeletePersistent("db-crud"); err != nil {
		t.Fatalf("DeletePersistent: %v", err)
	}
	if s.HasJob("db-crud") {
		t.Fatalf("expected runtime job removed after delete")
	}
}

func TestAddAppliesDefaultTimeout(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil, nil, nil, nil)
	if err := s.Add(Job{
		Name:     "default-timeout",
		Schedule: "* * * * *",
		Prompt:   "ping",
	}); err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	jobs := s.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].TimeoutSec != int(defaultJobTimeout/time.Second) {
		t.Fatalf("expected default timeout %d, got %d", int(defaultJobTimeout/time.Second), jobs[0].TimeoutSec)
	}
}

func TestAddUsesPerJobTimeout(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil, nil, nil, nil)
	if err := s.Add(Job{
		Name:       "custom-timeout",
		Schedule:   "* * * * *",
		Prompt:     "ping",
		TimeoutSec: 42,
	}); err != nil {
		t.Fatalf("Add() failed: %v", err)
	}
	jobs := s.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].TimeoutSec != 42 {
		t.Fatalf("expected timeout 42, got %d", jobs[0].TimeoutSec)
	}
}

func TestMarkJobRunningPreventsConcurrentExecution(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil, nil, nil, nil)
	if !s.markJobRunning("same-job") {
		t.Fatalf("expected first mark to succeed")
	}
	if s.markJobRunning("same-job") {
		t.Fatalf("expected second mark to fail while job is running")
	}
	s.clearJobRunning("same-job")
	if !s.markJobRunning("same-job") {
		t.Fatalf("expected mark to succeed after clear")
	}
}

func TestAddTriggerJobWithoutSchedule(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil, nil, nil, nil)
	if err := s.Add(Job{
		Name:    "watch-interval",
		Trigger: "every 6 hours",
		Prompt:  "check price",
	}); err != nil {
		t.Fatalf("Add(trigger-only) failed: %v", err)
	}
	jobs := s.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Trigger != "every 6 hours" {
		t.Fatalf("expected trigger to be preserved, got %q", jobs[0].Trigger)
	}
	if jobs[0].Schedule != "" {
		t.Fatalf("expected empty schedule for trigger job, got %q", jobs[0].Schedule)
	}
	if jobs[0].NextRun.IsZero() {
		t.Fatalf("expected computed next run for interval trigger")
	}
}

func TestAddRejectsScheduleAndTriggerTogether(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil, nil, nil, nil)
	err := s.Add(Job{
		Name:     "bad-job",
		Schedule: "* * * * *",
		Trigger:  "every 1 hour",
		Prompt:   "bad",
	})
	if err == nil {
		t.Fatalf("expected error when both schedule and trigger are set")
	}
}

func TestStartRegistersTriggerLoops(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil, nil, nil, nil)
	if err := s.Add(Job{
		Name:    "watch-start",
		Trigger: "every 10 minutes",
		Prompt:  "check",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.Start()
	defer s.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		_, ok := s.triggerCancel["watch-start"]
		s.mu.RUnlock()
		if ok {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected trigger loop registration after Start()")
}

func TestFileChangedTriggerRunsJobOnModification(t *testing.T) {
	db := setupCronTestDB(t)
	defer db.Close()

	sessions := storage.NewSessionStore(db)
	messages := storage.NewMessageStore(db)
	file := filepath.Join(t.TempDir(), "watched.txt")
	if err := os.WriteFile(file, []byte("v1"), 0644); err != nil {
		t.Fatalf("seed watched file: %v", err)
	}

	engine := agentctx.NewEngine(agentctx.NewNoOpEmbedder(), 4000, 5)
	agentInstance := agent.NewAgent(&cronMockProvider{}, engine, nil, config.DefaultConfig().Agent, "test-model")
	s := NewScheduler(agentInstance, sessions, messages, engine, nil, db, nil)
	// RunStart not needed; we'll directly run trigger loop with a short poll for determinism.
	spec, err := parseTrigger("file_changed " + file)
	if err != nil {
		t.Fatalf("parseTrigger: %v", err)
	}
	spec.PollEvery = 50 * time.Millisecond
	job := Job{Name: "file-watch-job", Trigger: "file_changed " + file, Prompt: "watch"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.runTriggerLoop(ctx, job, spec)

	// Prime baseline mtime.
	time.Sleep(120 * time.Millisecond)
	if err := os.WriteFile(file, []byte("v2"), 0644); err != nil {
		t.Fatalf("update watched file: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		allSessions, err := sessions.List(10)
		if err != nil {
			t.Fatalf("List sessions: %v", err)
		}
		if len(allSessions) == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		count, err := messages.CountBySession(allSessions[0].ID)
		if err != nil {
			t.Fatalf("CountBySession: %v", err)
		}
		if count > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected file_changed trigger to run job and persist at least one message")
}
