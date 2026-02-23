package tools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/openclio/openclio/internal/storage"
)

func init() {
	_ = ReplaceTool("kg_search", kgSearchTool)
	_ = ReplaceTool("kg_add_node", kgAddNodeTool)
	_ = ReplaceTool("kg_add_edge", kgAddEdgeTool)
	_ = ReplaceTool("kg_get_node", kgGetNodeTool)
	_ = ReplaceTool("kg_delete_node", kgDeleteNodeTool)
}

func normalizeKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func openKGStore() (*storage.DB, *storage.KnowledgeGraphStore, error) {
	dbPath, err := dbPath()
	if err != nil {
		return nil, nil, err
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	store := storage.NewKnowledgeGraphStore(db)
	return db, store, nil
}

func kgSearchTool(ctx context.Context, payload map[string]any) (any, error) {
	q, _ := payload["query"].(string)
	nt, _ := payload["type"].(string)
	limit := 100
	if l, ok := payload["limit"]; ok {
		switch v := l.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		}
	}
	db, store, err := openKGStore()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	nodes, err := store.SearchNodes(q, nt, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, map[string]any{
			"id":              n.ID,
			"type":            n.Type,
			"name":            n.Name,
			"normalized_name": n.NormalizedName,
			"confidence":      n.Confidence,
			"created_at":      n.CreatedAt,
			"updated_at":      n.UpdatedAt,
		})
	}
	return out, nil
}

func kgAddNodeTool(ctx context.Context, payload map[string]any) (any, error) {
	kind, _ := payload["type"].(string)
	nameI, ok := payload["name"]
	if !ok {
		return nil, fmt.Errorf("name is required")
	}
	name, _ := nameI.(string)
	conf := 0.5
	if c, ok := payload["confidence"]; ok {
		switch v := c.(type) {
		case float64:
			conf = v
		case int:
			conf = float64(v)
		}
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("name cannot be empty")
	}

	db, _, err := openKGStore()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Use same upsert SQL as storage.upsertNodeTx
	key := normalizeKey(name)
	stmt := `INSERT INTO kg_nodes (type, name, normalized_name, confidence, created_at, updated_at)
		 VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(normalized_name) DO UPDATE SET
		   type = excluded.type,
		   name = excluded.name,
		   confidence = CASE
		     WHEN excluded.confidence > kg_nodes.confidence THEN excluded.confidence
		     ELSE kg_nodes.confidence
		   END,
		   updated_at = datetime('now')`
	if _, err := storage.ExecWithRetry(db.Conn(), stmt, kind, name, key, conf); err != nil {
		return nil, err
	}
	var id int64
	if err := db.Conn().QueryRow(`SELECT id FROM kg_nodes WHERE normalized_name = ?`, key).Scan(&id); err != nil {
		return nil, err
	}
	return map[string]any{
		"id":              float64(id),
		"type":            kind,
		"name":            name,
		"normalized_name": key,
		"confidence":      conf,
	}, nil
}

func kgAddEdgeTool(ctx context.Context, payload map[string]any) (any, error) {
	fromID := int64(0)
	toID := int64(0)
	relation, _ := payload["relation"].(string)

	// resolve from
	if v, ok := payload["from_id"]; ok {
		switch x := v.(type) {
		case float64:
			fromID = int64(x)
		case int:
			fromID = int64(x)
		case int64:
			fromID = x
		}
	} else if v, ok := payload["from_name"]; ok {
		name, _ := v.(string)
		out, err := kgAddNodeTool(ctx, map[string]any{"name": name, "type": "entity", "confidence": 0.5})
		if err != nil {
			return nil, err
		}
		m := out.(map[string]any)
		if idf, ok := m["id"].(int64); ok {
			fromID = idf
		} else if idfF, ok := m["id"].(float64); ok {
			fromID = int64(idfF)
		}
	}

	// resolve to
	if v, ok := payload["to_id"]; ok {
		switch x := v.(type) {
		case float64:
			toID = int64(x)
		case int:
			toID = int64(x)
		case int64:
			toID = x
		}
	} else if v, ok := payload["to_name"]; ok {
		name, _ := v.(string)
		out, err := kgAddNodeTool(ctx, map[string]any{"name": name, "type": "entity", "confidence": 0.5})
		if err != nil {
			return nil, err
		}
		m := out.(map[string]any)
		if idf, ok := m["id"].(int64); ok {
			toID = idf
		} else if idfF, ok := m["id"].(float64); ok {
			toID = int64(idfF)
		}
	}

	if fromID <= 0 || toID <= 0 {
		return nil, fmt.Errorf("from_id and to_id (or names) are required")
	}
	if strings.TrimSpace(relation) == "" {
		return nil, fmt.Errorf("relation is required")
	}

	db, _, err := openKGStore()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	res, err := storage.ExecWithRetry(db.Conn(), `INSERT INTO kg_edges (from_node_id, relation, to_node_id, source_message_id, created_at)
	 VALUES (?, ?, ?, ?, datetime('now'))`, fromID, strings.TrimSpace(relation), toID, 0)
	if err != nil {
		return nil, err
	}
	edgeID, _ := res.LastInsertId()
	return map[string]any{
		"id":           float64(edgeID),
		"from_node_id": float64(fromID),
		"to_node_id":   float64(toID),
		"relation":     relation,
	}, nil
}

