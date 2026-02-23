package tools

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/template"
)

func init() {
	_ = ReplaceTool("json_query", jsonQueryTool)
	_ = ReplaceTool("csv_read", csvReadTool)
	_ = ReplaceTool("template_render", templateRenderTool)
}

// jsonQueryTool accepts either "json" (string) or "path" to a file, and a "query"
// which is a dot-separated path (e.g., "items.0.name"). It returns the matched value.
func jsonQueryTool(ctx context.Context, payload map[string]any) (any, error) {
	raw := ""
	if p, ok := payload["json"].(string); ok && strings.TrimSpace(p) != "" {
		raw = p
	} else if p, ok := payload["path"].(string); ok && strings.TrimSpace(p) != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading json file: %w", err)
		}
		raw = string(b)
	} else {
		return nil, fmt.Errorf("json or path is required")
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	q, _ := payload["query"].(string)
	if strings.TrimSpace(q) == "" {
		return doc, nil
	}
	parts := strings.Split(q, ".")
	curr := doc
	for _, part := range parts {
		switch c := curr.(type) {
		case map[string]any:
			v, ok := c[part]
			if !ok {
				// try with numbers as keys fallback
				curr = nil
				break
			}
			curr = v
		case []any:
			// index expected
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(c) {
				curr = nil
				break
			}
			curr = c[idx]
		default:
			curr = nil
		}
		if curr == nil {
			break
		}
	}
	if curr == nil {
		return nil, fmt.Errorf("path not found: %s", q)
	}
	return curr, nil
}

// csvReadTool reads CSV from "path" or "content" and returns []map[string]any.
func csvReadTool(ctx context.Context, payload map[string]any) (any, error) {
	var r *csv.Reader
	if p, ok := payload["path"].(string); ok && strings.TrimSpace(p) != "" {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = csv.NewReader(f)
	} else if c, ok := payload["content"].(string); ok && c != "" {
		r = csv.NewReader(strings.NewReader(c))
	} else {
		return nil, fmt.Errorf("path or content is required")
	}
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return []map[string]any{}, nil
	}
	header := true
	if h, ok := payload["header"].(bool); ok {
		header = h
	}
	out := make([]map[string]any, 0, len(records))
	if header {
		cols := records[0]
		for i := 1; i < len(records); i++ {
			row := make(map[string]any)
			for j := 0; j < len(cols) && j < len(records[i]); j++ {
				row[cols[j]] = records[i][j]
			}
			out = append(out, row)
		}
	} else {
		for _, rec := range records {
			row := make(map[string]any)
			for j := 0; j < len(rec); j++ {
				row[strconv.Itoa(j)] = rec[j]
			}
			out = append(out, row)
		}
	}
	return out, nil
}

// templateRenderTool renders a text/template using provided "template" and "vars" (map).
func templateRenderTool(ctx context.Context, payload map[string]any) (any, error) {
	tmplStr, _ := payload["template"].(string)
	if strings.TrimSpace(tmplStr) == "" {
		return nil, fmt.Errorf("template is required")
	}
	vars := map[string]any{}
	if v, ok := payload["vars"].(map[string]any); ok {
		vars = v
	}
	tmpl, err := template.New("t").Parse(tmplStr)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return nil, err
	}
	return map[string]any{"rendered": buf.String()}, nil
}
