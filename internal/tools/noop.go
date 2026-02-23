package tools

import (
	"context"
	"fmt"
)

// ErrNotImplemented is returned by noop tool implementations.
var ErrNotImplemented = fmt.Errorf("tool not implemented")

// NoopTool returns a ToolFunc that always returns ErrNotImplemented with the tool name.
func NoopTool(name string) ToolFunc {
	return func(ctx context.Context, payload map[string]any) (any, error) {
		return nil, fmt.Errorf("%w: %s", ErrNotImplemented, name)
	}
}
