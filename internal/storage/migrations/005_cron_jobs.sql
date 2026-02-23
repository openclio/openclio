CREATE TABLE IF NOT EXISTS cron_jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    schedule     TEXT NOT NULL,
    prompt       TEXT NOT NULL,
    channel      TEXT NOT NULL DEFAULT '',
    session_mode TEXT NOT NULL DEFAULT 'isolated',
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at   DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_cron_jobs_enabled ON cron_jobs(enabled);
CREATE INDEX IF NOT EXISTS idx_cron_jobs_updated_at ON cron_jobs(updated_at DESC);
