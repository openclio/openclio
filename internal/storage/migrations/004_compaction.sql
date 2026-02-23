ALTER TABLE messages ADD COLUMN compacted INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_messages_session_compacted_created
ON messages(session_id, compacted, created_at);
