// Package cron runs scheduled agent tasks defined in config.
package cron

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	robfig "github.com/robfig/cron/v3"

	"github.com/openclio/openclio/internal/agent"
	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/plugin"
	"github.com/openclio/openclio/internal/storage"
)

const defaultJobTimeout = 5 * time.Minute
const defaultTriggerPollInterval = 30 * time.Second

// Job defines a scheduled task.
type Job struct {
	ID          int64  `yaml:"-"`
	Name        string `yaml:"name"`
	Schedule    string `yaml:"schedule"`
	Trigger     string `yaml:"trigger,omitempty"` // event-driven trigger, e.g. "every 6 hours"
	Prompt      string `yaml:"prompt"`
	Channel     string `yaml:"channel"`
	SessionMode string `yaml:"session_mode,omitempty"` // isolated | shared
	TimeoutSec  int    `yaml:"timeout_seconds,omitempty"`
	Enabled     bool   `yaml:"-"`
	Source      string `yaml:"-"`
}

// JobStatus is the runtime status of a registered job.
type JobStatus struct {
	ID          int64
	Name        string
	Schedule    string
	Trigger     string
	Channel     string
	LastRun     time.Time
	NextRun     time.Time
	SessionMode string
	TimeoutSec  int
	Enabled     bool
	Source      string
}

// HistoryEntry is a past job run record.
type HistoryEntry struct {
	JobName    string
	RanAt      time.Time
	DurationMs int64
	Success    bool
	Output     string
}

// Scheduler manages scheduled agent tasks.
type Scheduler struct {
	cron          *robfig.Cron
	parser        robfig.Parser
	agentInstance *agent.Agent
	sessions      *storage.SessionStore
	messages      *storage.MessageStore
	contextEngine *agentctx.Engine
	manager       *plugin.Manager
	db            *storage.DB
	cronStore     *storage.CronJobStore
	logger        *slog.Logger
	mu            sync.RWMutex
	jobs          []Job
	entryIDs      map[string]robfig.EntryID
	triggerCancel map[string]context.CancelFunc
	triggerCtx    context.Context
	triggerStop   context.CancelFunc
	started       bool
	runMu         sync.Mutex
	running       map[string]struct{}
}

// NewScheduler creates a new cron scheduler.
func NewScheduler(
	agentInstance *agent.Agent,
	sessions *storage.SessionStore,
	messages *storage.MessageStore,
	contextEngine *agentctx.Engine,
	manager *plugin.Manager,
	db *storage.DB,
	logger *slog.Logger,
) *Scheduler {
	parser := robfig.NewParser(
		robfig.SecondOptional |
			robfig.Minute |
			robfig.Hour |
			robfig.Dom |
			robfig.Month |
			robfig.Dow |
			robfig.Descriptor,
	)
	s := &Scheduler{
		cron:          robfig.New(robfig.WithParser(parser)),
		parser:        parser,
		agentInstance: agentInstance,
		sessions:      sessions,
		messages:      messages,
		contextEngine: contextEngine,
		manager:       manager,
		db:            db,
		logger:        logger,
		entryIDs:      make(map[string]robfig.EntryID),
		triggerCancel: make(map[string]context.CancelFunc),
		running:       make(map[string]struct{}),
	}
	if db != nil {
		s.cronStore = storage.NewCronJobStore(db)
	}
	return s
}

// Add registers a cron job.
func (s *Scheduler) Add(job Job) error {
	job.Source = "config"
	job.Enabled = true
	return s.addOrReplaceJob(job, false)
}

// HasJob reports whether a runtime job with this name is registered.
func (s *Scheduler) HasJob(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, _, ok := s.findJobLocked(name)
	return ok
}

// JobStatusByName returns one runtime job status by name.
func (s *Scheduler) JobStatusByName(name string) (JobStatus, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return JobStatus{}, fmt.Errorf("cron: job name is required")
	}
	jobs := s.ListJobs()
	for _, j := range jobs {
		if j.Name == name {
			return j, nil
		}
	}
	return JobStatus{}, fmt.Errorf("cron: job %q not found", name)
}

