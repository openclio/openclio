CREATE TABLE IF NOT EXISTS agent_profiles (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    description   TEXT NOT NULL DEFAULT '',
    provider      TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    is_active     INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at    DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_agent_profiles_active ON agent_profiles(is_active);
CREATE INDEX IF NOT EXISTS idx_agent_profiles_updated_at ON agent_profiles(updated_at DESC);

ALTER TABLE sessions ADD COLUMN agent_profile_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_sessions_agent_profile_id ON sessions(agent_profile_id);
