//go:build !enterprise

package extensions

import "github.com/openclio/openclio/internal/tools"

// Register wires optional enterprise extension points.
// Community builds intentionally register nothing.
func Register(_ *tools.Registry) error {
	return nil
}
