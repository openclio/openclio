CREATE TABLE IF NOT EXISTS privacy_redactions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    category   TEXT NOT NULL,
    count      INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_privacy_redactions_category ON privacy_redactions(category);
CREATE INDEX IF NOT EXISTS idx_privacy_redactions_created_at ON privacy_redactions(created_at DESC);