// LoadPersistedJobs registers all DB-backed cron jobs at runtime.
// Config-defined jobs keep precedence when names collide.
func (s *Scheduler) LoadPersistedJobs() (loaded int, skipped int, err error) {
	if s.cronStore == nil {
		return 0, 0, nil
	}
	records, err := s.cronStore.List()
	if err != nil {
		return 0, 0, err
	}

	for _, rec := range records {
		if s.HasJob(rec.Name) {
			skipped++
			if s.logger != nil {
				s.logger.Warn("skipping persisted cron job due to duplicate name",
					"name", rec.Name, "precedence", "config")
			}
			continue
		}
		job := Job{
			ID:          rec.ID,
			Name:        rec.Name,
			Schedule:    rec.Schedule,
			Trigger:     rec.Trigger,
			Prompt:      rec.Prompt,
			Channel:     rec.Channel,
			SessionMode: rec.SessionMode,
			TimeoutSec:  rec.TimeoutSec,
			Enabled:     rec.Enabled,
			Source:      "db",
		}
		if err := s.addOrReplaceJob(job, false); err != nil {
			skipped++
			if s.logger != nil {
				s.logger.Warn("skipping invalid persisted cron job", "name", rec.Name, "error", err)
			}
			continue
		}
		loaded++
	}
	return loaded, skipped, nil
}

// CreatePersistent creates and registers a DB-backed cron job.
func (s *Scheduler) CreatePersistent(job Job) (JobStatus, error) {
	if s.cronStore == nil {
		return JobStatus{}, fmt.Errorf("cron: persistent job store is unavailable")
	}
	job.Name = strings.TrimSpace(job.Name)
	job.Source = "db"
	if err := validateJobSpec(job.Name, job.Schedule, job.Trigger, job.Prompt, job.SessionMode, job.TimeoutSec, s.parser); err != nil {
		return JobStatus{}, err
	}
	if s.HasJob(job.Name) {
		return JobStatus{}, fmt.Errorf("cron: duplicate job name %q", job.Name)
	}

	created, err := s.cronStore.Create(storage.CronJob{
		Name:        job.Name,
		Schedule:    job.Schedule,
		Trigger:     job.Trigger,
		Prompt:      job.Prompt,
		Channel:     job.Channel,
		SessionMode: job.SessionMode,
		TimeoutSec:  job.TimeoutSec,
		Enabled:     job.Enabled,
	})
	if err != nil {
		return JobStatus{}, err
	}

	job.ID = created.ID
	job.Enabled = created.Enabled
	if err := s.addOrReplaceJob(job, false); err != nil {
		_ = s.cronStore.DeleteByName(job.Name)
		return JobStatus{}, err
	}
	return s.JobStatusByName(job.Name)
}

