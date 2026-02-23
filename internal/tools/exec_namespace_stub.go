//go:build !linux

package tools

import (
	"fmt"
	"os/exec"
)

func applyNamespaceSandbox(cmd *exec.Cmd, networkAccess bool) error {
	_ = cmd
	_ = networkAccess
	return fmt.Errorf("namespace sandbox is only supported on Linux")
}