func kgGetNodeTool(ctx context.Context, payload map[string]any) (any, error) {
	var id int64
	if v, ok := payload["id"]; ok {
		switch x := v.(type) {
		case float64:
			id = int64(x)
		case int:
			id = int64(x)
		case int64:
			id = x
		}
	}
	name, _ := payload["name"].(string)
	db, store, err := openKGStore()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if id == 0 && strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("id or name is required")
	}

	var node storage.KGNode
	if id > 0 {
		row := db.Conn().QueryRow(`SELECT id, type, name, normalized_name, confidence, created_at, updated_at FROM kg_nodes WHERE id = ?`, id)
		if err := row.Scan(&node.ID, &node.Type, &node.Name, &node.NormalizedName, &node.Confidence, &node.CreatedAt, &node.UpdatedAt); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("node not found")
			}
			return nil, err
		}
	} else {
		key := normalizeKey(name)
		row := db.Conn().QueryRow(`SELECT id, type, name, normalized_name, confidence, created_at, updated_at FROM kg_nodes WHERE normalized_name = ?`, key)
		if err := row.Scan(&node.ID, &node.Type, &node.Name, &node.NormalizedName, &node.Confidence, &node.CreatedAt, &node.UpdatedAt); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("node not found")
			}
			return nil, err
		}
	}

	// fetch edges
	edges, err := store.ListEdges(200)
	if err != nil {
		return nil, err
	}
	outEdges := make([]map[string]any, 0)
	for _, e := range edges {
		if e.FromNodeID == node.ID || e.ToNodeID == node.ID {
			outEdges = append(outEdges, map[string]any{
				"id":             e.ID,
				"from_node_id":   e.FromNodeID,
				"from_node_name": e.FromNodeName,
				"relation":       e.Relation,
				"to_node_id":     e.ToNodeID,
				"to_node_name":   e.ToNodeName,
				"source_message": e.SourceMessageID,
				"created_at":     e.CreatedAt,
			})
		}
	}

	return map[string]any{
		"node": map[string]any{
			"id":              node.ID,
			"type":            node.Type,
			"name":            node.Name,
			"normalized_name": node.NormalizedName,
			"confidence":      node.Confidence,
			"created_at":      node.CreatedAt,
			"updated_at":      node.UpdatedAt,
		},
		"edges": outEdges,
	}, nil
}

func kgDeleteNodeTool(ctx context.Context, payload map[string]any) (any, error) {
	var id int64
	if v, ok := payload["id"]; ok {
		switch x := v.(type) {
		case float64:
			id = int64(x)
		case int:
			id = int64(x)
		case int64:
			id = x
		}
	}
	name, _ := payload["name"].(string)
	db, store, err := openKGStore()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if id == 0 && strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("id or name is required")
	}
	if id == 0 {
		key := normalizeKey(name)
		row := db.Conn().QueryRow(`SELECT id FROM kg_nodes WHERE normalized_name = ?`, key)
		if err := row.Scan(&id); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("node not found")
			}
			return nil, err
		}
	}
	if err := store.DeleteNode(id); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": float64(id)}, nil
}
