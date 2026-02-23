-- Phase 2: stability/performance hardening.
CREATE INDEX IF NOT EXISTS idx_messages_session_created ON messages(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_session ON token_usage(session_id);
CREATE INDEX IF NOT EXISTS idx_cron_history_job_ran_at ON cron_history(job_name, ran_at DESC);

ALTER TABLE cron_jobs ADD COLUMN timeout_seconds INTEGER NOT NULL DEFAULT 300;
