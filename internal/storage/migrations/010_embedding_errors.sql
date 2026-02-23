CREATE TABLE IF NOT EXISTS embedding_errors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    error TEXT NOT NULL,
    count INTEGER NOT NULL DEFAULT 1,
    first_seen DATETIME NOT NULL DEFAULT (datetime('now')),
    last_seen DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source, error)
);

CREATE INDEX IF NOT EXISTS idx_embedding_errors_source ON embedding_errors(source);
CREATE INDEX IF NOT EXISTS idx_embedding_errors_last_seen ON embedding_errors(last_seen DESC);
