package tools

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/openclio/openclio/internal/memory"
)

var memStore *memory.Store

func init() {
	// Initialize memory store. Use env OPENCLIO_MEMORY_DSN or default to in-memory.
	dsn := os.Getenv("OPENCILo_MEMORY_DSN")
	if dsn == "" {
		dsn = ":memory:"
	}
	s, err := memory.NewStore(dsn)
	if err != nil {
		// fallback to in-memory if file open fails
		_ = err
		s, _ = memory.NewStore(":memory:")
	}
	memStore = s

	// register or replace real implementations
	_ = ReplaceTool("memory_write", memoryWriteTool)
	_ = ReplaceTool("memory_search", memorySearchTool)
	_ = ReplaceTool("memory_read", memoryReadTool)
	_ = ReplaceTool("memory_list", memoryListTool)
	_ = ReplaceTool("memory_delete", memoryDeleteTool)
}

func memoryWriteTool(ctx context.Context, payload map[string]any) (any, error) {
	contentI, ok := payload["content"]
	if !ok {
		return nil, fmt.Errorf("missing content")
	}
	content, ok := contentI.(string)
	if !ok {
		return nil, fmt.Errorf("content must be string")
	}
	var meta map[string]interface{}
	if m, ok := payload["metadata"]; ok {
		if mm, ok := m.(map[string]any); ok {
			meta = make(map[string]interface{}, len(mm))
			for k, v := range mm {
				meta[k] = v
			}
		}
	}
	id, err := memStore.Add(ctx, content, meta)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

func memorySearchTool(ctx context.Context, payload map[string]any) (any, error) {
	qi, _ := payload["query"]
	query, _ := qi.(string)
	ki, _ := payload["k"]
	k := 10
	if ki != nil {
		switch v := ki.(type) {
		case int:
			k = v
		case float64:
			k = int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				k = n
			}
		}
	}
	res, err := memStore.Search(ctx, query, k)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res))
	for _, m := range res {
		out = append(out, map[string]any{
			"id":         m.ID,
			"content":    m.Content,
			"metadata":   m.Metadata,
			"created_at": m.CreatedAt,
		})
	}
	return out, nil
}

func memoryReadTool(ctx context.Context, payload map[string]any) (any, error) {
	idI, ok := payload["id"]
	if !ok {
		return nil, fmt.Errorf("missing id")
	}
	id, _ := idI.(string)
	m, err := memStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}
	return map[string]any{
		"id":         m.ID,
		"content":    m.Content,
		"metadata":   m.Metadata,
		"created_at": m.CreatedAt,
	}, nil
}

func memoryListTool(ctx context.Context, payload map[string]any) (any, error) {
	li, _ := payload["limit"]
	offi, _ := payload["offset"]
	limit := 50
	offset := 0
	if li != nil {
		switch v := li.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		}
	}
	if offi != nil {
		switch v := offi.(type) {
		case int:
			offset = v
		case float64:
			offset = int(v)
		}
	}
	ms, err := memStore.List(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, map[string]any{
			"id":         m.ID,
			"content":    m.Content,
			"metadata":   m.Metadata,
			"created_at": m.CreatedAt,
		})
	}
	return out, nil
}

func memoryDeleteTool(ctx context.Context, payload map[string]any) (any, error) {
	idI, ok := payload["id"]
	if !ok {
		return nil, fmt.Errorf("missing id")
	}
	id, _ := idI.(string)
	if err := memStore.Delete(ctx, id); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}
