CREATE TABLE IF NOT EXISTS action_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tool_name TEXT NOT NULL,
    target_path TEXT,
    before_exists INTEGER NOT NULL DEFAULT 0,
    before_content TEXT,
    after_content TEXT,
    command TEXT,
    work_dir TEXT,
    output TEXT,
    success INTEGER NOT NULL DEFAULT 1,
    error TEXT,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_action_log_tool_name ON action_log(tool_name);
CREATE INDEX IF NOT EXISTS idx_action_log_created_at ON action_log(created_at DESC);