// UpdatePersistent updates one DB-backed cron job and hot-reloads its schedule.
func (s *Scheduler) UpdatePersistent(name string, patch Job) (JobStatus, error) {
	if s.cronStore == nil {
		return JobStatus{}, fmt.Errorf("cron: persistent job store is unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return JobStatus{}, fmt.Errorf("cron: job name is required")
	}

	if runtimeJob, ok := s.runtimeJob(name); ok && runtimeJob.Source == "config" {
		return JobStatus{}, fmt.Errorf("cron: config job %q is read-only via API", name)
	}

	current, err := s.cronStore.GetByName(name)
	if err != nil {
		if err == storage.ErrNotFound {
			return JobStatus{}, fmt.Errorf("cron: job %q not found", name)
		}
		return JobStatus{}, err
	}

	next := Job{
		ID:          current.ID,
		Name:        current.Name,
		Schedule:    strings.TrimSpace(patch.Schedule),
		Trigger:     strings.TrimSpace(patch.Trigger),
		Prompt:      strings.TrimSpace(patch.Prompt),
		Channel:     strings.TrimSpace(patch.Channel),
		SessionMode: strings.TrimSpace(patch.SessionMode),
		TimeoutSec:  patch.TimeoutSec,
		Enabled:     current.Enabled,
		Source:      "db",
	}
	explicitSchedule := strings.TrimSpace(patch.Schedule) != ""
	explicitTrigger := strings.TrimSpace(patch.Trigger) != ""

	if next.Schedule == "" {
		next.Schedule = current.Schedule
	}
	if next.Trigger == "" {
		next.Trigger = current.Trigger
	}
	if explicitSchedule {
		next.Trigger = ""
	}
	if explicitTrigger {
		next.Schedule = ""
	}
	if next.Prompt == "" {
		next.Prompt = current.Prompt
	}
	if next.Channel == "" {
		next.Channel = current.Channel
	}
	if next.SessionMode == "" {
		next.SessionMode = current.SessionMode
	}
	if next.TimeoutSec <= 0 {
		next.TimeoutSec = current.TimeoutSec
	}
	if err := validateJobSpec(next.Name, next.Schedule, next.Trigger, next.Prompt, next.SessionMode, next.TimeoutSec, s.parser); err != nil {
		return JobStatus{}, err
	}

	if err := s.cronStore.UpdateByName(name, storage.CronJob{
		Schedule:    next.Schedule,
		Trigger:     next.Trigger,
		Prompt:      next.Prompt,
		Channel:     next.Channel,
		SessionMode: next.SessionMode,
		TimeoutSec:  next.TimeoutSec,
	}); err != nil {
		if err == storage.ErrNotFound {
			return JobStatus{}, fmt.Errorf("cron: job %q not found", name)
		}
		return JobStatus{}, err
	}
	if err := s.addOrReplaceJob(next, true); err != nil {
		return JobStatus{}, err
	}
	return s.JobStatusByName(name)
}

// DeletePersistent deletes one DB-backed cron job and unregisters it from runtime.
func (s *Scheduler) DeletePersistent(name string) error {
	if s.cronStore == nil {
		return fmt.Errorf("cron: persistent job store is unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("cron: job name is required")
	}
	if runtimeJob, ok := s.runtimeJob(name); ok && runtimeJob.Source == "config" {
		return fmt.Errorf("cron: config job %q is read-only via API", name)
	}
	if err := s.cronStore.DeleteByName(name); err != nil {
		if err == storage.ErrNotFound {
			return fmt.Errorf("cron: job %q not found", name)
		}
		return err
	}
	_ = s.removeJob(name)
	return nil
}

// SetPersistentEnabled toggles one DB-backed cron job and hot-reloads runtime schedule.
func (s *Scheduler) SetPersistentEnabled(name string, enabled bool) (JobStatus, error) {
	if s.cronStore == nil {
		return JobStatus{}, fmt.Errorf("cron: persistent job store is unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return JobStatus{}, fmt.Errorf("cron: job name is required")
	}
	if runtimeJob, ok := s.runtimeJob(name); ok && runtimeJob.Source == "config" {
		return JobStatus{}, fmt.Errorf("cron: config job %q is read-only via API", name)
	}
	if err := s.cronStore.SetEnabled(name, enabled); err != nil {
		if err == storage.ErrNotFound {
			return JobStatus{}, fmt.Errorf("cron: job %q not found", name)
		}
		return JobStatus{}, err
	}
	rec, err := s.cronStore.GetByName(name)
	if err != nil {
		return JobStatus{}, err
	}
	job := Job{
		ID:          rec.ID,
		Name:        rec.Name,
		Schedule:    rec.Schedule,
		Trigger:     rec.Trigger,
		Prompt:      rec.Prompt,
		Channel:     rec.Channel,
		SessionMode: rec.SessionMode,
		TimeoutSec:  rec.TimeoutSec,
		Enabled:     rec.Enabled,
		Source:      "db",
	}
	if err := s.addOrReplaceJob(job, true); err != nil {
		return JobStatus{}, err
	}
	return s.JobStatusByName(name)
}

// Start begins the scheduler (non-blocking).
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.triggerCtx, s.triggerStop = context.WithCancel(context.Background())
	for _, job := range s.jobs {
		if job.Enabled {
			s.startTriggerLoopLocked(job)
		}
	}
	jobCount := len(s.jobs)
	s.mu.Unlock()

	s.cron.Start()
	if s.logger != nil {
		s.logger.Info("cron scheduler started", "jobs", jobCount)
	}
}

// Stop gracefully stops the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.triggerStop != nil {
		s.triggerStop()
		s.triggerStop = nil
	}
	for name := range s.triggerCancel {
		delete(s.triggerCancel, name)
	}
	s.started = false
	s.mu.Unlock()

	ctx := s.cron.Stop()
	<-ctx.Done()
	if s.logger != nil {
		s.logger.Info("cron scheduler stopped")
	}
}

