package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentctx "github.com/openclio/openclio/internal/context"
	"github.com/openclio/openclio/internal/storage"
)

func init() {
	_ = ReplaceTool("sessions_list", sessionsListTool)
	_ = ReplaceTool("sessions_history", sessionsHistoryTool)
	_ = ReplaceTool("sessions_send", sessionsSendTool)
	_ = ReplaceTool("sessions_status", sessionsStatusTool)
	_ = ReplaceTool("agents_list", agentsListTool)
}

func sessionsListTool(ctx context.Context, payload map[string]any) (any, error) {
	limit := 50
	if l, ok := payload["limit"]; ok {
		switch v := l.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		}
	}
	dbPath, err := dbPath()
	if err != nil {
		return nil, err
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store := storage.NewSessionStore(db)
	sessions, err := store.List(limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, map[string]any{
			"id":               s.ID,
			"channel":          s.Channel,
			"sender_id":        s.SenderID,
			"created_at":       s.CreatedAt,
			"last_active":      s.LastActive,
			"metadata":         s.Metadata,
			"agent_profile_id": s.AgentProfileID,
		})
	}
	return out, nil
}

func sessionsHistoryTool(ctx context.Context, payload map[string]any) (any, error) {
	sessionID, _ := payload["session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	dbPath, err := dbPath()
	if err != nil {
		return nil, err
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	msgStore := storage.NewMessageStore(db)
	msgs, err := msgStore.GetBySession(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{
			"id":         float64(m.ID),
			"session_id": m.SessionID,
			"role":       m.Role,
			"content":    m.Content,
			"summary":    m.Summary,
			"tokens":     m.Tokens,
			"created_at": m.CreatedAt,
		})
	}
	return out, nil
}

func sessionsSendTool(ctx context.Context, payload map[string]any) (any, error) {
	sessionID, _ := payload["session_id"].(string)
	role, _ := payload["role"].(string)
	content, _ := payload["content"].(string)
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(role) == "" || strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("session_id, role, and content are required")
	}
	dbPath, err := dbPath()
	if err != nil {
		return nil, err
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	msgStore := storage.NewMessageStore(db)
	tokens := agentctx.EstimateTokens(content)
	msg, err := msgStore.Insert(sessionID, role, content, tokens)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":         float64(msg.ID),
		"session_id": msg.SessionID,
		"role":       msg.Role,
		"content":    msg.Content,
		"tokens":     msg.Tokens,
		"created_at": msg.CreatedAt,
	}, nil
}

func sessionsStatusTool(ctx context.Context, payload map[string]any) (any, error) {
	sessionID, _ := payload["session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	dbPath, err := dbPath()
	if err != nil {
		return nil, err
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store := storage.NewSessionStore(db)
	sess, err := store.Get(sessionID)
	if err != nil {
		if err == storage.ErrNotFound {
			return map[string]any{"status": "not_found"}, nil
		}
		return nil, err
	}
	// Simple heuristic: active if last_active within last 5 minutes
	status := "idle"
	if !sess.LastActive.IsZero() && time.Since(sess.LastActive) < 5*time.Minute {
		status = "active"
	}
	return map[string]any{
		"session_id":  sessionID,
		"status":      status,
		"last_active": sess.LastActive,
	}, nil
}

func agentsListTool(ctx context.Context, payload map[string]any) (any, error) {
	dbPath, err := dbPath()
	if err != nil {
		return nil, err
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store := storage.NewAgentProfileStore(db)
	profiles, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, map[string]any{
			"id":          p.ID,
			"name":        p.Name,
			"description": p.Description,
			"provider":    p.Provider,
			"model":       p.Model,
			"is_active":   p.IsActive,
			"created_at":  p.CreatedAt,
			"updated_at":  p.UpdatedAt,
		})
	}
	return out, nil
}
