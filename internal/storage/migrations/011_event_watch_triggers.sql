ALTER TABLE cron_jobs ADD COLUMN trigger TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_cron_jobs_trigger ON cron_jobs(trigger);
