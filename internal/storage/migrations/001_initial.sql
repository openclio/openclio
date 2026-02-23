-- Agent initial schema
-- Creates core tables for sessions, messages, memories, and tool results.

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    channel     TEXT NOT NULL DEFAULT 'cli',
    sender_id   TEXT NOT NULL DEFAULT 'local',
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    last_active DATETIME NOT NULL DEFAULT (datetime('now')),
    metadata    TEXT DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_sessions_channel ON sessions(channel);
CREATE INDEX IF NOT EXISTS idx_sessions_last_active ON sessions(last_active);

CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool_result', 'system')),
    content     TEXT NOT NULL,
    summary     TEXT,
    embedding   BLOB,
    tokens      INTEGER DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
CREATE INDEX IF NOT EXISTS idx_messages_session_created ON messages(session_id, created_at);

CREATE TABLE IF NOT EXISTS memories (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    key            TEXT NOT NULL,
    value          TEXT NOT NULL,
    embedding      BLOB,
    source_session TEXT,
    created_at     DATETIME NOT NULL DEFAULT (datetime('now')),
    accessed_at    DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_memories_key ON memories(key);

CREATE TABLE IF NOT EXISTS tool_results (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id     INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    tool_name      TEXT NOT NULL,
    full_result    TEXT,
    summary        TEXT,
    tokens_full    INTEGER DEFAULT 0,
    tokens_summary INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_tool_results_message ON tool_results(message_id);

-- Migration tracking
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
