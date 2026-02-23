package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/openclio/openclio/internal/kg"
)

// KGNode is one stored knowledge graph node.
type KGNode struct {
	ID             int64     `json:"id"`
	Type           string    `json:"type"`
	Name           string    `json:"name"`
	NormalizedName string    `json:"normalized_name"`
	Confidence     float64   `json:"confidence"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// KGEdge is one stored knowledge graph edge.
type KGEdge struct {
	ID              int64     `json:"id"`
	FromNodeID      int64     `json:"from_node_id"`
	FromNodeType    string    `json:"from_node_type,omitempty"`
	FromNodeName    string    `json:"from_node_name,omitempty"`
	Relation        string    `json:"relation"`
	ToNodeID        int64     `json:"to_node_id"`
	ToNodeType      string    `json:"to_node_type,omitempty"`
	ToNodeName      string    `json:"to_node_name,omitempty"`
	SourceMessageID int64     `json:"source_message_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// KnowledgeGraphStore persists nodes and edges extracted from conversation turns.
type KnowledgeGraphStore struct {
	db *sql.DB
}

// NewKnowledgeGraphStore creates a new KnowledgeGraphStore.
func NewKnowledgeGraphStore(db *DB) *KnowledgeGraphStore {
	return &KnowledgeGraphStore{db: db.Conn()}
}

// IngestExtracted inserts/upserts extracted entities and relations for one message.
func (s *KnowledgeGraphStore) IngestExtracted(messageID int64, entities []kg.Entity, relations []kg.Relation) error {
	if len(entities) == 0 && len(relations) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin knowledge graph ingest transaction: %w", err)
	}
	defer tx.Rollback()

	nodeIDs := make(map[string]int64, len(entities))
	for _, entity := range entities {
		id, err := upsertNodeTx(tx, entity.Type, entity.Name, entity.Confidence)
		if err != nil {
			return err
		}
		nodeIDs[normalizeEntityKey(entity.Name)] = id
	}

	for _, rel := range relations {
		fromKey := normalizeEntityKey(rel.From)
		toKey := normalizeEntityKey(rel.To)

		fromID, ok := nodeIDs[fromKey]
		if !ok {
			id, err := upsertNodeTx(tx, "entity", rel.From, 0.5)
			if err != nil {
				return err
			}
			fromID = id
			nodeIDs[fromKey] = id
		}

		toID, ok := nodeIDs[toKey]
		if !ok {
			id, err := upsertNodeTx(tx, "entity", rel.To, 0.5)
			if err != nil {
				return err
			}
			toID = id
			nodeIDs[toKey] = id
		}

		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO kg_edges (from_node_id, relation, to_node_id, source_message_id)
			 VALUES (?, ?, ?, ?)`,
			fromID,
			strings.TrimSpace(rel.Relation),
			toID,
			messageID,
		); err != nil {
			return fmt.Errorf("inserting knowledge graph edge: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge graph ingest transaction: %w", err)
	}
	return nil
}

// ListNodes returns the latest knowledge graph nodes.
func (s *KnowledgeGraphStore) ListNodes(limit int) ([]KGNode, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, type, name, normalized_name, confidence, created_at, updated_at
		 FROM kg_nodes
		 ORDER BY updated_at DESC, id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing knowledge graph nodes: %w", err)
	}
	defer rows.Close()

	nodes := make([]KGNode, 0, limit)
	for rows.Next() {
		var node KGNode
		if err := rows.Scan(
			&node.ID,
			&node.Type,
			&node.Name,
			&node.NormalizedName,
			&node.Confidence,
			&node.CreatedAt,
			&node.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning knowledge graph node: %w", err)
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

// ListEdges returns the latest knowledge graph edges.
func (s *KnowledgeGraphStore) ListEdges(limit int) ([]KGEdge, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT e.id, e.from_node_id, e.relation, e.to_node_id, e.source_message_id, e.created_at,
		        fn.type, fn.name, tn.type, tn.name
		 FROM kg_edges e
		 JOIN kg_nodes fn ON fn.id = e.from_node_id
		 JOIN kg_nodes tn ON tn.id = e.to_node_id
		 ORDER BY e.id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing knowledge graph edges: %w", err)
	}
	defer rows.Close()

	edges := make([]KGEdge, 0, limit)
	for rows.Next() {
		var edge KGEdge
		if err := rows.Scan(
			&edge.ID,
			&edge.FromNodeID,
			&edge.Relation,
			&edge.ToNodeID,
			&edge.SourceMessageID,
			&edge.CreatedAt,
			&edge.FromNodeType,
			&edge.FromNodeName,
			&edge.ToNodeType,
			&edge.ToNodeName,
		); err != nil {
			return nil, fmt.Errorf("scanning knowledge graph edge: %w", err)
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

// SearchNodes finds nodes by entity name and/or type.
func (s *KnowledgeGraphStore) SearchNodes(query, nodeType string, limit int) ([]KGNode, error) {
	if limit <= 0 {
		limit = 100
	}
	query = strings.TrimSpace(strings.ToLower(query))
	nodeType = strings.TrimSpace(strings.ToLower(nodeType))

	sqlStmt := `SELECT id, type, name, normalized_name, confidence, created_at, updated_at
		FROM kg_nodes`
	conds := make([]string, 0, 2)
	args := make([]any, 0, 3)

	if query != "" {
		pattern := "%" + query + "%"
		conds = append(conds, "(normalized_name LIKE ? OR LOWER(name) LIKE ?)")
		args = append(args, pattern, pattern)
	}
	if nodeType != "" {
		conds = append(conds, "LOWER(type) = ?")
		args = append(args, nodeType)
	}
	if len(conds) > 0 {
		sqlStmt += " WHERE " + strings.Join(conds, " AND ")
	}
	sqlStmt += " ORDER BY confidence DESC, updated_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(sqlStmt, args...)
	if err != nil {
		return nil, fmt.Errorf("searching knowledge graph nodes: %w", err)
	}
	defer rows.Close()

	nodes := make([]KGNode, 0, limit)
	for rows.Next() {
		var node KGNode
		if err := rows.Scan(
			&node.ID,
			&node.Type,
			&node.Name,
			&node.NormalizedName,
			&node.Confidence,
			&node.CreatedAt,
			&node.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning knowledge graph search node: %w", err)
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

// UpdateNode updates one existing knowledge graph node.
func (s *KnowledgeGraphStore) UpdateNode(id int64, kind, name string, confidence float64) error {
	if id <= 0 {
		return fmt.Errorf("invalid node id")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("entity name cannot be empty")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "entity"
	}
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	result, err := ExecWithRetry(
		s.db,
		`UPDATE kg_nodes
		 SET type = ?, name = ?, normalized_name = ?, confidence = ?, updated_at = datetime('now')
		 WHERE id = ?`,
		kind,
		name,
		normalizeEntityKey(name),
		confidence,
		id,
	)
	if err != nil {
		return fmt.Errorf("updating knowledge graph node: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteNode removes one knowledge graph node by ID.
func (s *KnowledgeGraphStore) DeleteNode(id int64) error {
	if id <= 0 {
		return fmt.Errorf("invalid node id")
	}
	result, err := ExecWithRetry(s.db, "DELETE FROM kg_nodes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting knowledge graph node: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func upsertNodeTx(tx *sql.Tx, kind, name string, confidence float64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("entity name cannot be empty")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "entity"
	}
	key := normalizeEntityKey(name)

	if _, err := tx.Exec(
		`INSERT INTO kg_nodes (type, name, normalized_name, confidence, created_at, updated_at)
		 VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(normalized_name) DO UPDATE SET
		   type = excluded.type,
		   name = excluded.name,
		   confidence = CASE
		     WHEN excluded.confidence > kg_nodes.confidence THEN excluded.confidence
		     ELSE kg_nodes.confidence
		   END,
		   updated_at = datetime('now')`,
		kind, name, key, confidence,
	); err != nil {
		return 0, fmt.Errorf("upserting knowledge graph node: %w", err)
	}

	var id int64
	if err := tx.QueryRow(`SELECT id FROM kg_nodes WHERE normalized_name = ?`, key).Scan(&id); err != nil {
		return 0, fmt.Errorf("loading node id for %q: %w", name, err)
	}
	return id, nil
}

func normalizeEntityKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
