//go:build linux

package tools

import (
	"os/exec"
	"syscall"
)

func applyNamespaceSandbox(cmd *exec.Cmd, networkAccess bool) error {
	flags := uintptr(syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS)
	if !networkAccess {
		flags |= syscall.CLONE_NEWNET
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: flags,
	}
	return nil
}
