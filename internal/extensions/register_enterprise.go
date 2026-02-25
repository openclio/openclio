//go:build enterprise

package extensions

import "github.com/openclio/openclio/internal/tools"

// Register wires enterprise-only extensions for enterprise-tagged builds.
//
// The public repository keeps this as a noop so `-tags enterprise` compiles
// without proprietary code. Private enterprise repositories can replace this
// implementation with real extension registration.
func Register(_ *tools.Registry) error {
	return nil
}
