//go:build enterprise

package edition

// Name returns the active product edition for this build.
func Name() string {
	return "enterprise"
}
