CREATE TABLE IF NOT EXISTS cron_history (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_name    TEXT NOT NULL,
    ran_at      DATETIME NOT NULL DEFAULT (datetime('now')),
    duration_ms INTEGER NOT NULL DEFAULT 0,
    success     INTEGER NOT NULL DEFAULT 1,
    output      TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_cron_history_job ON cron_history(job_name);
CREATE INDEX IF NOT EXISTS idx_cron_history_ran ON cron_history(ran_at DESC);