// ListJobs returns status for all registered jobs including next run time.
func (s *Scheduler) ListJobs() []JobStatus {
	s.mu.RLock()
	jobs := make([]Job, len(s.jobs))
	copy(jobs, s.jobs)
	s.mu.RUnlock()

	lastRuns := s.latestRunsByJob()
	var out []JobStatus
	for _, job := range jobs {
		nextRun := time.Time{}
		if job.Enabled {
			if strings.TrimSpace(job.Trigger) != "" {
				if spec, err := parseTrigger(job.Trigger); err == nil {
					if spec.Kind == "interval" {
						if last := lastRuns[job.Name]; !last.IsZero() {
							nextRun = last.Add(spec.Interval)
						} else {
							nextRun = time.Now().Add(spec.Interval)
						}
					}
				}
			} else if schedule, err := s.parser.Parse(job.Schedule); err == nil {
				nextRun = schedule.Next(time.Now())
			}
		}
		mode := strings.ToLower(strings.TrimSpace(job.SessionMode))
		if mode == "" {
			mode = "isolated"
		}
		source := strings.TrimSpace(job.Source)
		if source == "" {
			source = "config"
		}
		out = append(out, JobStatus{
			ID:          job.ID,
			Name:        job.Name,
			Schedule:    job.Schedule,
			Trigger:     job.Trigger,
			Channel:     job.Channel,
			LastRun:     lastRuns[job.Name],
			NextRun:     nextRun,
			SessionMode: mode,
			TimeoutSec:  job.TimeoutSec,
			Enabled:     job.Enabled,
			Source:      source,
		})
	}
	return out
}

// RunNow triggers a named job immediately, regardless of its schedule.
func (s *Scheduler) RunNow(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("cron: job name is required")
	}
	job, ok := s.runtimeJob(name)
	if !ok {
		return fmt.Errorf("cron: job %q not found", name)
	}
	s.runJob(job)
	return nil
}

