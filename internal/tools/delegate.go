package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DelegateTool runs parallel sub-agent delegation through an injected executor.
type DelegateTool struct {
	executor DelegationExecutor
}

// NewDelegateTool creates a delegate tool.
func NewDelegateTool(executor DelegationExecutor) *DelegateTool {
	return &DelegateTool{executor: executor}
}

func (t *DelegateTool) Name() string { return "delegate" }

func (t *DelegateTool) Description() string {
	return "Delegate a complex objective into parallel sub-tasks and synthesize a final answer. Use this for large multi-part research or comparison requests."
}

func (t *DelegateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"objective": {
				"type": "string",
				"description": "Overall goal to solve"
			},
			"tasks": {
				"type": "array",
				"items": {"type": "string"},
				"minItems": 1,
				"description": "Independent sub-tasks that can run in parallel"
			}
		},
		"required": ["objective", "tasks"]
	}`)
}

type delegateParams struct {
	Objective string   `json:"objective"`
	Tasks     []string `json:"tasks"`
}

func (t *DelegateTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	if t.executor == nil {
		return "", fmt.Errorf("delegate is unavailable")
	}

	var p delegateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}

	objective := strings.TrimSpace(p.Objective)
	if objective == "" {
		return "", fmt.Errorf("objective is required")
	}

	cleanTasks := make([]string, 0, len(p.Tasks))
	for _, task := range p.Tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		cleanTasks = append(cleanTasks, task)
	}
	if len(cleanTasks) == 0 {
		return "", fmt.Errorf("tasks must include at least one non-empty item")
	}

	out, err := t.executor.Delegate(ctx, objective, cleanTasks)
	if err != nil {
		return "", fmt.Errorf("delegation failed: %w", err)
	}
	return out, nil
}
