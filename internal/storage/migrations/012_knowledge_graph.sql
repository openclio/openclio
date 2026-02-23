CREATE TABLE IF NOT EXISTS kg_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL UNIQUE,
    confidence REAL NOT NULL DEFAULT 0.5,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_kg_nodes_type ON kg_nodes(type);
CREATE INDEX IF NOT EXISTS idx_kg_nodes_updated_at ON kg_nodes(updated_at DESC);

CREATE TABLE IF NOT EXISTS kg_edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_node_id INTEGER NOT NULL REFERENCES kg_nodes(id) ON DELETE CASCADE,
    relation TEXT NOT NULL,
    to_node_id INTEGER NOT NULL REFERENCES kg_nodes(id) ON DELETE CASCADE,
    source_message_id INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(from_node_id, relation, to_node_id)
);

CREATE INDEX IF NOT EXISTS idx_kg_edges_from ON kg_edges(from_node_id);
CREATE INDEX IF NOT EXISTS idx_kg_edges_to ON kg_edges(to_node_id);
CREATE INDEX IF NOT EXISTS idx_kg_edges_relation ON kg_edges(relation);