// History returns the last N runs from the cron_history table.
func (s *Scheduler) History(limit int) ([]HistoryEntry, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Conn().Query(`
		SELECT job_name, ran_at, duration_ms, success, output
		FROM cron_history
		ORDER BY ran_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var successInt int
		var ranAtStr string
		if err := rows.Scan(&e.JobName, &ranAtStr, &e.DurationMs, &successInt, &e.Output); err != nil {
			continue
		}
		e.Success = successInt == 1
		if parsed, parseErr := time.Parse("2006-01-02 15:04:05", ranAtStr); parseErr == nil {
			e.RanAt = parsed
		} else if parsed, parseErr := time.Parse(time.RFC3339, ranAtStr); parseErr == nil {
			e.RanAt = parsed
		} else if s.logger != nil {
			s.logger.Warn("cron: failed to parse history timestamp", "ran_at", ranAtStr)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (s *Scheduler) addOrReplaceJob(job Job, replace bool) error {
	job.Name = strings.TrimSpace(job.Name)
	job.Schedule = strings.TrimSpace(job.Schedule)
	job.Trigger = strings.TrimSpace(job.Trigger)
	job.Prompt = strings.TrimSpace(job.Prompt)
	job.Channel = strings.TrimSpace(job.Channel)
	job.SessionMode = strings.ToLower(strings.TrimSpace(job.SessionMode))
	if job.SessionMode == "" {
		job.SessionMode = "isolated"
	}
	if job.TimeoutSec <= 0 {
		job.TimeoutSec = int(defaultJobTimeout / time.Second)
	}
	if strings.TrimSpace(job.Source) == "" {
		job.Source = "config"
	}
	if err := validateJobSpec(job.Name, job.Schedule, job.Trigger, job.Prompt, job.SessionMode, job.TimeoutSec, s.parser); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx, existing, exists := s.findJobLocked(job.Name)
	if exists && !replace {
		return fmt.Errorf("duplicate cron job name %q", job.Name)
	}

	if exists {
		if entryID, ok := s.entryIDs[existing.Name]; ok {
			s.cron.Remove(entryID)
			delete(s.entryIDs, existing.Name)
		}
		if cancel, ok := s.triggerCancel[existing.Name]; ok {
			cancel()
			delete(s.triggerCancel, existing.Name)
		}
	}

	if job.Enabled {
		if job.Trigger != "" {
			s.startTriggerLoopLocked(job)
		} else {
			runtimeJob := job
			entryID, err := s.cron.AddFunc(job.Schedule, func() {
				s.runJob(runtimeJob)
			})
			if err != nil {
				return fmt.Errorf("invalid schedule %q for job %q: %w", job.Schedule, job.Name, err)
			}
			s.entryIDs[job.Name] = entryID
		}
	}

	if exists {
		s.jobs[idx] = job
	} else {
		s.jobs = append(s.jobs, job)
	}

	if s.logger != nil {
		s.logger.Info("cron job registered",
			"name", job.Name,
			"schedule", job.Schedule,
			"trigger", job.Trigger,
			"enabled", job.Enabled,
			"source", job.Source)
	}
	return nil
}

func (s *Scheduler) removeJob(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("cron: job name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx, _, ok := s.findJobLocked(name)
	if !ok {
		return fmt.Errorf("cron: job %q not found", name)
	}
	if entryID, hasEntry := s.entryIDs[name]; hasEntry {
		s.cron.Remove(entryID)
		delete(s.entryIDs, name)
	}
	if cancel, hasWatch := s.triggerCancel[name]; hasWatch {
		cancel()
		delete(s.triggerCancel, name)
	}
	s.jobs = append(s.jobs[:idx], s.jobs[idx+1:]...)
	return nil
}

func (s *Scheduler) runtimeJob(name string) (Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, job, ok := s.findJobLocked(name)
	return job, ok
}

func (s *Scheduler) findJobLocked(name string) (int, Job, bool) {
	for i := range s.jobs {
		if s.jobs[i].Name == name {
			return i, s.jobs[i], true
		}
	}
	return -1, Job{}, false
}

func validateJobSpec(name, schedule, trigger, prompt, sessionMode string, timeoutSec int, parser robfig.Parser) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("cron job name cannot be empty")
	}
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("cron job %q prompt cannot be empty", name)
	}
	mode := strings.ToLower(strings.TrimSpace(sessionMode))
	if mode == "" {
		mode = "isolated"
	}
	if mode != "isolated" && mode != "shared" {
		return fmt.Errorf("cron job %q has invalid session_mode %q (valid: isolated, shared)", name, sessionMode)
	}
	if timeoutSec < 0 {
		return fmt.Errorf("cron job %q timeout_seconds cannot be negative", name)
	}
	schedule = strings.TrimSpace(schedule)
	trigger = strings.TrimSpace(trigger)
	if schedule == "" && trigger == "" {
		return fmt.Errorf("cron job %q must define either schedule or trigger", name)
	}
	if schedule != "" && trigger != "" {
		return fmt.Errorf("cron job %q cannot define both schedule and trigger", name)
	}
	if schedule != "" {
		if _, err := parser.Parse(schedule); err != nil {
			return fmt.Errorf("invalid schedule %q for job %q: %w", schedule, name, err)
		}
	}
	if trigger != "" {
		if _, err := parseTrigger(trigger); err != nil {
			return fmt.Errorf("invalid trigger %q for job %q: %w", trigger, name, err)
		}
	}
	return nil
}

type triggerSpec struct {
	Kind      string
	Interval  time.Duration
	FilePath  string
	PollEvery time.Duration
}

func parseTrigger(raw string) (triggerSpec, error) {
	trigger := strings.TrimSpace(raw)
	if trigger == "" {
		return triggerSpec{}, fmt.Errorf("trigger cannot be empty")
	}

	lower := strings.ToLower(trigger)
	if strings.HasPrefix(lower, "every ") {
		intervalText := strings.TrimSpace(trigger[len("every "):])
		interval, err := parseHumanDuration(intervalText)
		if err != nil {
			return triggerSpec{}, fmt.Errorf("parsing interval: %w", err)
		}
		poll := defaultTriggerPollInterval
		if interval < poll {
			poll = interval
		}
		if poll < time.Second {
			poll = time.Second
		}
		return triggerSpec{
			Kind:      "interval",
			Interval:  interval,
			PollEvery: poll,
		}, nil
	}

	if strings.HasPrefix(lower, "file_changed ") {
		path := strings.TrimSpace(trigger[len("file_changed "):])
		if path == "" {
			return triggerSpec{}, fmt.Errorf("file_changed trigger requires a file path")
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return triggerSpec{}, fmt.Errorf("resolving file path: %w", err)
		}
		return triggerSpec{
			Kind:      "file_changed",
			FilePath:  absPath,
			PollEvery: 5 * time.Second,
		}, nil
	}

	return triggerSpec{}, fmt.Errorf("unsupported trigger format")
}

func parseHumanDuration(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return 0, fmt.Errorf("duration cannot be empty")
	}
	if d, err := time.ParseDuration(trimmed); err == nil && d > 0 {
		return d, nil
	}

	parts := strings.Fields(trimmed)
	if len(parts) != 2 {
		return 0, fmt.Errorf("expected format '<number> <unit>'")
	}

	n, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid duration number %q", parts[0])
	}
	var unit time.Duration
	switch parts[1] {
	case "second", "seconds", "sec", "secs":
		unit = time.Second
	case "minute", "minutes", "min", "mins":
		unit = time.Minute
	case "hour", "hours", "hr", "hrs":
		unit = time.Hour
	case "day", "days":
		unit = 24 * time.Hour
	default:
		return 0, fmt.Errorf("unsupported duration unit %q", parts[1])
	}
	return time.Duration(n * float64(unit)), nil
}

func (s *Scheduler) startTriggerLoopLocked(job Job) {
	if strings.TrimSpace(job.Trigger) == "" {
		return
	}
	if !s.started {
		return
	}
	spec, err := parseTrigger(job.Trigger)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("invalid trigger definition; trigger loop not started", "name", job.Name, "trigger", job.Trigger, "error", err)
		}
		return
	}
	if cancel, exists := s.triggerCancel[job.Name]; exists {
		cancel()
		delete(s.triggerCancel, job.Name)
	}

	baseCtx := s.triggerCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(baseCtx)
	s.triggerCancel[job.Name] = cancel

	go s.runTriggerLoop(ctx, job, spec)
}

func (s *Scheduler) runTriggerLoop(ctx context.Context, job Job, spec triggerSpec) {
	ticker := time.NewTicker(spec.PollEvery)
	defer ticker.Stop()

	switch spec.Kind {
	case "interval":
		nextRun := time.Now().Add(spec.Interval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Now().Before(nextRun) {
					continue
				}
				s.runJob(job)
				nextRun = time.Now().Add(spec.Interval)
			}
		}

	case "file_changed":
		var lastMod time.Time
		initialized := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(spec.FilePath)
				if err != nil {
					continue
				}
				mod := info.ModTime()
				if !initialized {
					lastMod = mod
					initialized = true
					continue
				}
				if mod.After(lastMod) {
					lastMod = mod
					s.runJob(job)
				}
			}
		}
	}
}

func (s *Scheduler) markJobRunning(name string) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if _, exists := s.running[name]; exists {
		return false
	}
	s.running[name] = struct{}{}
	return true
}

func (s *Scheduler) clearJobRunning(name string) {
	s.runMu.Lock()
	delete(s.running, name)
	s.runMu.Unlock()
}

func (s *Scheduler) jobTimeout(job Job) time.Duration {
	if job.TimeoutSec > 0 {
		return time.Duration(job.TimeoutSec) * time.Second
	}
	return defaultJobTimeout
}

func (s *Scheduler) runJob(job Job) {
	if s.logger != nil {
		s.logger.Info("running cron job", "name", job.Name)
	}
	if !s.markJobRunning(job.Name) {
		if s.logger != nil {
			s.logger.Warn("cron job already running, skipping", "name", job.Name)
		}
		return
	}
	defer s.clearJobRunning(job.Name)

	start := time.Now()
	success := true
	output := ""

	if s.sessions == nil || s.messages == nil || s.agentInstance == nil {
		if s.logger != nil {
			s.logger.Warn("cron scheduler missing runtime dependencies, skipping job", "name", job.Name)
		}
		return
	}

	session, err := s.selectSession(job)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("cron: failed to prepare session", "job", job.Name, "error", err)
		}
		return
	}

	tokens := agentctx.EstimateTokens(job.Prompt)
	s.messages.Insert(session.ID, "user", job.Prompt, tokens)

	msgProvider := &cronMsgProvider{messages: s.messages, sessionID: session.ID}

	ctx, cancel := context.WithTimeout(context.Background(), s.jobTimeout(job))
	defer cancel()

	resp, err := s.agentInstance.Run(ctx, session.ID, job.Prompt, msgProvider, nil)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		success = false
		output = err.Error()
		if s.logger != nil {
			s.logger.Error("cron: agent error", "job", job.Name, "error", err)
		}
	} else {
		output = resp.Text
		if len(output) > 500 {
			output = output[:500] + "... [truncated]"
		}
		if s.logger != nil {
			s.logger.Info("cron job complete", "name", job.Name, "duration_ms", durationMs)
		}
		respTokens := agentctx.EstimateTokens(resp.Text)
		s.messages.Insert(session.ID, "assistant", resp.Text, respTokens)

		if job.Channel != "" && s.manager != nil {
			s.manager.Send(job.Channel, plugin.OutboundMessage{
				ChatID: "cron:" + job.Name,
				UserID: "cron",
				Text:   fmt.Sprintf("📅 *%s*\n\n%s", job.Name, resp.Text),
			})
		}
	}

	if s.db != nil {
		successInt := 1
		if !success {
			successInt = 0
		}
		s.db.Conn().Exec(
			`INSERT INTO cron_history (job_name, duration_ms, success, output) VALUES (?, ?, ?, ?)`,
			job.Name, durationMs, successInt, output,
		)
	}
}

func (s *Scheduler) selectSession(job Job) (*storage.Session, error) {
	mode := strings.ToLower(strings.TrimSpace(job.SessionMode))
	if mode == "" {
		mode = "isolated"
	}

	channel := "cron:" + job.Name
	if mode == "shared" {
		existing, err := s.sessions.GetByChannelSender(channel, "cron")
		if err == nil {
			return existing, nil
		}
		if err != storage.ErrNotFound {
			return nil, err
		}
	}
	return s.sessions.Create(channel, "cron")
}

func (s *Scheduler) latestRunsByJob() map[string]time.Time {
	out := make(map[string]time.Time)
	if s.db == nil {
		return out
	}
	rows, err := s.db.Conn().Query(`
		SELECT job_name, MAX(ran_at) AS last_ran
		FROM cron_history
		GROUP BY job_name
	`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var jobName string
		var ranAtStr string
		if err := rows.Scan(&jobName, &ranAtStr); err != nil {
			continue
		}
		if tm, err := time.Parse("2006-01-02 15:04:05", ranAtStr); err == nil {
			out[jobName] = tm
			continue
		}
		if tm, err := time.Parse(time.RFC3339, ranAtStr); err == nil {
			out[jobName] = tm
			continue
		}
		if s.logger != nil {
			s.logger.Warn("cron: failed to parse latest run timestamp", "ran_at", ranAtStr)
		}
		out[jobName] = time.Time{}
	}
	return out
}

type cronMsgProvider struct {
	messages  *storage.MessageStore
	sessionID string
}

func (p *cronMsgProvider) GetRecentMessages(sessionID string, limit int) ([]agentctx.ContextMessage, error) {
	msgs, err := p.messages.GetRecent(sessionID, limit)
	if err != nil {
		return nil, err
	}
	var result []agentctx.ContextMessage
	for _, m := range msgs {
		result = append(result, agentctx.ContextMessage{Role: m.Role, Content: m.Content})
	}
	return result, nil
}

func (p *cronMsgProvider) GetStoredEmbeddings(sessionID string) ([]agentctx.StoredEmbedding, error) {
	msgs, err := p.messages.GetEmbeddings(sessionID)
	if err != nil {
		return nil, err
	}
	var result []agentctx.StoredEmbedding
	for _, m := range msgs {
		result = append(result, agentctx.StoredEmbedding{
			MessageID: m.ID, SessionID: m.SessionID,
			Role: m.Role, Content: m.Content, Summary: m.Summary,
			Tokens: m.Tokens, Embedding: m.Embedding,
		})
	}
	return result, nil
}

func (p *cronMsgProvider) SearchKnowledge(query, nodeType string, limit int) ([]agentctx.KnowledgeNode, error) {
	nodes, err := p.messages.SearchKnowledge(query, nodeType, limit)
	if err != nil {
		return nil, err
	}
	out := make([]agentctx.KnowledgeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, agentctx.KnowledgeNode{
			ID:         n.ID,
			Type:       n.Type,
			Name:       n.Name,
			Confidence: n.Confidence,
		})
	}
	return out, nil
}

func (p *cronMsgProvider) GetOldMessages(sessionID string, keepRecentTurns int) ([]agent.CompactionMessage, error) {
	msgs, err := p.messages.GetOldMessages(sessionID, keepRecentTurns)
	if err != nil {
		return nil, err
	}
	result := make([]agent.CompactionMessage, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, agent.CompactionMessage{
			ID:      m.ID,
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return result, nil
}

func (p *cronMsgProvider) ArchiveMessages(sessionID string, olderThanID int64) (int64, error) {
	return p.messages.ArchiveMessages(sessionID, olderThanID)
}

func (p *cronMsgProvider) InsertCompactionSummary(sessionID, content string, tokens int) error {
	_, err := p.messages.Insert(sessionID, "system", content, tokens)
	return err
}
